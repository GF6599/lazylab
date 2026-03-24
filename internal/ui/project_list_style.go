// Semantic color variables and lipgloss styles for the TUI.
//
// Color vars and style vars are declared without initializers; they are set by
// applyTheme() (called from init and at runtime when the user cycles themes).
// Consumer files reference these globals directly — no changes needed when
// themes are switched because View() re-reads them on every frame.

package ui

import "github.com/charmbracelet/lipgloss"

// Semantic color palette — populated by applyTheme().
// Typed as lipgloss.TerminalColor (interface) to allow future use of
// lipgloss.AdaptiveColor for light/dark terminal support.
var (
	colorHighlightLow lipgloss.TerminalColor
	colorHighlightMed lipgloss.TerminalColor
	colorText         lipgloss.TerminalColor
	colorMuted        lipgloss.TerminalColor
	colorSubtle       lipgloss.TerminalColor
	colorSuccess      lipgloss.TerminalColor
	colorActive       lipgloss.TerminalColor
	colorWarning      lipgloss.TerminalColor
	colorAccent       lipgloss.TerminalColor
	colorError        lipgloss.TerminalColor
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

// Border and layout styles: cached to avoid per-frame allocation in renderBorderedPane,
// buildTabString, buildBottomBorder, and renderInfoBar.
var (
	borderUnfocusedStyle      lipgloss.Style
	borderFocusedStyle        lipgloss.Style
	borderTitleUnfocusedStyle lipgloss.Style
	borderTitleFocusedStyle   lipgloss.Style
	scrollThumbStyle          lipgloss.Style
	activeTabStyle            lipgloss.Style
	inactiveTabStyle          lipgloss.Style
	tabSepStyle               lipgloss.Style
	borderFooterStyle         lipgloss.Style
	infoBarStatusStyle        lipgloss.Style
	infoBarHintsStyle         lipgloss.Style
	infoBarContextStyle       lipgloss.Style
	diffCursorBgStyle         lipgloss.Style
	modalLabelStyle           lipgloss.Style
	modalFocusLabelStyle      lipgloss.Style
	modalBorderStyle          lipgloss.Style
)

func init() {
	applyTheme(ThemeRosePineMoon)
}
