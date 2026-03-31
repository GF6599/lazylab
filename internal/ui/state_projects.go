// state_projects.go manages project list state: selection, pagination, search
// filtering, favorites, pipeline status prefetching, visible-projects caching,
// and detail-pane render caching.
package ui

import (
	"fmt"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// startSearch activates the search input and returns the blink command.
func (m Model) startSearch() (tea.Model, tea.Cmd) {
	m.search.active = true
	m.search.query = ""
	m.search.input.SetValue("")
	m.search.input.Focus()
	return m, textinput.Blink
}

func (m Model) selectedProject() (gitlab.ProjectNode, bool) {
	projects := m.visibleProjects()
	if len(projects) == 0 || m.selected < 0 || m.selected >= len(projects) {
		return gitlab.ProjectNode{}, false
	}
	return projects[m.selected], true
}

func (m Model) currentSelectedProjectID() (int, bool) {
	project, ok := m.selectedProject()
	if !ok {
		return 0, false
	}
	return project.ID, true
}

// ensureSelectionBounds clamps m.selected to [0, len(visibleProjects)-1].
// Must be called after any operation that changes the visible project set
// (page navigation, search query change, tab switch, project list reload)
// to prevent out-of-bounds selection indexes.
func (m *Model) ensureSelectionBounds() {
	projects := m.visibleProjects()
	if len(projects) == 0 {
		m.selected = 0
		return
	}
	if m.selected >= len(projects) {
		m.selected = len(projects) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// queueBatchPrefetchPipelineStatus enqueues a single [batchFetchPipelineStatusCmd]
// for all visible projects whose pipeline status is missing or stale (older than
// pipelineRefreshInterval). Projects already loading are skipped. Each project
// is marked as loading before the command fires to prevent duplicate fetches
// from overlapping ticks.
func (m *Model) queueBatchPrefetchPipelineStatus() tea.Cmd {
	// Skip if a previous batch is still in-flight to prevent accumulation
	if m.batchInFlight != nil && m.batchInFlight.Load() {
		return nil
	}

	visible := m.visibleProjects()
	if len(visible) == 0 {
		return nil
	}

	var toFetch []gitlab.ProjectNode
	for _, project := range visible {
		state, _ := m.pipelineStatus.Get(project.ID)
		if !state.loading && (state.lastFetched.IsZero() || time.Since(state.lastFetched) > pipelineRefreshInterval) {
			toFetch = append(toFetch, project)
		}
	}

	if len(toFetch) == 0 {
		return nil
	}

	for _, project := range toFetch {
		state, _ := m.pipelineStatus.Get(project.ID)
		state.loading = true
		m.pipelineStatus.Set(project.ID, state)
	}

	m.batchInFlight.Store(true)
	return batchFetchPipelineStatusCmd(m.ctx, m.client, m.opts.PipelineTimeout, toFetch, m.batchInFlight)
}

func (m *Model) queuePipelineFetchForSelection(force bool) tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		return nil
	}
	return m.queuePipelineFetch(project, force)
}

// queuePipelineFetch starts a latest-pipeline status fetch for a project,
// guarding against duplicate in-flight requests (returns nil if already loading)
// and redundant fetches within pipelineRefreshInterval (unless force is true).
func (m *Model) queuePipelineFetch(project gitlab.ProjectNode, force bool) tea.Cmd {
	state, _ := m.pipelineStatus.Get(project.ID)
	if state.loading {
		return nil
	}
	if !force && !state.lastFetched.IsZero() && time.Since(state.lastFetched) < pipelineRefreshInterval {
		return nil
	}
	ref := pipelineAllRefsRef
	state.loading = true
	state.err = nil
	state.empty = false
	state.ref = ref
	m.pipelineStatus.Set(project.ID, state)
	return fetchPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, ref)
}

// queuePipelineViewRefresh refreshes all pipeline data for the currently viewed
// pipeline: the pipeline list, stages, jobs, bridges, expanded child jobs, and
// the active log. Called on every auto-refresh tick (5s) to keep the UI live.
// Each sub-fetch is independently guarded against duplicate in-flight requests.
func (m *Model) queuePipelineViewRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	var cmds []tea.Cmd
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	perPage := m.pipelineView.perPage
	if perPage <= 0 {
		perPage = pipelinePerPage
	}
	if !m.pipelineView.loading {
		m.pipelineView.loading = true
		cmds = append(cmds, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, page, perPage))
	}
	if cmd := m.queuePipelineSubRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineLogRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// visibleProjects returns the project list for the current view state. The
// result is memoized: repeated calls with the same (tab, search query, page)
// triple return the cached slice without recomputing. Call invalidateVisibleCache
// when any of these inputs change.
//
// For the favorites tab, projects are filtered from allProjects (no pagination).
// For the "all" tab, search applies a fuzzy filter across all projects;
// without search, pagination slices allProjects by page.
func (m *Model) visibleProjects() []gitlab.ProjectNode {
	// Determine the base set based on active tab
	if m.projectTab == projectTabFavorites {
		// Favorites tab: filter allProjects to favorites only
		if m.visibleCache != nil && m.visibleCacheTab == projectTabFavorites && m.visibleCacheQuery == m.search.query {
			return m.visibleCache
		}
		// Build ID→project index for quick lookup
		byID := make(map[int]gitlab.ProjectNode, len(m.allProjects))
		for _, p := range m.allProjects {
			byID[p.ID] = p
		}
		filtered := make([]gitlab.ProjectNode, 0, len(m.favOrder))
		for _, id := range m.favOrder {
			p, ok := byID[id]
			if !ok {
				continue
			}
			if m.search.query != "" && !fuzzyMatch(p.PathWithNamespace, m.search.query) && !fuzzyMatch(p.Name, m.search.query) {
				continue
			}
			filtered = append(filtered, p)
		}
		m.visibleCache = filtered
		m.visibleCacheQuery = m.search.query
		m.visibleCachePage = -1
		m.visibleCacheTab = projectTabFavorites
		return filtered
	}

	// All tab: existing behavior
	if m.search.query != "" {
		if m.search.query == m.visibleCacheQuery && m.visibleCache != nil && m.visibleCacheTab == projectTabAll {
			return m.visibleCache
		}
		filtered := make([]gitlab.ProjectNode, 0, len(m.allProjects))
		for _, p := range m.allProjects {
			if fuzzyMatch(p.PathWithNamespace, m.search.query) || fuzzyMatch(p.Name, m.search.query) {
				filtered = append(filtered, p)
			}
		}
		m.visibleCache = filtered
		m.visibleCacheQuery = m.search.query
		m.visibleCachePage = -1
		m.visibleCacheTab = projectTabAll
		return filtered
	}

	if m.page == m.visibleCachePage && m.visibleCache != nil && m.visibleCacheQuery == "" && m.visibleCacheTab == projectTabAll {
		return m.visibleCache
	}

	pageData := m.pageSlice(m.page)
	m.visibleCache = pageData
	m.visibleCachePage = m.page
	m.visibleCacheQuery = ""
	m.visibleCacheTab = projectTabAll
	return pageData
}

// invalidateVisibleCache clears the visibleProjects cache. Setting
// visibleCache to nil is sufficient: visibleProjects() nil-checks
// the cache slice before returning it.
func (m *Model) invalidateVisibleCache() {
	m.visibleCache = nil
	m.visibleCacheQuery = ""
	m.visibleCachePage = -1
	m.visibleCacheTab = projectTabAll
}

// cachedDetailPane returns the detail pane view, using cache when valid
func (m *Model) cachedDetailPane(width, height int) string {
	visible := m.visibleProjects()
	if len(visible) == 0 {
		// Clear cache for empty state
		m.detailCacheProjectID = 0
		m.detailCachePipelineID = 0
		m.detailCachePipelineHas = false
		m.detailCacheWidth = 0
		m.detailCacheHeight = 0
		m.detailCacheOutput = ""
		// Render empty state
		return renderDetailPane(m, width)
	}

	project := visible[m.selected]
	pipelineState, _ := m.pipelineStatus.Get(project.ID)

	// Check cache validity
	cacheValid := m.detailCacheProjectID == project.ID &&
		m.detailCachePipelineID == pipelineState.info.ID &&
		m.detailCachePipelineHas == pipelineState.hasInfo &&
		m.detailCacheWidth == width &&
		m.detailCacheHeight == height

	if cacheValid && m.detailCacheOutput != "" {
		return m.detailCacheOutput
	}

	// Render fresh
	output := renderDetailPane(m, width)

	// Update cache
	m.detailCacheProjectID = project.ID
	m.detailCachePipelineID = pipelineState.info.ID
	m.detailCachePipelineHas = pipelineState.hasInfo
	m.detailCacheWidth = width
	m.detailCacheHeight = height
	m.detailCacheOutput = output

	return output
}

// invalidateDetailCache clears the detail pane render cache
func (m *Model) invalidateDetailCache() {
	m.detailCacheProjectID = 0
	m.detailCachePipelineID = 0
	m.detailCachePipelineHas = false
	m.detailCacheWidth = 0
	m.detailCacheHeight = 0
	m.detailCacheOutput = ""
}

func (m Model) pageSlice(page int) []gitlab.ProjectNode {
	if page <= 0 {
		page = 1
	}
	if len(m.allProjects) == 0 || !m.pagesReady[page] {
		return nil
	}
	start := (page - 1) * m.opts.ProjectsPerPage
	if start >= len(m.allProjects) {
		return nil
	}
	end := start + m.opts.ProjectsPerPage
	end = min(end, len(m.allProjects))
	return m.allProjects[start:end]
}

// updateProjectList syncs the bubbles list component with the current visible projects
func (m *Model) updateProjectList() {
	visible := m.visibleProjects()
	items := make([]list.Item, len(visible))
	for i, p := range visible {
		items[i] = projectItem{project: p}
	}
	m.projectList.SetItems(items)

	// Note: No need to call SetDelegate() - delegate holds pointer to shared pipelineStatus cache

	// Sync selection with list cursor
	if m.selected >= 0 && m.selected < len(items) {
		m.projectList.Select(m.selected)
	}
}

// appendPage inserts a page of projects at the correct offset in allProjects,
// growing the slice with zero-value placeholders if pages arrive out of order.
// This ensures pageSlice works regardless of the order in which background
// page fetches complete.
func (m *Model) appendPage(page gitlab.ProjectPage) {
	m.pagesReady[page.Page] = true
	m.pagesLoaded = len(m.pagesReady)

	// Insert projects at the correct offset so pageSlice works regardless of
	// the order in which pages arrive (important for lazy pagination).
	perPage := m.opts.ProjectsPerPage
	if perPage <= 0 {
		perPage = 30
	}
	insertAt := (page.Page - 1) * perPage
	needed := insertAt + len(page.Projects)
	if needed > len(m.allProjects) {
		// Grow the slice with zero-value placeholders
		m.allProjects = append(m.allProjects, make([]gitlab.ProjectNode, needed-len(m.allProjects))...)
	}
	copy(m.allProjects[insertAt:], page.Projects)

	m.invalidateVisibleCache()
	if m.totalPages <= 0 {
		m.totalPages = page.TotalPages
	}
	if m.totalPages <= 0 {
		m.totalPages = m.pagesLoaded
	}
	m.ensureSelectionBounds()
}

// movePage navigates the project list by delta pages. Returns nil immediately
// for the favorites tab (favorites are not paginated). If the target page
// hasn't been background-loaded yet, starts a foreground fetch.
func (m *Model) movePage(delta int) tea.Cmd {
	if m.projectTab == projectTabFavorites {
		return nil
	}
	target := m.page + delta
	target = max(target, 1)
	if m.totalPages > 0 && target > m.totalPages {
		target = m.totalPages
	}
	if target == m.page {
		return nil
	}
	m.page = target
	m.paginator.Page = m.page - 1 // Paginator is 0-indexed
	if !m.pagesReady[m.page] {
		m.backgroundLoading = true
		m.status = fmt.Sprintf("Loading page %d...", m.page)
		return fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, m.page, true)
	}
	m.status = fmt.Sprintf("Viewing page %d", m.page)
	m.ensureSelectionBounds()
	m.updateProjectList()
	return nil
}

func (m *Model) copyCloneCommand() {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return
	}
	if project.SSHURLToRepo == "" {
		m.status = "Project has no SSH URL"
		return
	}
	cmd := fmt.Sprintf("git clone %s", project.SSHURLToRepo)
	if err := clipboard.WriteAll(cmd); err != nil {
		m.status = "Failed to copy clone command"
		m.logError("copy clipboard", "err", err)
		return
	}
	m.status = "Copied clone command to clipboard"
}

// updateViewportSizes updates viewport dimensions when terminal resizes.
// viewport.Model exposes Width/Height as public fields (no SetSize method).
func (m *Model) updateViewportSizes() {
	if m.mode == modeExplorer || (m.mode == modeMultiPanel && m.explorer.project.ID != 0) {
		width := previewContentWidth(m.width)
		height := previewContentHeight(m.height)
		if m.explorer.preview.viewport.Width != width || m.explorer.preview.viewport.Height != height {
			m.explorer.preview.viewport.Width = width
			m.explorer.preview.viewport.Height = height
		}
	}
	if m.mode == modePipelines || m.mode == modeMultiPanel {
		var width, height int
		if m.mode == modeMultiPanel {
			layout := computeLayout(m.width, m.height, m.focus)
			if layout.OK {
				width = layout.DetailWidth
				height = layout.DetailHeight
			}
		} else {
			width = pipelineLogContentWidth(m.width)
			height = pipelineLogContentHeight(m.height)
		}
		if width > 0 && height > 0 {
			if m.pipelineView.logViewport.Width != width || m.pipelineView.logViewport.Height != height {
				m.pipelineView.logViewport.Width = width
				m.pipelineView.logViewport.Height = height
			}
			if m.mrView.mrViewport.Width != width || m.mrView.mrViewport.Height != height {
				m.mrView.mrViewport.Width = width
				m.mrView.mrViewport.Height = height
			}
		}
	}
}
