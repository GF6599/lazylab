// state_projects.go manages project list state: selection, pagination, search
// filtering, favorites, pipeline status prefetching, visible-projects caching,
// and detail-pane render caching.
package ui

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// startSearch focuses the (cleared) search input, returning textinput.Blink to
// drive the cursor.
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

// clearSelectionDebounce cancels any queued sidebar auto-load for the selected
// project. This is needed when the visible selection disappears or the
// multi-panel auto-load flow is no longer active.
func (m *Model) clearSelectionDebounce() {
	m.selectionPending = nil
	m.selectionDebounce = nil
}

// handleSelectedProjectChange invalidates project-detail rendering and, in the
// multi-panel layout, re-queues sidebar data loading when the selected project
// identity changes due to search, pagination, favorites, or list reloads.
func (m *Model) handleSelectedProjectChange(prevID int, prevOK bool) tea.Cmd {
	currID, currOK := m.currentSelectedProjectID()
	if prevID == currID && prevOK == currOK {
		return nil
	}
	m.invalidateDetailCache()
	m.populateDetailCache()
	if m.mode != modeMultiPanel || !currOK {
		m.clearSelectionDebounce()
		return nil
	}
	return m.autoLoadSelectedProjectData()
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
	// NewModel initializes this, but tests and zero-value Models may not.
	if m.batchInFlight == nil {
		m.batchInFlight = &atomic.Bool{}
	}

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

	perPage := m.displayPerPage()
	if m.page == m.visibleCachePage && m.visibleCachePerPage == perPage && m.visibleCache != nil &&
		m.visibleCacheQuery == "" && m.visibleCacheTab == projectTabAll {
		return m.visibleCache
	}

	pageData := m.pageSlice(m.page)
	m.visibleCache = pageData
	m.visibleCachePage = m.page
	m.visibleCachePerPage = perPage
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
	m.visibleCachePerPage = 0
	m.visibleCacheTab = projectTabAll
}

// detailCacheState memoizes the rendered detail pane keyed by the inputs that
// affect its output. A zero value means "no cached output" and forces a render.
type detailCacheState struct {
	projectID   int
	pipelineID  int
	pipelineHas bool
	width       int
	height      int
	output      string
}

// renderDetailCached returns the cached detail pane render if it matches the
// current inputs, otherwise renders fresh without mutating cache. This is a
// read-only View-time accessor; populateDetailCache writes the cache from
// Update paths.
func (m Model) renderDetailCached(width, height int) string {
	visible := m.visibleProjects()
	if len(visible) == 0 {
		return renderDetailPane(&m, width)
	}
	project := visible[m.selected]
	pipelineState, _ := m.pipelineStatus.Peek(project.ID)
	key := detailCacheState{
		projectID:   project.ID,
		pipelineID:  pipelineState.info.ID,
		pipelineHas: pipelineState.hasInfo,
		width:       width,
		height:      height,
	}
	if m.detailCache.output != "" &&
		m.detailCache.projectID == key.projectID &&
		m.detailCache.pipelineID == key.pipelineID &&
		m.detailCache.pipelineHas == key.pipelineHas &&
		m.detailCache.width == key.width &&
		m.detailCache.height == key.height {
		return m.detailCache.output
	}
	return renderDetailPane(&m, width)
}

// populateDetailCache pre-renders the project detail pane and stores it so
// subsequent View calls hit the cache. Called from Update paths when any input
// to the cache key changes (selection, pipeline status, window size).
func (m *Model) populateDetailCache() {
	if m.mode != modeMultiPanel {
		return
	}
	layout := computeLayout(m.width, m.height, m.focus)
	if !layout.OK {
		return
	}
	width, height := layout.DetailWidth, layout.DetailHeight
	visible := m.visibleProjects()
	if len(visible) == 0 || m.selected >= len(visible) {
		m.detailCache = detailCacheState{}
		return
	}
	project := visible[m.selected]
	pipelineState, _ := m.pipelineStatus.Peek(project.ID)
	m.detailCache = detailCacheState{
		projectID:   project.ID,
		pipelineID:  pipelineState.info.ID,
		pipelineHas: pipelineState.hasInfo,
		width:       width,
		height:      height,
		output:      renderDetailPane(m, width),
	}
}

// invalidateDetailCache clears the detail pane render cache.
func (m *Model) invalidateDetailCache() {
	m.detailCache = detailCacheState{}
}

// panePageSize reports the rows a panel can draw, which is what one page has to hold for the panel
// to have no blank space under its last row. Zero means there is no pane to measure.
func (m Model) panePageSize(panel PanelID) int {
	if m.mode != modeMultiPanel {
		return 0
	}
	layout := computeLayout(m.width, m.height, m.focus)
	if !layout.OK {
		return 0
	}
	return max(0, layout.PanelHeights[panel])
}

// apiPerPage is the page size projects are fetched in, which decides where a fetched page lands in
// allProjects. It is deliberately separate from how many one screen shows.
func (m Model) apiPerPage() int {
	if m.opts.ProjectsPerPage > 0 {
		return m.opts.ProjectsPerPage
	}
	return defaultProjectsPerPage
}

// displayPerPage is how many projects one page shows, which follows the pane.
func (m Model) displayPerPage() int {
	if n := m.panePageSize(PanelProjects); n > 0 {
		return n
	}
	return m.apiPerPage()
}

func (m Model) pipelinePageSize() int {
	return fetchablePageSize(m.panePageSize(PanelPipelines), pipelinePerPage)
}

func (m Model) mrPageSize() int {
	return fetchablePageSize(m.panePageSize(PanelMRs), mrPerPage)
}

// fetchablePageSize leaves the last rows of a pane taller than the ceiling blank, because GitLab
// answers a larger request with a page of the ceiling size anyway.
func fetchablePageSize(room, fallback int) int {
	if room <= 0 {
		return fallback
	}
	return min(room, maxAPIPerPage)
}

// pageHolding exists because resizing a pane moves every page boundary, so the page number the user
// was on names a different set of rows afterwards, and only the position survives the change.
func pageHolding(position, perPage int) int {
	if position < 1 || perPage < 1 {
		return 1
	}
	return (position-1)/perPage + 1
}

// displayTotalPages counts the pages the collection fills at the current pane height. It falls back
// to what has loaded so far while the collection total is still unknown, so paging never offers a
// page there is nothing behind.
func (m Model) displayTotalPages() int {
	perPage := m.displayPerPage()
	total := m.totalProjects
	if total <= 0 {
		total = m.loadedProjectCount()
	}
	if perPage <= 0 || total <= 0 {
		return 1
	}
	return max(1, (total+perPage-1)/perPage)
}

// loadedProjectCount reports how many leading projects are real. A page still in flight leaves
// zero value placeholders in allProjects rather than a shorter slice, so the length of that slice
// overstates what there is to show until every page has landed.
func (m Model) loadedProjectCount() int {
	ready := 0
	for m.pagesReady[ready+1] {
		ready++
	}
	return min(ready*m.apiPerPage(), len(m.allProjects))
}

func (m Model) pageSlice(page int) []gitlab.ProjectNode {
	if page <= 0 {
		page = 1
	}
	loaded := m.loadedProjectCount()
	perPage := m.displayPerPage()
	start := (page - 1) * perPage
	if loaded == 0 || start >= loaded {
		return nil
	}
	return m.allProjects[start:min(start+perPage, loaded)]
}

// updateProjectList syncs the bubbles list component with the current visible projects
func (m *Model) updateProjectList() {
	visible := m.visibleProjects()
	items := make([]list.Item, len(visible))
	for i, p := range visible {
		items[i] = projectItem{project: p}
	}
	m.projectList.SetItems(items)

	// SetItems keeps the delegate, so the shared status cache and the animation frame
	// already in it both survive.

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
	insertAt := (page.Page - 1) * m.apiPerPage()
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
	target := max(m.page+delta, 1)
	if total := m.displayTotalPages(); target > total {
		target = total
	}
	if target == m.page {
		return nil
	}
	m.page = target
	m.invalidateVisibleCache()
	if missing, ok := m.firstMissingFetchPage(target); ok {
		m.status = fmt.Sprintf("Loading page %d...", target)
		// An in-flight background fetch already chains toward every missing
		// page, so a second dispatch would only duplicate requests.
		if m.backgroundLoading {
			return nil
		}
		m.backgroundLoading = true
		return fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.apiPerPage(), missing, true)
	}
	m.status = fmt.Sprintf("Viewing page %d", target)
	m.ensureSelectionBounds()
	m.updateProjectList()
	return nil
}

// queueBackgroundProjectFetch starts fetching the first project page not yet
// loaded, walking toward the server-reported total. It returns nil when the
// collection is complete, a fetch is already in flight, or the total is
// unknown because GitLab withholds counts past ten thousand items, where
// movePage still fetches pages on demand.
func (m *Model) queueBackgroundProjectFetch() tea.Cmd {
	if m.backgroundLoading || m.client == nil {
		return nil
	}
	perPage := m.apiPerPage()
	if perPage <= 0 || m.totalProjects <= 0 {
		return nil
	}
	lastPage := (m.totalProjects + perPage - 1) / perPage
	for page := 1; page <= lastPage; page++ {
		if !m.pagesReady[page] {
			m.backgroundLoading = true
			return fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, perPage, page, true)
		}
	}
	return nil
}

// firstMissingFetchPage reports the first fetched page a screen needs and does not have. A screen
// and a fetch hold different numbers of projects, so one screen can span several fetched pages and
// the two page numbers do not correspond.
func (m Model) firstMissingFetchPage(displayPage int) (int, bool) {
	perScreen, perFetch := m.displayPerPage(), m.apiPerPage()
	if perScreen <= 0 || perFetch <= 0 {
		return 0, false
	}
	start := (displayPage - 1) * perScreen
	end := start + perScreen
	if m.totalProjects > 0 {
		end = min(end, m.totalProjects)
	}
	if end <= start {
		return 0, false
	}
	for p := start/perFetch + 1; p <= (end-1)/perFetch+1; p++ {
		if !m.pagesReady[p] {
			return p, true
		}
	}
	return 0, false
}

// copyCloneCommand returns a Cmd that copies "git clone <ssh-url>" for the
// selected project to the clipboard off the event loop. Guard paths still
// set m.status synchronously and return a nil Cmd.
func (m *Model) copyCloneCommand() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	if project.SSHURLToRepo == "" {
		m.status = "Project has no SSH URL"
		return nil
	}
	cmd := fmt.Sprintf("git clone %s", project.SSHURLToRepo)
	return writeClipboardCmd(cmd, "Copied clone command to clipboard")
}

// syncPanelSizes re-pushes the panel dimensions when anything the layout reads has moved. The
// focused panel takes most of the sidebar height, and a list or a table holds its own height until
// something hands it a new one, so a panel that grows keeps drawing the old number of rows.
//
// This is a gate rather than a plain call because the sizing repopulates the detail cache, which is
// real rendering work and must not run on every animation frame. A caller that has rebuilt a
// sub-component at zero size has to call updateViewportSizes itself, because the key it would
// compare against has not moved.
func (m *Model) syncPanelSizes() tea.Cmd {
	key := layoutKey{
		mode:              m.mode,
		explorerProjectID: m.explorer.project.ID,
		width:             m.width,
		height:            m.height,
		focus:             m.focus,
	}
	if key == m.lastLayoutKey {
		return nil
	}
	m.lastLayoutKey = key
	m.updateViewportSizes()
	return m.queuePageSizeRefetch()
}

// updateViewportSizes propagates the latest layout dimensions into every
// sub-component that caches its own size (lists, tables, viewports). View
// renderers must be pure, so all dimensional state is pushed from here.
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
	if m.mode == modeMultiPanel {
		layout := computeLayout(m.width, m.height, m.focus)
		if layout.OK {
			m.applyMultiPanelLayout(layout)
		}
	} else if m.mode == modePipelines {
		width := pipelineLogContentWidth(m.width)
		height := pipelineLogContentHeight(m.height)
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

// applyMultiPanelLayout pushes per-panel dimensions into every sub-component
// from a freshly computed layout. Called on every resize and on any state
// change that affects the panel geometry (focus, screen mode, layout mode).
func (m *Model) applyMultiPanelLayout(layout layoutResult) {
	sidebarWidth := layout.SidebarWidth
	detailWidth, detailHeight := layout.DetailWidth, layout.DetailHeight

	// Projects list. One page holds as many projects as the pane draws, so a pane that changed
	// size is showing a different page, and its rows have to be rebuilt rather than only resized.
	projHeight := max(1, layout.PanelHeights[PanelProjects])
	m.projectList.SetSize(sidebarWidth, projHeight)
	m.ensureSelectionBounds()
	m.updateProjectList()

	// Pipelines list
	pipelinesHeight := max(1, layout.PanelHeights[PanelPipelines])
	m.pipelineView.pipelineList.SetSize(sidebarWidth, pipelinesHeight)

	// Stage table: columns scale with width; height excludes header + bottom
	// hint line (the hint is conditional but we always reserve a row to avoid
	// reflows when the selected job changes).
	stagesHeight := max(1, layout.PanelHeights[PanelStages]-3)
	m.pipelineView.stageTable.SetColumns(stageTableColumns(sidebarWidth))
	m.pipelineView.stageTable.SetWidth(sidebarWidth)
	m.pipelineView.stageTable.SetHeight(stagesHeight)

	// Log viewport in the detail pane. The pipeline log renderer prepends a
	// few header lines (title, KV pairs, divider) before the viewport; we
	// underestimate slightly so the viewport never overflows the pane.
	logVPHeight := max(1, detailHeight-pipelineLogHeaderReserve)
	if m.pipelineView.logViewport.Width != detailWidth || m.pipelineView.logViewport.Height != logVPHeight {
		m.pipelineView.logViewport.Width = detailWidth
		m.pipelineView.logViewport.Height = logVPHeight
	}
	if m.mrView.mrViewport.Width != detailWidth || m.mrView.mrViewport.Height != detailHeight {
		m.mrView.mrViewport.Width = detailWidth
		m.mrView.mrViewport.Height = detailHeight
	}

	// Detail-pane cache is keyed on width/height; resize invalidates it and
	// repopulates from the latest project selection so the next View hits cache.
	m.populateDetailCache()
}
