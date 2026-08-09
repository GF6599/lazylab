// Message handlers for merge request concerns: MR list loading, discussions,
// diffs, discussion resolution, replies, and comment creation.

package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/diffutil"
	"github.com/GF6599/lazylab/internal/redacting"
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
			diffs, _ := m.mrView.diffs.Get(mr.IID)
			content := renderMRCommentsText(msg.discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines)
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
	m.mrView.diffLineMap = diffutil.BuildLineMap(toDiffutilFiles(msg.diffs))
	var cmd tea.Cmd
	mr := m.mrView.selectedMR()
	if mr != nil && mr.IID == msg.mrIID {
		switch m.mrView.detailTab {
		case mrDetailTabDiff:
			content := renderMRDiffText(msg.diffs, m.mrViewportWidth(), m.mrView.diffCursor)
			m.setMRViewportContent(content)
			m.mrView.mrViewport.GotoTop()
		case mrDetailTabComments:
			// Re-render comments with diff context now that diffs are available
			if discussions, ok := m.mrView.discussions.Get(mr.IID); ok {
				content := renderMRCommentsText(discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion, msg.diffs, m.opts.DiffContextLines)
				m.setMRViewportContent(content)
			}
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
		// The two guards differ on purpose. The user can move to another merge request while
		// this request is in flight, so the revert follows the answer and the redraw follows
		// the screen.
		m.optimisticToggleResolved(msg.mrIID, msg.discussionID, !msg.resolved)
		if mr := m.mrView.selectedMR(); mr != nil && mr.IID == msg.mrIID {
			if discussions, ok := m.mrView.discussions.Get(msg.mrIID); ok {
				diffs, _ := m.mrView.diffs.Get(msg.mrIID)
				content := renderMRCommentsText(discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines)
				m.setMRViewportContent(content)
			}
		}
		m.status = fmt.Sprintf("Failed to resolve discussion: %v", redacting.Redact(msg.err.Error()))
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
		m.status = fmt.Sprintf("Failed to send reply: %v", redacting.Redact(msg.err.Error()))
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
		m.status = fmt.Sprintf("Failed to post comment: %v", redacting.Redact(msg.err.Error()))
		return m, nil
	}
	m.mrView.reply = mrReplyState{}
	m.status = "Comment posted"
	m.mrView.discussions.SetLoading(msg.mrIID)
	return m, fetchMRDiscussionsCmd(m.ctx, m.client, m.opts.APITimeout, msg.projectID, msg.mrIID)
}

func (m Model) handleMRCreated(msg mrCreatedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.mrView.createMR.sending = false
		m.mrView.createMR.err = msg.err
		m.status = fmt.Sprintf("Failed to create MR: %v", redacting.Redact(msg.err.Error()))
		return m, nil
	}
	m.mrView.createMR = createMRState{}
	m.status = fmt.Sprintf("Created !%d: %s", msg.mr.IID, msg.mr.Title)
	m.mrView.loading = true
	m.mrView.selected = 0
	m.mrView.tab = mrTabOpen
	return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, msg.projectID, "opened", 1, mrPerPage)
}

func (m Model) handleBranchesLoaded(msg branchesLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mrView.project.ID != msg.projectID {
		return m, nil
	}
	if !m.mrView.createMR.active || !m.mrView.createMR.branchPicker.active {
		return m, nil
	}
	m.mrView.createMR.branchPicker.loading = false
	if msg.err != nil {
		m.mrView.createMR.branchPicker.err = msg.err
		return m, nil
	}
	m.mrView.createMR.branchPicker.branches = msg.branches
	m.mrView.createMR.branchPicker.filtered = msg.branches
	m.mrView.createMR.branchPicker.selected = 0
	return m, nil
}
