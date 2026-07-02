package ui

import "testing"

// TestLRUCache_GetSet: a stored value is retrievable and an empty cache reports misses.
// Given an empty capacity-3 cache, when "a" is read before and after being set, then the first read
// misses and the second returns the stored value.
// Why it matters: consumers treat a miss as "go fetch", so a false miss re-fires API calls and a false
// hit serves data that was never stored.
func TestLRUCache_GetSet(t *testing.T) {
	// Given: an empty cache
	c := NewLRUCache[string, int](3)

	// When/Then: reading before any set misses
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss on empty cache")
	}

	// And: after a set, the same key returns the stored value
	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}
}

// TestLRUCache_Eviction: overflowing capacity evicts the least-recently-used entry.
// Given a capacity-2 cache holding "a" and "b", when "c" is set, then "a" is gone, "b" and "c" remain,
// and the length holds at capacity.
// Why it matters: evicting the wrong entry drops the pipeline status of a project the user is looking at
// while keeping ones scrolled past long ago.
func TestLRUCache_Eviction(t *testing.T) {
	// Given: a full capacity-2 cache
	c := NewLRUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: a third entry overflows the capacity
	c.Set("c", 3) // should evict "a"

	// Then: the oldest entry is evicted and the rest survive at capacity
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("Get(b) = %d, %v; want 2, true", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != 3 {
		t.Fatalf("Get(c) = %d, %v; want 3, true", v, ok)
	}
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
}

// TestLRUCache_GetPromotes: reading an entry refreshes its recency.
// Given a full capacity-2 cache, when "a" is read and "c" is then set, then "b" is evicted instead of "a".
// Why it matters: without promotion the cache degrades to FIFO and evicts exactly the hot entries that
// every refresh tick reads.
func TestLRUCache_GetPromotes(t *testing.T) {
	// Given: a full capacity-2 cache
	c := NewLRUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: "a" is read and a new entry forces an eviction
	c.Get("a")
	c.Set("c", 3) // should evict "b", not "a"

	// Then: the unread entry is the one evicted
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted after 'a' was promoted")
	}
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != 3 {
		t.Fatalf("Get(c) = %d, %v; want 3, true", v, ok)
	}
}

// TestLRUCache_PeekDoesNotPromote: Peek reads a value without refreshing its recency.
// Given a full capacity-2 cache, when "a" is peeked and "c" is then set, then "a" is still the entry
// evicted.
// Why it matters: the render path peeks every visible row on every frame, and if rendering counted as
// use, recency would track whatever scrolled by instead of what the user actually selected.
func TestLRUCache_PeekDoesNotPromote(t *testing.T) {
	// Given: a full capacity-2 cache
	c := NewLRUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: "a" is peeked (which must not promote, so "a" stays oldest)
	if v, ok := c.Peek("a"); !ok || v != 1 {
		t.Fatalf("Peek(a) = %d, %v; want 1, true", v, ok)
	}
	c.Set("c", 3) // should evict "a" (still oldest), not "b"

	// Then: the peeked entry is still the one evicted
	if _, ok := c.Peek("a"); ok {
		t.Fatal("expected 'a' to be evicted; Peek must not promote")
	}
	if v, ok := c.Peek("b"); !ok || v != 2 {
		t.Fatalf("Peek(b) = %d, %v; want 2, true", v, ok)
	}
}

// TestLRUCache_PeekMissing: peeking an absent key reports the zero value and false.
// Given a cache without the requested key, when Peek runs, then it returns 0 and ok=false.
// Why it matters: a non-false ok on a miss would make render paths treat uninitialized data as a cached
// pipeline status.
func TestLRUCache_PeekMissing(t *testing.T) {
	// Given: a cache that never stored the key
	c := NewLRUCache[string, int](2)

	// When/Then: peeking it reports a zero value and a miss
	if v, ok := c.Peek("missing"); ok || v != 0 {
		t.Fatalf("Peek(missing) = %d, %v; want 0, false", v, ok)
	}
}

// TestLRUCache_SetExistingNoEviction: updating an existing key replaces its value without evicting.
// Given a full capacity-2 cache, when "a" is set again with a new value, then the length stays at 2 and
// both entries are readable with "a" updated.
// Why it matters: if an in-place update counted as an insertion, refreshing one project's status would
// evict an unrelated live entry.
func TestLRUCache_SetExistingNoEviction(t *testing.T) {
	// Given: a full capacity-2 cache
	c := NewLRUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: an existing key is updated in place
	c.Set("a", 10) // update existing, should not evict anything

	// Then: nothing is evicted and the value is replaced
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
	if v, _ := c.Get("a"); v != 10 {
		t.Fatalf("Get(a) = %d; want 10", v)
	}
	if v, _ := c.Get("b"); v != 2 {
		t.Fatalf("Get(b) = %d; want 2", v)
	}
}

// TestLRUCache_SetExistingPromotes: updating an existing key also refreshes its recency.
// Given a full capacity-2 cache, when "a" is rewritten and "c" is then set, then "b" is the entry evicted.
// Why it matters: freshly rewritten entries are the ones being actively refreshed, so evicting them first
// would churn exactly the hottest data.
func TestLRUCache_SetExistingPromotes(t *testing.T) {
	// Given: a full capacity-2 cache
	c := NewLRUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: an existing key is rewritten and a new entry forces an eviction
	c.Set("a", 10) // update promotes "a"; "b" is now oldest
	c.Set("c", 3)  // should evict "b"

	// Then: the untouched entry is the one evicted
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}
	if v, _ := c.Get("a"); v != 10 {
		t.Fatalf("Get(a) = %d; want 10", v)
	}
}

// TestLRUCache_Delete: deleting removes the entry and deleting a missing key is a no-op.
// Given a cache holding "a" and "b", when "a" and then an absent key are deleted, then "a" is gone, the
// length drops to 1, and the no-op delete leaves it there.
// Why it matters: a delete that desynced the map from the recency list would corrupt every later
// eviction decision.
func TestLRUCache_Delete(t *testing.T) {
	// Given: a cache holding two entries
	c := NewLRUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: one entry is deleted
	c.Delete("a")

	// Then: it is gone and the length reflects the removal
	if _, ok := c.Get("a"); ok {
		t.Fatal("Delete should remove value")
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d after Delete; want 1", c.Len())
	}

	// And: deleting a missing key is a no-op
	c.Delete("z")
	if c.Len() != 1 {
		t.Fatalf("Len() = %d after deleting missing key; want 1", c.Len())
	}
}

// TestLRUCache_Clear: clearing empties the cache entirely.
// Given a cache holding two entries, when Clear runs, then the length is 0 and previous keys miss.
// Why it matters: a Clear that emptied the map but not the recency list (or vice versa) would leave
// ghost nodes that misdirect every subsequent eviction.
func TestLRUCache_Clear(t *testing.T) {
	// Given: a cache holding two entries
	c := NewLRUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: the cache is cleared
	c.Clear()

	// Then: it is empty and previous keys miss
	if c.Len() != 0 {
		t.Fatalf("Len() = %d after Clear; want 0", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss after Clear")
	}
}

// TestLRUCache_Range: iteration visits every entry once, from least to most recently used.
// Given three entries inserted in order, when Range collects the keys, then it visits a, b, c exactly
// once each.
// Why it matters: sweeps built on Range would skip or double-process entries if iteration lost order or
// repeated keys.
func TestLRUCache_Range(t *testing.T) {
	// Given: three entries inserted in order
	c := NewLRUCache[string, int](4)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	// When: Range collects the keys
	var keys []string
	c.Range(func(k string, v int) bool {
		keys = append(keys, k)
		return true
	})

	// Then: every entry is visited once in insertion (recency) order
	want := []string{"a", "b", "c"}
	if len(keys) != len(want) {
		t.Fatalf("Range visited %d entries; want %d", len(keys), len(want))
	}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("Range order[%d] = %q; want %q", i, keys[i], k)
		}
	}
}

// TestLRUCache_RangeEarlyStop: returning false from the callback stops iteration.
// Given three entries, when the callback returns false on the second, then only two entries are visited.
// Why it matters: callers rely on early stop to bound scan work, and ignoring it would turn capped
// lookups into full scans on a hot path.
func TestLRUCache_RangeEarlyStop(t *testing.T) {
	// Given: three entries
	c := NewLRUCache[int, int](4)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30)

	// When: the callback stops after key 2
	var visited int
	c.Range(func(k, v int) bool {
		visited++
		return k != 2 // stop after key 2
	})

	// Then: iteration ends immediately
	if visited != 2 {
		t.Fatalf("Range visited %d entries; want 2", visited)
	}
}

// TestLRUCache_RangeOrder: promotion reorders iteration to follow recency.
// Given entries a, b, c with "a" then read, when Range collects the keys, then the order is b, c, a.
// Why it matters: recency order is eviction order, so an entry left unmoved after a read would be evicted
// as "oldest" right after being used.
func TestLRUCache_RangeOrder(t *testing.T) {
	// Given: three entries with the first promoted by a read
	c := NewLRUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	// Promote "a" so order becomes b, c, a.
	c.Get("a")

	// When: Range collects the keys
	var keys []string
	c.Range(func(k string, v int) bool {
		keys = append(keys, k)
		return true
	})

	// Then: iteration reflects the promoted order
	want := []string{"b", "c", "a"}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("Range order[%d] = %q; want %q", i, keys[i], k)
		}
	}
}

// TestLRUCache_ZeroMaxSize: a zero-capacity cache stores nothing.
// Given a cache built with max size 0, when a value is set, then the length stays 0 and reads miss.
// Why it matters: a zero capacity must fail closed as "cache nothing" rather than grow without any
// eviction bound.
func TestLRUCache_ZeroMaxSize(t *testing.T) {
	// Given: a cache with zero capacity
	c := NewLRUCache[string, int](0)

	// When: a value is set
	c.Set("a", 1)

	// Then: nothing is stored
	if c.Len() != 0 {
		t.Fatalf("Len() = %d; want 0 with zero max size", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss with zero max size")
	}
}

// TestLRUCache_SingleElement: a capacity-1 cache always holds exactly the newest entry.
// Given a cache of size 1, when "a" and then "b" are set, then "b" replaces "a" and the length stays 1.
// Why it matters: capacity 1 makes head and tail the same node, the easiest place for the recency list's
// bookkeeping to corrupt itself.
func TestLRUCache_SingleElement(t *testing.T) {
	// Given: a capacity-1 cache holding one entry
	c := NewLRUCache[string, int](1)
	c.Set("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}

	// When: a second entry is set
	c.Set("b", 2) // evicts "a"

	// Then: only the newest entry remains
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("Get(b) = %d, %v; want 2, true", v, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d; want 1", c.Len())
	}
}

// TestLRUCache_Len: Len tracks the number of stored entries.
// Given an empty cache, when two entries are set, then Len goes from 0 to 2.
// Why it matters: eviction triggers compare Len against capacity, so a drifting count either evicts
// early or lets the cache grow unbounded.
func TestLRUCache_Len(t *testing.T) {
	// Given: an empty cache
	c := NewLRUCache[string, int](5)
	if c.Len() != 0 {
		t.Fatalf("Len() = %d; want 0", c.Len())
	}

	// When/Then: each set is reflected in the count
	c.Set("a", 1)
	c.Set("b", 2)
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
}

// TestLRUCache_DeleteThenRefill: a delete frees a slot that a later set reuses without evicting.
// Given a full capacity-2 cache, when "a" is deleted and "c" is set, then "b" and "c" are both present at
// capacity.
// Why it matters: if delete left a ghost in the recency list, the freed slot would still count against
// capacity and the next set would evict a live entry.
func TestLRUCache_DeleteThenRefill(t *testing.T) {
	// Given: a full capacity-2 cache
	c := NewLRUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)

	// When: one entry is deleted and a new one fills the slot
	c.Delete("a")
	c.Set("c", 3)

	// Then: the cache is back at capacity with the survivor and the new entry
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be gone")
	}
	if v, _ := c.Get("b"); v != 2 {
		t.Fatalf("Get(b) = %d; want 2", v)
	}
	if v, _ := c.Get("c"); v != 3 {
		t.Fatalf("Get(c) = %d; want 3", v)
	}
}

// BenchmarkLRUCache_GetPromote measures the cost of Get on a hot key in a
// capacity-1000 cache. The old slice-based implementation was O(n) per Get
// (linear scan + slice splice on promote); the current container/list impl is
// O(1). Run with `go test -bench BenchmarkLRUCache -run none ./internal/ui`
// to lock in the improvement and catch future regressions.
func BenchmarkLRUCache_GetPromote(b *testing.B) {
	c := NewLRUCache[int, int](1000)
	for i := range 1000 {
		c.Set(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		// Touch the oldest key repeatedly to force a promote-from-back, which
		// is the worst case the old impl had to do a full scan for.
		c.Get(0)
	}
}

func BenchmarkLRUCache_SetEvict(b *testing.B) {
	c := NewLRUCache[int, int](1000)
	for i := range 1000 {
		c.Set(i, i)
	}
	i := 0
	b.ResetTimer()
	for b.Loop() {
		c.Set(1000+i, i)
		i++
	}
}
