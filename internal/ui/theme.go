package ui

import "github.com/charmbracelet/lipgloss"

// ThemeName identifies a color theme preset.
type ThemeName int

const (
	ThemeRosePineMoon ThemeName = iota
	ThemeTokyoNight
	ThemeCatppuccinMocha
	ThemeGruvboxDark
	themeCount // sentinel for cycling
)

var themeNames = [themeCount]string{
	"Rose Pine Moon",
	"Tokyo Night",
	"Catppuccin Mocha",
	"Gruvbox Dark",
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

// themePalette holds the 10 hex color values that define a theme.
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
}

// currentTheme tracks the active theme; read by save/load preferences.
var currentTheme ThemeName

// applyTheme sets the 10 global color vars from the given theme's palette
// and rebuilds all derived styles.
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

	rebuildStyles()
}

// rebuildStyles reassigns all global style vars from the current color vars.
func rebuildStyles() {
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	itemStyle = lipgloss.NewStyle().Foreground(colorText)
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(colorActive).Background(colorHighlightLow)
	statusStyle = lipgloss.NewStyle().Faint(true).Foreground(colorSubtle)
	errorStyle = lipgloss.NewStyle().Foreground(colorError)
	searchStyle = lipgloss.NewStyle().Faint(true).Foreground(colorSubtle)
	progressStyle = lipgloss.NewStyle().Faint(true).Foreground(colorMuted)
	pipelineSuccess = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	pipelineFailed = lipgloss.NewStyle().Bold(true).Foreground(colorError)
	pipelineRunning = lipgloss.NewStyle().Bold(true).Foreground(colorActive)
	pipelinePending = lipgloss.NewStyle().Bold(true).Foreground(colorWarning)
	pipelineCanceled = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	pipelineSkipped = lipgloss.NewStyle().Bold(true).Foreground(colorSubtle)
	pipelineUnknown = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)

	paneBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorSubtle)
	paneBorderFocusStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorActive)
	explorerHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	explorerFocusHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorActive)
	detailHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	detailDividerStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	detailLabelStyle = lipgloss.NewStyle().Foreground(colorMuted)
	detailValueStyle = lipgloss.NewStyle().Foreground(colorText)
	explorerPathStyle = lipgloss.NewStyle().Foreground(colorMuted)
	explorerHintStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	explorerErrorStyle = lipgloss.NewStyle().Foreground(colorError)
	diffAddStyle = lipgloss.NewStyle().Foreground(colorSuccess)
	diffDelStyle = lipgloss.NewStyle().Foreground(colorError)
	diffHunkStyle = lipgloss.NewStyle().Foreground(colorWarning)

	rebuildPipelineStatusStyles()
}
