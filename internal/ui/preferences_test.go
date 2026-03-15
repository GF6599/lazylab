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

	if err := store.Save(LayoutWide, ScreenHalf); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	layout, screen, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if layout != LayoutWide {
		t.Errorf("Load() layout = %d, want %d (LayoutWide)", layout, LayoutWide)
	}
	if screen != ScreenHalf {
		t.Errorf("Load() screen = %d, want %d (ScreenHalf)", screen, ScreenHalf)
	}
}

func TestPreferencesStore_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	store := &preferencesStore{
		path: filepath.Join(dir, "does_not_exist.json"),
		host: "gitlab.com",
	}

	layout, screen, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error on nonexistent file: %v", err)
	}
	if layout != LayoutDefault {
		t.Errorf("Load() layout = %d, want %d (LayoutDefault)", layout, LayoutDefault)
	}
	if screen != ScreenNormal {
		t.Errorf("Load() screen = %d, want %d (ScreenNormal)", screen, ScreenNormal)
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
	}
	data, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store := &preferencesStore{path: path, host: "gitlab.com"}
	layout, screen, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if layout != LayoutDefault {
		t.Errorf("Load() layout = %d, want %d (LayoutDefault)", layout, LayoutDefault)
	}
	if screen != ScreenNormal {
		t.Errorf("Load() screen = %d, want %d (ScreenNormal)", screen, ScreenNormal)
	}
}

func TestPreferencesStore_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences_bad.json")

	if err := os.WriteFile(path, []byte("{not json!!!"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store := &preferencesStore{path: path, host: "gitlab.com"}
	_, _, err := store.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid JSON, got nil")
	}
}

func TestPreferencesStore_AllModes(t *testing.T) {
	layouts := []LayoutMode{LayoutDefault, LayoutWide}
	screens := []ScreenMode{ScreenNormal, ScreenHalf, ScreenFull}

	for _, l := range layouts {
		for _, s := range screens {
			dir := t.TempDir()
			store := &preferencesStore{
				path: filepath.Join(dir, "preferences.json"),
				host: "gitlab.com",
			}

			if err := store.Save(l, s); err != nil {
				t.Fatalf("Save(%d, %d) error: %v", l, s, err)
			}

			gotLayout, gotScreen, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error after Save(%d, %d): %v", l, s, err)
			}
			if gotLayout != l {
				t.Errorf("Save(%d, %d) then Load(): layout = %d, want %d", l, s, gotLayout, l)
			}
			if gotScreen != s {
				t.Errorf("Save(%d, %d) then Load(): screen = %d, want %d", l, s, gotScreen, s)
			}
		}
	}
}
