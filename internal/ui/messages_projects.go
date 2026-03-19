// Message handlers for project-level concerns: cache loading, project fetching,
// pipeline status badges, commit loading, and search debouncing.

package ui

import (
	"errors"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleCacheLoaded processes the on-disk project cache result. On a cache hit,
// all pages are marked ready immediately and the pipeline refresh ticker starts.
// On a miss (or error), it falls back to a foreground API fetch. In multi-panel
// mode it also triggers sidebar data loading for the initially selected project.
func (m Model) handleCacheLoaded(msg cacheLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.logError("load cache", "err", msg.err)
	}
	if !msg.found || len(msg.projects) == 0 {
		m.loading = true
		m.status = "Cache empty, contacting GitLab..."
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	}
	m.loading = false
	m.err = nil
	m.backgroundLoading = false
	m.allProjects = msg.projects
	m.invalidateVisibleCache()
	totalProjects := len(msg.projects)
	perPage := m.opts.ProjectsPerPage
	if perPage <= 0 {
		perPage = 30
	}
	m.totalPages = (totalProjects + perPage - 1) / perPage
	if m.totalPages <= 0 {
		m.totalPages = 1
	}
	m.pagesReady = make(map[int]bool, m.totalPages)
	for p := 1; p <= m.totalPages; p++ {
		m.pagesReady[p] = true
	}
	m.pagesLoaded = m.totalPages
	m.page = 1
	m.selected = 0

	// Update paginator
	m.paginator.SetTotalPages(m.totalPages)
	m.paginator.Page = 0 // Paginator is 0-indexed
	if totalProjects == 0 {
		m.status = "Cache loaded (empty)"
	} else {
		m.status = fmt.Sprintf("Loaded %d cached projects", totalProjects)
	}
	m.ensureSelectionBounds()
	m.updateProjectList()
	// Batch prefetch pipeline status for all visible projects and start ticker
	cmds := []tea.Cmd{pipelineTickCmd()}
	if prefetchCmd := (&m).queueBatchPrefetchPipelineStatus(); prefetchCmd != nil {
		cmds = append(cmds, prefetchCmd)
	}
	// Auto-load sidebar data for initially selected project in multi-panel mode
	if m.mode == modeMultiPanel {
		if cmd := (&m).autoLoadSelectedProjectData(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// handleProjectsLoaded processes a fetched page of projects. Background loads
// (triggered by lazy pagination) append to allProjects and save to cache.
// Foreground loads (first page or forced refresh) replace the entire project
// list, reset pagination, start the pipeline ticker, and batch-prefetch
// pipeline statuses for the visible page.
func (m Model) handleProjectsLoaded(msg projectsLoadedMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if msg.err != nil {
		if msg.background {
			m.backgroundLoading = false
			m.status = "Background load failed"
		} else {
			m.loading = false
			m.status = "Failed to load projects"
		}
		m.err = msg.err
		m.logError("load projects", "err", msg.err, "background", msg.background)
		return m, nil
	}

	if msg.background {
		m.appendPage(msg.page)
		m.backgroundLoading = false
		m.updateProjectList()
		m.status = fmt.Sprintf("Loaded page %d", msg.page.Page)
		if m.cache != nil && len(m.allProjects) > 0 {
			cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	}

	// Foreground load resets project cache.
	m.loading = false
	m.err = nil
	m.page = msg.page.Page
	if m.page <= 0 {
		m.page = 1
	}
	m.totalPages = msg.page.TotalPages
	if m.totalPages <= 0 {
		m.totalPages = m.page
	}
	m.allProjects = slices.Clone(msg.page.Projects)
	m.invalidateVisibleCache()
	m.pagesReady = map[int]bool{m.page: true}
	m.pagesLoaded = len(m.pagesReady)
	m.selected = 0

	// Update paginator
	m.paginator.SetTotalPages(m.totalPages)
	m.paginator.Page = m.page - 1 // Paginator is 0-indexed
	if len(m.allProjects) == 0 {
		m.status = "No projects returned"
	} else {
		m.status = fmt.Sprintf("Loaded %d projects", len(m.allProjects))
	}
	m.backgroundLoading = false
	m.ensureSelectionBounds()
	m.updateProjectList()
	// Batch prefetch pipeline status for all visible projects and start ticker on first page
	if batchCmd := (&m).queueBatchPrefetchPipelineStatus(); batchCmd != nil {
		cmds = append(cmds, batchCmd)
	}
	if msg.page.Page == 1 {
		cmds = append(cmds, pipelineTickCmd())
	}
	if m.cache != nil && len(m.allProjects) > 0 {
		cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
	}
	// Auto-load sidebar data for initially selected project in multi-panel mode
	if m.mode == modeMultiPanel {
		if cmd := (&m).autoLoadSelectedProjectData(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// handlePipelineStatus updates the cached pipeline status for a single project.
// ErrNoPipelines is treated as empty (not an error) so the UI can show a
// distinct "no pipeline" icon. If the affected project is currently selected,
// the detail pane cache is invalidated to trigger a re-render.
func (m Model) handlePipelineStatus(msg pipelineStatusMsg) (tea.Model, tea.Cmd) {
	selectedID := 0
	if m.mode == modeProjects || m.mode == modeMultiPanel {
		visible := m.visibleProjects()
		if m.selected >= 0 && m.selected < len(visible) {
			selectedID = visible[m.selected].ID
		}
	}

	state, _ := m.pipelineStatus.Get(msg.projectID)
	state.loading = false
	state.ref = msg.ref
	now := time.Now()
	state.lastFetched = now
	state.lastAccessed = now
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			state.empty = true
			state.err = nil
			state.hasInfo = false
			state.info = gitlab.PipelineSummary{}
		} else {
			state.err = msg.err
			state.empty = false
			state.hasInfo = false
		}
	} else {
		state.info = msg.pipeline
		state.hasInfo = true
		state.err = nil
		state.empty = false
	}
	m.pipelineStatus.Set(msg.projectID, state)
	if msg.projectID == selectedID {
		(&m).invalidateDetailCache()
	}
	return m, nil
}

// handleBatchPipelineStatus processes the results from batchFetchPipelineStatusCmd,
// updating the pipelineStatus map for each project in a single pass. If the
// currently selected project was among the results, the detail pane cache is
// invalidated so the updated status icon renders immediately.
func (m Model) handleBatchPipelineStatus(msg batchPipelineStatusMsg) (tea.Model, tea.Cmd) {
	selectedID := 0
	if m.mode == modeProjects || m.mode == modeMultiPanel {
		visible := m.visibleProjects()
		if m.selected >= 0 && m.selected < len(visible) {
			selectedID = visible[m.selected].ID
		}
	}

	now := time.Now()
	for projectID, result := range msg.results {
		state, _ := m.pipelineStatus.Get(projectID)
		state.loading = false
		state.lastFetched = now
		state.lastAccessed = now

		if result.err != nil {
			state.err = result.err
			state.hasInfo = false
		} else if result.empty {
			state.empty = true
			state.hasInfo = false
		} else {
			state.info = result.pipeline
			state.hasInfo = true
			state.err = nil
			state.empty = false
		}

		m.pipelineStatus.Set(projectID, state)
		if projectID == selectedID {
			(&m).invalidateDetailCache()
		}
	}

	return m, nil
}

// handlePipelineTick is called every pipelineRefreshInterval (5s). Its behavior
// depends on the current mode:
//   - modeProjects: refreshes the selected project's pipeline status badge.
//   - modePipelines: refreshes the entire pipeline view (list + stages + jobs + log).
//   - modeMultiPanel: does both — batch-refreshes visible project badges and
//     refreshes the active pipeline view if one is open.
//
// The caller (Update) always re-enqueues the tick after handling, forming a
// self-sustaining refresh loop.
func (m Model) handlePipelineTick() (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeProjects:
		cmd := (&m).queuePipelineFetchForSelection(false)
		return m, cmd
	case modePipelines:
		cmd := (&m).queuePipelineViewRefresh()
		return m, cmd
	case modeMultiPanel:
		// In multi-panel mode, refresh both project pipeline badges and pipeline view data
		var cmds []tea.Cmd
		// Batch-refresh pipeline status icons for all visible projects
		if cmd := (&m).queueBatchPrefetchPipelineStatus(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.pipelineView.project.ID != 0 {
			if cmd := (&m).queuePipelineViewRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	default:
		return m, nil
	}
}

// handleSelectionDebounce fires the full data load for the selected project
// after the debounce timer expires. Stale ticks (from earlier selections)
// are discarded by comparing timestamps.
func (m Model) handleSelectionDebounce(msg selectionDebounceTickMsg) (tea.Model, tea.Cmd) {
	if m.selectionDebounce == nil || !msg.timestamp.Equal(*m.selectionDebounce) {
		return m, nil
	}
	if m.selectionPending == nil || m.selectionPending.ID != msg.projectID {
		return m, nil
	}
	project := *m.selectionPending
	m.selectionDebounce = nil
	m.selectionPending = nil
	return m, (&m).loadSelectedProjectData(project)
}

// handleCommitsLoaded stores fetched commits and invalidates the detail pane
// cache so the "Recent Commits" section renders on the next frame. Errors are
// logged but not surfaced to the user — missing commits are non-critical.
func (m Model) handleCommitsLoaded(msg commitsLoadedMsg) (tea.Model, tea.Cmd) {
	m.commitLoading[msg.projectID] = false
	if msg.err != nil {
		m.logError("load commits", "err", msg.err, "project", msg.projectID)
		return m, nil
	}
	m.commitCache[msg.projectID] = msg.commits
	// Invalidate detail cache so commits appear immediately
	selectedID := 0
	if project, ok := m.selectedProject(); ok {
		selectedID = project.ID
	}
	if msg.projectID == selectedID {
		(&m).invalidateDetailCache()
	}
	return m, nil
}

func (m Model) handleSearchDebounceTickMsg(msg searchDebounceTickMsg) (tea.Model, tea.Cmd) {
	// Ignore stale ticks - only process if timer matches
	if m.search.debounceTimer == nil || !msg.timestamp.Equal(*m.search.debounceTimer) {
		return m, nil
	}

	// Verify query still matches
	if msg.query != m.search.pendingQuery {
		return m, nil
	}

	// Apply the pending query
	m.search.debounceTimer = nil
	m.search.query = m.search.pendingQuery
	m.search.pendingQuery = ""

	// Now run the expensive filter
	m.invalidateVisibleCache()
	m.ensureSelectionBounds()
	m.updateProjectList()

	// Batch prefetch pipeline status for filtered results
	return m, (&m).queueBatchPrefetchPipelineStatus()
}
