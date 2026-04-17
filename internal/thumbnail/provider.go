package thumbnail

import (
	"encoding/hex"
	"fmt"
	"sync"
)

// Provider is the thumbnail access interface the rest of the app sees.
// It hides which mode is active (Eager cache vs Tiered LRU) and where
// bytes come from (RAM, pack, or a just-now load).
//
// Method shapes match docs/system-design.md → "ThumbnailProvider
// Interface" so other packages can depend on a stable contract.
type Provider interface {
	// Get returns cached bytes if the thumbnail is currently in RAM.
	Get(photoPath string) ([]byte, bool)

	// GetOrLoad returns bytes from the cache, loading from the pack
	// synchronously on a miss. A genuinely absent thumbnail returns
	// (nil, nil) — callers can distinguish that from an error.
	GetOrLoad(photoPath string) ([]byte, error)

	// Prefetch hints that these paths should be warm at the given
	// priority. Does not block.
	Prefetch(paths []string, tier Tier)

	// MemoryUsage returns the current RAM bytes consumed by cached
	// thumbnails.
	MemoryUsage() int64

	// SaveAll flushes any pending writes to disk.
	SaveAll() error

	// Close stops background workers and releases resources.
	Close() error
}

// PathHasher resolves a photo path to its [32]byte SHA-256 hash — the
// key used by Pack. Declared as a function type so Provider stays
// decoupled from the library package.
type PathHasher func(photoPath string) ([packHashSize]byte, bool)

// Publisher is the Hub subset the provider uses to emit
// "thumbnail:ready" events.
type Publisher interface {
	Publish(kind string, payload any)
}

// Mode selects between Eager (load everything into RAM at startup, no
// eviction) and Tiered (budgeted LRU fed by the pre-fetcher).
type Mode int

const (
	ModeTiered Mode = iota
	ModeEager
)

// Config parameterises NewProvider. A nil Publisher is allowed (no WS
// events emitted); a nil PathHasher is a programmer error and panics
// on first use.
type Config struct {
	Pack       *Pack
	Hasher     PathHasher
	Publisher  Publisher
	Mode       Mode
	BudgetBytes int64 // RAM budget for the LRU; ignored in Eager mode
	Workers    int   // pre-fetcher worker count (default: 2)
}

// TieredProvider is the sole concrete Provider. It wraps a Pack (for
// on-disk reads), a tiered LRU (for RAM caching), and a prefetcher
// (goroutine pool driven by a priority queue).
type TieredProvider struct {
	cfg   Config
	cache *tieredLRU
	pref  *prefetcher
	mode  Mode

	// hashes caches path → pack hash lookups so Get/GetOrLoad don't
	// re-decode the hex string on every call. The ok field records
	// "hasher said no" so unknown paths don't keep hitting the library.
	hashMu sync.RWMutex
	hashes map[string]hashLookup
}

type hashLookup struct {
	hash [packHashSize]byte
	ok   bool
}

// NewProvider builds a provider around an already-open Pack. Callers
// typically invoke WarmEager after construction when Mode == Eager so
// the cache is populated before any HTTP traffic arrives.
func NewProvider(cfg Config) *TieredProvider {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	budget := cfg.BudgetBytes
	if cfg.Mode == ModeEager {
		// Eager disables eviction; the LRU is just a map with pinned
		// entries so lookups and MemoryUsage still work.
		budget = 0
	}
	p := &TieredProvider{
		cfg:    cfg,
		cache:  newTieredLRU(budget),
		mode:   cfg.Mode,
		hashes: make(map[string]hashLookup),
	}
	p.pref = newPrefetcher(p, cfg.Workers)
	p.pref.start()
	return p
}

// WarmEager loads every thumbnail for paths into the cache at
// TierBackground. Intended for Mode == Eager after a full scan when
// the library is known to fit in RAM.
func (p *TieredProvider) WarmEager(paths []string) error {
	for _, path := range paths {
		data, err := p.loadFromPack(path)
		if err != nil {
			return fmt.Errorf("warming cache for %s: %w", path, err)
		}
		if data == nil {
			continue
		}
		p.cache.Put(path, data, TierBackground)
	}
	return nil
}

// Get returns cached bytes only. Never touches disk.
func (p *TieredProvider) Get(photoPath string) ([]byte, bool) {
	return p.cache.Get(photoPath)
}

// GetOrLoad returns cached bytes if present, otherwise fetches from
// the pack, inserts at TierBackground, and returns the bytes. A
// genuinely missing thumbnail yields (nil, nil).
func (p *TieredProvider) GetOrLoad(photoPath string) ([]byte, error) {
	if data, ok := p.cache.Get(photoPath); ok {
		return data, nil
	}
	data, err := p.loadFromPack(photoPath)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	p.insert(photoPath, data, TierBackground)
	return data, nil
}

// Prefetch enqueues paths at the given tier. Duplicates already in
// cache are skipped quickly inside the worker.
func (p *TieredProvider) Prefetch(paths []string, tier Tier) {
	p.pref.enqueue(paths, tier)
}

// MemoryUsage exposes the LRU's current byte usage.
func (p *TieredProvider) MemoryUsage() int64 {
	return p.cache.Used()
}

// SaveAll flushes pending pack writes.
func (p *TieredProvider) SaveAll() error {
	return p.cfg.Pack.Save()
}

// Close stops the prefetcher. The Pack is owned by main and is
// closed there so other things (final save, etc.) can share it.
func (p *TieredProvider) Close() error {
	p.pref.stop()
	return nil
}

// insert caches data under path and publishes "thumbnail:ready" so
// frontends can swap placeholders for the real image.
func (p *TieredProvider) insert(path string, data []byte, tier Tier) {
	p.cache.Put(path, data, tier)
	if p.cfg.Publisher != nil {
		p.cfg.Publisher.Publish("thumbnail:ready", map[string]any{
			"path": path,
			"size": len(data),
		})
	}
}

// loadFromPack resolves path → hash and reads the JPEG bytes. Returns
// (nil, nil) when the hash is unknown or the pack has no such entry.
func (p *TieredProvider) loadFromPack(path string) ([]byte, error) {
	hash, ok := p.resolveHash(path)
	if !ok {
		return nil, nil
	}
	data, ok, err := p.cfg.Pack.Get(hash)
	if err != nil {
		return nil, fmt.Errorf("reading thumb for %s: %w", path, err)
	}
	if !ok {
		return nil, nil
	}
	return data, nil
}

// resolveHash memoises the path → hash mapping. A miss goes through
// the PathHasher; the result is cached regardless so repeated
// lookups for unknown paths don't re-hit the library map.
func (p *TieredProvider) resolveHash(path string) ([packHashSize]byte, bool) {
	p.hashMu.RLock()
	cached, seen := p.hashes[path]
	p.hashMu.RUnlock()
	if seen {
		return cached.hash, cached.ok
	}

	hash, ok := p.cfg.Hasher(path)
	p.hashMu.Lock()
	p.hashes[path] = hashLookup{hash: hash, ok: ok}
	p.hashMu.Unlock()
	return hash, ok
}

// HashFromHex turns a Photo.Hash hex string into the [32]byte key the
// pack uses. Exported so main.go can build a PathHasher closure.
func HashFromHex(s string) ([packHashSize]byte, bool) {
	var h [packHashSize]byte
	if len(s) != 2*packHashSize {
		return h, false
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != packHashSize {
		return h, false
	}
	copy(h[:], b)
	return h, true
}
