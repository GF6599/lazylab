// info_bar.go renders the single-line status bar at the bottom of the screen.
//
// The bar is divided into three sections:
//   - Left: spinner + status message (loading state, action confirmations)
//   - Center: contextual keybinding hints for the focused panel
//   - Right: selected project path for orientation
//
// When the terminal is too narrow, the center section is truncated first to
// preserve the status message and project context.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// renderInfoBar renders the bottom status bar with three sections:
// status (left), keybinding hints (center), project context (right).
func renderInfoBar(m *Model, width int) string {
	if width <= 0 {
		return ""
	}

	leftStyle := lipgloss.NewStyle().Foreground(rosePineFoam)
	centerStyle := lipgloss.NewStyle().Foreground(rosePineSubtle)
	rightStyle := lipgloss.NewStyle().Foreground(rosePineMuted)

	// Left: spinner + status
	var left string
	if m.isLoading() {
		left = leftStyle.Render(m.spinner.View() + " " + m.status)
	} else if m.status != "" {
		left = leftStyle.Render(m.status)
	}

	// Right: project context
	var right string
	if proj, ok := m.selectedProject(); ok {
		right = rightStyle.Render(proj.PathWithNamespace)
	}

	// Center: contextual keybindings
	hints := panelKeyHints(m.focus.Active, m)
	center := centerStyle.Render(hints)

	// Layout: left ... center ... right
	leftWidth := ansi.StringWidth(left)
	rightWidth := ansi.StringWidth(right)
	centerWidth := ansi.StringWidth(center)

	if leftWidth+centerWidth+rightWidth+4 > width {
		// Truncate center to fit
		available := width - leftWidth - rightWidth - 4
		if available > 3 {
			center = centerStyle.Render(ansi.Truncate(hints, available, "…"))
			centerWidth = ansi.StringWidth(center)
		} else {
			center = ""
			centerWidth = 0
		}
	}

	gap1 := max(1, (width-leftWidth-centerWidth-rightWidth)/2-leftWidth/2)
	gap2 := max(1, width-leftWidth-gap1-centerWidth-rightWidth)
	if gap1 < 0 {
		gap1 = 0
	}
	if gap2 < 0 {
		gap2 = 0
	}

	return left + strings.Repeat(" ", gap1) + center + strings.Repeat(" ", gap2) + right
}

// panelKeyHints returns a compact keybinding cheat-sheet for the focused panel.
// These are intentionally terse — full help is available via '?'.
func panelKeyHints(panel PanelID, m *Model) string {
	switch panel {
	case PanelProjects:
		return "j/k nav · / search · f fav · t tab · e explorer · Enter pipelines · r refresh"
	case PanelPipelines:
		return "j/k nav · l stages · [ ] page · R retry · C cancel · t tab · r refresh"
	case PanelStages:
		return "j/k nav · J/K log · R retry · C cancel · P play · t tab"
	case PanelMRs:
		return "j/k nav · J/K scroll · [ ] filter · t tab · Ctrl+O copy URL"
	case PanelDetail:
		return "J/K scroll · Tab switch panels"
	default:
		return "Tab switch · 1-5 jump · -/+ layout · = screen mode · ? help"
	}
}

// panelFooter returns the "N of M" position indicator rendered in the bottom
// border of each sidebar panel.
func panelFooter(panel PanelID, m *Model) string {
	switch panel {
	case PanelProjects:
		visible := m.visibleProjects()
		if len(visible) == 0 {
			return ""
		}
		return fmt.Sprintf("%d of %d", m.selected+1, len(visible))
	case PanelPipelines:
		if len(m.pipelineView.pipelines) == 0 {
			return ""
		}
		return fmt.Sprintf("%d of %d", m.pipelineView.selected+1, len(m.pipelineView.pipelines))
	case PanelStages:
		jobCount := len(m.pipelineView.jobRows)
		if jobCount == 0 {
			return ""
		}
		return fmt.Sprintf("%d of %d", m.pipelineView.stageSelected+1, jobCount)
	case PanelMRs:
		if len(m.mrView.mrs) == 0 {
			return ""
		}
		return fmt.Sprintf("%d of %d", m.mrView.selected+1, len(m.mrView.mrs))
	default:
		return ""
	}
}
