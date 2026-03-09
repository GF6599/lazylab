// Message handlers for merge request concerns: MR list loading, discussions,
// diffs, discussion resolution, replies, and comment creation.

package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleMRsLoaded(msg mrsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	// Stash previously selected IID before overwriting the list
	prevIID := 0
	if m.mrView.selected >= 0 && m.mrView.selected < len(m.mrView.mrs) {
		prevIID = m.mrView.mrs[m.mrView.selected].IID
	}

	m.mrView.loading = false
	if msg.err != nil {
		m.mrView.err = msg.err
		return m, nil
	}
	m.mrView.err = nil
	m.mrView.mrs = msg.mrs
	m.mrView.page = msg.page
	m.mrView.prevPage = msg.prevPage
	m.mrView.nextPage = msg.nextPage
	m.mrView.totalPages = msg.totalPages

	// Preserve selection by matching on IID
	if prevIID != 0 {
		for i, mr := range m.mrView.mrs {
			if mr.IID == prevIID {
				m.mrView.selected = i
				return m, nil
			}
		}
	}
	if m.mrView.selected >= len(m.mrView.mrs) {
		m.mrView.selected = max(0, len(m.mrView.mrs)-1)
	}
	return m, nil
}

func (m Model) handleMRDiscussionsLoaded(msg mrDiscussionsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.mrView.discussions.SetErr(msg.mrIID, msg.err)
		return m, nil
	}
	m.mrView.discussions.Set(msg.mrIID, msg.discussions)
	if m.mrView.detailTab == mrDetailTabComments {
		mr := m.mrView.selectedMR()
		if mr != nil && mr.IID == msg.mrIID {
			content := renderMRCommentsText(msg.discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion)
			m.setMRViewportContent(content)
			m.mrView.mrViewport.GotoTop()
		}
	}
	return m, nil
}

func (m Model) handleMRDiffsLoaded(msg mrDiffsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.mrView.diffs.SetErr(msg.mrIID, msg.err)
		return m, nil
	}
	m.mrView.diffs.Set(msg.mrIID, msg.diffs)
	m.mrView.diffCursor = 0
	m.mrView.diffLineMap = buildDiffLineMap(msg.diffs)
	var cmd tea.Cmd
	if m.mrView.detailTab == mrDetailTabDiff {
		mr := m.mrView.selectedMR()
		if mr != nil && mr.IID == msg.mrIID {
			content := renderMRDiffText(msg.diffs, m.mrViewportWidth(), m.mrView.diffCursor)
			m.setMRViewportContent(content)
			m.mrView.mrViewport.GotoTop()
		}
	}
	// Fetch diff refs for positioned comments
	cmd = fetchMRDiffRefsCmd(m.ctx, m.client, m.opts.APITimeout, msg.projectID, msg.mrIID)
	return m, cmd
}

func (m Model) handleMRDiscussionResolved(msg mrDiscussionResolvedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		// Revert optimistic update on failure
		m.optimisticToggleResolved(msg.mrIID, msg.discussionID, !msg.resolved)
		if discussions, ok := m.mrView.discussions.Get(msg.mrIID); ok {
			content := renderMRCommentsText(discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion)
			m.setMRViewportContent(content)
		}
		m.status = fmt.Sprintf("Failed to resolve discussion: %v", msg.err)
		return m, nil
	}
	if msg.resolved {
		m.status = "Discussion resolved"
	} else {
		m.status = "Discussion unresolved"
	}
	return m, nil
}

func (m Model) handleMRDiscussionReply(msg mrDiscussionReplyMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.mrView.reply.sending = false
		m.mrView.reply.err = msg.err
		m.status = fmt.Sprintf("Failed to send reply: %v", msg.err)
		return m, nil
	}
	// Close modal and re-fetch discussions to show the new reply
	m.mrView.reply = mrReplyState{}
	m.status = "Reply sent"
	m.mrView.discussions.SetLoading(msg.mrIID)
	return m, fetchMRDiscussionsCmd(m.ctx, m.client, m.opts.APITimeout, msg.projectID, msg.mrIID)
}

func (m Model) handleMRDiffRefsLoaded(msg mrDiffRefsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.logError("load MR diff refs", "err", msg.err, "mr", msg.mrIID)
		return m, nil
	}
	mr := m.mrView.selectedMR()
	if mr != nil && mr.IID == msg.mrIID {
		m.mrView.diffRefs = msg.diffRefs
	}
	return m, nil
}

func (m Model) handleMRDiscussionCreated(msg mrDiscussionCreatedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.mrView.reply.sending = false
		m.mrView.reply.err = msg.err
		m.status = fmt.Sprintf("Failed to post comment: %v", msg.err)
		return m, nil
	}
	m.mrView.reply = mrReplyState{}
	m.status = "Comment posted"
	m.mrView.discussions.SetLoading(msg.mrIID)
	return m, fetchMRDiscussionsCmd(m.ctx, m.client, m.opts.APITimeout, msg.projectID, msg.mrIID)
}
