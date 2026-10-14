package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Rust release reads and writes these files too. Its tests prove that its writer produces the
// fixtures below, so these tests hold the Go side of the same contract.
var rustFixtures = filepath.Join("..", "..", "tests", "fixtures", "store")

// TestProjectCache_DecodesTheFileTheRustReleaseWrites: the Go structs read a cache the Rust release saved.
// Given the cache the Rust tests prove the Rust writer produces, when it is decoded into cacheFile,
// then every field holds the value Rust saved, and an unset time reads as the zero time.
// Why it matters: a user who runs both releases shares one cache, so a field the Go release cannot
// read would lose data, such as the clone address, until the next refresh.
func TestProjectCache_DecodesTheFileTheRustReleaseWrites(t *testing.T) {
	// Given: the cache file the Rust release writes
	data, err := os.ReadFile(filepath.Join(rustFixtures, "rust_projects.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// When: it is decoded with the Go structs
	var file cacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Then: the envelope and both projects hold what Rust saved
	if file.Version != cacheVersion || file.TotalProjects != 135 || len(file.Projects) != 2 {
		t.Fatalf("envelope = version %d, total %d, %d projects", file.Version, file.TotalProjects, len(file.Projects))
	}
	if want := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC); !file.CachedAt.Equal(want) {
		t.Errorf("CachedAt = %v, want %v", file.CachedAt, want)
	}
	api, web := file.Projects[0], file.Projects[1]
	if api.ID != 9001 || api.PathWithNamespace != "acme/api" || api.Description != "" ||
		api.SSHURLToRepo != "git@gitlab.example.com:acme/api.git" || api.Visibility != "private" ||
		api.DefaultBranch != "main" {
		t.Errorf("api = %+v", api)
	}
	if want := time.Date(2026, 10, 13, 3, 17, 35, 257_000_000, time.UTC); !api.LastActivityAt.Equal(want) {
		t.Errorf("api.LastActivityAt = %v, want %v", api.LastActivityAt, want)
	}
	if web.ID != 9002 || web.Description != "The public site" || web.StarCount != 1 ||
		web.DefaultBranch != "" || !web.LastActivityAt.IsZero() {
		t.Errorf("web = %+v", web)
	}
}

// TestFavoritesStore_WritesTheBytesTheRustReleaseWrites: both releases write the same favorites file.
// Given two favorites in a set order, when the Go store saves them, then the file equals the fixture
// the Rust tests prove the Rust writer produces, byte for byte.
// Why it matters: the same bytes mean each release reads what the other wrote, order included.
func TestFavoritesStore_WritesTheBytesTheRustReleaseWrites(t *testing.T) {
	// Given: a store for the fixture host
	store := &favoritesStore{
		path: filepath.Join(t.TempDir(), "favorites.json"),
		host: "https://gitlab.example.com",
	}

	// When: two favorites are saved
	if err := store.Save([]int{9002, 9001}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Then: the file is the shared fixture
	got, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(rustFixtures, "favorites.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("saved:\n%s\nwant:\n%s", got, want)
	}
}
