package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestFavoritesStore_SaveAndLoad: saved favorites load back complete and in the same order.
// Given three project IDs saved in a deliberate order, when the store loads them back, then all three
// return in exactly the positions they were saved in.
// Why it matters: this file is the only record of which projects a user starred, so a lossy or
// reordering round-trip would drop or scramble their pinned projects between sessions.
func TestFavoritesStore_SaveAndLoad(t *testing.T) {
	// Given: a store in a temp directory and favorites in a deliberate order
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "favorites_test.json"),
		host: "gitlab.com",
	}
	ids := []int{100, 7, 42}

	// When: the favorites are saved and loaded back
	if err := store.Save(ids); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Then: every ID round-trips in its original position
	if len(loaded) != 3 {
		t.Fatalf("Load() returned %d items, want 3", len(loaded))
	}
	for i, want := range ids {
		if loaded[i] != want {
			t.Errorf("Load()[%d] = %d, want %d", i, loaded[i], want)
		}
	}
}

// TestFavoritesStore_OrderPreservation: favorites keep their user-defined order instead of being sorted.
// Given IDs saved in descending order, when they are loaded back and the raw JSON is inspected, then
// both the returned slice and the bytes on disk keep that exact order.
// Why it matters: users reorder favorites in the TUI, and any sort-on-save would silently undo that
// arrangement on the next launch.
func TestFavoritesStore_OrderPreservation(t *testing.T) {
	// Given: favorites in descending order, so any sorting would visibly reorder them
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "favorites_order.json"),
		host: "gitlab.com",
	}
	ids := []int{300, 200, 100, 50, 1}

	// When: they are saved and loaded back
	if err := store.Save(ids); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Then: the loaded slice preserves the exact order (not sorted)
	for i, want := range ids {
		if loaded[i] != want {
			t.Errorf("Load()[%d] = %d, want %d", i, loaded[i], want)
		}
	}

	// And: the raw JSON on disk lists the IDs in the same order
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	raw := string(data)
	i300 := indexOf(raw, "300")
	i200 := indexOf(raw, "200")
	i100 := indexOf(raw, "100")
	if i300 >= i200 || i200 >= i100 {
		t.Errorf("IDs not in user-defined order in output: positions 300=%d 200=%d 100=%d", i300, i200, i100)
	}
}

// TestFavoritesStore_LoadNonexistent: loading with no favorites file succeeds with an empty list.
// Given a store whose file does not exist, when Load runs, then it returns no error and zero favorites.
// Why it matters: every first launch hits this path, and treating the missing file as an error would
// greet users who simply have not starred anything yet with a startup failure.
func TestFavoritesStore_LoadNonexistent(t *testing.T) {
	// Given: a store pointing at a file that does not exist
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "does_not_exist.json"),
		host: "gitlab.com",
	}

	// When: favorites are loaded
	loaded, err := store.Load()
	// Then: the load succeeds with zero favorites
	if err != nil {
		t.Fatalf("Load() error on nonexistent file: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("Load() returned %d items, want 0", len(loaded))
	}
}

// TestFavoritesStore_SaveEmpty: saving an empty favorites list round-trips cleanly.
// Given a fresh store, when an empty ID slice is saved and loaded back, then both operations succeed
// and the load returns zero favorites.
// Why it matters: unstarring the last favorite writes an empty list, and a failure here would either
// break the toggle or resurrect the removed favorite on the next launch.
func TestFavoritesStore_SaveEmpty(t *testing.T) {
	// Given: a store in a temp directory
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "favorites_empty.json"),
		host: "gitlab.com",
	}

	// When: an empty favorites list is saved and loaded back
	if err := store.Save([]int{}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Then: the round-trip yields zero favorites
	if len(loaded) != 0 {
		t.Fatalf("Load() returned %d items, want 0", len(loaded))
	}
}

// TestFavoritesStore_VersionMigration: a v1 favorites file reads as empty rather than trusted or fatal.
// Given an on-disk favorites file written with version 1 (the old sorted format), when Load runs,
// then it returns no error and zero favorites.
// Why it matters: v1 files hold auto-sorted IDs that no longer represent user ordering, so honoring
// them would show a wrong order while erroring would break startup for everyone upgrading.
func TestFavoritesStore_VersionMigration(t *testing.T) {
	// Given: an on-disk favorites file in the old v1 format
	dir := t.TempDir()
	path := filepath.Join(dir, "favorites_v1.json")
	v1 := favoritesFile{
		Version:    1,
		Host:       "gitlab.com",
		ProjectIDs: []int{7, 42, 100},
	}
	data, err := json.MarshalIndent(v1, "", "  ")
	if err != nil {
		t.Fatalf("marshal v1: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	// When: the current store loads it
	store := &favoritesStore{path: path, host: "gitlab.com"}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Then: the stale version reads as a cache miss, not data and not an error
	if len(loaded) != 0 {
		t.Fatalf("Load() on v1 file returned %d items, want 0 (cache miss)", len(loaded))
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
