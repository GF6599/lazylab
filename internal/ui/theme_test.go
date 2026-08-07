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
	for th := ThemeRosePine; th < themeCount; th++ {
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
	applyTheme(ThemeRosePine)
}

// TestApplyTheme_RebuildStyles: applying a theme rebuilds the derived styles, not just the color vars.
// Given styles built for the previously active theme, when Tokyo Night is applied, then
// selectedItemStyle's foreground equals the color Tokyo Night marks the current row with.
// Why it matters: lipgloss styles capture colors when built, so skipping the rebuild would repoint
// the color vars but leave the marked row frozen in the old theme.
func TestApplyTheme_RebuildStyles(t *testing.T) {
	// When: Tokyo Night is applied over whatever styles were active
	applyTheme(ThemeTokyoNight)

	// Then: the prebuilt marked-row style carries Tokyo Night's marked foreground
	fg := selectedItemStyle.GetForeground()
	want := lipgloss.Color(themes[ThemeTokyoNight].Accent)
	if fg != want {
		t.Errorf("selectedItemStyle foreground = %v, want %v after Tokyo Night", fg, want)
	}
	// Restore the default so later tests render with the expected palette.
	applyTheme(ThemeRosePine)
}

// TestNextTheme_Cycles: NextTheme steps through all ten presets and wraps back to the first.
// Given each preset paired with its expected successor, when NextTheme advances from it, then the
// successor comes back, with the last preset wrapping to Rose Pine.
// Why it matters: the ~ hotkey walks this cycle, and a broken step or missing wrap would leave some
// themes unreachable or strand the user on the final one.
func TestNextTheme_Cycles(t *testing.T) {
	// Given: each preset paired with the preset that should follow it
	tests := []struct {
		input ThemeName
		want  ThemeName
	}{
		{ThemeRosePine, ThemeTokyoNight},
		{ThemeTokyoNight, ThemeCatppuccinMocha},
		{ThemeCatppuccinMocha, ThemeGruvboxDark},
		{ThemeGruvboxDark, ThemeDracula},
		{ThemeDracula, ThemeNord},
		{ThemeNord, ThemeSolarizedDark},
		{ThemeSolarizedDark, ThemeKanagawa},
		{ThemeKanagawa, ThemeEverforestDark},
		{ThemeEverforestDark, ThemeOneDark},
		{ThemeOneDark, ThemeRosePine},
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
	for th := ThemeRosePine; th < themeCount; th++ {
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
// preset yields its human-readable name and both invalid values yield "Rose Pine".
// Why it matters: the theme value comes from the persisted preferences file, and without the fallback
// a corrupt or future-version preference would index past the names array and panic.
func TestThemeLabel(t *testing.T) {
	// Given: each preset with its display name, plus out-of-range values
	tests := []struct {
		input ThemeName
		want  string
	}{
		{ThemeRosePine, "Rose Pine"},
		{ThemeTokyoNight, "Tokyo Night"},
		{ThemeCatppuccinMocha, "Catppuccin Mocha"},
		{ThemeGruvboxDark, "Gruvbox Dark"},
		{ThemeDracula, "Dracula"},
		{ThemeNord, "Nord"},
		{ThemeSolarizedDark, "Solarized Dark"},
		{ThemeKanagawa, "Kanagawa"},
		{ThemeEverforestDark, "Everforest Dark"},
		{ThemeOneDark, "One Dark"},
		{ThemeName(-1), "Rose Pine"},
		{ThemeName(99), "Rose Pine"},
	}
	for _, tc := range tests {
		// When/Then: the label matches, with invalid values falling back to the default name
		got := ThemeLabel(tc.input)
		if got != tc.want {
			t.Errorf("ThemeLabel(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestMarkerColors_AreGoldAndIris: the marker is gold and the row it marks is iris.
// Given the default preset, when a theme is applied, then the marker color is the palette's gold and
// the marked color is its iris, and the marked row carries no background.
// Why it matters: the brackets are chrome saying "this one" and the label is the item, so collapsing
// the two onto one color, or filling the row behind them, loses the distinction they exist to draw.
func TestMarkerColors_AreGoldAndIris(t *testing.T) {
	// Given/When: the default preset is applied
	applyTheme(ThemeRosePine)

	// Then: the marker takes gold and the marked row takes iris
	if want := lipgloss.Color("#f6c177"); colorMarker != want {
		t.Errorf("colorMarker = %v, want %v (gold)", colorMarker, want)
	}
	if want := lipgloss.Color("#c4a7e7"); colorMarked != want {
		t.Errorf("colorMarked = %v, want %v (iris)", colorMarked, want)
	}

	// And: the marked row carries no background, because a fill is not a marker
	if bg := selectedItemStyle.GetBackground(); bg != (lipgloss.NoColor{}) {
		t.Errorf("marked row has background %v, the bracket pair is the marker", bg)
	}
}

// TestDefaultTheme_IsRosePineMain: the preset the app starts in carries the house Rose Pine palette.
// Given the Rose Pine (main) palette under its published slot names, when the default preset is read,
// then every one of its ten slots carries that palette's value.
// Why it matters: the palette is declared once for every surface, so a slot that drifts here leaves
// the tool as the only surface that does not match, which stays invisible until two sit side by side.
func TestDefaultTheme_IsRosePineMain(t *testing.T) {
	// Given: Rose Pine (main), by the names the palette publishes
	const (
		hlLow  = "#21202e"
		hlMed  = "#403d52"
		text   = "#e0def4"
		muted  = "#6e6a86"
		subtle = "#908caa"
		pine   = "#31748f"
		foam   = "#9ccfd8"
		gold   = "#f6c177"
		iris   = "#c4a7e7"
		love   = "#eb6f92"
	)
	want := themePalette{
		HighlightLow: hlLow, HighlightMed: hlMed, Text: text, Muted: muted, Subtle: subtle,
		Success: pine, Active: foam, Warning: gold, Accent: iris, Error: love,
	}

	// When: the preset the app starts in is read
	got := themes[ThemeRosePine]

	// Then: every slot carries the palette's value
	if got != want {
		t.Errorf("default preset =\n  %+v\nwant\n  %+v", got, want)
	}
}
