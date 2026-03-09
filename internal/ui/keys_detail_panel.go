// keys_detail_panel.go contains key handling for the Detail pane
// in the multi-panel layout.

package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleDetailPanelKey handles keys when the Detail pane is focused. It uses
// focus.PrevActive to determine context: scrolling targets either the pipeline
// log viewport or the MR viewport depending on which sidebar panel the user
// came from.
func (m Model) handleDetailPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	isMR := m.focus.PrevActive == PanelMRs
	isMRComments := isMR && m.mrView.detailTab == mrDetailTabComments

	// MR comments tab: j/k navigates discussions, r resolves, enter replies
	if isMRComments {
		switch key {
		case "left", "h":
			m.focus.Active = m.focus.PrevActive
			return m, nil
		case "esc":
			m.focus.Active = PanelProjects
			return m, nil
		case "down", "j":
			return m.moveDiscussionSelection(1)
		case "up", "k":
			return m.moveDiscussionSelection(-1)
		case "J", "ctrl+d":
			m.mrView.mrViewport.HalfPageDown()
			return m, nil
		case "K", "ctrl+u":
			m.mrView.mrViewport.HalfPageUp()
			return m, nil
		case "<", "g":
			m.mrView.selectedDiscussion = 0
			return m.refreshMRCommentsViewport()
		case ">", "G":
			return m.moveDiscussionToEnd()
		case "r":
			return m.toggleDiscussionResolved()
		case "enter":
			return m.openMRReplyModal()
		case "c":
			return m.openMRNewCommentModal()
		case "t":
			return m.cycleDetailTab()
		case "T":
			return m.cycleDetailTabReverse()
		case "ctrl+o":
			m.copyMRComment()
			return m, nil
		}
		return m, nil
	}

	isMRDiff := isMR && m.mrView.detailTab == mrDetailTabDiff

	switch key {
	case "left", "h":
		m.focus.Active = m.focus.PrevActive
		return m, nil
	case "esc":
		// Jump to Projects (home)
		m.focus.Active = PanelProjects
		return m, nil
	case "down", "j":
		if isMRDiff {
			return m.moveDiffCursor(1)
		} else if isMR {
			m.mrView.mrViewport.ScrollDown(1)
		} else {
			m.pipelineView.logViewport.ScrollDown(1)
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		}
		return m, nil
	case "up", "k":
		if isMRDiff {
			return m.moveDiffCursor(-1)
		} else if isMR {
			m.mrView.mrViewport.ScrollUp(1)
		} else {
			m.pipelineView.logViewport.ScrollUp(1)
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case "J", "ctrl+d":
		if isMR {
			m.mrView.mrViewport.HalfPageDown()
		} else {
			m.pipelineView.logViewport.HalfPageDown()
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		}
		return m, nil
	case "K", "ctrl+u":
		if isMR {
			m.mrView.mrViewport.HalfPageUp()
		} else {
			m.pipelineView.logViewport.HalfPageUp()
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case "<", "g":
		if isMRDiff {
			return m.moveDiffCursorTo(0)
		} else if isMR {
			m.mrView.mrViewport.GotoTop()
		} else {
			m.pipelineView.logViewport.GotoTop()
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case ">", "G":
		if isMRDiff && len(m.mrView.diffLineMap) > 0 {
			return m.moveDiffCursorTo(len(m.mrView.diffLineMap) - 1)
		} else if isMR {
			m.mrView.mrViewport.GotoBottom()
		} else {
			m.pipelineView.logViewport.GotoBottom()
			m.pipelineView.logAutoFollow = true
		}
		return m, nil
	case "c":
		if isMRDiff {
			return m.openMRDiffCommentModal()
		} else if isMR {
			return m.openMRNewCommentModal()
		}
	case "t":
		return m.cycleDetailTab()
	case "T":
		return m.cycleDetailTabReverse()
	case "R":
		switch m.focus.PrevActive {
		case PanelPipelines:
			return m.openRetryModal()
		case PanelStages:
			return m.openRetryModalForJob()
		}
	case "ctrl+o":
		switch m.focus.PrevActive {
		case PanelProjects:
			m.copyCloneCommand()
		case PanelPipelines:
			m.copyPipelineURL()
		case PanelStages:
			m.copyJobURL()
		case PanelMRs:
			m.copyMRURL()
		}
	}
	return m, nil
}

// moveDiffCursor moves the diff cursor by delta, re-renders, and scrolls to keep visible.
func (m Model) moveDiffCursor(delta int) (tea.Model, tea.Cmd) {
	if len(m.mrView.diffLineMap) == 0 {
		return m, nil
	}
	newCursor := m.mrView.diffCursor + delta
	newCursor = max(newCursor, 0)
	newCursor = min(newCursor, len(m.mrView.diffLineMap)-1)
	if newCursor == m.mrView.diffCursor {
		return m, nil
	}
	return m.moveDiffCursorTo(newCursor)
}

// moveDiffCursorTo sets the diff cursor to a specific position, re-renders, and scrolls.
func (m Model) moveDiffCursorTo(pos int) (tea.Model, tea.Cmd) {
	m.mrView.diffCursor = pos
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	diffs, ok := m.mrView.diffs.Get(mr.IID)
	if !ok {
		return m, nil
	}
	content := renderMRDiffText(diffs, m.mrViewportWidth(), m.mrView.diffCursor)
	m.setMRViewportContent(content)

	// Scroll viewport to keep cursor visible
	vpHeight := m.mrView.mrViewport.Height
	yOffset := m.mrView.mrViewport.YOffset
	if m.mrView.diffCursor < yOffset {
		m.mrView.mrViewport.SetYOffset(m.mrView.diffCursor)
	} else if m.mrView.diffCursor >= yOffset+vpHeight {
		m.mrView.mrViewport.SetYOffset(m.mrView.diffCursor - vpHeight + 1)
	}
	return m, nil
}
