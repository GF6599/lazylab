// messages_page_size.go keeps each paged pane asking GitLab for a page the size of that pane. The
// projects pane is absent because every project is already in memory, so it resizes without asking
// GitLab for anything.

package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// pagedPane is a panel whose page size follows the pane that draws it.
type pagedPane struct {
	panel     PanelID
	projectID int
	// want is the size the pane has room for, drawn is the size of the page on screen.
	want  int
	drawn int
}

func (m Model) pipelinePane() pagedPane {
	return pagedPane{
		panel:     PanelPipelines,
		projectID: m.pipelineView.project.ID,
		want:      m.pipelinePageSize(),
		drawn:     m.pipelineView.perPage,
	}
}

func (m Model) mrPane() pagedPane {
	return pagedPane{
		panel:     PanelMRs,
		projectID: m.mrView.project.ID,
		want:      m.mrPageSize(),
		drawn:     m.mrView.perPage,
	}
}

// pageSizeMoved is false for a panel with no project to ask about, and for one with no room to
// measure, which is the case on a terminal too small to draw the sidebar at all.
func (m Model) pageSizeMoved(p pagedPane) bool {
	return p.projectID != 0 && m.panePageSize(p.panel) > 0 && p.want != p.drawn
}

// queuePageSizeRefetch waits for a timer rather than fetching now, because dragging a terminal
// corner delivers a resize per frame and a fetch per frame is a burst GitLab answers with a refusal.
func (m *Model) queuePageSizeRefetch() tea.Cmd {
	pipelines, mrs := m.pipelinePane(), m.mrPane()
	if !m.pageSizeMoved(pipelines) && !m.pageSizeMoved(mrs) {
		return nil
	}
	return pageSizeTickCmd(pipelines.want, mrs.want)
}

// handlePageSizeSettled acts on a timer only when the size it names is still wanted.
//
// A timer cannot be cancelled once armed, so every frame of a drag delivers one and the size it
// names has to still be the size the pane wants for its fetch to be worth sending. The size is the
// identity here, so a drag that ends where it started sends nothing at all.
func (m Model) handlePageSizeSettled(msg pageSizeTickMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if p := m.pipelinePane(); p.want == msg.pipelines && m.pageSizeMoved(p) {
		m.pipelineView.loading = true
		cmds = append(cmds, m.refetchPipelinePage(p))
	}
	if p := m.mrPane(); p.want == msg.mrs && m.pageSizeMoved(p) {
		m.mrView.loading = true
		cmds = append(cmds, m.refetchMRPage(p))
	}
	return m, tea.Batch(cmds...)
}

// startPipelinePage records the size a fresh collection is fetched at and returns it.
//
// A fresh collection may record its size up front, because it has no rows on screen for that size
// to contradict. A resize may not, which is what refetchPipelinePage is careful about: the drawn
// size only moves when the drawn rows move.
func (m *Model) startPipelinePage() int {
	m.pipelineView.perPage = m.pipelinePageSize()
	return m.pipelineView.perPage
}

func (m *Model) startMRPage() int {
	m.mrView.perPage = m.mrPageSize()
	return m.mrView.perPage
}

// refetchPipelinePage follows the row the user was on rather than the page number, because a resize
// moves every page boundary.
//
// It leaves the page number and the page size alone. Those two describe the rows on screen, so they
// move when the rows do and not when the request goes out, or the footer counts a position against
// a page nobody is looking at yet, and keeps counting it if the request never lands.
//
// Keeping the rows and the cursor in place also lets handlePipelinesLoaded read which pipeline was
// selected and put the cursor back on it when the new page arrives.
func (m Model) refetchPipelinePage(p pagedPane) tea.Cmd {
	position := collectionPosition(m.pipelineView.page, p.drawn, m.pipelineView.selected)
	page := pageHolding(position, p.want)
	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, p.projectID, page, p.want)
}

func (m Model) refetchMRPage(p pagedPane) tea.Cmd {
	position := collectionPosition(m.mrView.page, p.drawn, m.mrView.selected)
	page := pageHolding(position, p.want)
	return fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, p.projectID, mrTabStateString(m.mrView.tab), page, p.want)
}
