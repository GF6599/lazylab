// keys_detail_panel.go contains key handling for the Detail pane
// in the multi-panel layout.

package ui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// handleDetailPanelKey handles keys when the Detail pane is focused. It uses
// focus.PrevActive to determine context: scrolling targets either the pipeline
// log viewport or the MR viewport depending on which sidebar panel the user
// came from.
func (m Model) handleDetailPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	isMR := m.focus.PrevActive == PanelMRs
	isMRComments := isMR && m.mrView.detailTab == mrDetailTabComments

	// MR comments tab: j/k navigates discussions, r resolves, enter replies
	if isMRComments {
		switch {
		case key.Matches(msg, m.keys.Left):
			m.focus.Active = m.focus.PrevActive
			return m, nil
		case key.Matches(msg, m.keys.Back):
			m.focus.Active = PanelProjects
			return m, nil
		case key.Matches(msg, m.keys.Down):
			return m.moveDiscussionSelection(1)
		case key.Matches(msg, m.keys.Up):
			return m.moveDiscussionSelection(-1)
		case key.Matches(msg, m.keys.ScrollDown) || key.Matches(msg, m.keys.HalfDown):
			m.mrView.mrViewport.HalfPageDown()
			return m, nil
		case key.Matches(msg, m.keys.ScrollUp) || key.Matches(msg, m.keys.HalfUp):
			m.mrView.mrViewport.HalfPageUp()
			return m, nil
		case key.Matches(msg, m.keys.Top):
			m.mrView.selectedDiscussion = 0
			return m.refreshMRCommentsViewport()
		case key.Matches(msg, m.keys.Bottom):
			return m.moveDiscussionToEnd()
		case key.Matches(msg, m.keys.ResolveDiscussion):
			return m.toggleDiscussionResolved()
		case key.Matches(msg, m.keys.Enter):
			return m.openMRReplyModal()
		case key.Matches(msg, m.keys.Comment):
			return m.openMRNewCommentModal()
		case key.Matches(msg, m.keys.CycleTab):
			return m.cycleDetailTab()
		case key.Matches(msg, m.keys.CycleTabRv):
			return m.cycleDetailTabReverse()
		case key.Matches(msg, m.keys.Copy):
			return m, m.copyMRComment()
		}
		return m, nil
	}

	isMRDiff := isMR && m.mrView.detailTab == mrDetailTabDiff

	switch {
	case key.Matches(msg, m.keys.Left):
		m.focus.Active = m.focus.PrevActive
		return m, nil
	case key.Matches(msg, m.keys.Back):
		// Jump to Projects (home)
		m.focus.Active = PanelProjects
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if isMRDiff {
			return m.moveDiffCursor(1)
		} else if isMR {
			m.mrView.mrViewport.ScrollDown(1)
		} else {
			m.pipelineView.logViewport.ScrollDown(1)
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		}
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if isMRDiff {
			return m.moveDiffCursor(-1)
		} else if isMR {
			m.mrView.mrViewport.ScrollUp(1)
		} else {
			m.pipelineView.logViewport.ScrollUp(1)
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case key.Matches(msg, m.keys.ScrollDown) || key.Matches(msg, m.keys.HalfDown):
		if isMR {
			m.mrView.mrViewport.HalfPageDown()
		} else {
			m.pipelineView.logViewport.HalfPageDown()
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		}
		return m, nil
	case key.Matches(msg, m.keys.ScrollUp) || key.Matches(msg, m.keys.HalfUp):
		if isMR {
			m.mrView.mrViewport.HalfPageUp()
		} else {
			m.pipelineView.logViewport.HalfPageUp()
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case key.Matches(msg, m.keys.Top):
		if isMRDiff {
			return m.moveDiffCursorTo(0)
		} else if isMR {
			m.mrView.mrViewport.GotoTop()
		} else {
			m.pipelineView.logViewport.GotoTop()
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case key.Matches(msg, m.keys.Bottom):
		if isMRDiff && len(m.mrView.diffLineMap) > 0 {
			return m.moveDiffCursorTo(len(m.mrView.diffLineMap) - 1)
		} else if isMR {
			m.mrView.mrViewport.GotoBottom()
		} else {
			m.pipelineView.logViewport.GotoBottom()
			m.pipelineView.logAutoFollow = true
		}
		return m, nil
	case key.Matches(msg, m.keys.Comment):
		if isMRDiff {
			return m.openMRDiffCommentModal()
		} else if isMR {
			return m.openMRNewCommentModal()
		}
	case key.Matches(msg, m.keys.CycleTab):
		return m.cycleDetailTab()
	case key.Matches(msg, m.keys.CycleTabRv):
		return m.cycleDetailTabReverse()
	case key.Matches(msg, m.keys.Retry):
		switch m.focus.PrevActive {
		case PanelPipelines:
			return m.openRetryModal()
		case PanelStages:
			return m.openRetryModalForJob()
		}
	case key.Matches(msg, m.keys.Copy):
		switch m.focus.PrevActive {
		case PanelProjects:
			return m, m.copyCloneCommand()
		case PanelPipelines:
			return m, m.copyPipelineURL()
		case PanelStages:
			return m, m.copyJobURL()
		case PanelMRs:
			return m, m.copyMRURL()
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
