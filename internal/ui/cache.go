// cache.go persists the project list to disk so subsequent launches feel instant.
//
// The cache lives at ~/.cache/lazylab/projects_<host>.json, keyed by GitLab host
// to support multiple instances. It uses a versioned envelope (cacheVersion) so
// that incompatible schema changes silently invalidate stale files rather than
// crashing. A 24-hour TTL forces periodic refresh without manual intervention.
//
// Writes use an atomic temp-file-then-rename strategy: data is written to a
// temporary file in the same directory, then os.Rename swaps it in. This
// prevents partial reads if the TUI is killed mid-write or another instance
// is reading concurrently.

package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GF6599/lazylab/internal/gitlab"
)

const (
	cacheVersion = 1
	cacheTTL     = 24 * time.Hour // Cache expires after 24 hours
)

var errCacheNotFound = errors.New("cache not found")

// projectCache manages read/write access to the on-disk project list cache
// for a single GitLab host.
type projectCache struct {
	path string
	host string
}

// cacheFile is the versioned JSON envelope written to disk. Bump cacheVersion
// when the schema changes; Load will treat mismatched versions as cache misses.
type cacheFile struct {
	Version  int                  `json:"version"`
	Host     string               `json:"host"`
	CachedAt time.Time            `json:"cached_at"`
	Projects []gitlab.ProjectNode `json:"projects"`
}

// newProjectCache initializes the cache directory (~/.cache/lazylab/) and
// returns a handle scoped to the given GitLab host. The directory is created
// with 0700 permissions to avoid exposing cached project metadata to other users.
func newProjectCache(host string) (*projectCache, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("user cache dir: %w", err)
	}
	dir := filepath.Join(base, "lazylab")
	// Use 0o700 for cache directory (user-only access) for security
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	name := fmt.Sprintf("projects_%s.json", sanitizeHost(host))
	return &projectCache{
		path: filepath.Join(dir, name),
		host: host,
	}, nil
}

// Load reads the project cache from disk. It returns errCacheNotFound (treated
// as a cache miss by callers) when the file is absent, the version doesn't
// match, the TTL has expired, or the project list is empty. This lets the
// caller fall through to a live API fetch without special-casing each scenario.
func (c *projectCache) Load() ([]gitlab.ProjectNode, error) {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errCacheNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}
	var file cacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode cache: %w", err)
	}
	if file.Version != cacheVersion {
		return nil, fmt.Errorf("cache version mismatch: %d", file.Version)
	}
	// Check cache TTL - expire after 24 hours
	if !file.CachedAt.IsZero() && time.Since(file.CachedAt) > cacheTTL {
		return nil, errCacheNotFound
	}
	if len(file.Projects) == 0 {
		return nil, errCacheNotFound
	}
	return file.Projects, nil
}

// Save persists the project list atomically. It writes to a temp file first,
// then renames into place so concurrent readers never see a half-written file.
// Empty project lists are silently skipped to avoid caching error states.
func (c *projectCache) Save(projects []gitlab.ProjectNode) error {
	if len(projects) == 0 {
		return nil
	}
	payload := cacheFile{
		Version:  cacheVersion,
		Host:     c.host,
		CachedAt: time.Now(),
		Projects: projects,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}

	// Use atomic temp file creation to avoid race conditions
	dir := filepath.Dir(c.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(c.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create cache tmp: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Write data and close
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write cache tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close cache tmp: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, c.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit cache: %w", err)
	}
	return nil
}

// sanitizeHost converts a GitLab host URL into a safe filesystem name by
// stripping the scheme and replacing special characters with underscores.
func sanitizeHost(host string) string {
	if host == "" {
		host = "gitlab.com"
	}
	replacer := strings.NewReplacer("https://", "", "http://", "", "/", "_", ":", "_")
	host = replacer.Replace(host)
	host = strings.ReplaceAll(host, ".", "_")
	return host
}
