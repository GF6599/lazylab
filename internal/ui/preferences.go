// preferences.go persists per-host panel layout preferences to disk.
//
// Preferences are stored under the OS cache directory (os.UserCacheDir) in a
// "lazylab" subdirectory as preferences_<host>.json, using the same host-keyed
// strategy as the project cache and favorites. The file format is versioned
// (preferencesVersion) for forward compatibility. Writes use the same atomic
// temp+rename pattern as cache.go to prevent corruption.

package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const preferencesVersion = 2

// preferencesStore manages read/write access to the per-host preferences file.
type preferencesStore struct {
	path string
	host string
}

// preferencesFile is the versioned JSON envelope persisted to disk.
type preferencesFile struct {
	Version    int        `json:"version"`
	Host       string     `json:"host"`
	LayoutMode LayoutMode `json:"layout_mode"`
	ScreenMode ScreenMode `json:"screen_mode"`
	Theme      ThemeName  `json:"theme"`
}

// newPreferencesStore initializes the cache directory and returns a store
// scoped to the given GitLab host.
func newPreferencesStore(host string) (*preferencesStore, error) {
	dir, err := ensureCacheDir()
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("preferences_%s.json", sanitizeHost(host))
	return &preferencesStore{
		path: filepath.Join(dir, name),
		host: host,
	}, nil
}

// Load reads preferences from disk. Returns zero-value defaults (not an error)
// when the file doesn't exist or the version doesn't match, so first-run
// callers don't need special handling.
func (s *preferencesStore) Load() (LayoutMode, ScreenMode, ThemeName, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return LayoutDefault, ScreenNormal, ThemeRosePine, nil
	}
	if err != nil {
		return LayoutDefault, ScreenNormal, ThemeRosePine, fmt.Errorf("read preferences: %w", err)
	}
	var file preferencesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return LayoutDefault, ScreenNormal, ThemeRosePine, fmt.Errorf("decode preferences: %w", err)
	}
	if file.Version != preferencesVersion {
		return LayoutDefault, ScreenNormal, ThemeRosePine, nil
	}
	// Validate enum ranges
	if file.LayoutMode < LayoutDefault || file.LayoutMode > LayoutWide {
		return LayoutDefault, ScreenNormal, ThemeRosePine, nil
	}
	if file.ScreenMode < ScreenNormal || file.ScreenMode > ScreenFull {
		return LayoutDefault, ScreenNormal, ThemeRosePine, nil
	}
	theme := file.Theme
	if theme < 0 || theme >= themeCount {
		theme = ThemeRosePine
	}
	return file.LayoutMode, file.ScreenMode, theme, nil
}

// Save writes preferences atomically using temp+rename.
func (s *preferencesStore) Save(layout LayoutMode, screen ScreenMode, theme ThemeName) error {
	payload := preferencesFile{
		Version:    preferencesVersion,
		Host:       s.host,
		LayoutMode: layout,
		ScreenMode: screen,
		Theme:      theme,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode preferences: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create preferences tmp: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write preferences tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close preferences tmp: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit preferences: %w", err)
	}
	return nil
}
