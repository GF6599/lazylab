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

	"github.com/charmbracelet/x/ansi"
)

// renderInfoBar renders the bottom status bar with three sections:
// status (left), keybinding hints (center), project context (right).
func renderInfoBar(m *Model, width int) string {
	if width <= 0 {
		return ""
	}

	// Left: spinner + status
	var left string
	if m.isLoading() {
		left = infoBarStatusStyle.Render(m.spinner.View() + " " + m.status)
	} else if m.status != "" {
		left = infoBarStatusStyle.Render(m.status)
	}

	// Right: project context
	var right string
	if proj, ok := m.selectedProject(); ok {
		right = infoBarContextStyle.Render(proj.PathWithNamespace)
	}

	// Center: contextual keybindings
	hints := panelKeyHints(m.focus.Active, m)
	center := infoBarHintsStyle.Render(hints)

	// Layout: left ... center ... right
	leftWidth := ansi.StringWidth(left)
	rightWidth := ansi.StringWidth(right)
	centerWidth := ansi.StringWidth(center)

	if leftWidth+centerWidth+rightWidth+4 > width {
		// Truncate center to fit
		available := width - leftWidth - rightWidth - 4
		if available > 3 {
			center = infoBarHintsStyle.Render(ansi.Truncate(hints, available, "…"))
			centerWidth = ansi.StringWidth(center)
		} else {
			center = ""
			centerWidth = 0
		}
	}

	gap1 := max(1, (width-leftWidth-centerWidth-rightWidth)/2-leftWidth/2)
	gap2 := max(1, width-leftWidth-gap1-centerWidth-rightWidth)
	gap1 = max(gap1, 0)
	gap2 = max(gap2, 0)

	return left + strings.Repeat(" ", gap1) + center + strings.Repeat(" ", gap2) + right
}

// panelKeyHints returns a compact keybinding cheat-sheet for the focused panel.
// These are intentionally terse — full help is available via '?'.
func panelKeyHints(panel PanelID, m *Model) string {
	switch panel {
	case PanelProjects:
		return "/ search · f fav · e explorer · t tab · r refresh · Ctrl+O copy · ? help"
	case PanelPipelines:
		return "j/k nav · l stages · R retry · C cancel · r refresh · [ ] page · t tab · Ctrl+O copy · ? help"
	case PanelStages:
		return "j/k nav · J/K log · R retry · C cancel · P play · r refresh · t tab · Ctrl+O copy · ? help"
	case PanelMRs:
		return "j/k nav · J/K scroll · c comment · N new MR · [ ] page · t tab · Ctrl+O copy · ? help"
	case PanelDetail:
		return detailPanelKeyHints(m)
	default:
		return "Tab switch · 1-4 jump · +/- layout · = screen mode · ~ theme · ? help"
	}
}

// detailPanelKeyHints returns context-dependent hints for the Detail pane,
// varying by whether the user came from a pipeline or MR panel and which
// MR tab is active.
func detailPanelKeyHints(m *Model) string {
	if m.focus.PrevActive == PanelMRs {
		switch m.mrView.detailTab {
		case mrDetailTabComments:
			return "j/k discussions · r resolve · Enter reply · c comment · t tab · h back · Ctrl+O copy · ? help"
		case mrDetailTabDiff:
			return "j/k lines · c comment · t tab · h back · Ctrl+O copy · ? help"
		default:
			return "J/K scroll · c comment · t tab · h back · Ctrl+O copy · ? help"
		}
	}
	return "J/K scroll · R retry · t tab · h back · Ctrl+O copy · ? help"
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
		return formatPosition(
			collectionPosition(m.page, m.displayPerPage(), m.selected),
			knownTotal(m.totalProjects, len(visible)),
		)
	case PanelPipelines:
		if len(m.pipelineView.pipelines) == 0 {
			return ""
		}
		return formatPosition(
			collectionPosition(m.pipelineView.page, m.pipelineView.perPage, m.pipelineView.selected),
			knownTotal(m.pipelineView.totalItems, len(m.pipelineView.pipelines)),
		)
	case PanelStages:
		jobCount := len(m.pipelineView.jobRows)
		if jobCount == 0 {
			return ""
		}
		// Stages are never paged, so the rows in hand are the whole set.
		return formatPosition(m.pipelineView.stageSelected+1, jobCount)
	case PanelMRs:
		if len(m.mrView.mrs) == 0 {
			return ""
		}
		return formatPosition(
			collectionPosition(m.mrView.page, m.mrView.perPage, m.mrView.selected),
			knownTotal(m.mrView.totalItems, len(m.mrView.mrs)),
		)
	default:
		return ""
	}
}

func formatPosition(at, total int) string {
	return fmt.Sprintf("%d of %d", at, total)
}

func collectionPosition(page, perPage, selected int) int {
	if page < 1 || perPage < 1 {
		return selected + 1
	}
	return (page-1)*perPage + selected + 1
}

// knownTotal prefers the collection total, and counts what is in hand when GitLab withheld it,
// which it does once a collection passes ten thousand items.
func knownTotal(reported, inHand int) int {
	if reported > 0 {
		return reported
	}
	return inHand
}
