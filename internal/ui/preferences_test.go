package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := &preferencesStore{
		path: filepath.Join(dir, "preferences_test.json"),
		host: "gitlab.com",
	}

	if err := store.Save(LayoutWide, ScreenHalf, ThemeTokyoNight); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	layout, screen, theme, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
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

func TestPreferencesStore_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	store := &preferencesStore{
		path: filepath.Join(dir, "does_not_exist.json"),
		host: "gitlab.com",
	}

	layout, screen, theme, err := store.Load()
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

func TestPreferencesStore_VersionMismatch(t *testing.T) {
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

	store := &preferencesStore{path: path, host: "gitlab.com"}
	layout, screen, theme, err := store.Load()
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

func TestPreferencesStore_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences_bad.json")

	if err := os.WriteFile(path, []byte("{not json!!!"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store := &preferencesStore{path: path, host: "gitlab.com"}
	_, _, _, err := store.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid JSON, got nil")
	}
}

func TestPreferencesStore_AllModes(t *testing.T) {
	layouts := []LayoutMode{LayoutDefault, LayoutWide}
	screens := []ScreenMode{ScreenNormal, ScreenHalf, ScreenFull}
	themesList := []ThemeName{ThemeRosePineMoon, ThemeTokyoNight, ThemeCatppuccinMocha, ThemeGruvboxDark}

	for _, l := range layouts {
		for _, s := range screens {
			for _, th := range themesList {
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

func TestPreferencesStore_InvalidTheme(t *testing.T) {
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

	store := &preferencesStore{path: path, host: "gitlab.com"}
	_, _, theme, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if theme != ThemeRosePineMoon {
		t.Errorf("Load() theme = %d, want %d (ThemeRosePineMoon) for invalid theme value", theme, ThemeRosePineMoon)
	}
}
