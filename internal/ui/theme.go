// theme.go defines color theme presets and the runtime theme-switching mechanism.
//
// The TUI enforces its own palette so colors render consistently regardless of
// the terminal's native theme. Ten presets are available; the user cycles through them
// with the ~ hotkey. The selected theme is persisted via the preferences store.
//
// Architecture: applyTheme sets 10 package-level color vars and rebuilds ~30
// style vars (in project_list_style.go). Since Bubble Tea calls View() on
// every frame reading these globals, the next render picks up the new palette
// automatically. Bubble Tea sub-components that cache their own style copies
// (spinner, help, paginator, table) are refreshed separately by
// Model.refreshThemeSubComponents.

package ui

import "github.com/charmbracelet/lipgloss"

// ThemeName identifies a color theme preset. Persisted as an int in the
// preferences JSON; values must remain stable across versions.
type ThemeName int

// The available theme presets, ordered as the user cycles through them with the
// ~ hotkey. Their iota values are the persisted preference, so existing entries
// must keep their order; append new presets before themeCount.
const (
	ThemeRosePineMoon ThemeName = iota
	ThemeTokyoNight
	ThemeCatppuccinMocha
	ThemeGruvboxDark
	ThemeDracula
	ThemeNord
	ThemeSolarizedDark
	ThemeKanagawa
	ThemeEverforestDark
	ThemeOneDark
	themeCount // sentinel for cycling
)

var themeNames = [themeCount]string{
	"Rose Pine Moon",
	"Tokyo Night",
	"Catppuccin Mocha",
	"Gruvbox Dark",
	"Dracula",
	"Nord",
	"Solarized Dark",
	"Kanagawa",
	"Everforest Dark",
	"One Dark",
}

// ThemeLabel returns the display name for a theme.
func ThemeLabel(t ThemeName) string {
	if t >= 0 && t < themeCount {
		return themeNames[t]
	}
	return themeNames[ThemeRosePineMoon]
}

// NextTheme returns the next theme in the cycle, wrapping around.
func NextTheme(t ThemeName) ThemeName {
	return (t + 1) % themeCount
}

// themePalette holds the 10 semantic hex color slots that define a theme.
// Each slot maps to a UI role (e.g. Active = focused borders, selected items;
// Error = failed pipelines, error messages). Palettes are sourced from
// community color schemes and curated for low-contrast dark terminals.
type themePalette struct {
	HighlightLow string
	HighlightMed string
	Text         string
	Muted        string
	Subtle       string
	Success      string
	Active       string
	Warning      string
	Accent       string
	Error        string
}

var themes = [themeCount]themePalette{
	// Rose Pine Moon
	{
		HighlightLow: "#2a283e",
		HighlightMed: "#44415a",
		Text:         "#e0def4",
		Muted:        "#6e6a86",
		Subtle:       "#908caa",
		Success:      "#31748f",
		Active:       "#9ccfd8",
		Warning:      "#f6c177",
		Accent:       "#c4a7e7",
		Error:        "#eb6f92",
	},
	// Tokyo Night
	{
		HighlightLow: "#292e42",
		HighlightMed: "#3b4261",
		Text:         "#c0caf5",
		Muted:        "#565f89",
		Subtle:       "#737aa2",
		Success:      "#9ece6a",
		Active:       "#7dcfff",
		Warning:      "#e0af68",
		Accent:       "#bb9af7",
		Error:        "#f7768e",
	},
	// Catppuccin Mocha
	{
		HighlightLow: "#313244",
		HighlightMed: "#45475a",
		Text:         "#cdd6f4",
		Muted:        "#6c7086",
		Subtle:       "#9399b2",
		Success:      "#a6e3a1",
		Active:       "#89dceb",
		Warning:      "#f9e2af",
		Accent:       "#cba6f7",
		Error:        "#f38ba8",
	},
	// Gruvbox Dark
	{
		HighlightLow: "#3c3836",
		HighlightMed: "#504945",
		Text:         "#ebdbb2",
		Muted:        "#928374",
		Subtle:       "#a89984",
		Success:      "#b8bb26",
		Active:       "#83a598",
		Warning:      "#fabd2f",
		Accent:       "#d3869b",
		Error:        "#fb4934",
	},
	// Dracula
	{
		HighlightLow: "#44475a",
		HighlightMed: "#6272a4",
		Text:         "#f8f8f2",
		Muted:        "#7c8dba",
		Subtle:       "#bd93f9",
		Success:      "#50fa7b",
		Active:       "#8be9fd",
		Warning:      "#f1fa8c",
		Accent:       "#ff79c6",
		Error:        "#ff5555",
	},
	// Nord
	{
		HighlightLow: "#3b4252",
		HighlightMed: "#434c5e",
		Text:         "#eceff4",
		Muted:        "#4c566a",
		Subtle:       "#81a1c1",
		Success:      "#a3be8c",
		Active:       "#88c0d0",
		Warning:      "#ebcb8b",
		Accent:       "#b48ead",
		Error:        "#bf616a",
	},
	// Solarized Dark
	{
		HighlightLow: "#073642",
		HighlightMed: "#094959",
		Text:         "#839496",
		Muted:        "#586e75",
		Subtle:       "#93a1a1",
		Success:      "#859900",
		Active:       "#2aa198",
		Warning:      "#b58900",
		Accent:       "#6c71c4",
		Error:        "#dc322f",
	},
	// Kanagawa
	{
		HighlightLow: "#2a2a37",
		HighlightMed: "#363646",
		Text:         "#dcd7ba",
		Muted:        "#727169",
		Subtle:       "#938aa9",
		Success:      "#76946a",
		Active:       "#7e9cd8",
		Warning:      "#e6c384",
		Accent:       "#957fb8",
		Error:        "#c34043",
	},
	// Everforest Dark
	{
		HighlightLow: "#374145",
		HighlightMed: "#4a555b",
		Text:         "#d3c6aa",
		Muted:        "#859289",
		Subtle:       "#9da9a0",
		Success:      "#a7c080",
		Active:       "#7fbbb3",
		Warning:      "#dbbc7f",
		Accent:       "#d699b6",
		Error:        "#e67e80",
	},
	// One Dark
	{
		HighlightLow: "#3e4452",
		HighlightMed: "#4b5263",
		Text:         "#abb2bf",
		Muted:        "#5c6370",
		Subtle:       "#848b98",
		Success:      "#98c379",
		Active:       "#61afef",
		Warning:      "#e5c07b",
		Accent:       "#c678dd",
		Error:        "#e06c75",
	},
}

// currentTheme tracks the active theme; read by save/load preferences.
var currentTheme ThemeName

// applyTheme sets the 10 global color vars from the given theme's palette,
// rebuilds all derived style vars, and updates currentTheme. Out-of-range
// values fall back to ThemeRosePineMoon.
//
// This only updates package-level globals. Bubble Tea sub-components that
// store their own style copies (spinner, help, paginator, stage table) must
// be refreshed separately via Model.refreshThemeSubComponents, and
// Model.clearGlamourRenderers should be called to drop stale glamour
// renderers compiled against the previous theme.
func applyTheme(t ThemeName) {
	if t < 0 || t >= themeCount {
		t = ThemeRosePineMoon
	}
	currentTheme = t
	p := themes[t]

	colorHighlightLow = lipgloss.Color(p.HighlightLow)
	colorHighlightMed = lipgloss.Color(p.HighlightMed)
	colorText = lipgloss.Color(p.Text)
	colorMuted = lipgloss.Color(p.Muted)
	colorSubtle = lipgloss.Color(p.Subtle)
	colorSuccess = lipgloss.Color(p.Success)
	colorActive = lipgloss.Color(p.Active)
	colorWarning = lipgloss.Color(p.Warning)
	colorAccent = lipgloss.Color(p.Accent)
	colorError = lipgloss.Color(p.Error)

	// Rose Pine names the marker gold and the marked iris, which are the same
	// two hues a theme already carries as its warning and accent. Aliasing them
	// keeps every preset consistent without a second copy of the hex.
	colorMarker = colorWarning
	colorMarked = colorAccent

	rebuildStyles()
}

// rebuildStyles reassigns all ~30 global style vars from the current color
// vars. Called by applyTheme after updating the color vars.
func rebuildStyles() {
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	itemStyle = lipgloss.NewStyle().Foreground(colorText)
	markerStyle = lipgloss.NewStyle().Foreground(colorMarker)
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(colorMarked)
	errorStyle = lipgloss.NewStyle().Foreground(colorError)
	searchStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	progressStyle = lipgloss.NewStyle().Foreground(colorMuted)
	pipelineSuccess = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	pipelineFailed = lipgloss.NewStyle().Bold(true).Foreground(colorError)
	pipelineRunning = lipgloss.NewStyle().Bold(true).Foreground(colorActive)
	pipelinePending = lipgloss.NewStyle().Bold(true).Foreground(colorWarning)
	pipelineCanceled = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	pipelineSkipped = lipgloss.NewStyle().Bold(true).Foreground(colorSubtle)
	pipelineUnknown = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)

	paneBorderStyle = lipgloss.NewStyle().Border(frameBorder).BorderForeground(colorSubtle)
	paneBorderFocusStyle = lipgloss.NewStyle().Border(frameBorder).BorderForeground(colorActive)
	explorerHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	explorerFocusHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorActive)
	detailHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	detailDividerStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	detailLabelStyle = lipgloss.NewStyle().Foreground(colorMuted)
	detailValueStyle = lipgloss.NewStyle().Foreground(colorText)
	explorerPathStyle = lipgloss.NewStyle().Foreground(colorMuted)
	explorerHintStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	explorerErrorStyle = lipgloss.NewStyle().Foreground(colorError)
	diffAddStyle = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	diffDelStyle = lipgloss.NewStyle().Bold(true).Foreground(colorError)
	diffHunkStyle = lipgloss.NewStyle().Foreground(colorWarning)

	// Border and layout styles (cached to avoid per-frame allocation)
	borderUnfocusedStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	borderFocusedStyle = lipgloss.NewStyle().Foreground(colorActive)
	borderTitleUnfocusedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	borderTitleFocusedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorActive)
	scrollThumbStyle = lipgloss.NewStyle().Foreground(colorActive)
	activeTabStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	inactiveTabStyle = lipgloss.NewStyle().Foreground(colorMuted)
	tabSepStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	borderFooterStyle = lipgloss.NewStyle().Foreground(colorMuted)
	infoBarStatusStyle = lipgloss.NewStyle().Foreground(colorActive)
	infoBarHintsStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	infoBarContextStyle = lipgloss.NewStyle().Foreground(colorMuted)
	diffCursorBgStyle = lipgloss.NewStyle().Background(colorHighlightMed)
	modalLabelStyle = lipgloss.NewStyle().Foreground(colorMuted)
	modalFocusLabelStyle = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	modalBorderStyle = lipgloss.NewStyle().
		Border(frameBorder).
		BorderForeground(colorSubtle).
		Padding(1, 2)

	mrTextareaBaseStyle = lipgloss.NewStyle().Foreground(colorText)
	mrTextareaPlaceholderStyle = lipgloss.NewStyle().Foreground(colorMuted)
	mrTextareaCursorLineStyle = lipgloss.NewStyle().Foreground(colorText).Background(colorHighlightLow)
	mrTextareaPromptStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	mrTextareaCursorStyle = lipgloss.NewStyle().Foreground(colorActive)
	mrTextinputTextStyle = lipgloss.NewStyle().Foreground(colorText)
	mrTextinputPlaceholderSt = lipgloss.NewStyle().Foreground(colorMuted)
	mrTextinputPromptStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	mrTextinputCursorStyle = lipgloss.NewStyle().Foreground(colorActive)

	rebuildPipelineStatusStyles()
}
