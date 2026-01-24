package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazylab/internal/gitlab"
)

const (
	cacheVersion = 1
	cacheTTL     = 24 * time.Hour // Cache expires after 24 hours
)

var errCacheNotFound = errors.New("cache not found")

type projectCache struct {
	path string
	host string
}

type cacheFile struct {
	Version  int                  `json:"version"`
	Host     string               `json:"host"`
	CachedAt time.Time            `json:"cached_at"`
	Projects []gitlab.ProjectNode `json:"projects"`
}

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
		os.Remove(tmpPath)
		return fmt.Errorf("write cache tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close cache tmp: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, c.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("commit cache: %w", err)
	}
	return nil
}

func sanitizeHost(host string) string {
	if host == "" {
		host = "gitlab.com"
	}
	replacer := strings.NewReplacer("https://", "", "http://", "", "/", "_", ":", "_")
	host = replacer.Replace(host)
	host = strings.ReplaceAll(host, ".", "_")
	return host
}
