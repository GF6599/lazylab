package ui

import (
	"errors"
	"testing"
)

// TestAsyncCache_GetSet: a stored value is returned on lookup and an unset key reports a miss.
// Given a fresh cache, when a key is looked up before and after Set, then the first lookup misses and
// the second returns the stored value with a hit.
// Why it matters: panels branch on this hit/miss answer to choose between rendering cached data and
// fetching, so a phantom hit would render empty content and a lost value would refetch every refresh.
func TestAsyncCache_GetSet(t *testing.T) {
	// Given: a fresh, empty cache
	c := NewAsyncCache[int, string]()

	// When/Then: a key that was never set misses
	if _, ok := c.Get(1); ok {
		t.Fatal("expected miss on empty cache")
	}

	// When: a value is stored for the key
	c.Set(1, "hello")

	// Then: the lookup returns that value with a hit
	v, ok := c.Get(1)
	if !ok || v != "hello" {
		t.Fatalf("Get(1) = %q, %v; want %q, true", v, ok, "hello")
	}
}

// TestAsyncCache_Loading: SetLoading marks a key as in flight and Set clears the mark.
// Given a fresh cache, when a key is marked loading and then receives its value, then IsLoading flips
// from false to true and back to false.
// Why it matters: this flag suppresses duplicate fetches and drives spinners, so a mark that never
// clears would pin a panel on its spinner and block every future refresh of that key.
func TestAsyncCache_Loading(t *testing.T) {
	// Given: a fresh cache
	c := NewAsyncCache[int, string]()

	// When/Then: a key that was never marked reports not loading
	if c.IsLoading(1) {
		t.Fatal("should not be loading initially")
	}

	// When: the key is marked loading
	c.SetLoading(1)

	// Then: it reports loading
	if !c.IsLoading(1) {
		t.Fatal("should be loading after SetLoading")
	}

	// And: storing the value clears the mark
	c.Set(1, "done")
	if c.IsLoading(1) {
		t.Fatal("Set should clear loading")
	}
}

// TestAsyncCache_Err: a failed load records its error and a later success clears it.
// Given a key marked loading, when SetErr and then a successful Set run for it, then the error
// replaces the loading flag and the new value wipes the error.
// Why it matters: a loading flag that survives failure would pin a spinner over the error message,
// and a sticky error would keep a failure banner up after a retry already succeeded.
func TestAsyncCache_Err(t *testing.T) {
	// Given: a key that is mid-load
	c := NewAsyncCache[int, string]()
	testErr := errors.New("boom")
	c.SetLoading(1)

	// When: the load fails
	c.SetErr(1, testErr)

	// Then: the key stops loading and surfaces the recorded error
	if c.IsLoading(1) {
		t.Fatal("SetErr should clear loading")
	}
	if !errors.Is(c.Err(1), testErr) {
		t.Fatalf("Err(1) = %v; want %v", c.Err(1), testErr)
	}

	// And: a later successful Set clears the error
	c.Set(1, "ok")
	if c.Err(1) != nil {
		t.Fatal("Set should clear error")
	}
}

// TestAsyncCache_Delete: deleting a key removes its value, loading flag, and error together.
// Given a key holding a cached value and a recorded error, when Delete runs, then Get misses and both
// IsLoading and Err report nothing for it.
// Why it matters: state surviving a delete would show a ghost spinner or stale error for a job whose
// data is gone, misreporting it until the next full reload.
func TestAsyncCache_Delete(t *testing.T) {
	// Given: a key with a value, then a loading mark, then an error (SetErr clears the mark)
	c := NewAsyncCache[int, string]()
	c.Set(1, "v")
	c.SetLoading(1)
	c.SetErr(1, errors.New("e"))

	// When: the key is deleted
	c.Delete(1)

	// Then: no value, loading flag, or error remains
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

// TestAsyncCache_Clear: clearing the cache empties it completely.
// Given a cache holding two values, when Clear runs, then Len reports zero entries.
// Why it matters: Clear backs resetCaches on pipeline reloads and page changes, and entries surviving
// it would keep rendering the previous page's stages and jobs as if they were current.
func TestAsyncCache_Clear(t *testing.T) {
	// Given: a cache holding two values
	c := NewAsyncCache[int, string]()
	c.Set(1, "a")
	c.Set(2, "b")

	// When: the cache is cleared
	c.Clear()

	// Then: no entries remain
	if c.Len() != 0 {
		t.Fatalf("Len() = %d after Clear; want 0", c.Len())
	}
}

// TestAsyncCache_Len: Len counts exactly the values stored so far.
// Given a fresh cache, when two values are stored, then Len goes from zero to two.
// Why it matters: evictOldLogs compares Len against the log-cache cap, so an overcount would evict
// logs users are still reading and an undercount would let the cache grow without bound.
func TestAsyncCache_Len(t *testing.T) {
	// Given: a fresh cache
	c := NewAsyncCache[string, int]()

	// When/Then: Len is zero before any value is stored
	if c.Len() != 0 {
		t.Fatalf("Len() = %d; want 0", c.Len())
	}

	// When: two values are stored
	c.Set("a", 1)
	c.Set("b", 2)

	// Then: Len counts both
	if c.Len() != 2 {
		t.Fatalf("Len() = %d; want 2", c.Len())
	}
}

// TestAsyncCache_Keys: Keys returns exactly the stored keys, in no guaranteed order.
// Given three stored values, when Keys is read, then it holds those three keys and nothing else.
// Why it matters: evictOldLogs sorts Keys to pick the oldest job logs to drop, so a missing or extra
// key would evict the wrong log or leak entries past the cache cap.
func TestAsyncCache_Keys(t *testing.T) {
	// Given: a fresh cache
	c := NewAsyncCache[int, string]()

	// When/Then: Keys is empty before any value is stored
	if got := c.Keys(); len(got) != 0 {
		t.Fatalf("Keys() on empty cache = %v; want empty", got)
	}

	// When: three values are stored
	c.Set(7, "a")
	c.Set(3, "b")
	c.Set(5, "c")

	// Then: Keys holds exactly those three keys
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

// TestAsyncCache_KeysExcludesLoadingOnlyEntries: a key that is only loading does not appear in Keys.
// Given a key marked loading but never given a value, when Keys is read, then it comes back empty.
// Why it matters: eviction treats Keys as cached entries it may delete, so listing an in-flight key
// would over-evict real logs and wipe the loading marks that guard against duplicate fetches.
func TestAsyncCache_KeysExcludesLoadingOnlyEntries(t *testing.T) {
	// Given: a key marked loading with no value stored
	c := NewAsyncCache[int, string]()
	c.SetLoading(1)

	// When/Then: Keys omits the loading-only key
	if got := c.Keys(); len(got) != 0 {
		t.Errorf("Keys() = %v; loading-only keys should not appear", got)
	}
}

// TestAsyncCache_LoadingMapMirrorsLoadingState: LoadingMap holds exactly the keys still in flight.
// Given two keys marked loading, when one completes via Set, then LoadingMap still contains the
// in-flight key and no longer the completed one.
// Why it matters: shouldFetchPipelineData reads this map to skip fetches already in flight, so a
// stale entry would silently block a pipeline's refresh forever and a missing one would double-fetch.
func TestAsyncCache_LoadingMapMirrorsLoadingState(t *testing.T) {
	// Given: two keys marked loading
	c := NewAsyncCache[int, string]()
	c.SetLoading(1)
	c.SetLoading(2)

	// When: one of them completes with a value
	c.Set(2, "done")

	// Then: LoadingMap keeps the in-flight key and drops the completed one
	loading := c.LoadingMap()
	if !loading[1] {
		t.Error("LoadingMap() missing key 1")
	}
	if loading[2] {
		t.Error("LoadingMap() should not contain key 2 after Set")
	}
}
