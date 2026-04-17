package thumbnail

import (
	"container/list"
	"sync"
)

// Tier tags a cache entry with its pre-fetch priority. Lower numbers
// mean "more important" — the viewport is Tier 1 and never gets
// evicted while the user can see it; background speculation is Tier 5
// and goes first under memory pressure.
//
// The tier system is spelled out in docs/system-design.md →
// "Priority tiers" and the pre-fetcher that feeds the cache in
// docs/system-design.md → "Predictive Pre-Fetcher".
type Tier uint8

const (
	TierViewport     Tier = 1
	TierLookahead    Tier = 2
	TierFolder       Tier = 3
	TierChildFolder  Tier = 4
	TierBackground   Tier = 5
	tierCount             = 5
)

// lruEntry is a single cached thumbnail plus the bookkeeping the LRU
// needs to find and evict it in O(1).
type lruEntry struct {
	path string
	tier Tier
	size int64
	data []byte
	// elem is the handle inside the per-tier LRU list. Eviction pops
	// from the tail (least recently used) without scanning the map.
	elem *list.Element
}

// tieredLRU is a memory-bounded cache with five priority tiers. Each
// tier is its own doubly-linked list ordered by recency; eviction
// walks tiers from background (5) back to viewport (1) and pops tails
// until the cache is under budget. Tier 1 is never evicted — the
// viewport has a first-class reservation.
//
// The cache stores one canonical entry per path. Re-inserting a path
// with a higher priority (smaller Tier number) promotes it; inserting
// with a lower priority leaves the stored tier untouched so a
// background refresh can't demote a viewport entry.
type tieredLRU struct {
	mu     sync.Mutex
	budget int64
	used   int64
	byPath map[string]*lruEntry
	tiers  [tierCount + 1]*list.List // index by Tier value; tiers[0] unused
}

// newTieredLRU builds an empty cache bounded to budget bytes. A
// non-positive budget disables eviction entirely (useful for Eager
// mode where we know the library fits in RAM).
func newTieredLRU(budget int64) *tieredLRU {
	c := &tieredLRU{
		budget: budget,
		byPath: make(map[string]*lruEntry),
	}
	for i := 1; i <= tierCount; i++ {
		c.tiers[i] = list.New()
	}
	return c
}

// Get returns the bytes for path (and true) if cached, touching the
// LRU so the entry moves to the front of its tier.
func (c *tieredLRU) Get(path string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byPath[path]
	if !ok {
		return nil, false
	}
	c.tiers[e.tier].MoveToFront(e.elem)
	return e.data, true
}

// Put inserts or updates path. Re-inserting only promotes the tier
// (never demotes) and always moves the entry to the front of its
// current tier's LRU list. Oversized entries are silently dropped —
// a single thumbnail must fit the budget to be worth caching.
func (c *tieredLRU) Put(path string, data []byte, tier Tier) {
	if tier < 1 || tier > tierCount {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	size := int64(len(data))
	if c.budget > 0 && size > c.budget {
		return
	}

	if existing, ok := c.byPath[path]; ok {
		c.promoteLocked(existing, data, size, tier)
		return
	}

	entry := &lruEntry{path: path, tier: tier, size: size, data: data}
	entry.elem = c.tiers[tier].PushFront(entry)
	c.byPath[path] = entry
	c.used += size
	c.evictIfNeededLocked()
}

// Contains reports whether path is cached. Does not update LRU
// ordering — useful for the pre-fetcher to skip re-loading.
func (c *tieredLRU) Contains(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.byPath[path]
	return ok
}

// Used returns the current RAM cost in bytes.
func (c *tieredLRU) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Len reports how many entries are cached (across all tiers).
func (c *tieredLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byPath)
}

// promoteLocked updates an existing entry's bytes, size, and tier.
// Only ever raises priority (smaller Tier wins).
func (c *tieredLRU) promoteLocked(e *lruEntry, data []byte, size int64, tier Tier) {
	c.used += size - e.size
	e.data = data
	e.size = size

	newTier := e.tier
	if tier < newTier {
		newTier = tier
	}
	if newTier != e.tier {
		c.tiers[e.tier].Remove(e.elem)
		e.tier = newTier
		e.elem = c.tiers[newTier].PushFront(e)
	} else {
		c.tiers[e.tier].MoveToFront(e.elem)
	}
	c.evictIfNeededLocked()
}

// evictIfNeededLocked drops tails starting from the lowest priority
// (Tier 5) until the cache is back under budget. Tier 1 is skipped —
// it's the viewport reservation.
func (c *tieredLRU) evictIfNeededLocked() {
	if c.budget <= 0 {
		return
	}
	for c.used > c.budget {
		victim := c.pickEvictionVictimLocked()
		if victim == nil {
			// Everything left is pinned (Tier 1). Over budget but
			// there's nothing we're allowed to drop.
			return
		}
		c.tiers[victim.tier].Remove(victim.elem)
		delete(c.byPath, victim.path)
		c.used -= victim.size
	}
}

// pickEvictionVictimLocked returns the oldest entry in the lowest-
// priority non-empty tier, excluding Tier 1.
func (c *tieredLRU) pickEvictionVictimLocked() *lruEntry {
	for t := tierCount; t > int(TierViewport); t-- {
		back := c.tiers[t].Back()
		if back != nil {
			return back.Value.(*lruEntry)
		}
	}
	return nil
}
