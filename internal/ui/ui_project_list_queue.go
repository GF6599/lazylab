// Package ui contains the Bubble Tea models, views, and styles for the TUI.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
)

func (m Model) openProjectActions(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modeProjectActions

	// Initialize action menu list
	items := make([]list.Item, len(projectActionOptions))
	for i, option := range projectActionOptions {
		items[i] = actionMenuItem{label: option, index: i}
	}

	delegate := actionMenuDelegate{}
	menuList := list.New(items, delegate, 0, 0)
	menuList.Title = ""
	menuList.SetShowStatusBar(false)
	menuList.SetShowPagination(false)
	menuList.SetShowHelp(false)
	menuList.SetFilteringEnabled(false)
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
}

func (m Model) openExplorer(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	ref := project.DefaultBranch
	if ref == "" {
		ref = "main"
	}
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: project,
		ref:     ref,
		stack: []dirState{
			{path: "", loading: true},
		},
	}
	m.status = fmt.Sprintf("Browsing %s", project.PathWithNamespace)
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, ref, "")
}

func (m Model) openPipelineView(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modePipelines

	// Initialize stage table
	columns := []table.Column{
		{Title: "Stage", Width: 20},
		{Title: "Status", Width: 12},
		{Title: "Jobs", Width: 30},
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(false),
		table.WithHeight(10),
	)

	// Style the table with Rose Pine colors
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(rosePineMuted).
		BorderBottom(true).
		Bold(false).
		Foreground(rosePineSubtle)
	s.Selected = s.Selected.
		Foreground(rosePineBase).
		Background(rosePineRose).
		Bold(false)
	t.SetStyles(s)

	// Initialize pipeline list
	delegate := pipelineDelegate{}
	pipelineList := list.New([]list.Item{}, delegate, 0, 0)
	pipelineList.Title = ""
	pipelineList.SetShowStatusBar(false)
	pipelineList.SetShowPagination(false)
	pipelineList.SetShowHelp(false)
	pipelineList.SetFilteringEnabled(false)
	pipelineList.Styles.Title = titleStyle

	m.pipelineView = pipelineViewState{
		project:       project,
		pipelineList:  pipelineList,
		loading:       true,
		page:          1,
		totalPages:    1,
		perPage:       pipelinePerPage,
		stageCache:    make(map[int][]gitlab.PipelineStage),
		stageLoading:  make(map[int]bool),
		stageErr:      make(map[int]error),
		stageTable:    t,
		jobsCache:     make(map[int][]gitlab.PipelineJob),
		jobsLoading:   make(map[int]bool),
		jobsErr:       make(map[int]error),
		logCache:      make(map[int]string),
		logLoading:    make(map[int]bool),
		logErr:        make(map[int]error),
		logAutoFollow: true,
		focus:         pipelineFocusPipelines,
	}
	m.status = fmt.Sprintf("Pipelines for %s", project.PathWithNamespace)
	return m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, m.pipelineView.page, m.pipelineView.perPage)
}

func (m *Model) closePipelineView() {
	m.mode = modeProjects
	m.pipelineView = pipelineViewState{}
	m.actionMenu = actionMenuState{}
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
	m.pipelineView.stageCache = make(map[int][]gitlab.PipelineStage)
	m.pipelineView.stageLoading = make(map[int]bool)
	m.pipelineView.stageErr = make(map[int]error)
	m.pipelineView.stageSelected = 0
	m.pipelineView.jobsCache = make(map[int][]gitlab.PipelineJob)
	m.pipelineView.jobsLoading = make(map[int]bool)
	m.pipelineView.jobsErr = make(map[int]error)
	m.pipelineView.logCache = make(map[int]string)
	m.pipelineView.logLoading = make(map[int]bool)
	m.pipelineView.logErr = make(map[int]error)
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
	m.pipelineView.stageCache = make(map[int][]gitlab.PipelineStage)
	m.pipelineView.stageLoading = make(map[int]bool)
	m.pipelineView.stageErr = make(map[int]error)
	m.pipelineView.stageSelected = 0
	m.pipelineView.jobsCache = make(map[int][]gitlab.PipelineJob)
	m.pipelineView.jobsLoading = make(map[int]bool)
	m.pipelineView.jobsErr = make(map[int]error)
	m.pipelineView.logCache = make(map[int]string)
	m.pipelineView.logLoading = make(map[int]bool)
	m.pipelineView.logErr = make(map[int]error)
	m.resetPipelineLogPreview()
	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, target, perPage)
}

func (m Model) descendDirectory(entry gitlab.TreeNode) (tea.Model, tea.Cmd) {
	newState := dirState{
		path:    entry.Path,
		loading: true,
	}
	m.explorer.stack = append(m.explorer.stack, newState)
	m.explorer.preview = previewState{}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m Model) navigateExplorerUp() (tea.Model, tea.Cmd) {
	if len(m.explorer.stack) <= 1 {
		m.closeExplorer("Back to projects")
		return m, nil
	}
	m.explorer.stack = m.explorer.stack[:len(m.explorer.stack)-1]
	m.explorer.preview = previewState{}
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
	m.explorer.preview = previewState{}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), cur.path)
}

func (m *Model) closeExplorer(status string) {
	m.mode = modeProjects
	m.explorer = explorerState{}
	if status != "" {
		m.status = status
	}
}

func (m *Model) movePage(delta int) {
	target := m.page + delta
	if target < 1 {
		target = 1
	}
	if m.totalPages > 0 && target > m.totalPages {
		target = m.totalPages
	}
	if target == m.page {
		return
	}
	m.page = target
	m.paginator.Page = m.page - 1 // Paginator is 0-indexed
	if !m.pagesReady[m.page] {
		m.status = fmt.Sprintf("Page %d is still caching (%d/%d)", m.page, m.pagesLoaded, m.totalPages)
	} else {
		m.status = fmt.Sprintf("Viewing page %d", m.page)
	}
	m.ensureSelectionBounds()
	m.updateProjectList()
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

func (m *Model) queuePipelineStagesForSelection() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.stageCache != nil {
		if _, ok := m.pipelineView.stageCache[pipeline.ID]; ok {
			return nil
		}
	}
	if m.pipelineView.stageLoading == nil {
		m.pipelineView.stageLoading = make(map[int]bool)
	}
	if m.pipelineView.stageLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.stageLoading[pipeline.ID] = true
	if m.pipelineView.stageErr != nil {
		delete(m.pipelineView.stageErr, pipeline.ID)
	}
	return fetchPipelineStagesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) queuePipelineJobsForSelection() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache != nil {
		if _, ok := m.pipelineView.jobsCache[pipeline.ID]; ok {
			return nil
		}
	}
	if m.pipelineView.jobsLoading == nil {
		m.pipelineView.jobsLoading = make(map[int]bool)
	}
	if m.pipelineView.jobsLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.jobsLoading[pipeline.ID] = true
	if m.pipelineView.jobsErr != nil {
		delete(m.pipelineView.jobsErr, pipeline.ID)
	}
	return fetchPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) queuePipelineStagesRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.stageLoading == nil {
		m.pipelineView.stageLoading = make(map[int]bool)
	}
	if m.pipelineView.stageLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.stageLoading[pipeline.ID] = true
	if m.pipelineView.stageErr != nil {
		delete(m.pipelineView.stageErr, pipeline.ID)
	}
	return fetchPipelineStagesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) queuePipelineJobsRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsLoading == nil {
		m.pipelineView.jobsLoading = make(map[int]bool)
	}
	if m.pipelineView.jobsLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.jobsLoading[pipeline.ID] = true
	if m.pipelineView.jobsErr != nil {
		delete(m.pipelineView.jobsErr, pipeline.ID)
	}
	return fetchPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) selectedPipelineStages() []gitlab.PipelineStage {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.stageCache == nil {
		return nil
	}
	return m.pipelineView.stageCache[pipeline.ID]
}

func (m *Model) selectedPipelineJob() *gitlab.PipelineJob {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache == nil {
		return nil
	}
	jobs, ok := m.pipelineView.jobsCache[pipeline.ID]
	if !ok {
		return nil
	}
	stages := m.selectedPipelineStages()
	if len(stages) == 0 {
		return nil
	}
	stageIndex := m.pipelineView.stageSelected
	if stageIndex < 0 || stageIndex >= len(stages) {
		stageIndex = max(0, len(stages)-1)
	}
	stageName := stages[stageIndex].Name
	return latestJobForStage(jobs, stageName)
}

func (m *Model) queuePipelineLogPreview() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache == nil {
		return m.queuePipelineJobsForSelection()
	}
	jobs, ok := m.pipelineView.jobsCache[pipeline.ID]
	if !ok {
		return m.queuePipelineJobsForSelection()
	}
	stages := m.selectedPipelineStages()
	if len(stages) == 0 {
		m.pipelineView.logPreview = previewState{content: "No stages available.", loading: false}
		return nil
	}
	if m.pipelineView.stageSelected >= len(stages) {
		m.pipelineView.stageSelected = max(0, len(stages)-1)
	}
	stageName := stages[m.pipelineView.stageSelected].Name
	job := latestJobForStage(jobs, stageName)
	if job == nil {
		m.pipelineView.logPreview = previewState{content: "No jobs available for stage.", loading: false}
		return nil
	}
	if m.pipelineView.logCache != nil {
		if content, ok := m.pipelineView.logCache[job.ID]; ok {
			prevJobID := m.pipelineView.logJobID
			m.pipelineView.logPreview = previewState{
				path:    job.Name,
				content: content,
				raw:     content,
				loading: false,
			}
			m.pipelineView.logViewport.SetContent(content)
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
	}
	if m.pipelineView.logLoading == nil {
		m.pipelineView.logLoading = make(map[int]bool)
	}
	if m.pipelineView.logLoading[job.ID] {
		return nil
	}
	m.pipelineView.logLoading[job.ID] = true
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, job.ID)
	}
	m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
	m.pipelineView.logJobID = job.ID
	return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}

func (m *Model) queuePipelineLogRefresh() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache == nil {
		return m.queuePipelineJobsForSelection()
	}
	jobs, ok := m.pipelineView.jobsCache[pipeline.ID]
	if !ok {
		return m.queuePipelineJobsForSelection()
	}
	stages := m.selectedPipelineStages()
	if len(stages) == 0 {
		if m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{content: "No stages available.", loading: false}
		}
		return nil
	}
	if m.pipelineView.stageSelected >= len(stages) {
		m.pipelineView.stageSelected = max(0, len(stages)-1)
	}
	stageName := stages[m.pipelineView.stageSelected].Name
	job := latestJobForStage(jobs, stageName)
	if job == nil {
		if m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{content: "No jobs available for stage.", loading: false}
		}
		return nil
	}
	if m.pipelineView.logLoading == nil {
		m.pipelineView.logLoading = make(map[int]bool)
	}
	if m.pipelineView.logLoading[job.ID] {
		return nil
	}
	m.pipelineView.logLoading[job.ID] = true
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, job.ID)
	}
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

func (m *Model) refreshPipelineLogPreviewFromCache() bool {
	if m.pipelineView.logCache == nil {
		return false
	}
	if m.pipelineView.logAutoFollow && m.pipelineView.pendingLogJobID != 0 {
		m.pipelineView.logJobID = m.pipelineView.pendingLogJobID
		m.pipelineView.pendingLogJobID = 0
	}
	if m.pipelineView.logJobID == 0 {
		return false
	}
	content, ok := m.pipelineView.logCache[m.pipelineView.logJobID]
	if !ok || content == "" {
		return false
	}
	if content == m.pipelineView.logPreview.raw && m.pipelineView.logPreview.content != "" {
		return false
	}
	m.pipelineView.logPreview = previewState{
		path:    fmt.Sprintf("job-%d", m.pipelineView.logJobID),
		content: content,
		raw:     content,
		loading: false,
	}
	return true
}

func (m *Model) resetPipelineLogPreview() {
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
}

func (m *Model) visibleProjects() []gitlab.ProjectNode {
	// Check if cache is valid
	if m.search.query != "" {
		// Search mode: cache valid if query matches
		if m.search.query == m.visibleCacheQuery && m.visibleCache != nil {
			return m.visibleCache
		}

		// Recompute and cache
		filtered := make([]gitlab.ProjectNode, 0, len(m.allProjects))
		for _, p := range m.allProjects {
			if fuzzyMatch(p.PathWithNamespace, m.search.query) || fuzzyMatch(p.Name, m.search.query) {
				filtered = append(filtered, p)
			}
		}
		m.visibleCache = filtered
		m.visibleCacheQuery = m.search.query
		m.visibleCachePage = -1 // Invalid in search mode
		return filtered
	}

	// Pagination mode: cache valid if page matches
	if m.page == m.visibleCachePage && m.visibleCache != nil && m.visibleCacheQuery == "" {
		return m.visibleCache
	}

	// Recompute and cache
	pageData := m.pageSlice(m.page)
	m.visibleCache = pageData
	m.visibleCachePage = m.page
	m.visibleCacheQuery = ""
	return pageData
}

// invalidateVisibleCache clears the visibleProjects cache
func (m *Model) invalidateVisibleCache() {
	m.visibleCache = nil
	m.visibleCacheQuery = ""
	m.visibleCachePage = -1
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
	m.allProjects = append(m.allProjects, page.Projects...)
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
	if m.pipelineView.logCache == nil || len(m.pipelineView.logCache) <= maxLogCacheEntries {
		return
	}

	// Find jobs to evict (oldest by job ID - assumes increasing IDs over time)
	jobIDs := make([]int, 0, len(m.pipelineView.logCache))
	for jobID := range m.pipelineView.logCache {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Ints(jobIDs)

	// Keep the current job and the most recent ones, remove the oldest
	toRemove := len(jobIDs) - maxLogCacheEntries
	for i := 0; i < toRemove; i++ {
		jobID := jobIDs[i]
		// Don't evict currently displayed log
		if jobID == m.pipelineView.logJobID {
			continue
		}
		delete(m.pipelineView.logCache, jobID)
		if m.pipelineView.logLoading != nil {
			delete(m.pipelineView.logLoading, jobID)
		}
		if m.pipelineView.logErr != nil {
			delete(m.pipelineView.logErr, jobID)
		}
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

func (m *Model) queueExplorerPreview() tea.Cmd {
	entry := m.selectedEntry()
	if entry == nil {
		m.explorer.preview = previewState{}
		return nil
	}
	if entry.IsDir() {
		m.explorer.preview = previewState{
			path:    entry.Path,
			loading: true,
		}
		return fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
	}
	if m.explorer.preview.loading && m.explorer.preview.path == entry.Path {
		return nil
	}
	if !m.explorer.preview.loading && m.explorer.preview.path == entry.Path && m.explorer.preview.content != "" && m.explorer.preview.err == nil {
		return nil
	}
	m.explorer.preview = previewState{path: entry.Path, loading: true}
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

// updateStageTable updates the stage table with current pipeline stages and jobs
func (m *Model) updateStageTable() {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.pipelineView.stageTable.SetRows([]table.Row{})
		return
	}

	stages := m.pipelineView.stageCache[pipeline.ID]
	jobs := m.pipelineView.jobsCache[pipeline.ID]

	if len(stages) == 0 {
		m.pipelineView.stageTable.SetRows([]table.Row{})
		return
	}

	// Build rows for the table
	rows := make([]table.Row, len(stages))
	for i, stage := range stages {
		status := stage.Status
		if status == "" {
			status = "unknown"
		}
		summary := stageJobSummary(jobs, stage.Name)
		if summary != "" {
			summary = strings.TrimPrefix(summary, " ")
		}
		rows[i] = table.Row{
			stage.Name,
			pipelineStatusLabel(status),
			summary,
		}
	}

	m.pipelineView.stageTable.SetRows(rows)

	// Update cursor position to match stageSelected
	if m.pipelineView.stageSelected >= 0 && m.pipelineView.stageSelected < len(stages) {
		m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
	}
}

// updateViewportSizes updates viewport dimensions when terminal resizes
func (m *Model) updateViewportSizes() {
	if m.mode == modeExplorer {
		width := previewContentWidth(m.width)
		height := previewContentHeight(m.height)
		if m.explorer.preview.viewport.Width != width || m.explorer.preview.viewport.Height != height {
			m.explorer.preview.viewport.Width = width
			m.explorer.preview.viewport.Height = height
		}
	}
	if m.mode == modePipelines {
		width := pipelineLogContentWidth(m.width)
		height := pipelineLogContentHeight(m.height)
		if m.pipelineView.logViewport.Width != width || m.pipelineView.logViewport.Height != height {
			m.pipelineView.logViewport.Width = width
			m.pipelineView.logViewport.Height = height
		}
	}
}
