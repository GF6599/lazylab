package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazylab/internal/gitlab"
)

func TestProjectCache_SaveAndLoad(t *testing.T) {
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

	// Test Save
	if err := cache.Save(projects); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Fatalf("Cache file was not created at %s", cachePath)
	}

	// Test Load
	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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

func TestProjectCache_LoadNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "nonexistent.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	_, err := cache.Load()
	if err == nil {
		t.Fatal("Expected error when loading nonexistent cache")
	}
	if err != errCacheNotFound {
		t.Fatalf("Expected errCacheNotFound, got: %v", err)
	}
}

func TestProjectCache_SaveEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "empty_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	// Save empty slice should be no-op
	if err := cache.Save([]gitlab.ProjectNode{}); err != nil {
		t.Fatalf("Save empty failed: %v", err)
	}

	// File should not be created
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatal("Cache file should not be created for empty projects")
	}
}

func TestProjectCache_ConcurrentSave(t *testing.T) {
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

	done := make(chan error, 2)

	// Concurrent saves should not race
	go func() {
		done <- cache.Save(projects1)
	}()
	go func() {
		done <- cache.Save(projects2)
	}()

	// Wait for both to complete
	err1 := <-done
	err2 := <-done

	if err1 != nil {
		t.Errorf("First save failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Second save failed: %v", err2)
	}

	// File should exist and be valid
	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load after concurrent saves failed: %v", err)
	}

	// Should have either projects1 or projects2, not corrupted
	if len(loaded) != 1 {
		t.Fatalf("Expected 1 project, got %d (possible corruption)", len(loaded))
	}

	if loaded[0].ID != 1 && loaded[0].ID != 2 {
		t.Fatalf("Unexpected project ID: %d", loaded[0].ID)
	}
}

func TestProjectCache_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "invalid_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	// Write invalid JSON
	if err := os.WriteFile(cachePath, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("Failed to write invalid JSON: %v", err)
	}

	_, err := cache.Load()
	if err == nil {
		t.Fatal("Expected error when loading invalid JSON")
	}
}

func TestProjectCache_VersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "version_cache.json")

	cache := &projectCache{
		path: cachePath,
		host: "https://gitlab.com",
	}

	// Write cache with wrong version
	wrongVersion := `{
		"version": 999,
		"host": "https://gitlab.com",
		"cached_at": "2024-01-01T00:00:00Z",
		"projects": []
	}`

	if err := os.WriteFile(cachePath, []byte(wrongVersion), 0o644); err != nil {
		t.Fatalf("Failed to write version mismatch cache: %v", err)
	}

	_, err := cache.Load()
	if err == nil {
		t.Fatal("Expected error when loading cache with wrong version")
	}
}

func TestSanitizeHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "gitlab_com"},
		{"https://gitlab.com", "gitlab_com"},
		{"http://gitlab.example.com", "gitlab_example_com"},
		{"https://gitlab.com:8080/api/v4", "gitlab_com_8080_api_v4"},
		{"gitlab.internal", "gitlab_internal"},
	}

	for _, tt := range tests {
		got := sanitizeHost(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNewProjectCache(t *testing.T) {
	cache, err := newProjectCache("https://gitlab.example.com")
	if err != nil {
		t.Fatalf("newProjectCache failed: %v", err)
	}

	if cache.host != "https://gitlab.example.com" {
		t.Errorf("Expected host https://gitlab.example.com, got %s", cache.host)
	}

	if cache.path == "" {
		t.Error("Cache path should not be empty")
	}

	// Verify path contains sanitized host
	if !filepath.IsAbs(cache.path) {
		t.Error("Cache path should be absolute")
	}
}

func TestProjectCache_TTLExpiration(t *testing.T) {
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

	// Save cache
	if err := cache.Save(projects); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load immediately - should work
	loaded, err := cache.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Expected 1 project, got %d", len(loaded))
	}

	// Manually modify cache file to have old timestamp
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("Failed to read cache: %v", err)
	}

	var file cacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("Failed to unmarshal cache: %v", err)
	}

	// Set timestamp to 25 hours ago (beyond TTL)
	file.CachedAt = time.Now().Add(-25 * time.Hour)

	data, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal cache: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatalf("Failed to write cache: %v", err)
	}

	// Load should now fail due to TTL expiration
	_, err = cache.Load()
	if err == nil {
		t.Fatal("Expected error for expired cache, got nil")
	}
	if err != errCacheNotFound {
		t.Fatalf("Expected errCacheNotFound, got: %v", err)
	}
}

func TestNewProjectCache_DirectoryCreation(t *testing.T) {
	// Test that newProjectCache creates the cache directory
	cache, err := newProjectCache("https://gitlab.example.com")
	if err != nil {
		t.Fatalf("newProjectCache failed: %v", err)
	}

	// Get the cache directory
	cacheDir := filepath.Dir(cache.path)

	// Check the directory exists
	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("Cache directory does not exist: %v", err)
	}

	// Verify it's a directory
	if !info.IsDir() {
		t.Fatal("Cache path is not a directory")
	}

	// Log the permissions for informational purposes
	// Note: Actual permissions may vary by platform and umask
	perm := info.Mode().Perm()
	t.Logf("Cache directory created with permissions: %o", perm)

	// Verify the cache path is under the cache directory
	if !strings.HasPrefix(cache.path, cacheDir) {
		t.Errorf("Cache path %s should be under directory %s", cache.path, cacheDir)
	}
}
