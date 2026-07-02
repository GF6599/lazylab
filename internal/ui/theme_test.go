package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestApplyTheme_SetsColors: applying any theme preset repoints the shared color vars at its palette.
// Given every theme preset in the cycle, when each is applied in turn, then the package-level text,
// error, and active color vars equal that preset's palette values.
// Why it matters: View() reads these globals on every frame, so a preset that fails to propagate
// would leave the whole UI painted in the previous theme after the user presses ~.
func TestApplyTheme_SetsColors(t *testing.T) {
	// Given: every theme preset in the cycle
	for th := ThemeRosePineMoon; th < themeCount; th++ {
		// When: the preset is applied
		applyTheme(th)

		// Then: the shared color vars carry the preset's palette values
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
	// Restore the default so later tests render with the expected palette.
	applyTheme(ThemeRosePineMoon)
}

// TestApplyTheme_RebuildStyles: applying a theme rebuilds the derived styles, not just the color vars.
// Given styles built for the previously active theme, when Tokyo Night is applied, then
// selectedItemStyle's foreground equals Tokyo Night's Active color.
// Why it matters: lipgloss styles capture colors when built, so skipping the rebuild would repoint
// the color vars but leave selection highlights frozen in the old theme.
func TestApplyTheme_RebuildStyles(t *testing.T) {
	// When: Tokyo Night is applied over whatever styles were active
	applyTheme(ThemeTokyoNight)

	// Then: the prebuilt selection style carries Tokyo Night's Active foreground
	fg := selectedItemStyle.GetForeground()
	want := lipgloss.Color(themes[ThemeTokyoNight].Active)
	if fg != want {
		t.Errorf("selectedItemStyle foreground = %v, want %v after Tokyo Night", fg, want)
	}
	// Restore the default so later tests render with the expected palette.
	applyTheme(ThemeRosePineMoon)
}

// TestNextTheme_Cycles: NextTheme steps through all ten presets and wraps back to the first.
// Given each preset paired with its expected successor, when NextTheme advances from it, then the
// successor comes back, with the last preset wrapping to Rose Pine Moon.
// Why it matters: the ~ hotkey walks this cycle, and a broken step or missing wrap would leave some
// themes unreachable or strand the user on the final one.
func TestNextTheme_Cycles(t *testing.T) {
	// Given: each preset paired with the preset that should follow it
	tests := []struct {
		input ThemeName
		want  ThemeName
	}{
		{ThemeRosePineMoon, ThemeTokyoNight},
		{ThemeTokyoNight, ThemeCatppuccinMocha},
		{ThemeCatppuccinMocha, ThemeGruvboxDark},
		{ThemeGruvboxDark, ThemeDracula},
		{ThemeDracula, ThemeNord},
		{ThemeNord, ThemeSolarizedDark},
		{ThemeSolarizedDark, ThemeKanagawa},
		{ThemeKanagawa, ThemeEverforestDark},
		{ThemeEverforestDark, ThemeOneDark},
		{ThemeOneDark, ThemeRosePineMoon},
	}
	for _, tc := range tests {
		// When/Then: advancing yields exactly the paired successor
		got := NextTheme(tc.input)
		if got != tc.want {
			t.Errorf("NextTheme(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// TestTheme_ContrastSafety: no theme paints a foreground role in the color of the background behind it.
// Given every theme's palette, when each foreground role is compared with the highlight background it
// renders on, then Muted and Subtle differ from HighlightMed and Text differs from HighlightLow.
// Why it matters: an equal pair renders literally invisible text, so a bad palette would make a
// selected row's details (or the inactive selection) vanish for every user of that theme.
func TestTheme_ContrastSafety(t *testing.T) {
	// Given: every theme preset's palette
	for th := ThemeRosePineMoon; th < themeCount; th++ {
		p := themes[th]
		name := ThemeLabel(th)

		// When/Then: each foreground role differs from the highlight background it renders on
		if p.Muted == p.HighlightMed {
			t.Errorf("%s: Muted (%s) == HighlightMed: muted text invisible on selection background", name, p.Muted)
		}
		if p.Subtle == p.HighlightMed {
			t.Errorf("%s: Subtle (%s) == HighlightMed: subtle text invisible on selection background", name, p.Subtle)
		}
		if p.Text == p.HighlightLow {
			t.Errorf("%s: Text (%s) == HighlightLow: primary text invisible on inactive selection", name, p.Text)
		}
	}
}

// TestThemeLabel: every preset maps to its display name and out-of-range values fall back to the default.
// Given all ten presets plus the invalid values -1 and 99, when ThemeLabel resolves each, then every
// preset yields its human-readable name and both invalid values yield "Rose Pine Moon".
// Why it matters: the theme value comes from the persisted preferences file, and without the fallback
// a corrupt or future-version preference would index past the names array and panic.
func TestThemeLabel(t *testing.T) {
	// Given: each preset with its display name, plus out-of-range values
	tests := []struct {
		input ThemeName
		want  string
	}{
		{ThemeRosePineMoon, "Rose Pine Moon"},
		{ThemeTokyoNight, "Tokyo Night"},
		{ThemeCatppuccinMocha, "Catppuccin Mocha"},
		{ThemeGruvboxDark, "Gruvbox Dark"},
		{ThemeDracula, "Dracula"},
		{ThemeNord, "Nord"},
		{ThemeSolarizedDark, "Solarized Dark"},
		{ThemeKanagawa, "Kanagawa"},
		{ThemeEverforestDark, "Everforest Dark"},
		{ThemeOneDark, "One Dark"},
		{ThemeName(-1), "Rose Pine Moon"},
		{ThemeName(99), "Rose Pine Moon"},
	}
	for _, tc := range tests {
		// When/Then: the label matches, with invalid values falling back to the default name
		got := ThemeLabel(tc.input)
		if got != tc.want {
			t.Errorf("ThemeLabel(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
