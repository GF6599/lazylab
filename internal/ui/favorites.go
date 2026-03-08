// favorites.go persists per-host favorite project IDs to disk.
//
// Favorites are stored under the OS cache directory (os.UserCacheDir) in a
// "lazylab" subdirectory as favorites_<host>.json, using the same host-keyed
// strategy as the project cache so each GitLab instance has its own set. The
// file format is versioned (favoritesVersion) for forward compatibility.
// Writes use the same atomic temp+rename pattern as cache.go to prevent
// corruption.
//
// The on-disk slice preserves user-defined ordering so favorites can be
// reordered in the TUI and the order persists across sessions. Callers
// derive a map[int]bool from the slice for O(1) membership lookup.

package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const favoritesVersion = 2

// favoritesStore manages read/write access to the per-host favorites file.
type favoritesStore struct {
	path string
	host string
}

// favoritesFile is the versioned JSON envelope. ProjectIDs preserve
// user-defined ordering (no sorting on write).
type favoritesFile struct {
	Version    int    `json:"version"`
	Host       string `json:"host"`
	ProjectIDs []int  `json:"project_ids"`
}

// newFavoritesStore initializes the cache directory and returns a store
// scoped to the given GitLab host.
func newFavoritesStore(host string) (*favoritesStore, error) {
	dir, err := ensureCacheDir()
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("favorites_%s.json", sanitizeHost(host))
	return &favoritesStore{
		path: filepath.Join(dir, name),
		host: host,
	}, nil
}

// Load reads favorites from disk. Returns an empty slice (not an error) when
// the file doesn't exist, so first-run callers don't need special handling.
// The returned slice preserves on-disk ordering (user-defined).
func (s *favoritesStore) Load() ([]int, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read favorites: %w", err)
	}
	var file favoritesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode favorites: %w", err)
	}
	if file.Version != favoritesVersion {
		// Stale version (e.g. v1 sorted-only format) — treat as cache miss
		return nil, nil
	}
	return file.ProjectIDs, nil
}

// Save writes favorites atomically using temp+rename. The slice order is
// preserved as-is (user-defined ordering).
func (s *favoritesStore) Save(ids []int) error {
	payload := favoritesFile{
		Version:    favoritesVersion,
		Host:       s.host,
		ProjectIDs: ids,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode favorites: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create favorites tmp: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write favorites tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close favorites tmp: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit favorites: %w", err)
	}
	return nil
}
