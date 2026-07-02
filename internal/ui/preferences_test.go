package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPreferencesStore_SaveAndLoad: saved layout, screen, and theme settings load back intact.
// Given a store in a temp directory, when LayoutWide, ScreenHalf, and ThemeTokyoNight are saved and then
// loaded, then all three values round-trip unchanged.
// Why it matters: a lossy round-trip silently resets the user's chosen layout and theme on every launch.
func TestPreferencesStore_SaveAndLoad(t *testing.T) {
	// Given: a preferences store in a temp directory
	dir := t.TempDir()
	store := &preferencesStore{
		path: filepath.Join(dir, "preferences_test.json"),
		host: "gitlab.com",
	}

	// When: non-default preferences are saved and loaded back
	if err := store.Save(LayoutWide, ScreenHalf, ThemeTokyoNight); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	layout, screen, theme, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Then: all three values round-trip unchanged
	if layout != LayoutWide {
		t.Errorf("Load() layout = %d, want %d (LayoutWide)", layout, LayoutWide)
	}
	if screen != ScreenHalf {
		t.Errorf("Load() screen = %d, want %d (ScreenHalf)", screen, ScreenHalf)
	}
	if theme != ThemeTokyoNight {
		t.Errorf("Load() theme = %d, want %d (ThemeTokyoNight)", theme, ThemeTokyoNight)
	}
}

// TestPreferencesStore_LoadNonexistent: a missing preferences file loads as defaults without error.
// Given a store pointed at a path that does not exist, when Load runs, then it returns LayoutDefault,
// ScreenNormal, and ThemeRosePineMoon with a nil error.
// Why it matters: first launch has no preferences file, so an error here would break startup for every
// new user.
func TestPreferencesStore_LoadNonexistent(t *testing.T) {
	// Given: a store whose file does not exist
	dir := t.TempDir()
	store := &preferencesStore{
		path: filepath.Join(dir, "does_not_exist.json"),
		host: "gitlab.com",
	}

	// When: the preferences are loaded
	layout, screen, theme, err := store.Load()
	// Then: defaults come back with no error
	if err != nil {
		t.Fatalf("Load() error on nonexistent file: %v", err)
	}
	if layout != LayoutDefault {
		t.Errorf("Load() layout = %d, want %d (LayoutDefault)", layout, LayoutDefault)
	}
	if screen != ScreenNormal {
		t.Errorf("Load() screen = %d, want %d (ScreenNormal)", screen, ScreenNormal)
	}
	if theme != ThemeRosePineMoon {
		t.Errorf("Load() theme = %d, want %d (ThemeRosePineMoon)", theme, ThemeRosePineMoon)
	}
}

// TestPreferencesStore_VersionMismatch: a preferences file from another schema version falls back to
// defaults without error.
// Given a preferences file written with version 999 and non-default values, when Load runs, then the
// stored values are ignored and the defaults come back with a nil error.
// Why it matters: honoring fields from a stale schema could load meaningless enum values, and erroring
// would break startup for everyone who saved preferences before a format change.
func TestPreferencesStore_VersionMismatch(t *testing.T) {
	// Given: a preferences file with an unknown version and non-default values
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences_old.json")

	old := preferencesFile{
		Version:    999,
		Host:       "gitlab.com",
		LayoutMode: LayoutWide,
		ScreenMode: ScreenFull,
		Theme:      ThemeGruvboxDark,
	}
	data, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// When: the preferences are loaded
	store := &preferencesStore{path: path, host: "gitlab.com"}
	layout, screen, theme, err := store.Load()
	// Then: the stored values are ignored in favor of the defaults, with no error
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if layout != LayoutDefault {
		t.Errorf("Load() layout = %d, want %d (LayoutDefault)", layout, LayoutDefault)
	}
	if screen != ScreenNormal {
		t.Errorf("Load() screen = %d, want %d (ScreenNormal)", screen, ScreenNormal)
	}
	if theme != ThemeRosePineMoon {
		t.Errorf("Load() theme = %d, want %d (ThemeRosePineMoon)", theme, ThemeRosePineMoon)
	}
}

// TestPreferencesStore_InvalidJSON: a corrupt preferences file surfaces a load error.
// Given a file containing invalid JSON, when Load runs, then it returns a non-nil error.
// Why it matters: decoding garbage as preferences would apply arbitrary layout and theme values, and the
// explicit error lets the caller fall back deliberately instead.
func TestPreferencesStore_InvalidJSON(t *testing.T) {
	// Given: a preferences file that is not valid JSON
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences_bad.json")

	if err := os.WriteFile(path, []byte("{not json!!!"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// When/Then: loading it reports an error
	store := &preferencesStore{path: path, host: "gitlab.com"}
	_, _, _, err := store.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid JSON, got nil")
	}
}

// TestPreferencesStore_AllModes: every layout, screen, and theme combination round-trips exactly.
// Given each of the 2x3x4 mode combinations, when it is saved to a fresh store and loaded, then the same
// three values come back.
// Why it matters: Load validates enum ranges, so an off-by-one in those bounds would silently reset a
// legitimate setting (such as the last theme in the list) to defaults.
func TestPreferencesStore_AllModes(t *testing.T) {
	// Given: every combination of layout, screen mode, and theme
	layouts := []LayoutMode{LayoutDefault, LayoutWide}
	screens := []ScreenMode{ScreenNormal, ScreenHalf, ScreenFull}
	themesList := []ThemeName{ThemeRosePineMoon, ThemeTokyoNight, ThemeCatppuccinMocha, ThemeGruvboxDark}

	for _, l := range layouts {
		for _, s := range screens {
			for _, th := range themesList {
				// When: the combination is saved to a fresh store and loaded back
				dir := t.TempDir()
				store := &preferencesStore{
					path: filepath.Join(dir, "preferences.json"),
					host: "gitlab.com",
				}

				if err := store.Save(l, s, th); err != nil {
					t.Fatalf("Save(%d, %d, %d) error: %v", l, s, th, err)
				}

				gotLayout, gotScreen, gotTheme, err := store.Load()
				if err != nil {
					t.Fatalf("Load() error after Save(%d, %d, %d): %v", l, s, th, err)
				}

				// Then: the exact values round-trip
				if gotLayout != l {
					t.Errorf("Save(%d, %d, %d) then Load(): layout = %d, want %d", l, s, th, gotLayout, l)
				}
				if gotScreen != s {
					t.Errorf("Save(%d, %d, %d) then Load(): screen = %d, want %d", l, s, th, gotScreen, s)
				}
				if gotTheme != th {
					t.Errorf("Save(%d, %d, %d) then Load(): theme = %d, want %d", l, s, th, gotTheme, th)
				}
			}
		}
	}
}

// TestPreferencesStore_InvalidTheme: an out-of-range theme value falls back to the default theme.
// Given a current-version preferences file whose theme is 99, when Load runs, then it succeeds and the
// theme comes back as ThemeRosePineMoon.
// Why it matters: the theme indexes a fixed palette array, so honoring an out-of-range value from an old
// or hand-edited file would panic on the first render.
func TestPreferencesStore_InvalidTheme(t *testing.T) {
	// Given: a current-version preferences file with an out-of-range theme
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences_bad_theme.json")

	file := preferencesFile{
		Version:    preferencesVersion,
		Host:       "gitlab.com",
		LayoutMode: LayoutDefault,
		ScreenMode: ScreenNormal,
		Theme:      ThemeName(99),
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// When: the preferences are loaded
	store := &preferencesStore{path: path, host: "gitlab.com"}
	_, _, theme, err := store.Load()
	// Then: the load succeeds and the theme is clamped to the default
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if theme != ThemeRosePineMoon {
		t.Errorf("Load() theme = %d, want %d (ThemeRosePineMoon) for invalid theme value", theme, ThemeRosePineMoon)
	}
}
