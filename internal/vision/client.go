// Package vision is the core-side client for the optional
// kestrel-vision sidecar. It probes the sidecar's loopback HTTP
// endpoint, posts images for detection, and reports availability so
// the scanner and the UI status badge can degrade gracefully when the
// sidecar is absent.
//
// The sidecar binary lives under cmd/kestrel-vision/ in this repo and
// speaks the protocol defined in internal/vision/protocol. Core never
// links ONNX runtime or model weights: every ML dependency stays
// behind the HTTP boundary so the core binary remains CGO-free.
package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// probeInterval is how often Client refreshes its availability view
// in the background when Start() has been called. Short enough that a
// user launching the sidecar sees the UI badge flip within a few
// seconds; long enough that an idle app isn't spinning on probes.
const probeInterval = 5 * time.Second

// probeTimeout caps how long a single /healthz attempt waits before
// we call the sidecar "unreachable". A responsive sidecar replies in
// single-digit ms; anything north of a second is either hung or
// busy, and either way the UI should surface "not available".
const probeTimeout = 2 * time.Second

// detectTimeout is the per-image budget for a /detect call. Generous
// enough for CPU-only inference on a multi-face group shot, tight
// enough that a hung sidecar doesn't stall the scanner.
const detectTimeout = 30 * time.Second

// State enumerates what the status badge and scanner see.
type State uint8

const (
	// StateUnknown is the initial state before the first probe. The
	// UI treats it the same as StateOff but can avoid flicker by
	// waiting for the first real result.
	StateUnknown State = iota

	// StateOff means the endpoint file is missing — the sidecar has
	// never been started on this machine or has cleaned up after
	// itself on shutdown.
	StateOff

	// StateError means we found an endpoint file but the probe
	// failed: wrong token, wrong version, unreachable, timeout.
	StateError

	// StateOn means the last probe succeeded. Detect calls will be
	// dispatched.
	StateOn
)

// String renders the state for logs and JSON.
func (s State) String() string {
	switch s {
	case StateOff:
		return "off"
	case StateError:
		return "error"
	case StateOn:
		return "on"
	default:
		return "unknown"
	}
}

// Status is the snapshot the /api/vision/status handler returns and
// the UI badge renders. LastError is only meaningful when State is
// StateError. InFlight counts per-image Detect calls currently in
// progress so the badge can surface "busy" separately from "on" —
// without this the user can't tell a wedged sidecar from an idle
// healthy one.
type Status struct {
	State       string   `json:"state"`
	Version     string   `json:"version,omitempty"`
	Models      []string `json:"models,omitempty"`
	LastError   string   `json:"lastError,omitempty"`
	CheckedAt   int64    `json:"checkedAt"`
	InFlight    int      `json:"inFlight"`
	LastDetect  int64    `json:"lastDetect,omitempty"`
	DetectCount int64    `json:"detectCount"`
}

// endpoint is the contents of vision.endpoint written by the sidecar
// at startup: where to reach it and what token to present. Kept in
// one struct so the sidecar and client serialize identical shapes.
type endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Client talks to the sidecar. It is safe for concurrent use; the
// cached status is updated atomically and Detect only holds the HTTP
// client's own internals.
type Client struct {
	endpointPath string
	http         *http.Client

	mu       sync.RWMutex
	status   Status
	ep       endpoint
	hasEp    bool
	started  atomic.Bool
	stopOnce sync.Once
	stop     chan struct{}

	// Activity counters live outside the status mutex so the hot
	// Detect path doesn't contend with the slower probe path. Probe
	// snapshots fold these atomics into the Status copy.
	inFlight    atomic.Int64
	detectCount atomic.Int64
	lastDetect  atomic.Int64
}

// NewClient constructs a Client that reads the sidecar handshake file
// from endpointPath (see platform.VisionEndpointPath). Probing does
// not run until Start is called — tests can use NewClient + ProbeOnce
// deterministically.
func NewClient(endpointPath string) *Client {
	return &Client{
		endpointPath: endpointPath,
		http:         &http.Client{Timeout: detectTimeout},
		status: Status{
			State:     StateUnknown.String(),
			CheckedAt: time.Now().Unix(),
		},
		stop: make(chan struct{}),
	}
}

// Start kicks off the background probe loop. Idempotent: calling it
// twice is a no-op. Pass a root context so the loop exits on app
// shutdown in addition to the internal Stop.
func (c *Client) Start(ctx context.Context) {
	if !c.started.CompareAndSwap(false, true) {
		return
	}
	go c.probeLoop(ctx)
}

// Stop tells the probe loop to exit. Safe to call even if Start was
// never invoked.
func (c *Client) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
}

// Available reports whether Detect is expected to succeed. The
// scanner consults this before dispatching per-image work so a down
// sidecar silently skips detection rather than stalling the pipeline.
func (c *Client) Available() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status.State == StateOn.String()
}

// Snapshot returns the most recent status for the API and UI badge.
// Returned by value so callers can't mutate the Client's copy. The
// activity fields (InFlight, DetectCount, LastDetect) are folded in
// from their atomics so callers see a consistent-ish picture even
// though the counters update outside the status mutex.
func (c *Client) Snapshot() Status {
	c.mu.RLock()
	s := c.status
	c.mu.RUnlock()
	s.InFlight = int(c.inFlight.Load())
	s.DetectCount = c.detectCount.Load()
	s.LastDetect = c.lastDetect.Load()
	return s
}

// Detect posts the file at path to the sidecar and returns its
// detection result. Returns an error (and does not mark the client
// unavailable) for per-request failures so a single bad image doesn't
// knock out the whole pipeline; the background probe is the one
// authority on "is the sidecar alive".
func (c *Client) Detect(ctx context.Context, path string) (*protocol.DetectResponse, error) {
	if !c.Available() {
		return nil, errors.New("vision sidecar not available")
	}
	c.mu.RLock()
	ep := c.ep
	c.mu.RUnlock()

	c.inFlight.Add(1)
	defer func() {
		c.inFlight.Add(-1)
		c.detectCount.Add(1)
		c.lastDetect.Store(time.Now().Unix())
	}()

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s for detection: %w", path, err)
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL+protocol.PathDetect, f)
	if err != nil {
		return nil, fmt.Errorf("building detect request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+ep.Token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling detect for %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("detect for %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	var out protocol.DetectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding detect response for %s: %w", path, err)
	}
	return &out, nil
}

// ProbeOnce runs one availability probe synchronously and updates
// the cached status. Exposed so tests can drive the state machine
// without spinning up the probe loop, and so the status endpoint can
// force a fresh check when the UI asks.
func (c *Client) ProbeOnce(ctx context.Context) Status {
	ep, err := readEndpoint(c.endpointPath)
	if err != nil {
		c.setStatus(Status{
			State:     StateOff.String(),
			CheckedAt: time.Now().Unix(),
		}, endpoint{}, false)
		return c.Snapshot()
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.URL+protocol.PathHealthz, nil)
	if err != nil {
		c.setStatus(Status{
			State:     StateError.String(),
			LastError: err.Error(),
			CheckedAt: time.Now().Unix(),
		}, ep, true)
		return c.Snapshot()
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)

	// Dedicated short-timeout client for probes so a wedged sidecar
	// doesn't delay the tick past probeInterval.
	probeHTTP := &http.Client{Timeout: probeTimeout}
	resp, err := probeHTTP.Do(req)
	if err != nil {
		c.setStatus(Status{
			State:     StateError.String(),
			LastError: err.Error(),
			CheckedAt: time.Now().Unix(),
		}, ep, true)
		return c.Snapshot()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.setStatus(Status{
			State:     StateError.String(),
			LastError: fmt.Sprintf("healthz returned %d: %s", resp.StatusCode, bytes.TrimSpace(body)),
			CheckedAt: time.Now().Unix(),
		}, ep, true)
		return c.Snapshot()
	}

	var h protocol.Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		c.setStatus(Status{
			State:     StateError.String(),
			LastError: fmt.Errorf("decoding healthz: %w", err).Error(),
			CheckedAt: time.Now().Unix(),
		}, ep, true)
		return c.Snapshot()
	}

	c.setStatus(Status{
		State:     StateOn.String(),
		Version:   h.Version,
		Models:    h.Models,
		CheckedAt: time.Now().Unix(),
	}, ep, true)
	return c.Snapshot()
}

// probeLoop runs ProbeOnce on a ticker until ctx or Stop fires.
func (c *Client) probeLoop(ctx context.Context) {
	// First probe immediately so the UI badge reflects reality on the
	// first poll rather than after probeInterval seconds of "unknown".
	c.ProbeOnce(ctx)

	t := time.NewTicker(probeInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-t.C:
			c.ProbeOnce(ctx)
		}
	}
}

// setStatus writes status and (optionally) the endpoint under the
// client's mutex. Extracted so every ProbeOnce exit path shares the
// same locking contract.
func (c *Client) setStatus(s Status, ep endpoint, hasEp bool) {
	c.mu.Lock()
	c.status = s
	c.ep = ep
	c.hasEp = hasEp
	c.mu.Unlock()
}

// readEndpoint loads the sidecar handshake file. A missing file is
// reported as fs.ErrNotExist wrapped; callers distinguish "file
// missing → sidecar off" from "probe failed → sidecar error".
func readEndpoint(path string) (endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return endpoint{}, fmt.Errorf("reading vision endpoint from %s: %w", path, err)
	}
	var ep endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		return endpoint{}, fmt.Errorf("decoding vision endpoint at %s: %w", path, err)
	}
	if ep.URL == "" || ep.Token == "" {
		return endpoint{}, fmt.Errorf("vision endpoint at %s is incomplete", path)
	}
	return ep, nil
}

// WriteEndpoint serialises ep to path. Exported so the sidecar main
// (cmd/kestrel-vision) can call it without duplicating the JSON shape.
// Atomic write via tempfile + rename so a crashed write never leaves
// a half-written file that would fail the client's probe.
func WriteEndpoint(path, url, token string) error {
	data, err := json.Marshal(endpoint{URL: url, Token: token})
	if err != nil {
		return fmt.Errorf("encoding vision endpoint: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, path, err)
	}
	return nil
}

// RemoveEndpoint deletes the handshake file. Called by the sidecar
// on graceful shutdown so core flips to StateOff on the next probe.
// A missing file is not an error.
func RemoveEndpoint(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}
