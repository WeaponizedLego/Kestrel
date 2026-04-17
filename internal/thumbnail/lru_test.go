package thumbnail

import (
	"bytes"
	"testing"
)

func TestLRU_PutGet(t *testing.T) {
	c := newTieredLRU(1024)
	c.Put("a", []byte("AAAA"), TierFolder)

	got, ok := c.Get("a")
	if !ok || !bytes.Equal(got, []byte("AAAA")) {
		t.Fatalf("Get = %q, %v", got, ok)
	}
	if _, ok := c.Get("missing"); ok {
		t.Fatal("Get returned ok on missing path")
	}
}

func TestLRU_EvictsLowestTierFirst(t *testing.T) {
	c := newTieredLRU(30) // budget only holds three 10-byte entries
	c.Put("bg", bytes.Repeat([]byte{0x01}, 10), TierBackground)
	c.Put("folder", bytes.Repeat([]byte{0x02}, 10), TierFolder)
	c.Put("view", bytes.Repeat([]byte{0x03}, 10), TierViewport)
	// Insert a fourth entry — must evict "bg" (lowest priority).
	c.Put("look", bytes.Repeat([]byte{0x04}, 10), TierLookahead)

	if _, ok := c.Get("bg"); ok {
		t.Fatal("TierBackground entry should have been evicted first")
	}
	for _, p := range []string{"folder", "view", "look"} {
		if _, ok := c.Get(p); !ok {
			t.Fatalf("%q evicted unexpectedly", p)
		}
	}
}

func TestLRU_ViewportTierNeverEvicted(t *testing.T) {
	c := newTieredLRU(20) // room for two 10-byte entries
	c.Put("v1", bytes.Repeat([]byte{0x01}, 10), TierViewport)
	c.Put("v2", bytes.Repeat([]byte{0x02}, 10), TierViewport)
	// A third viewport insert forces over-budget, but nothing in
	// Tier 1 may be evicted — the cache must simply stay over budget.
	c.Put("v3", bytes.Repeat([]byte{0x03}, 10), TierViewport)

	for _, p := range []string{"v1", "v2", "v3"} {
		if _, ok := c.Get(p); !ok {
			t.Fatalf("viewport entry %q was evicted", p)
		}
	}
	if c.Used() != 30 {
		t.Fatalf("Used = %d, want 30 (over budget is allowed for Tier 1)", c.Used())
	}
}

func TestLRU_PromoteKeepsHigherPriority(t *testing.T) {
	c := newTieredLRU(0) // unlimited, focus on tier logic
	c.Put("p", []byte("x"), TierBackground)
	c.Put("p", []byte("x"), TierViewport)
	// Re-inserting at a lower priority must not demote.
	c.Put("p", []byte("x"), TierChildFolder)

	// After three puts, "p" should still sit in Tier 1.
	if c.tiers[TierViewport].Len() != 1 {
		t.Fatalf("expected Tier 1 list to have 1 entry, got %d", c.tiers[TierViewport].Len())
	}
	if c.tiers[TierChildFolder].Len() != 0 || c.tiers[TierBackground].Len() != 0 {
		t.Fatal("entry did not stay pinned in Tier 1")
	}
}

func TestLRU_UnboundedBudget(t *testing.T) {
	c := newTieredLRU(0)
	for i := 0; i < 100; i++ {
		c.Put(string(rune(i)), bytes.Repeat([]byte{byte(i)}, 1000), TierBackground)
	}
	if c.Len() != 100 {
		t.Fatalf("Len = %d, want 100 (budget=0 disables eviction)", c.Len())
	}
}
