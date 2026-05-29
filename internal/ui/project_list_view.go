package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// View is the top-level Bubble Tea render entry point. It dispatches to the
// active mode's renderer and composites modal overlays (help, retry confirm)
// on top of the base view when needed.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	if width < MinTerminalWidth || height < MinTerminalHeight {
		return renderTooSmallView(width, height)
	}

	// Render help overlay if requested
	if m.showHelp {
		return m.renderHelpView(width)
	}

	// Multi-panel mode: the new default
	if m.mode == modeMultiPanel {
		// Explorer overlay on top of multi-panel
		if m.explorer.project.ID != 0 && len(m.explorer.stack) > 0 {
			return renderExplorerView(m, width)
		}
		base := renderMultiPanelView(&m, width, m.height)
		if modal, ok := m.activeModalOverlay(width); ok {
			return overlayCentered(base, modal, width)
		}
		return base
	}

	// Standalone modes (used by tests and legacy paths)
	var mainView string
	switch m.mode {
	case modeExplorer:
		mainView = renderExplorerView(m, width)
	case modePipelines:
		base := renderPipelineView(m, width)
		if modal, ok := m.activeModalOverlay(width); ok {
			return overlayCentered(base, modal, width)
		}
		mainView = base
	default:
		return ""
	}

	// Add help bar at bottom
	return mainView + "\n" + m.renderHelpBar()
}

// activeModalOverlay returns the rendered modal string if any modal is active.
func (m Model) activeModalOverlay(width int) (string, bool) {
	switch {
	case m.mrView.createMR.active:
		return renderCreateMRModal(m, width), true
	case m.mrView.reply.active:
		return renderMRReplyModal(m, width), true
	case m.pipelineView.retryConfirm.active:
		return renderPipelineRetryConfirmModal(m, width), true
	}
	return "", false
}

// renderHelpView shows a full-screen help overlay with mode-aware key bindings
// arranged in a 3-column grid. Replaces (not overlays) the entire view.
func (m Model) renderHelpView(width int) string {
	var keys []key.Binding
	switch m.mode {
	case modeExplorer:
		keys = explorerKeyMap()
	case modePipelines:
		keys = pipelinesKeyMap()
	case modeMultiPanel:
		keys = multiPanelKeyMap(m.focus.Active, m.focus.PrevActive, &m)
	default:
		keys = projectsKeyMap()
	}

	// Convert to 2D array for multi-column layout (3 columns)
	cols := 3
	var keyGroups [][]key.Binding
	for i := 0; i < len(keys); i += cols {
		end := min(i+cols, len(keys))
		keyGroups = append(keyGroups, keys[i:end])
	}

	helpView := m.help.FullHelpView(keyGroups)
	title := titleStyle.Render("Help - Press ? or Esc to close")

	content := modalBorderStyle.Width(width - 4).Render(title + "\n\n" + helpView)

	return content
}

// renderHelpBar returns a single-line hint bar for the bottom of legacy mode views.
// Dispatches to mode-specific binding sets so users see only relevant shortcuts.
func (m Model) renderHelpBar() string {
	var bindings []key.Binding
	switch m.mode {
	case modeExplorer:
		bindings = explorerShortHelp(m.keys)
	case modePipelines:
		bindings = pipelinesShortHelp(m.keys)
	default:
		bindings = projectsShortHelp(m.keys)
	}
	return m.help.ShortHelpView(bindings)
}

// paneGap is the horizontal spacing between adjacent panes (in characters).
const paneGap = 1

// paneHeaderStyle returns the header style for a pane, using a distinct
// color for the focused pane so users can tell which pane has input focus.
func paneHeaderStyle(focused bool) lipgloss.Style {
	if focused {
		return explorerFocusHeaderStyle
	}
	return explorerHeaderStyle
}

// renderPane wraps content in a bordered box, normalizing it to exact
// width x height so all panes align when joined horizontally.
func renderPane(content string, width, height int, focused bool) string {
	lines := normalizeColumn(content, width, height)
	style := paneBorderStyle
	if focused {
		style = paneBorderFocusStyle
	}
	return style.Render(strings.Join(lines, "\n"))
}

func renderPaneGap(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return baseWidthStyle.Width(width).Height(height).Render("")
}
