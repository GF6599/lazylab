package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFavoritesStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "favorites_test.json"),
		host: "gitlab.com",
	}

	// Save some favorites in a specific order
	ids := []int{100, 7, 42}
	if err := store.Save(ids); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load them back
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("Load() returned %d items, want 3", len(loaded))
	}
	// Verify order is preserved
	for i, want := range ids {
		if loaded[i] != want {
			t.Errorf("Load()[%d] = %d, want %d", i, loaded[i], want)
		}
	}
}

func TestFavoritesStore_OrderPreservation(t *testing.T) {
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "favorites_order.json"),
		host: "gitlab.com",
	}

	// Save in reverse order
	ids := []int{300, 200, 100, 50, 1}
	if err := store.Save(ids); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Order must be exactly preserved (not sorted)
	for i, want := range ids {
		if loaded[i] != want {
			t.Errorf("Load()[%d] = %d, want %d", i, loaded[i], want)
		}
	}

	// Verify raw JSON preserves order
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

func TestFavoritesStore_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "does_not_exist.json"),
		host: "gitlab.com",
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error on nonexistent file: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("Load() returned %d items, want 0", len(loaded))
	}
}

func TestFavoritesStore_SaveEmpty(t *testing.T) {
	dir := t.TempDir()
	store := &favoritesStore{
		path: filepath.Join(dir, "favorites_empty.json"),
		host: "gitlab.com",
	}

	if err := store.Save([]int{}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("Load() returned %d items, want 0", len(loaded))
	}
}

func TestFavoritesStore_VersionMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "favorites_v1.json")

	// Write a v1 file (old format with sorted IDs)
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

	store := &favoritesStore{path: path, host: "gitlab.com"}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// Old version should be treated as cache miss (nil/empty)
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
