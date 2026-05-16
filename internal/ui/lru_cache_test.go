package ui

import "testing"

func TestLRUCache_GetSet(t *testing.T) {
	c := NewLRUCache[string, int](3)

	// Initially empty.
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss on empty cache")
	}

	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	c := NewLRUCache[string, int](2)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // should evict "a"

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

func TestLRUCache_GetPromotes(t *testing.T) {
	c := NewLRUCache[string, int](2)

	c.Set("a", 1)
	c.Set("b", 2)

	// Access "a" to promote it; "b" becomes the oldest.
	c.Get("a")
	c.Set("c", 3) // should evict "b", not "a"

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

func TestLRUCache_SetExistingNoEviction(t *testing.T) {
	c := NewLRUCache[string, int](2)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("a", 10) // update existing, should not evict anything

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

func TestLRUCache_SetExistingPromotes(t *testing.T) {
	c := NewLRUCache[string, int](2)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("a", 10) // update promotes "a"; "b" is now oldest
	c.Set("c", 3)  // should evict "b"

	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}
	if v, _ := c.Get("a"); v != 10 {
		t.Fatalf("Get(a) = %d; want 10", v)
	}
}

func TestLRUCache_Delete(t *testing.T) {
	c := NewLRUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)

	c.Delete("a")

	if _, ok := c.Get("a"); ok {
		t.Fatal("Delete should remove value")
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d after Delete; want 1", c.Len())
	}

	// Deleting a missing key is a no-op.
	c.Delete("z")
	if c.Len() != 1 {
		t.Fatalf("Len() = %d after deleting missing key; want 1", c.Len())
	}
}

func TestLRUCache_Clear(t *testing.T) {
	c := NewLRUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)

	c.Clear()

	if c.Len() != 0 {
		t.Fatalf("Len() = %d after Clear; want 0", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss after Clear")
	}
}

func TestLRUCache_Range(t *testing.T) {
	c := NewLRUCache[string, int](4)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	var keys []string
	c.Range(func(k string, v int) bool {
		keys = append(keys, k)
		return true
	})

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

func TestLRUCache_RangeEarlyStop(t *testing.T) {
	c := NewLRUCache[int, int](4)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30)

	var visited int
	c.Range(func(k, v int) bool {
		visited++
		return k != 2 // stop after key 2
	})

	if visited != 2 {
		t.Fatalf("Range visited %d entries; want 2", visited)
	}
}

func TestLRUCache_RangeOrder(t *testing.T) {
	c := NewLRUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	// Promote "a" so order becomes b, c, a.
	c.Get("a")

	var keys []string
	c.Range(func(k string, v int) bool {
		keys = append(keys, k)
		return true
	})

	want := []string{"b", "c", "a"}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("Range order[%d] = %q; want %q", i, keys[i], k)
		}
	}
}

func TestLRUCache_ZeroMaxSize(t *testing.T) {
	c := NewLRUCache[string, int](0)

	c.Set("a", 1)

	if c.Len() != 0 {
		t.Fatalf("Len() = %d; want 0 with zero max size", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected miss with zero max size")
	}
}

func TestLRUCache_SingleElement(t *testing.T) {
	c := NewLRUCache[string, int](1)

	c.Set("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}

	c.Set("b", 2) // evicts "a"
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

func TestLRUCache_Len(t *testing.T) {
	c := NewLRUCache[string, int](5)

	if c.Len() != 0 {
		t.Fatalf("Len() = %d; want 0", c.Len())
	}
	c.Set("a", 1)
	c.Set("b", 2)
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
}

func TestLRUCache_DeleteThenRefill(t *testing.T) {
	c := NewLRUCache[string, int](2)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Delete("a")
	c.Set("c", 3)

	// Should still be at capacity (2), "b" and "c" present.
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
