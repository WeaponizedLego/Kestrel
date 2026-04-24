package vision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// probeWithFakeSidecar is the happy-path walkthrough: write an
// endpoint file, stand up a stub server that answers /healthz with
// the version and models, run ProbeOnce, assert the client reports
// StateOn with the advertised metadata.
func TestProbeOnce_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != protocol.PathHealthz {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.Health{
			Version: "v0.1.0",
			Models:  []string{"arcface-r100", "yolov8n"},
		})
	}))
	defer srv.Close()

	ep := filepath.Join(t.TempDir(), "vision.endpoint")
	if err := WriteEndpoint(ep, srv.URL, "test-token"); err != nil {
		t.Fatalf("WriteEndpoint: %v", err)
	}

	c := NewClient(ep)
	status := c.ProbeOnce(context.Background())

	if status.State != StateOn.String() {
		t.Fatalf("state = %q, want %q (lastError=%q)", status.State, StateOn.String(), status.LastError)
	}
	if status.Version != "v0.1.0" {
		t.Errorf("version = %q, want v0.1.0", status.Version)
	}
	if !c.Available() {
		t.Error("Available() = false after successful probe")
	}
}

// When the endpoint file is missing, the client reports StateOff
// (not StateError): missing file = sidecar never started, a normal
// first-run condition, not a failure to surface.
func TestProbeOnce_NoEndpointFile(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "does-not-exist"))
	status := c.ProbeOnce(context.Background())
	if status.State != StateOff.String() {
		t.Fatalf("state = %q, want %q", status.State, StateOff.String())
	}
	if c.Available() {
		t.Error("Available() = true with no endpoint")
	}
}

// A present endpoint file pointing at an unreachable URL must land
// on StateError so the UI surfaces the problem rather than silently
// treating it as "off".
func TestProbeOnce_EndpointPresentButUnreachable(t *testing.T) {
	ep := filepath.Join(t.TempDir(), "vision.endpoint")
	// 127.0.0.1:1 is almost certainly closed; picking a fixed bogus
	// port keeps the test deterministic without consuming a real one.
	if err := WriteEndpoint(ep, "http://127.0.0.1:1", "t"); err != nil {
		t.Fatalf("WriteEndpoint: %v", err)
	}
	c := NewClient(ep)
	status := c.ProbeOnce(context.Background())
	if status.State != StateError.String() {
		t.Fatalf("state = %q, want %q", status.State, StateError.String())
	}
	if status.LastError == "" {
		t.Error("LastError should be populated on StateError")
	}
}

// Detect returns an error fast when Available() is false; the
// scanner relies on this so an unavailable sidecar doesn't cost a
// per-image HTTP round trip.
func TestDetect_NotAvailable(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "none"))
	_, err := c.Detect(context.Background(), "/dev/null")
	if err == nil {
		t.Fatal("expected error when client unavailable")
	}
}

// Detect happy path: probe OK, post a small file, assert the sidecar
// receives the body and the client decodes the response.
func TestDetect_OK(t *testing.T) {
	want := protocol.DetectResponse{
		Objects: []Object{{Label: "dog", Confidence: 0.91}},
	}
	_ = want // referenced via protocol.Object below

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case protocol.PathHealthz:
			_ = json.NewEncoder(w).Encode(protocol.Health{Version: "test", Models: []string{"stub"}})
		case protocol.PathDetect:
			buf := make([]byte, 16)
			n, _ := r.Body.Read(buf)
			if n == 0 {
				http.Error(w, "empty body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(protocol.DetectResponse{
				Objects: []protocol.Object{{Label: "dog", Confidence: 0.91}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ep := filepath.Join(t.TempDir(), "vision.endpoint")
	if err := WriteEndpoint(ep, srv.URL, "tok"); err != nil {
		t.Fatalf("WriteEndpoint: %v", err)
	}
	c := NewClient(ep)
	c.ProbeOnce(context.Background())
	if !c.Available() {
		t.Fatalf("client should be Available after successful probe: %+v", c.Snapshot())
	}

	// Write a tiny file so Detect has something to POST.
	img := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(img, []byte("fake-jpeg"), 0o600); err != nil {
		t.Fatalf("writing stub image: %v", err)
	}

	got, err := c.Detect(context.Background(), img)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got.Objects) != 1 || got.Objects[0].Label != "dog" {
		t.Errorf("got %+v, want one dog", got.Objects)
	}
}

// Object is a type alias so the earlier unused test variable compiles
// cleanly when the test suite is run on machines without ONNX.
type Object = protocol.Object
