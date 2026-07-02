package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestEnsureCacheDir_HonorsOverride: the suite's cache override redirects every persistent store.
// Given the cacheBaseOverride set by TestMain, when ensureCacheDir resolves, then the returned directory
// lives under the override, never the real user cache.
// Why it matters: if the override stopped taking effect, any test that saves a cache would write fixture
// projects into the developer's real projects_https_gitlab_com.json and corrupt their next real launch.
func TestEnsureCacheDir_HonorsOverride(t *testing.T) {
	// Given: the override installed by TestMain
	if cacheBaseOverride == "" {
		t.Fatal("expected TestMain to set cacheBaseOverride for the suite")
	}

	// When: the cache directory is resolved
	dir, err := ensureCacheDir()
	if err != nil {
		t.Fatalf("ensureCacheDir: %v", err)
	}

	// Then: it lives under the override, not the real user cache
	if !strings.HasPrefix(dir, cacheBaseOverride) {
		t.Fatalf("cache dir %q escaped the test override %q (would touch the real user cache)", dir, cacheBaseOverride)
	}
}

// TestProjectCache_SaveAndLoad: saved projects load back with their identity fields intact.
// Given two projects saved to a temp cache file, when the cache is loaded, then the file exists and each
// project's ID, name, and path round-trip.
// Why it matters: this cache is what renders instantly at startup, and a lossy round-trip would show
// wrong or blank projects until the background refetch lands.
func TestProjectCache_SaveAndLoad(t *testing.T) {
	// Given: a cache in a temp directory and two projects to persist
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.example.com",
	}

	projects := []gitlab.ProjectNode{
		{
			ID:                1,
			Name:              "test-project",
			PathWithNamespace: "org/test-project",
			Description:       "A test project",
			WebURL:            "https://gitlab.example.com/org/test-project",
			SSHURLToRepo:      "git@gitlab.example.com:org/test-project.git",
			LastActivityAt:    time.Now(),
			StarCount:         42,
			Visibility:        "private",
			DefaultBranch:     "main",
		},
		{
			ID:                2,
			Name:              "another-project",
			PathWithNamespace: "org/another-project",
			DefaultBranch:     "master",
		},
	}

	// When: the projects are saved and loaded back
	if err := cache.Save(projects); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Then: the cache file exists on disk
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Fatalf("Cache file was not created at %s", cachePath)
	}

	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// And: every project's identity fields round-trip
	if len(loaded) != len(projects) {
		t.Fatalf("Expected %d projects, got %d", len(projects), len(loaded))
	}

	for i, project := range projects {
		if loaded[i].ID != project.ID {
			t.Errorf("Project %d: ID mismatch, got %d want %d", i, loaded[i].ID, project.ID)
		}
		if loaded[i].Name != project.Name {
			t.Errorf("Project %d: Name mismatch, got %s want %s", i, loaded[i].Name, project.Name)
		}
		if loaded[i].PathWithNamespace != project.PathWithNamespace {
			t.Errorf("Project %d: Path mismatch, got %s want %s", i, loaded[i].PathWithNamespace, project.PathWithNamespace)
		}
	}
}

// TestProjectCache_LoadNonexistent: loading a missing cache file reports errCacheNotFound.
// Given a cache path that does not exist, when Load runs, then it fails with exactly errCacheNotFound.
// Why it matters: startup branches on this sentinel to fall back to a quiet foreground fetch, and any
// other error would surface a scary failure on every first run.
func TestProjectCache_LoadNonexistent(t *testing.T) {
	// Given: a cache whose file does not exist
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "nonexistent.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	// When: the cache is loaded
	_, err := cache.Load()

	// Then: the not-found sentinel comes back
	if err == nil {
		t.Fatal("Expected error when loading nonexistent cache")
	}
	if err != errCacheNotFound {
		t.Fatalf("Expected errCacheNotFound, got: %v", err)
	}
}

// TestProjectCache_SaveEmpty: saving an empty project list is a no-op that writes no file.
// Given an empty slice, when Save runs, then it succeeds and no cache file appears on disk.
// Why it matters: persisting an empty list (for example after a failed fetch) would replace a good cache
// and make the next launch start blank.
func TestProjectCache_SaveEmpty(t *testing.T) {
	// Given: a cache in a temp directory
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "empty_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	// When: an empty project list is saved
	if err := cache.Save([]gitlab.ProjectNode{}); err != nil {
		t.Fatalf("Save empty failed: %v", err)
	}

	// Then: no cache file is created
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatal("Cache file should not be created for empty projects")
	}
}

// TestProjectCache_ConcurrentSave: racing saves both succeed and leave one intact payload.
// Given two goroutines saving different single-project lists to the same path, when both complete, then
// neither errors and the file loads as exactly one of the two lists, not a blend of both.
// Why it matters: background page fetches save concurrently, and an unserialized write would tear the
// JSON and kill cache loading until the user deletes the file by hand.
func TestProjectCache_ConcurrentSave(t *testing.T) {
	// Given: one cache path and two competing project lists
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "concurrent_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	projects1 := []gitlab.ProjectNode{
		{ID: 1, Name: "project-1", PathWithNamespace: "org/project-1"},
	}
	projects2 := []gitlab.ProjectNode{
		{ID: 2, Name: "project-2", PathWithNamespace: "org/project-2"},
	}

	// When: both lists are saved concurrently
	done := make(chan error, 2)
	go func() {
		done <- cache.Save(projects1)
	}()
	go func() {
		done <- cache.Save(projects2)
	}()
	err1 := <-done
	err2 := <-done

	// Then: both saves succeed
	if err1 != nil {
		t.Errorf("First save failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Second save failed: %v", err2)
	}

	// And: the file loads as exactly one of the two payloads, uncorrupted
	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load after concurrent saves failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Expected 1 project, got %d (possible corruption)", len(loaded))
	}
	if loaded[0].ID != 1 && loaded[0].ID != 2 {
		t.Fatalf("Unexpected project ID: %d", loaded[0].ID)
	}
}

// TestProjectCache_InvalidJSON: a corrupt cache file surfaces a load error.
// Given a cache file containing invalid JSON, when Load runs, then it returns an error.
// Why it matters: decoding garbage into the project list would render junk entries, and the error lets
// startup discard the cache and refetch cleanly.
func TestProjectCache_InvalidJSON(t *testing.T) {
	// Given: a cache file holding invalid JSON
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "invalid_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	if err := os.WriteFile(cachePath, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("Failed to write invalid JSON: %v", err)
	}

	// When/Then: loading it reports an error
	_, err := cache.Load()
	if err == nil {
		t.Fatal("Expected error when loading invalid JSON")
	}
}

// TestProjectCache_VersionMismatch: a cache written with a different schema version refuses to load.
// Given a cache file with version 999, when Load runs, then it returns an error.
// Why it matters: honoring an old schema after a format change would deserialize fields into the wrong
// places instead of triggering a clean refetch.
func TestProjectCache_VersionMismatch(t *testing.T) {
	// Given: a cache file carrying an unknown version
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "version_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	wrongVersion := `{
		"version": 999,
		"host": "https://gitlab.com",
		"cached_at": "2024-01-01T00:00:00Z",
		"projects": []
	}`

	if err := os.WriteFile(cachePath, []byte(wrongVersion), 0o644); err != nil {
		t.Fatalf("Failed to write version mismatch cache: %v", err)
	}

	// When/Then: loading it reports an error
	_, err := cache.Load()
	if err == nil {
		t.Fatal("Expected error when loading cache with wrong version")
	}
}

// TestSanitizeHost: host URLs map to stable filesystem-safe cache names.
// Given hosts with and without scheme, port, and path, when each is sanitized, then it maps to its
// expected underscore form, with the empty host defaulting to https_gitlab_com.
// Why it matters: this name keys the per-host cache file, so an unstable mapping orphans the existing
// cache and cold-starts every launch.
func TestSanitizeHost(t *testing.T) {
	// Given: host inputs with their expected sanitized names
	tests := []struct {
		input string
		want  string
	}{
		{"", "https_gitlab_com"},
		{"https://gitlab.com", "https_gitlab_com"},
		{"http://gitlab.com", "http_gitlab_com"},
		{"http://gitlab.example.com", "http_gitlab_example_com"},
		{"https://gitlab.com:8080/api/v4", "https_gitlab_com-8080"},
		{"gitlab.internal", "https_gitlab_internal"},
	}

	for _, tt := range tests {
		// When/Then: sanitizing yields the expected filesystem-safe name
		got := sanitizeHost(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestSanitizeHost_NoCollisions: hosts differing only in scheme or port sanitize to distinct names.
// Given http/https and different-port variants of the same host, when both are sanitized, then the two
// names differ.
// Why it matters: colliding names make two GitLab instances overwrite each other's cache, so users see
// one host's projects listed under the other.
func TestSanitizeHost_NoCollisions(t *testing.T) {
	// Given: host pairs that would collide if scheme or port were dropped
	pairs := [][2]string{
		{"https://gitlab.com", "http://gitlab.com"},
		{"https://gitlab.com:8080", "https://gitlab.com:8443"},
	}
	for _, p := range pairs {
		// When/Then: each pair sanitizes to two distinct names
		a, b := sanitizeHost(p[0]), sanitizeHost(p[1])
		if a == b {
			t.Errorf("collision: sanitizeHost(%q) == sanitizeHost(%q) == %q", p[0], p[1], a)
		}
	}
}

// TestProjectCache_MigrateLegacy: a cache saved under the legacy filename is renamed and loaded.
// Given a valid cache file at the legacy sanitized name, when a cache pointed at the new name loads, then
// the projects come back, the legacy file is gone, and the new path exists.
// Why it matters: without the rename, everyone crossing the filename-scheme change would cold-start with
// an empty list while the stale legacy file lingered on disk forever.
func TestProjectCache_MigrateLegacy(t *testing.T) {
	dir := t.TempDir()
	host := "https://gitlab.example.com"

	// Given: a legacy-named cache file seeded in the temp dir
	legacyName := fmt.Sprintf("projects_%s.json", legacySanitizeHost(host))
	legacyPath := filepath.Join(dir, legacyName)
	payload := cacheFile{
		Version:  cacheVersion,
		Host:     host,
		CachedAt: time.Now(),
		Projects: []gitlab.ProjectNode{{ID: 1, Name: "p"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	// And: a cache pointing at the *new* sanitized path in the same dir
	newName := fmt.Sprintf("projects_%s.json", sanitizeHost(host))
	if newName == legacyName {
		t.Fatal("test precondition violated: new and legacy names match; nothing to migrate")
	}
	cache := &projectCache{
		path: filepath.Join(dir, newName),
		host: host,
	}

	// When: the cache is loaded through the new path
	got, err := cache.Load()
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}

	// Then: the projects come back and the file has moved from the legacy name to the new one
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("expected migrated project list, got %+v", got)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy file should have been renamed; still present at %s", legacyPath)
	}
	if _, err := os.Stat(cache.path); err != nil {
		t.Errorf("new path should exist after migration: %v", err)
	}
}

// TestNewProjectCache: the constructor records the host and resolves an absolute cache path.
// Given a host URL, when newProjectCache runs, then the cache keeps the host verbatim and its path is a
// non-empty absolute location.
// Why it matters: a relative cache path would scatter cache files into whatever directory the app
// happened to be launched from.
func TestNewProjectCache(t *testing.T) {
	// When: a cache is constructed for a host
	cache, err := newProjectCache("https://gitlab.example.com")
	if err != nil {
		t.Fatalf("newProjectCache failed: %v", err)
	}

	// Then: the host is kept verbatim and the path is absolute and non-empty
	if cache.host != "https://gitlab.example.com" {
		t.Errorf("Expected host https://gitlab.example.com, got %s", cache.host)
	}
	if cache.path == "" {
		t.Error("Cache path should not be empty")
	}
	if !filepath.IsAbs(cache.path) {
		t.Error("Cache path should be absolute")
	}
}

// TestProjectCache_TTLExpiration: a cache older than the TTL loads as not-found.
// Given a freshly saved cache whose CachedAt is rewritten to 25 hours ago, when Load runs, then the fresh
// load succeeded but the aged one fails with errCacheNotFound.
// Why it matters: without expiry a user returning after days still sees the stale project list with no
// signal that a refetch is due.
func TestProjectCache_TTLExpiration(t *testing.T) {
	// Given: a freshly saved single-project cache
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.example.com",
	}

	projects := []gitlab.ProjectNode{
		{
			ID:                1,
			Name:              "test-project",
			PathWithNamespace: "org/test-project",
		},
	}

	if err := cache.Save(projects); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// And: a fresh load works
	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Expected 1 project, got %d", len(loaded))
	}

	// When: the file's CachedAt is rewritten to 25 hours ago, past the 24-hour TTL
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("Failed to read cache: %v", err)
	}

	var file cacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("Failed to unmarshal cache: %v", err)
	}

	file.CachedAt = time.Now().Add(-25 * time.Hour)

	data, err = json.Marshal(file)
	if err != nil {
		t.Fatalf("Failed to marshal cache: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatalf("Failed to write cache: %v", err)
	}

	// Then: loading the aged cache reports the not-found sentinel
	_, err = cache.Load()
	if err == nil {
		t.Fatal("Expected error for expired cache, got nil")
	}
	if err != errCacheNotFound {
		t.Fatalf("Expected errCacheNotFound, got: %v", err)
	}
}

// TestNewProjectCache_DirectoryCreation: constructing a cache creates the lazylab directory on disk.
// Given a cache base with no lazylab directory yet, when newProjectCache runs, then <base>/lazylab exists
// as an owner-only directory and the returned cache path points inside it.
// Why it matters: on a fresh machine the first Save fails with a missing-directory error if the
// constructor does not create the directory itself, and group/other access would expose the cached
// project metadata on shared machines.
func TestNewProjectCache_DirectoryCreation(t *testing.T) {
	// Given: a fresh cache base with no lazylab directory yet
	base := t.TempDir()
	prev := cacheBaseOverride
	cacheBaseOverride = base
	t.Cleanup(func() { cacheBaseOverride = prev })

	// When: a cache is constructed for a host
	cache, err := newProjectCache("https://gitlab.example.com")
	if err != nil {
		t.Fatalf("newProjectCache failed: %v", err)
	}

	// Then: the lazylab directory now exists on disk under the base
	wantDir := filepath.Join(base, "lazylab")
	info, err := os.Stat(wantDir)
	if err != nil {
		t.Fatalf("cache directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("cache path %s is not a directory", wantDir)
	}

	// And: the cache file path points inside that directory
	if filepath.Dir(cache.path) != wantDir {
		t.Errorf("cache path %s should live directly in %s", cache.path, wantDir)
	}

	// And: the directory is private to the user. MkdirAll(0o700) can never add
	// group/other bits regardless of umask, so this check is deterministic.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("cache directory permissions %o grant group/other access, want owner-only", perm)
	}
}
