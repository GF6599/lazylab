package ui

import (
	"errors"
	"testing"
)

func TestAsyncCache_GetSet(t *testing.T) {
	c := NewAsyncCache[int, string]()

	// Initially empty.
	if _, ok := c.Get(1); ok {
		t.Fatal("expected miss on empty cache")
	}

	c.Set(1, "hello")
	v, ok := c.Get(1)
	if !ok || v != "hello" {
		t.Fatalf("Get(1) = %q, %v; want %q, true", v, ok, "hello")
	}
}

func TestAsyncCache_Loading(t *testing.T) {
	c := NewAsyncCache[int, string]()

	if c.IsLoading(1) {
		t.Fatal("should not be loading initially")
	}

	c.SetLoading(1)
	if !c.IsLoading(1) {
		t.Fatal("should be loading after SetLoading")
	}

	// Set clears loading.
	c.Set(1, "done")
	if c.IsLoading(1) {
		t.Fatal("Set should clear loading")
	}
}

func TestAsyncCache_Err(t *testing.T) {
	c := NewAsyncCache[int, string]()
	testErr := errors.New("boom")

	c.SetLoading(1)
	c.SetErr(1, testErr)

	if c.IsLoading(1) {
		t.Fatal("SetErr should clear loading")
	}
	if !errors.Is(c.Err(1), testErr) {
		t.Fatalf("Err(1) = %v; want %v", c.Err(1), testErr)
	}

	// Set clears error.
	c.Set(1, "ok")
	if c.Err(1) != nil {
		t.Fatal("Set should clear error")
	}
}

func TestAsyncCache_Delete(t *testing.T) {
	c := NewAsyncCache[int, string]()
	c.Set(1, "v")
	c.SetLoading(1)
	c.SetErr(1, errors.New("e"))

	c.Delete(1)
	if _, ok := c.Get(1); ok {
		t.Fatal("Delete should remove value")
	}
	if c.IsLoading(1) {
		t.Fatal("Delete should remove loading")
	}
	if c.Err(1) != nil {
		t.Fatal("Delete should remove error")
	}
}

func TestAsyncCache_Clear(t *testing.T) {
	c := NewAsyncCache[int, string]()
	c.Set(1, "a")
	c.Set(2, "b")

	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Len() = %d after Clear; want 0", c.Len())
	}
}

func TestAsyncCache_Len(t *testing.T) {
	c := NewAsyncCache[string, int]()
	if c.Len() != 0 {
		t.Fatalf("Len() = %d; want 0", c.Len())
	}
	c.Set("a", 1)
	c.Set("b", 2)
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
}

func TestAsyncCache_Keys(t *testing.T) {
	c := NewAsyncCache[int, string]()
	if got := c.Keys(); len(got) != 0 {
		t.Fatalf("Keys() on empty cache = %v; want empty", got)
	}
	c.Set(7, "a")
	c.Set(3, "b")
	c.Set(5, "c")
	got := c.Keys()
	if len(got) != 3 {
		t.Fatalf("Keys() = %v; want 3 entries", got)
	}
	// Map iteration order is not stable, so check via set membership.
	want := map[int]bool{3: true, 5: true, 7: true}
	for _, k := range got {
		if !want[k] {
			t.Errorf("unexpected key %d in Keys()", k)
		}
	}
}

func TestAsyncCache_KeysExcludesLoadingOnlyEntries(t *testing.T) {
	// SetLoading without Set should not count as a key — Keys reflects
	// fully-cached values only.
	c := NewAsyncCache[int, string]()
	c.SetLoading(1)
	if got := c.Keys(); len(got) != 0 {
		t.Errorf("Keys() = %v; loading-only keys should not appear", got)
	}
}

func TestAsyncCache_LoadingMapMirrorsLoadingState(t *testing.T) {
	c := NewAsyncCache[int, string]()
	c.SetLoading(1)
	c.SetLoading(2)
	c.Set(2, "done") // should clear 2 from loading map
	loading := c.LoadingMap()
	if !loading[1] {
		t.Error("LoadingMap() missing key 1")
	}
	if loading[2] {
		t.Error("LoadingMap() should not contain key 2 after Set")
	}
}
