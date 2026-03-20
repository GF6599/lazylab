// Semantic color variables and lipgloss styles for the TUI.
//
// Color vars and style vars are declared without initializers; they are set by
// applyTheme() (called from init and at runtime when the user cycles themes).
// Consumer files reference these globals directly — no changes needed when
// themes are switched because View() re-reads them on every frame.

package ui

import "github.com/charmbracelet/lipgloss"

// Semantic color palette — populated by applyTheme().
var (
	colorHighlightLow lipgloss.Color
	colorHighlightMed lipgloss.Color
	colorText         lipgloss.Color
	colorMuted        lipgloss.Color
	colorSubtle       lipgloss.Color
	colorSuccess      lipgloss.Color
	colorActive       lipgloss.Color
	colorWarning      lipgloss.Color
	colorAccent       lipgloss.Color
	colorError        lipgloss.Color
)

// Component styles: titles, list items, status indicators, and pipeline state colors.
var (
	titleStyle        lipgloss.Style
	itemStyle         lipgloss.Style
	selectedItemStyle lipgloss.Style
	statusStyle       lipgloss.Style
	errorStyle        lipgloss.Style
	searchStyle       lipgloss.Style
	progressStyle     lipgloss.Style
	pipelineSuccess   lipgloss.Style
	pipelineFailed    lipgloss.Style
	pipelineRunning   lipgloss.Style
	pipelinePending   lipgloss.Style
	pipelineCanceled  lipgloss.Style
	pipelineSkipped   lipgloss.Style
	pipelineUnknown   lipgloss.Style
)

// Pane styles: borders, headers, and content styles shared across the multi-pane layouts.
var (
	paneBorderStyle          lipgloss.Style
	paneBorderFocusStyle     lipgloss.Style
	explorerHeaderStyle      lipgloss.Style
	explorerFocusHeaderStyle lipgloss.Style
	detailHeaderStyle        lipgloss.Style
	detailDividerStyle       lipgloss.Style
	detailLabelStyle         lipgloss.Style
	detailValueStyle         lipgloss.Style
	explorerPathStyle        lipgloss.Style
	explorerHintStyle        lipgloss.Style
	explorerErrorStyle       lipgloss.Style
	diffAddStyle             lipgloss.Style
	diffDelStyle             lipgloss.Style
	diffHunkStyle            lipgloss.Style
)

func init() {
	applyTheme(ThemeRosePineMoon)
}
