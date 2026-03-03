// favorites.go persists per-host favorite project IDs to disk.
//
// Favorites are stored at ~/.cache/lazylab/favorites_<host>.json, using the
// same host-keyed strategy as the project cache so each GitLab instance has
// its own set. The file format is versioned (favoritesVersion) for forward
// compatibility. Writes use the same atomic temp+rename pattern as cache.go
// to prevent corruption.
//
// The in-memory representation is a map[int]bool for O(1) lookup during
// rendering; on disk it's stored as a sorted int slice for stability.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const favoritesVersion = 1

// favoritesStore manages read/write access to the per-host favorites file.
type favoritesStore struct {
	path string
	host string
}

// favoritesFile is the versioned JSON envelope. ProjectIDs are sorted on write
// for deterministic output (easier to diff/debug).
type favoritesFile struct {
	Version    int    `json:"version"`
	Host       string `json:"host"`
	ProjectIDs []int  `json:"project_ids"`
}

// newFavoritesStore initializes the cache directory and returns a store
// scoped to the given GitLab host.
func newFavoritesStore(host string) (*favoritesStore, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("user cache dir: %w", err)
	}
	dir := filepath.Join(base, "lazylab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	name := fmt.Sprintf("favorites_%s.json", sanitizeHost(host))
	return &favoritesStore{
		path: filepath.Join(dir, name),
		host: host,
	}, nil
}

// Load reads favorites from disk. Returns an empty map (not an error) when
// the file doesn't exist, so first-run callers don't need special handling.
func (s *favoritesStore) Load() (map[int]bool, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return make(map[int]bool), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read favorites: %w", err)
	}
	var file favoritesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode favorites: %w", err)
	}
	if file.Version != favoritesVersion {
		return nil, fmt.Errorf("favorites version mismatch: %d", file.Version)
	}
	result := make(map[int]bool, len(file.ProjectIDs))
	for _, id := range file.ProjectIDs {
		result[id] = true
	}
	return result, nil
}

// Save writes favorites atomically using temp+rename. IDs are sorted before
// writing for deterministic output.
func (s *favoritesStore) Save(ids map[int]bool) error {
	sorted := make([]int, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Ints(sorted)

	payload := favoritesFile{
		Version:    favoritesVersion,
		Host:       s.host,
		ProjectIDs: sorted,
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
		os.Remove(tmpPath)
		return fmt.Errorf("write favorites tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close favorites tmp: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("commit favorites: %w", err)
	}
	return nil
}
