package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
)

// openProjectActions transitions to the action menu modal for a project.
// The modal offers "View pipelines" or "Browse files" — it does not fetch
// any data, so it returns no command.
func (m Model) openProjectActions(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modeProjectActions

	// Initialize action menu list
	items := make([]list.Item, len(projectActionOptions))
	for i, option := range projectActionOptions {
		items[i] = actionMenuItem{label: option, index: i}
	}

	delegate := actionMenuDelegate{}
	menuList := newBareList(items, delegate, 0, 0)
	menuList.Styles.Title = titleStyle

	m.actionMenu = actionMenuState{
		project:  project,
		menuList: menuList,
		selected: 0,
	}
	m.status = fmt.Sprintf("Actions for %s", project.PathWithNamespace)
	return m, nil
}

func (m *Model) closeActionMenu() {
	m.mode = modeProjects
	m.actionMenu = actionMenuState{}
	m.updateProjectListSize()
}

// openExplorer transitions to the three-pane file browser. It resets all
// explorer state (stack, preview, bubbles lists) and starts a root tree
// fetch for the project's default branch. The preview viewport is initialized
// here with proper dimensions — all subsequent preview resets must preserve
// this viewport instance to avoid zero-sized rendering (see queueExplorerPreview).
func (m Model) openExplorer(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	ref := project.DefaultBranch
	if ref == "" {
		ref = "main"
	}
	m.mode = modeExplorer

	// Initialize bubbles lists for explorer panes
	delegate := treeEntryDelegate{}
	parentList := newBareList(nil, delegate, 0, 0)
	currentList := newBareList(nil, delegate, 0, 0)

	// Initialize preview viewport with proper dimensions
	previewVp := viewport.New(previewContentWidth(m.width), previewContentHeight(m.height))

	m.explorer = explorerState{
		project:     project,
		ref:         ref,
		stack:       []dirState{{path: "", loading: true}},
		parentList:  parentList,
		currentList: currentList,
		preview:     previewState{viewport: previewVp},
	}
	m.status = fmt.Sprintf("Browsing %s", project.PathWithNamespace)
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, ref, "")
}

// openPipelineView transitions to the pipeline list for a project. All
// pipeline caches (stages, jobs, logs, bridges, child jobs) are freshly
// initialized, and log auto-follow is enabled. Returns a command to fetch
// the first page of pipelines.
func (m Model) openPipelineView(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modePipelines

	// Initialize stage table (job-per-row layout)
	columns := []table.Column{
		{Title: "Job", Width: 24},
		{Title: "Stage", Width: 16},
		{Title: "Status", Width: 16},
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(false),
		table.WithHeight(10),
	)

	// Style the table with Rose Pine colors (matching pane borders)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(rosePineSubtle).
		BorderBottom(true).
		Bold(false).
		Foreground(rosePineSubtle)
	s.Selected = s.Selected.
		Foreground(rosePineText).
		Background(rosePineHighlightMed).
		Bold(false)
	s.Cell = s.Cell.
		Foreground(rosePineText)
	t.SetStyles(s)

	// Initialize pipeline list
	delegate := pipelineDelegate{}
	pipelineList := newBareList(nil, delegate, 0, 0)
	pipelineList.Styles.Title = titleStyle

	// Initialize log viewport with proper dimensions
	logVp := viewport.New(pipelineLogContentWidth(m.width), pipelineLogContentHeight(m.height))

	m.pipelineView = pipelineViewState{
		project:       project,
		pipelineList:  pipelineList,
		loading:       true,
		page:          1,
		totalPages:    1,
		perPage:       pipelinePerPage,
		stages:        NewAsyncCache[int, []gitlab.PipelineStage](),
		stageTable:    t,
		jobs:          NewAsyncCache[int, []gitlab.PipelineJob](),
		logs:          NewAsyncCache[int, string](),
		logViewport:   logVp,
		logAutoFollow: true,
		focus:         pipelineFocusPipelines,
		bridges:       NewAsyncCache[int, []gitlab.PipelineBridge](),
		childJobs:     NewAsyncCache[int, []gitlab.PipelineJob](),
	}
	m.status = fmt.Sprintf("Pipelines for %s", project.PathWithNamespace)
	return m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, m.pipelineView.page, m.pipelineView.perPage)
}

// clearRetryConfirm resets the retry confirmation modal fields only,
// without affecting the retrying flag or retry error.
func (m *Model) clearRetryConfirm() {
	m.pipelineView.confirmRetry = false
	m.pipelineView.confirmRetryID = 0
	m.pipelineView.confirmRetryRef = ""
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryJobID = 0
	m.pipelineView.confirmRetryJobName = ""
	m.pipelineView.confirmRetryJobStage = ""
	m.pipelineView.confirmRetryProjectID = 0
}

// clearAllRetryState resets all retry-related fields including the confirmation
// modal state, retrying flag, and retry error.
func (m *Model) clearAllRetryState() {
	m.clearRetryConfirm()
	m.pipelineView.retrying = false
	m.pipelineView.retryErr = nil
}

func (m *Model) closePipelineView() {
	m.mode = modeProjects
	m.pipelineView = pipelineViewState{}
	m.actionMenu = actionMenuState{}
	m.updateProjectListSize()
}

// resetPipelineViewCaches reinitializes all per-pipeline caches (stages, jobs,
// logs) so that stale data from a previous page or reload is not displayed.
func (pv *pipelineViewState) resetCaches() {
	pv.stages.Clear()
	pv.stageSelected = 0
	pv.jobRows = nil
	pv.stageJobRows = nil
	pv.jobs.Clear()
	pv.logs.Clear()
	pv.childJobs.Clear()
	// Note: matrixExpanded is intentionally preserved across refreshes
}

func (m *Model) reloadPipelineView() (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID == 0 {
		return *m, nil
	}
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	perPage := m.pipelineView.perPage
	if perPage <= 0 {
		perPage = pipelinePerPage
	}
	m.pipelineView.loading = true
	m.pipelineView.err = nil
	m.pipelineView.pipelines = nil
	m.pipelineView.selected = 0
	m.pipelineView.page = page
	m.pipelineView.perPage = perPage
	m.pipelineView.resetCaches()
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
	m.pipelineView.focus = pipelineFocusPipelines
	return *m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, page, perPage)
}

func (m *Model) changePipelinePage(delta int) tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	target := page + delta
	if target < 1 {
		target = 1
	}
	if m.pipelineView.totalPages > 0 && target > m.pipelineView.totalPages {
		target = m.pipelineView.totalPages
	}
	if target == page {
		return nil
	}
	perPage := m.pipelineView.perPage
	if perPage <= 0 {
		perPage = pipelinePerPage
	}
	m.pipelineView.loading = true
	m.pipelineView.err = nil
	m.pipelineView.pipelines = nil
	m.pipelineView.selected = 0
	m.pipelineView.page = target
	m.pipelineView.perPage = perPage
	m.pipelineView.totalPages = max(1, m.pipelineView.totalPages)
	m.pipelineView.resetCaches()
	m.resetPipelineLogPreview()
	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, target, perPage)
}

// descendDirectory pushes a new dirState onto the explorer stack and fetches
// its tree listing. Before descending, it copies the current list items into
// the parent list so the left pane shows the correct context.
//
// The preview viewport is preserved across the state reset — see
// queueExplorerPreview for the rationale.
func (m Model) descendDirectory(entry gitlab.TreeNode) (tea.Model, tea.Cmd) {
	// Copy current list items to parent list before descending
	m.explorer.parentList.SetItems(m.explorer.currentList.Items())
	if cur := m.currentDirState(); cur != nil {
		m.explorer.parentList.Select(cur.selected)
	}
	newState := dirState{
		path:    entry.Path,
		loading: true,
	}
	m.explorer.stack = append(m.explorer.stack, newState)
	m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m Model) navigateExplorerUp() (tea.Model, tea.Cmd) {
	if len(m.explorer.stack) <= 1 {
		m.closeExplorer("Back to projects")
		return m, nil
	}
	m.explorer.stack = m.explorer.stack[:len(m.explorer.stack)-1]
	m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
	return m, m.queueExplorerPreview()
}

func (m Model) reloadExplorerPath() (tea.Model, tea.Cmd) {
	cur := m.currentDirState()
	if cur == nil {
		return m, nil
	}
	cur.loading = true
	cur.err = nil
	cur.entries = nil
	m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), cur.path)
}

func (m *Model) closeExplorer(status string) {
	if m.mode != modeMultiPanel {
		m.mode = modeProjects
	}
	m.explorer = explorerState{}
	if status != "" {
		m.status = status
	}
}

func (m *Model) movePage(delta int) tea.Cmd {
	if m.projectTab == projectTabFavorites {
		return nil
	}
	target := m.page + delta
	if target < 1 {
		target = 1
	}
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
		if m.opts.Logger != nil {
			m.opts.Logger.Error("copy clipboard", "err", err)
		}
		return
	}
	m.status = "Copied clone command to clipboard"
}

// copyPipelineURL copies the selected pipeline's web URL to the clipboard.
func (m *Model) copyPipelineURL() {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return
	}
	if pipeline.WebURL == "" {
		m.status = "Pipeline has no URL"
		return
	}
	if err := clipboard.WriteAll(pipeline.WebURL); err != nil {
		m.status = "Failed to copy pipeline URL"
		if m.opts.Logger != nil {
			m.opts.Logger.Error("copy clipboard", "err", err)
		}
		return
	}
	m.status = fmt.Sprintf("Copied pipeline #%d URL", pipeline.ID)
}

// copyJobURL copies the selected job's web URL to the clipboard.
func (m *Model) copyJobURL() {
	job := m.selectedPipelineJob()
	if job == nil {
		m.status = "No job selected"
		return
	}
	if job.WebURL == "" {
		m.status = "Job has no URL"
		return
	}
	if err := clipboard.WriteAll(job.WebURL); err != nil {
		m.status = "Failed to copy job URL"
		if m.opts.Logger != nil {
			m.opts.Logger.Error("copy clipboard", "err", err)
		}
		return
	}
	m.status = fmt.Sprintf("Copied job %s URL", job.Name)
}

// copyMRURL copies the selected merge request's web URL to the clipboard.
func (m *Model) copyMRURL() {
	if len(m.mrView.mrs) == 0 || m.mrView.selected >= len(m.mrView.mrs) {
		m.status = "No merge request selected"
		return
	}
	mr := m.mrView.mrs[m.mrView.selected]
	if mr.WebURL == "" {
		m.status = "Merge request has no URL"
		return
	}
	if err := clipboard.WriteAll(mr.WebURL); err != nil {
		m.status = "Failed to copy MR URL"
		if m.opts.Logger != nil {
			m.opts.Logger.Error("copy clipboard", "err", err)
		}
		return
	}
	m.status = fmt.Sprintf("Copied !%d URL", mr.IID)
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
	visible := m.visibleProjects()
	if len(visible) == 0 {
		return nil
	}

	// Filter to projects that need fetching (not already cached or stale)
	var toFetch []gitlab.ProjectNode
	for _, project := range visible {
		state := m.pipelineStatus[project.ID]
		// Fetch if: not loading, not recently fetched (or never fetched), and not cached
		if !state.loading && (state.lastFetched.IsZero() || time.Since(state.lastFetched) > pipelineRefreshInterval) {
			toFetch = append(toFetch, project)
		}
	}

	if len(toFetch) == 0 {
		return nil
	}

	// Mark as loading to prevent duplicate fetches
	for _, project := range toFetch {
		state := m.pipelineStatus[project.ID]
		state.loading = true
		m.pipelineStatus[project.ID] = state
	}

	return batchFetchPipelineStatusCmd(m.ctx, m.client, m.opts.PipelineTimeout, toFetch)
}

func (m *Model) queuePipelineFetchForSelection(force bool) tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		return nil
	}
	return m.queuePipelineFetch(project, force)
}

func (m *Model) queuePipelineFetch(project gitlab.ProjectNode, force bool) tea.Cmd {
	if m.pipelineStatus == nil {
		m.pipelineStatus = make(map[int]pipelineState)
	}
	state := m.pipelineStatus[project.ID]
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
	m.pipelineStatus[project.ID] = state
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
	if cmd := m.queuePipelineStagesRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineJobsRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queueBridgesRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queueExpandedChildJobsRefresh(); cmd != nil {
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

func (m *Model) selectedPipeline() *gitlab.PipelineSummary {
	if len(m.pipelineView.pipelines) == 0 {
		return nil
	}
	if m.pipelineView.selected < 0 || m.pipelineView.selected >= len(m.pipelineView.pipelines) {
		return nil
	}
	return &m.pipelineView.pipelines[m.pipelineView.selected]
}

// shouldFetchPipelineData checks common guards for pipeline data fetching:
// project must be set, pipeline must be selected, and the given loading map
// must not already indicate a fetch in progress. Returns the pipeline ID and
// whether fetching should proceed.
func (m *Model) shouldFetchPipelineData(loading map[int]bool) (int, bool) {
	if m.pipelineView.project.ID == 0 {
		return 0, false
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return 0, false
	}
	if loading != nil && loading[pipeline.ID] {
		return pipeline.ID, false
	}
	return pipeline.ID, true
}

// queuePipelineStagesForSelection fetches stages for the selected pipeline,
// skipping if already cached. Used on initial selection — contrast with
// queuePipelineStagesRefresh which always re-fetches.
func (m *Model) queuePipelineStagesForSelection() tea.Cmd {
	pipelineID, ok := m.shouldFetchPipelineData(m.pipelineView.stages.LoadingMap())
	if !ok {
		return nil
	}
	if _, cached := m.pipelineView.stages.Get(pipelineID); cached {
		return nil
	}
	m.pipelineView.stages.SetLoading(pipelineID)
	return fetchPipelineStagesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID)
}

// queuePipelineJobsForSelection fetches jobs for the selected pipeline,
// skipping if already cached. See queuePipelineJobsRefresh for forced re-fetch.
func (m *Model) queuePipelineJobsForSelection() tea.Cmd {
	pipelineID, ok := m.shouldFetchPipelineData(m.pipelineView.jobs.LoadingMap())
	if !ok {
		return nil
	}
	if _, cached := m.pipelineView.jobs.Get(pipelineID); cached {
		return nil
	}
	m.pipelineView.jobs.SetLoading(pipelineID)
	return fetchPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID)
}

// queuePipelineStagesRefresh re-fetches stages unconditionally (ignores cache).
// Called during auto-refresh ticks to pick up status changes.
func (m *Model) queuePipelineStagesRefresh() tea.Cmd {
	pipelineID, ok := m.shouldFetchPipelineData(m.pipelineView.stages.LoadingMap())
	if !ok {
		return nil
	}
	m.pipelineView.stages.SetLoading(pipelineID)
	return fetchPipelineStagesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID)
}

// queuePipelineJobsRefresh re-fetches jobs unconditionally (ignores cache).
// Called during auto-refresh ticks to pick up status changes.
func (m *Model) queuePipelineJobsRefresh() tea.Cmd {
	pipelineID, ok := m.shouldFetchPipelineData(m.pipelineView.jobs.LoadingMap())
	if !ok {
		return nil
	}
	m.pipelineView.jobs.SetLoading(pipelineID)
	return fetchPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID)
}

// queueBridgesRefresh re-fetches bridges unconditionally (ignores cache).
// Called during auto-refresh ticks so bridge/downstream status updates appear.
func (m *Model) queueBridgesRefresh() tea.Cmd {
	pipelineID, ok := m.shouldFetchPipelineData(m.pipelineView.bridges.LoadingMap())
	if !ok {
		return nil
	}
	m.pipelineView.bridges.SetLoading(pipelineID)
	return fetchBridgesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID)
}

// queueExpandedChildJobsRefresh re-fetches child jobs for all expanded bridges
// of the selected pipeline. Only fetches for bridges whose group key is in
// matrixExpanded, and guards against duplicate requests via IsLoading.
func (m *Model) queueExpandedChildJobsRefresh() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	bridges, ok := m.pipelineView.bridges.Get(pipeline.ID)
	if !ok || len(bridges) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, b := range bridges {
		if b.DownstreamPipeline == nil {
			continue
		}
		groupKey := fmt.Sprintf("bridge:%d", b.ID)
		if !m.pipelineView.matrixExpanded[groupKey] {
			continue
		}
		dsID := b.DownstreamPipeline.ID
		if m.pipelineView.childJobs.IsLoading(dsID) {
			continue
		}
		projectID := b.DownstreamPipeline.ProjectID
		if projectID == 0 {
			projectID = m.pipelineView.project.ID
		}
		m.pipelineView.childJobs.SetLoading(dsID)
		cmds = append(cmds, fetchChildPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, projectID, dsID))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) selectedPipelineJob() *gitlab.PipelineJob {
	idx := m.pipelineView.stageSelected
	if idx < 0 || idx >= len(m.pipelineView.jobRows) {
		return nil
	}
	return &m.pipelineView.jobRows[idx]
}

func (m *Model) queuePipelineLogPreview() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if _, ok := m.pipelineView.jobs.Get(pipeline.ID); !ok {
		return m.queuePipelineJobsForSelection()
	}
	// Bridge rows don't have job traces — show bridge info instead
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridge {
		content := bridgePreviewContent(row.Bridge, row.IsLast)
		m.pipelineView.logPreview = previewState{
			path:    row.Bridge.Name,
			content: content,
			raw:     content,
			loading: false,
		}
		m.setLogViewportContent(content)
		m.pipelineView.logJobID = 0
		m.pipelineView.logViewport.GotoTop()
		return nil
	}
	// Bridge child rows have real jobs but may belong to a different project
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridgeChild && row.Job != nil {
		projectID := row.ChildProjectID
		if projectID == 0 {
			projectID = m.pipelineView.project.ID
		}
		job := row.Job
		if content, ok := m.pipelineView.logs.Get(job.ID); ok {
			prevJobID := m.pipelineView.logJobID
			m.pipelineView.logPreview = previewState{
				path: job.Name, content: content, raw: content, loading: false,
			}
			m.setLogViewportContent(content)
			m.pipelineView.logJobID = job.ID
			if m.pipelineView.logAutoFollow {
				m.pipelineView.logViewport.GotoBottom()
			} else if prevJobID != job.ID {
				m.pipelineView.logViewport.GotoTop()
			}
			return nil
		}
		if m.pipelineView.logs.IsLoading(job.ID) {
			return nil
		}
		m.pipelineView.logs.SetLoading(job.ID)
		m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
		m.pipelineView.logJobID = job.ID
		return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, projectID, job.ID)
	}
	job := m.selectedPipelineJob()
	if job == nil {
		m.pipelineView.logPreview = previewState{content: "No jobs available.", loading: false}
		return nil
	}
	if content, ok := m.pipelineView.logs.Get(job.ID); ok {
		prevJobID := m.pipelineView.logJobID
		m.pipelineView.logPreview = previewState{
			path:    job.Name,
			content: content,
			raw:     content,
			loading: false,
		}
		m.setLogViewportContent(content)
		m.pipelineView.logJobID = job.ID
		if m.pipelineView.logAutoFollow {
			m.pipelineView.logViewport.GotoBottom()
		} else {
			if prevJobID != job.ID {
				m.pipelineView.logViewport.GotoTop()
			}
		}
		return nil
	}
	if m.pipelineView.logs.IsLoading(job.ID) {
		return nil
	}
	m.pipelineView.logs.SetLoading(job.ID)
	m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
	m.pipelineView.logJobID = job.ID
	return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}

func (m *Model) queuePipelineLogRefresh() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if _, ok := m.pipelineView.jobs.Get(pipeline.ID); !ok {
		return m.queuePipelineJobsForSelection()
	}
	// Bridge rows don't have job traces — nothing to refresh
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridge {
		return nil
	}
	// Bridge child rows use the child project ID for log fetch
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridgeChild && row.Job != nil {
		projectID := row.ChildProjectID
		if projectID == 0 {
			projectID = m.pipelineView.project.ID
		}
		job := row.Job
		if m.pipelineView.logs.IsLoading(job.ID) {
			return nil
		}
		m.pipelineView.logs.SetLoading(job.ID)
		if job.ID != m.pipelineView.logJobID {
			m.pipelineView.pendingLogJobID = job.ID
		}
		return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, projectID, job.ID)
	}
	job := m.selectedPipelineJob()
	if job == nil {
		if m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{content: "No jobs available.", loading: false}
		}
		return nil
	}
	if m.pipelineView.logs.IsLoading(job.ID) {
		return nil
	}
	m.pipelineView.logs.SetLoading(job.ID)
	if job.ID != m.pipelineView.logJobID {
		m.pipelineView.pendingLogJobID = job.ID
		if m.pipelineView.logPreview.err != nil && m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
		}
	} else if m.pipelineView.logPreview.content == "" || m.pipelineView.logPreview.err != nil {
		m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
	}
	return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}

func (m *Model) resetPipelineLogPreview() {
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
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

// invalidateVisibleCache clears the visibleProjects cache
func (m *Model) invalidateVisibleCache() {
	m.visibleCache = nil
	m.visibleCacheQuery = ""
	m.visibleCachePage = -1
	m.visibleCacheTab = projectTabAll + 1 // Force mismatch
}

// evictOldPipelineStatusCache removes the least recently accessed pipeline status
// when the cache exceeds maxPipelineStatusCacheSize
func (m *Model) evictOldPipelineStatusCache() {
	if len(m.pipelineStatus) <= maxPipelineStatusCacheSize {
		return
	}

	// Find oldest entry by last accessed time
	var oldestID int
	var oldestTime time.Time
	first := true
	for id, state := range m.pipelineStatus {
		if first || state.lastAccessed.Before(oldestTime) {
			oldestID = id
			oldestTime = state.lastAccessed
			first = false
		}
	}

	delete(m.pipelineStatus, oldestID)
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
		return renderDetailPane(m, width, height)
	}

	project := visible[m.selected]
	pipelineState := m.pipelineStatus[project.ID]

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
	output := renderDetailPane(m, width, height)

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
	if end > len(m.allProjects) {
		end = len(m.allProjects)
	}
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

	// Note: No need to call SetDelegate() - delegate holds reference to shared pipelineStatus map

	// Sync selection with list cursor
	if m.selected >= 0 && m.selected < len(items) {
		m.projectList.Select(m.selected)
	}
}

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

// evictOldLogs removes oldest entries from logCache if it exceeds maxLogCacheEntries.
// This prevents unbounded memory growth when auto-refreshing pipeline logs.
func (m *Model) evictOldLogs() {
	if m.pipelineView.logs.Len() <= maxLogCacheEntries {
		return
	}

	// Find jobs to evict (oldest by job ID - assumes increasing IDs over time)
	jobIDs := m.pipelineView.logs.Keys()
	sort.Ints(jobIDs)

	// Keep the current job and the most recent ones, remove the oldest
	toRemove := len(jobIDs) - maxLogCacheEntries
	for i := 0; i < toRemove; i++ {
		jobID := jobIDs[i]
		// Don't evict currently displayed log
		if jobID == m.pipelineView.logJobID {
			continue
		}
		m.pipelineView.logs.Delete(jobID)
	}
}

// truncateLogContent truncates log content to maxLogSizeBytes if it exceeds the limit.
func truncateLogContent(content string) string {
	if len(content) <= maxLogSizeBytes {
		return content
	}
	truncated := content[:maxLogSizeBytes]
	return truncated + "\n\n... (log truncated at 1MB, full log available in GitLab web UI)"
}

// queueExplorerPreview starts an async fetch for the currently selected entry's
// preview (directory listing or file content). It skips redundant fetches if
// the preview is already loading or cached for the same path.
//
// IMPORTANT: Every previewState reset must preserve the viewport field.
// The viewport is initialized once in openExplorer with proper dimensions;
// replacing it with a zero-valued viewport causes View() to return empty
// content since Width/Height would be 0.
func (m *Model) queueExplorerPreview() tea.Cmd {
	entry := m.selectedEntry()
	if entry == nil {
		m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
		return nil
	}
	if entry.IsDir() {
		m.explorer.preview = previewState{
			path:     entry.Path,
			loading:  true,
			viewport: m.explorer.preview.viewport,
		}
		return fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
	}
	if m.explorer.preview.loading && m.explorer.preview.path == entry.Path {
		return nil
	}
	if !m.explorer.preview.loading && m.explorer.preview.path == entry.Path && m.explorer.preview.content != "" && m.explorer.preview.err == nil {
		return nil
	}
	m.explorer.preview = previewState{path: entry.Path, loading: true, viewport: m.explorer.preview.viewport}
	return fetchFileCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m *Model) currentDirState() *dirState {
	if len(m.explorer.stack) == 0 {
		return nil
	}
	return &m.explorer.stack[len(m.explorer.stack)-1]
}

func (m *Model) parentDirState() *dirState {
	if len(m.explorer.stack) < 2 {
		return nil
	}
	return &m.explorer.stack[len(m.explorer.stack)-2]
}

func (m *Model) selectedEntry() *gitlab.TreeNode {
	dir := m.currentDirState()
	if dir == nil || len(dir.entries) == 0 {
		return nil
	}
	if dir.selected < 0 || dir.selected >= len(dir.entries) {
		return nil
	}
	return &dir.entries[dir.selected]
}

// updateStageTable rebuilds the job/stage table from cached stages, jobs, and
// bridges for the selected pipeline. The table uses a one-job-per-row layout
// with matrix jobs collapsed into expandable group headers.
//
// Bridge-only stages (stages with no regular jobs, only bridge/trigger jobs)
// are injected synthetically because PipelineStages is built from
// ListPipelineJobs which excludes bridges.
//
// The stageJobRows slice and the parallel jobRows slice are kept in sync so
// that the table cursor index maps directly to a PipelineJob for log preview
// and retry operations. Bridge rows synthesize a PipelineJob from bridge
// fields as a fallback.
func (m *Model) updateStageTable() {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.pipelineView.stageTable.SetRows([]table.Row{})
		m.pipelineView.jobRows = nil
		m.pipelineView.stageJobRows = nil
		return
	}

	stages, _ := m.pipelineView.stages.Get(pipeline.ID)
	jobs, _ := m.pipelineView.jobs.Get(pipeline.ID)
	bridges, _ := m.pipelineView.bridges.Get(pipeline.ID)

	// Inject stages that only have bridge jobs (no regular jobs).
	// PipelineStages is built from ListPipelineJobs which excludes bridges,
	// so bridge-only stages would otherwise be invisible.
	if len(bridges) > 0 {
		stageSet := make(map[string]bool, len(stages))
		for _, s := range stages {
			stageSet[s.Name] = true
		}
		for _, b := range bridges {
			if !stageSet[b.Stage] {
				stageSet[b.Stage] = true
				stages = append(stages, gitlab.PipelineStage{
					Name:   b.Stage,
					Status: b.Status,
				})
			}
		}
	}

	if len(stages) == 0 || (len(jobs) == 0 && len(bridges) == 0) {
		m.pipelineView.stageTable.SetRows([]table.Row{})
		m.pipelineView.jobRows = nil
		m.pipelineView.stageJobRows = nil
		return
	}

	if m.pipelineView.matrixExpanded == nil {
		m.pipelineView.matrixExpanded = make(map[string]bool)
	}

	// Build child jobs map for expanded bridges
	var childJobsMap map[int][]gitlab.PipelineJob
	for _, b := range bridges {
		if b.DownstreamPipeline == nil {
			continue
		}
		groupKey := fmt.Sprintf("bridge:%d", b.ID)
		if !m.pipelineView.matrixExpanded[groupKey] {
			continue
		}
		dsID := b.DownstreamPipeline.ID
		if cJobs, ok := m.pipelineView.childJobs.Get(dsID); ok {
			if childJobsMap == nil {
				childJobsMap = make(map[int][]gitlab.PipelineJob)
			}
			childJobsMap[dsID] = cJobs
		}
	}

	richRows := buildStageJobRows(stages, jobs, bridges, m.pipelineView.matrixExpanded, childJobsMap)
	m.pipelineView.stageJobRows = richRows

	var tableRows []table.Row
	var jobRows []gitlab.PipelineJob
	lastStage := ""

	for _, row := range richRows {
		stageCol := ""
		if row.Stage != lastStage {
			stageCol = row.Stage
			lastStage = row.Stage
		}

		status := row.Status
		if status == "" {
			status = unknownStatus
		}
		statusLabel := pipelineStatusIcon(status) + " " + strings.ToUpper(status)

		switch row.Kind {
		case rowKindJob:
			tableRows = append(tableRows, table.Row{row.Job.Name, stageCol, statusLabel})
			jobRows = append(jobRows, *row.Job)
		case rowKindMatrixGroup:
			name := fmt.Sprintf("%s %s [%d]", iconTreeExpanded, row.BaseName, len(row.Jobs))
			tableRows = append(tableRows, table.Row{name, stageCol, statusLabel})
			// Map group header to first sub-job for log preview fallback
			jobRows = append(jobRows, row.Jobs[0])
		case rowKindMatrixChild:
			prefix := "├─"
			if row.IsLast {
				prefix = "└─"
			}
			name := fmt.Sprintf("  %s %s", prefix, row.Vars)
			// Children never show a stage column
			tableRows = append(tableRows, table.Row{name, "", statusLabel})
			jobRows = append(jobRows, *row.Job)
		case rowKindBridgeChild:
			prefix := "├─"
			if row.IsLast {
				prefix = "└─"
			}
			name := fmt.Sprintf("  %s %s", prefix, row.Job.Name)
			tableRows = append(tableRows, table.Row{name, "", statusLabel})
			jobRows = append(jobRows, *row.Job)
		case rowKindBridge:
			b := row.Bridge
			if row.IsLast && b.DownstreamPipeline != nil {
				// Expanded placeholder row (child jobs not yet loaded)
				name := fmt.Sprintf("  └─ child #%d", b.DownstreamPipeline.ID)
				tableRows = append(tableRows, table.Row{name, "", statusLabel})
			} else {
				// Bridge header row
				icon := iconTreeCollapsed
				if m.pipelineView.matrixExpanded[row.GroupKey] {
					icon = iconTreeExpanded
				}
				suffix := ""
				if b.DownstreamPipeline != nil {
					suffix = fmt.Sprintf(" → #%d", b.DownstreamPipeline.ID)
				}
				name := fmt.Sprintf("%s %s%s", icon, b.Name, suffix)
				tableRows = append(tableRows, table.Row{name, stageCol, statusLabel})
			}
			// Synthesize a PipelineJob from the bridge so log/retry still works
			jobRows = append(jobRows, gitlab.PipelineJob{
				ID:     b.ID,
				Name:   b.Name,
				Stage:  b.Stage,
				Status: b.Status,
			})
		}
	}

	m.pipelineView.jobRows = jobRows
	m.pipelineView.stageTable.SetRows(tableRows)

	if m.pipelineView.stageSelected >= len(tableRows) {
		m.pipelineView.stageSelected = max(0, len(tableRows)-1)
	}
	if m.pipelineView.stageSelected >= 0 && m.pipelineView.stageSelected < len(tableRows) {
		m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
	}
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

func (m *Model) updateProjectListSize() {
	if m.mode != modeProjects && m.mode != modeMultiPanel {
		return
	}
	if m.mode == modeMultiPanel {
		// In multi-panel mode, project list size is set during render
		return
	}
	listWidth, _, contentHeight, ok := projectPaneLayout(m.width, m.height)
	if !ok {
		return
	}
	listHeight := max(1, contentHeight-2)
	m.projectList.SetSize(listWidth, listHeight)
	m.projectList.SetWidth(listWidth)
}
