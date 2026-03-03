package ui

import (
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

	// Save some favorites
	ids := map[int]bool{42: true, 7: true, 100: true}
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
	for _, id := range []int{7, 42, 100} {
		if !loaded[id] {
			t.Errorf("Load() missing project ID %d", id)
		}
	}

	// Verify sorted output by reading raw JSON
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	raw := string(data)
	// IDs should appear in sorted order: 7, 42, 100
	i7 := indexOf(raw, "7")
	i42 := indexOf(raw, "42")
	i100 := indexOf(raw, "100")
	if i7 >= i42 || i42 >= i100 {
		t.Errorf("IDs not sorted in output: positions 7=%d 42=%d 100=%d", i7, i42, i100)
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

	if err := store.Save(map[int]bool{}); err != nil {
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

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
