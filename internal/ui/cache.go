// cache.go persists the project list to disk so subsequent launches feel instant.
//
// The cache lives under the OS cache directory (os.UserCacheDir) in a "lazylab"
// subdirectory — e.g. ~/Library/Caches/lazylab on macOS, ~/.cache/lazylab on
// Linux. Files are keyed by GitLab host to support multiple instances. The format
// uses a versioned envelope (cacheVersion) so that incompatible schema changes
// silently invalidate stale files rather than crashing. A 24-hour TTL forces
// periodic refresh without manual intervention.
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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GF6599/lazylab/internal/gitlab"
)

const (
	// Version 2 added TotalProjects. The bump also invalidates version-1 files
	// on purpose: they hold only the first fetched page while claiming to be
	// the whole collection, so honoring them would keep the truncation alive.
	cacheVersion = 2
	cacheTTL     = 24 * time.Hour // Cache expires after 24 hours
)

var errCacheNotFound = errors.New("cache not found")

// cacheBaseOverride, when non-empty, replaces os.UserCacheDir as the base
// directory for the lazylab cache. It exists solely so the test suite (via
// TestMain) can redirect persistent stores to a throwaway directory: a
// NewModel-based test that drives saveCacheCmd would otherwise write test
// fixtures into the developer's real cache for whatever host resolves
// (sanitizeHost("") == "https_gitlab_com", i.e. gitlab.com). Empty in
// production runs.
var cacheBaseOverride string

// ensureCacheDir returns the lazylab cache directory path, creating it if needed.
func ensureCacheDir() (string, error) {
	base := cacheBaseOverride
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("user cache dir: %w", err)
		}
	}
	dir := filepath.Join(base, "lazylab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	return dir, nil
}

// projectCache manages read/write access to the on-disk project list cache
// for a single GitLab host.
type projectCache struct {
	path string
	host string
}

// cacheFile is the versioned JSON envelope written to disk. Bump cacheVersion
// when the schema changes; Load will treat mismatched versions as cache misses.
// TotalProjects records the collection size the server reported, which can be
// larger than len(Projects): the cache may hold only the pages loaded so far.
type cacheFile struct {
	Version       int                  `json:"version"`
	Host          string               `json:"host"`
	CachedAt      time.Time            `json:"cached_at"`
	TotalProjects int                  `json:"total_projects"`
	Projects      []gitlab.ProjectNode `json:"projects"`
}

// newProjectCache initializes the cache directory and returns a handle scoped
// to the given GitLab host.
func newProjectCache(host string) (*projectCache, error) {
	dir, err := ensureCacheDir()
	if err != nil {
		return nil, err
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
//
// On the first miss for a given host, Load attempts to migrate a legacy-
// sanitized cache file (the pre-collision-fix name) into the current scheme so
// users don't lose their cache on upgrade.
func (c *projectCache) Load() ([]gitlab.ProjectNode, int, error) {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) && c.migrateLegacy() {
		data, err = os.ReadFile(c.path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, errCacheNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read cache: %w", err)
	}
	var file cacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, 0, fmt.Errorf("decode cache: %w", err)
	}
	if file.Version != cacheVersion {
		return nil, 0, fmt.Errorf("cache version mismatch: %d", file.Version)
	}
	// Check cache TTL - expire after 24 hours
	if !file.CachedAt.IsZero() && time.Since(file.CachedAt) > cacheTTL {
		return nil, 0, errCacheNotFound
	}
	if len(file.Projects) == 0 {
		return nil, 0, errCacheNotFound
	}
	return file.Projects, file.TotalProjects, nil
}

// Save persists the project list atomically. It writes to a temp file first,
// then renames into place so concurrent readers never see a half-written file.
// Empty project lists are silently skipped to avoid caching error states.
// totalProjects is the server-reported collection size, clamped up to the
// slice length so a caller passing a stale or zero total can never mark a
// cache as smaller than what it holds.
func (c *projectCache) Save(projects []gitlab.ProjectNode, totalProjects int) error {
	if len(projects) == 0 {
		return nil
	}
	payload := cacheFile{
		Version:       cacheVersion,
		Host:          c.host,
		CachedAt:      time.Now(),
		TotalProjects: max(totalProjects, len(projects)),
		Projects:      projects,
	}
	data, err := json.Marshal(payload)
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
		_ = tmpFile.Close()
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

// sanitizeHost converts a GitLab host URL into a deterministic, collision-free
// filesystem name. The scheme is preserved as a prefix so that http:// and
// https:// against the same host produce distinct cache files; the port is
// joined with "-" so it cannot collide with a hostname containing the same
// digits joined by underscores; dots become underscores for readability.
//
// Inputs without a scheme are treated as https:// so a bare "gitlab.internal"
// behaves the same as "https://gitlab.internal".
func sanitizeHost(host string) string {
	if host == "" {
		host = "https://gitlab.com"
	}
	if !strings.HasPrefix(host, "https://") && !strings.HasPrefix(host, "http://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return legacySanitizeHost(host)
	}
	h := strings.ReplaceAll(u.Host, ":", "-")
	h = strings.ReplaceAll(h, ".", "_")
	return u.Scheme + "_" + h
}

// legacySanitizeHost reproduces the pre-collision-fix sanitization scheme so
// that [projectCache.migrateLegacy] can locate cache files written by older
// versions of lazylab. Do not call from new code paths.
func legacySanitizeHost(host string) string {
	if host == "" {
		host = "gitlab.com"
	}
	replacer := strings.NewReplacer("https://", "", "http://", "", "/", "_", ":", "_")
	host = replacer.Replace(host)
	host = strings.ReplaceAll(host, ".", "_")
	return host
}

// migrateLegacy moves a cache file written under the legacy sanitization scheme
// to the current location. Returns true if a rename succeeded. Best-effort:
// failures (missing file, target already present, permission errors) all return
// false and let Load fall through to a fresh fetch.
func (c *projectCache) migrateLegacy() bool {
	legacyName := fmt.Sprintf("projects_%s.json", legacySanitizeHost(c.host))
	if filepath.Base(c.path) == legacyName {
		return false
	}
	legacyPath := filepath.Join(filepath.Dir(c.path), legacyName)
	if _, err := os.Stat(legacyPath); err != nil {
		return false
	}
	if _, err := os.Stat(c.path); err == nil {
		return false
	}
	return os.Rename(legacyPath, c.path) == nil
}
