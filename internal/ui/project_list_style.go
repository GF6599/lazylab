// Rose Pine Moon color palette and lipgloss styles for the TUI.
//
// Rose Pine Moon was chosen for its low-contrast dark palette that reduces eye
// strain during long terminal sessions. The palette provides enough semantic
// colors (love=error, pine=success, gold=warning, foam=active/selected) to
// convey pipeline status and navigation state without relying on icons alone.
//
// Styles are organized into three groups:
//   - Palette colors: raw Rose Pine Moon hex values, used nowhere else
//   - Component styles: general-purpose text styles (titles, items, errors, pipeline statuses)
//   - Pane styles: borders, headers, and content styles for the multi-pane layout

package ui

import "github.com/charmbracelet/lipgloss"

// Rose Pine Moon palette — see https://rosepinetheme.com/palette
var (
	rosePineHighlightLow = lipgloss.Color("#2a283e")
	rosePineHighlightMed = lipgloss.Color("#44415a")
	rosePineText         = lipgloss.Color("#e0def4")
	rosePineMuted        = lipgloss.Color("#6e6a86")
	rosePineSubtle       = lipgloss.Color("#908caa")
	rosePinePine         = lipgloss.Color("#31748f")
	rosePineFoam         = lipgloss.Color("#9ccfd8")
	rosePineGold         = lipgloss.Color("#f6c177")
	rosePineIris         = lipgloss.Color("#c4a7e7")
	rosePineLove         = lipgloss.Color("#eb6f92")
)

// Component styles: titles, list items, status indicators, and pipeline state colors.
// Pipeline status styles map CI status strings to semantic colors so users can
// scan pipeline health at a glance.
var (
	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(rosePineIris)
	itemStyle         = lipgloss.NewStyle().Foreground(rosePineText)
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(rosePineFoam).Background(rosePineHighlightLow)
	statusStyle       = lipgloss.NewStyle().Faint(true).Foreground(rosePineSubtle)
	errorStyle        = lipgloss.NewStyle().Foreground(rosePineLove)
	searchStyle       = lipgloss.NewStyle().Faint(true).Foreground(rosePineSubtle)
	progressStyle     = lipgloss.NewStyle().Faint(true).Foreground(rosePineMuted)
	pipelineSuccess   = lipgloss.NewStyle().Bold(true).Foreground(rosePinePine)
	pipelineFailed    = lipgloss.NewStyle().Bold(true).Foreground(rosePineLove)
	pipelineRunning   = lipgloss.NewStyle().Bold(true).Foreground(rosePineFoam)
	pipelinePending   = lipgloss.NewStyle().Bold(true).Foreground(rosePineGold)
	pipelineCanceled  = lipgloss.NewStyle().Bold(true).Foreground(rosePineMuted)
	pipelineSkipped   = lipgloss.NewStyle().Bold(true).Foreground(rosePineSubtle)
	pipelineUnknown   = lipgloss.NewStyle().Bold(true).Foreground(rosePineMuted)
)

// Pane styles: borders, headers, and content styles shared across the multi-pane
// layouts (projects, explorer, pipelines). Focused panes use foam-colored borders
// to indicate which pane receives keyboard input.
var (
	paneBorderStyle          = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(rosePineSubtle)
	paneBorderFocusStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(rosePineFoam)
	explorerHeaderStyle      = lipgloss.NewStyle().Bold(true).Foreground(rosePineIris)
	explorerFocusHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(rosePineFoam)
	detailHeaderStyle        = lipgloss.NewStyle().Bold(true).Foreground(rosePineIris)
	detailDividerStyle       = lipgloss.NewStyle().Foreground(rosePineSubtle)
	detailLabelStyle         = lipgloss.NewStyle().Foreground(rosePineMuted)
	detailValueStyle         = lipgloss.NewStyle().Foreground(rosePineText)
	explorerPathStyle        = lipgloss.NewStyle().Foreground(rosePineMuted)
	explorerHintStyle        = lipgloss.NewStyle().Foreground(rosePineSubtle)
	explorerErrorStyle       = lipgloss.NewStyle().Foreground(rosePineLove)
	diffAddStyle             = lipgloss.NewStyle().Foreground(rosePinePine)
	diffDelStyle             = lipgloss.NewStyle().Foreground(rosePineLove)
	diffHunkStyle            = lipgloss.NewStyle().Foreground(rosePineGold)
)
