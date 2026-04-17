package thumbnail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recordingPublisher captures Publish calls for assertions. The
// prefetcher publishes from worker goroutines, so access is mutex-
// guarded — the race detector otherwise trips on concurrent appends.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Kind    string
	Payload any
}

func (r *recordingPublisher) Publish(kind string, payload any) {
	r.mu.Lock()
	r.events = append(r.events, recordedEvent{kind, payload})
	r.mu.Unlock()
}

func (r *recordingPublisher) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

func TestProvider_GetOrLoadRoundTrip(t *testing.T) {
	pack, cleanup := newPackWithEntry(t, "/photos/a.jpg", []byte("thumb-bytes"))
	defer cleanup()

	prov := NewProvider(Config{
		Pack:        pack,
		Hasher:      staticHasher(map[string][packHashSize]byte{"/photos/a.jpg": hashStr("/photos/a.jpg")}),
		BudgetBytes: 1 << 20,
	})
	defer prov.Close()

	got, err := prov.GetOrLoad("/photos/a.jpg")
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if !bytes.Equal(got, []byte("thumb-bytes")) {
		t.Fatalf("GetOrLoad = %q, want %q", got, "thumb-bytes")
	}

	// Second call must hit the cache — no error, same bytes.
	if data, ok := prov.Get("/photos/a.jpg"); !ok || !bytes.Equal(data, got) {
		t.Fatalf("Get after load returned ok=%v data=%q", ok, data)
	}
}

func TestProvider_PrefetchEmitsEvent(t *testing.T) {
	pack, cleanup := newPackWithEntry(t, "/photos/b.jpg", []byte("bb"))
	defer cleanup()

	pub := &recordingPublisher{}
	prov := NewProvider(Config{
		Pack:        pack,
		Hasher:      staticHasher(map[string][packHashSize]byte{"/photos/b.jpg": hashStr("/photos/b.jpg")}),
		Publisher:   pub,
		BudgetBytes: 1 << 20,
	})
	defer prov.Close()

	prov.Prefetch([]string{"/photos/b.jpg"}, TierLookahead)

	// Poll for the async insert; prefetcher runs on its own goroutine.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := prov.Get("/photos/b.jpg"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := prov.Get("/photos/b.jpg"); !ok {
		t.Fatal("prefetcher never inserted into cache")
	}
	events := pub.snapshot()
	if len(events) == 0 || events[0].Kind != "thumbnail:ready" {
		t.Fatalf("expected thumbnail:ready event, got %+v", events)
	}
}

// newPackWithEntry creates a fresh pack at a temp path with one
// entry already stored. Returns the pack and a cleanup closure.
func newPackWithEntry(t *testing.T, path string, data []byte) (*Pack, func()) {
	t.Helper()
	packPath := filepath.Join(t.TempDir(), "thumbs.pack")
	pack, err := Open(packPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Put(hashStr(path), data); err != nil {
		t.Fatal(err)
	}
	return pack, func() { _ = pack.Close() }
}

func hashStr(s string) [packHashSize]byte { return sha256.Sum256([]byte(s)) }

// staticHasher returns a PathHasher backed by an explicit map.
func staticHasher(m map[string][packHashSize]byte) PathHasher {
	return func(path string) ([packHashSize]byte, bool) {
		h, ok := m[path]
		return h, ok
	}
}

func TestHashFromHex(t *testing.T) {
	raw := sha256.Sum256([]byte("hello"))
	h := hex.EncodeToString(raw[:])

	got, ok := HashFromHex(h)
	if !ok {
		t.Fatal("HashFromHex returned ok=false for a valid digest")
	}
	if got != raw {
		t.Fatalf("HashFromHex round-trip failed: %x vs %x", got, raw)
	}

	if _, ok := HashFromHex("too-short"); ok {
		t.Fatal("HashFromHex accepted a short string")
	}
}
