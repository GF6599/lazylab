package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestApplyTheme_SetsColors(t *testing.T) {
	for th := ThemeRosePineMoon; th < themeCount; th++ {
		applyTheme(th)
		p := themes[th]
		if colorText != lipgloss.Color(p.Text) {
			t.Errorf("applyTheme(%d): colorText = %v, want %v", th, colorText, p.Text)
		}
		if colorError != lipgloss.Color(p.Error) {
			t.Errorf("applyTheme(%d): colorError = %v, want %v", th, colorError, p.Error)
		}
		if colorActive != lipgloss.Color(p.Active) {
			t.Errorf("applyTheme(%d): colorActive = %v, want %v", th, colorActive, p.Active)
		}
	}
	// Restore default
	applyTheme(ThemeRosePineMoon)
}

func TestApplyTheme_RebuildStyles(t *testing.T) {
	applyTheme(ThemeTokyoNight)
	fg := selectedItemStyle.GetForeground()
	want := lipgloss.Color(themes[ThemeTokyoNight].Active)
	if fg != want {
		t.Errorf("selectedItemStyle foreground = %v, want %v after Tokyo Night", fg, want)
	}
	// Restore default
	applyTheme(ThemeRosePineMoon)
}

func TestNextTheme_Cycles(t *testing.T) {
	tests := []struct {
		input ThemeName
		want  ThemeName
	}{
		{ThemeRosePineMoon, ThemeTokyoNight},
		{ThemeTokyoNight, ThemeCatppuccinMocha},
		{ThemeCatppuccinMocha, ThemeGruvboxDark},
		{ThemeGruvboxDark, ThemeRosePineMoon},
	}
	for _, tc := range tests {
		got := NextTheme(tc.input)
		if got != tc.want {
			t.Errorf("NextTheme(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestThemeLabel(t *testing.T) {
	tests := []struct {
		input ThemeName
		want  string
	}{
		{ThemeRosePineMoon, "Rose Pine Moon"},
		{ThemeTokyoNight, "Tokyo Night"},
		{ThemeCatppuccinMocha, "Catppuccin Mocha"},
		{ThemeGruvboxDark, "Gruvbox Dark"},
		{ThemeName(-1), "Rose Pine Moon"},
		{ThemeName(99), "Rose Pine Moon"},
	}
	for _, tc := range tests {
		got := ThemeLabel(tc.input)
		if got != tc.want {
			t.Errorf("ThemeLabel(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
