// messages_page_size.go keeps each paged pane asking GitLab for a page the size of that pane. The
// projects pane is absent because every project is already in memory, so it resizes without asking
// GitLab for anything.

package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// queuePageSizeRefetch waits for a timer rather than fetching now, because dragging a terminal
// corner delivers a resize per frame and a fetch per frame is a burst GitLab answers with a refusal.
func (m *Model) queuePageSizeRefetch() tea.Cmd {
	pipelines, mrs := m.pipelinePageSize(), m.mrPageSize()
	if !m.pageSizeMoved(PanelPipelines, pipelines, m.pipelineView.perPage) &&
		!m.pageSizeMoved(PanelMRs, mrs, m.mrView.perPage) {
		return nil
	}
	return pageSizeTickCmd(pipelines, mrs)
}

// pageSizeMoved treats a panel with no room to measure as never moving, which is the case on a
// terminal too small to draw the sidebar at all.
func (m Model) pageSizeMoved(panel PanelID, want, inForce int) bool {
	return m.panePageSize(panel) > 0 && want != inForce
}

// handlePageSizeSettled acts on a timer only when the size it names is still wanted.
//
// A timer cannot be cancelled once armed, so every frame of a drag delivers one and the size it
// names has to still be the size the pane wants for its fetch to be worth sending. The size is the
// identity here, so a drag that ends where it started sends nothing at all.
func (m Model) handlePageSizeSettled(msg pageSizeTickMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if want := m.pipelinePageSize(); want == msg.pipelines &&
		m.pageSizeMoved(PanelPipelines, want, m.pipelineView.perPage) {
		cmds = append(cmds, (&m).refetchPipelinePage(want))
	}
	if want := m.mrPageSize(); want == msg.mrs &&
		m.pageSizeMoved(PanelMRs, want, m.mrView.perPage) {
		cmds = append(cmds, (&m).refetchMRPage(want))
	}
	return m, tea.Batch(cmds...)
}

// pipelineFetchSize records the size and returns it in one move, because a page counted at one size
// and fetched at another reports a position the rows on screen do not agree with.
func (m *Model) pipelineFetchSize() int {
	m.pipelineView.perPage = m.pipelinePageSize()
	return m.pipelineView.perPage
}

func (m *Model) mrFetchSize() int {
	m.mrView.perPage = m.mrPageSize()
	return m.mrView.perPage
}

// refetchPipelinePage follows the row the user was on rather than the page number, because a resize
// moves every page boundary.
//
// Unlike a page change this keeps the rows and the cursor in place, so handlePipelinesLoaded can
// read which pipeline was selected and put the cursor back on it when the new page lands.
//
// A panel with no project records the size and fetches nothing, because the size has to be recorded
// either way or every later resize arms a timer for a page that is never asked for.
func (m *Model) refetchPipelinePage(perPage int) tea.Cmd {
	page := pageHolding(collectionPosition(m.pipelineView.page, m.pipelineView.perPage, m.pipelineView.selected), perPage)
	m.pipelineView.perPage = perPage
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	m.pipelineView.page = page
	m.pipelineView.loading = true
	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, page, perPage)
}

func (m *Model) refetchMRPage(perPage int) tea.Cmd {
	page := pageHolding(collectionPosition(m.mrView.page, m.mrView.perPage, m.mrView.selected), perPage)
	m.mrView.perPage = perPage
	if m.mrView.project.ID == 0 {
		return nil
	}
	m.mrView.page = page
	m.mrView.loading = true
	return fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), page, perPage)
}
