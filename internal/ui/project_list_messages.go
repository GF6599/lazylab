// Package ui contains the Bubble Tea models, views, and styles for the TUI.
package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"lazylab/internal/gitlab"
)

func (m Model) handleCacheLoaded(msg cacheLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if m.opts.Logger != nil {
			m.opts.Logger.Error("load cache", "err", msg.err)
		}
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
	// Batch prefetch pipeline status for all visible projects
	return m, (&m).queueBatchPrefetchPipelineStatus()
}

func (m Model) handleTreeLoaded(msg treeLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeExplorer || m.explorer.project.ID != msg.projectID {
		return m, nil
	}
	// If this was triggered for directory preview (path matches preview.path), format preview.
	if m.explorer.preview.path != "" && m.explorer.preview.path == msg.path {
		if msg.err != nil {
			vp := m.explorer.preview.viewport
			m.explorer.preview = previewState{path: msg.path, err: msg.err, viewport: vp}
			m.status = "Failed to load directory preview"
			return m, nil
		}
		builder := &strings.Builder{}
		builder.WriteString(fmt.Sprintf("%s/\n", msg.path))
		for _, entry := range msg.entries {
			name := entry.Name
			if entry.IsDir() {
				name += "/"
			}
			builder.WriteString(fmt.Sprintf("%s %s", explorerEntryIcon(entry), name))
			builder.WriteString("\n")
		}
		content := builder.String()
		vp := m.explorer.preview.viewport
		m.explorer.preview = previewState{
			path:     msg.path,
			content:  content,
			loading:  false,
			viewport: vp,
		}
		m.explorer.preview.viewport.SetContent(content)
		m.explorer.preview.viewport.GotoTop()
		return m, nil
	}
	idx := m.findDirIndex(msg.path)
	if idx == -1 {
		return m, nil
	}
	dir := &m.explorer.stack[idx]
	if msg.err != nil {
		dir.loading = false
		dir.entries = nil
		dir.err = msg.err
		m.status = "Failed to load directory"
		return m, nil
	}
	dir.loading = false
	dir.err = nil
	dir.entries = msg.entries
	if dir.selected >= len(dir.entries) {
		dir.selected = max(0, len(dir.entries)-1)
	}

	// Update bubbles lists with new entries
	items := make([]list.Item, len(msg.entries))
	for i, entry := range msg.entries {
		items[i] = treeEntryItem{entry: entry}
	}

	// If this is the current directory, update currentList
	if idx == len(m.explorer.stack)-1 {
		m.explorer.currentList.SetItems(items)
		if dir.selected >= 0 && dir.selected < len(items) {
			m.explorer.currentList.Select(dir.selected)
		}
		return m, m.queueExplorerPreview()
	}

	// If this is the parent directory, update parentList
	if idx == len(m.explorer.stack)-2 {
		m.explorer.parentList.SetItems(items)
		if dir.selected >= 0 && dir.selected < len(items) {
			m.explorer.parentList.Select(dir.selected)
		}
	}

	return m, nil
}

func (m Model) handleFileLoaded(msg fileLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeExplorer || m.explorer.project.ID != msg.projectID {
		return m, nil
	}
	if msg.path != m.explorer.preview.path {
		return m, nil
	}
	m.explorer.preview.loading = false
	if msg.err != nil {
		m.explorer.preview.err = msg.err
		m.explorer.preview.content = ""
		m.explorer.preview.raw = ""
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
		m.status = "Failed to load file"
		return m, nil
	}
	width := previewContentWidth(m.width)
	highlighted, isHighlighted, err := (&m).highlightPreview(msg.path, msg.content, width)
	if err != nil {
		// Surface syntax highlighting errors to the user
		if m.opts.Logger != nil {
			m.opts.Logger.Debug("highlight preview", "err", err, "path", msg.path)
		}
		m.status = fmt.Sprintf("Syntax highlighting unavailable: %v", err)
		// Fall back to plain text
		m.explorer.preview.err = nil
		m.explorer.preview.raw = msg.content
		m.explorer.preview.content = msg.content
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
		m.explorer.preview.viewport.SetContent(msg.content)
		m.explorer.preview.viewport.GotoTop()
		return m, nil
	}
	m.explorer.preview.err = nil
	m.explorer.preview.raw = msg.content
	if isHighlighted {
		m.explorer.preview.content = highlighted
		m.explorer.preview.highlighted = true
		m.explorer.preview.highlightWidth = width
	} else {
		m.explorer.preview.content = msg.content
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
	}
	m.explorer.preview.viewport.SetContent(m.explorer.preview.content)
	m.explorer.preview.viewport.GotoTop()
	return m, nil
}

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
		if m.opts.Logger != nil {
			m.opts.Logger.Error("load projects", "err", msg.err, "background", msg.background)
		}
		return m, nil
	}

	if msg.background {
		m.appendPage(msg.page)
		m.updateProjectList()
		if m.totalPages > 0 {
			m.status = fmt.Sprintf("Caching %d/%d pages", m.pagesLoaded, m.totalPages)
		}
		if m.cache != nil && len(m.allProjects) > 0 {
			cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
		}
		if m.pagesLoaded >= m.totalPages && m.totalPages > 0 {
			m.backgroundLoading = false
			m.status = "All projects cached"
			if len(cmds) == 0 {
				return m, nil
			}
			return m, tea.Batch(cmds...)
		}
		if msg.page.NextPage > 0 {
			cmds = append(cmds, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, msg.page.NextPage, true))
		} else {
			m.backgroundLoading = false
			m.status = "All projects cached"
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
	m.allProjects = append([]gitlab.ProjectNode(nil), msg.page.Projects...)
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
	if msg.page.NextPage > 0 {
		m.backgroundLoading = true
		cmds = append(cmds, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, msg.page.NextPage, true))
	} else {
		m.backgroundLoading = false
	}
	m.ensureSelectionBounds()
	m.updateProjectList()
	// Batch prefetch pipeline status for all visible projects
	if batchCmd := (&m).queueBatchPrefetchPipelineStatus(); batchCmd != nil {
		cmds = append(cmds, batchCmd)
	}
	if m.cache != nil && len(m.allProjects) > 0 {
		cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineStatus(msg pipelineStatusMsg) (tea.Model, tea.Cmd) {
	selectedID := 0
	if m.mode == modeProjects {
		visible := m.visibleProjects()
		if m.selected >= 0 && m.selected < len(visible) {
			selectedID = visible[m.selected].ID
		}
	}

	state := m.pipelineStatus[msg.projectID]
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
	m.pipelineStatus[msg.projectID] = state
	(&m).evictOldPipelineStatusCache()
	if msg.projectID == selectedID {
		(&m).invalidateDetailCache()
	}
	return m, nil
}

func (m Model) handlePipelinesLoaded(msg pipelinesLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	prevSelectedID := 0
	if m.pipelineView.selected >= 0 && m.pipelineView.selected < len(m.pipelineView.pipelines) {
		prevSelectedID = m.pipelineView.pipelines[m.pipelineView.selected].ID
	}
	m.pipelineView.loading = false
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			m.pipelineView.err = nil
			m.pipelineView.pipelines = nil
			m.pipelineView.page = 1
			m.pipelineView.totalPages = 0
			return m, nil
		}
		m.pipelineView.err = msg.err
		return m, nil
	}
	m.pipelineView.err = nil
	if msg.page > 0 {
		m.pipelineView.page = msg.page
	}
	if msg.totalPages > 0 {
		m.pipelineView.totalPages = msg.totalPages
	}
	if m.pipelineView.totalPages > 0 && m.pipelineView.page > m.pipelineView.totalPages {
		m.pipelineView.page = m.pipelineView.totalPages
		m.pipelineView.loading = true
		perPage := m.pipelineView.perPage
		if perPage <= 0 {
			perPage = pipelinePerPage
		}
		return m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, m.pipelineView.page, perPage)
	}
	m.pipelineView.pipelines = msg.pipelines
	sort.SliceStable(m.pipelineView.pipelines, func(i, j int) bool {
		a := m.pipelineView.pipelines[i]
		b := m.pipelineView.pipelines[j]
		if !a.UpdatedAt.IsZero() && !b.UpdatedAt.IsZero() {
			if a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.ID > b.ID
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.ID != b.ID {
			return a.ID > b.ID
		}
		return a.Ref > b.Ref
	})

	// Update pipeline list with sorted pipelines
	items := make([]list.Item, len(m.pipelineView.pipelines))
	for i, p := range m.pipelineView.pipelines {
		items[i] = pipelineItem{summary: p}
	}
	m.pipelineView.pipelineList.SetItems(items)

	selectedSame := false
	if m.pipelineView.pendingSelectID != 0 {
		for i, p := range m.pipelineView.pipelines {
			if p.ID == m.pipelineView.pendingSelectID {
				m.pipelineView.selected = i
				selectedSame = true
				m.pipelineView.pendingSelectID = 0
				break
			}
		}
	}
	if !selectedSame && prevSelectedID != 0 {
		for i, p := range m.pipelineView.pipelines {
			if p.ID == prevSelectedID {
				m.pipelineView.selected = i
				selectedSame = true
				break
			}
		}
	}
	if !selectedSame {
		if len(m.pipelineView.pipelines) == 0 {
			m.pipelineView.selected = 0
		} else if m.pipelineView.selected >= len(m.pipelineView.pipelines) {
			m.pipelineView.selected = max(0, len(m.pipelineView.pipelines)-1)
		}
		m.pipelineView.stageSelected = 0
		m.resetPipelineLogPreview()
	}

	// Sync list selection with internal selected index
	if m.pipelineView.selected >= 0 && m.pipelineView.selected < len(items) {
		m.pipelineView.pipelineList.Select(m.pipelineView.selected)
	}

	cmds := []tea.Cmd{
		m.queuePipelineStagesForSelection(),
		m.queuePipelineJobsForSelection(),
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineStagesLoaded(msg pipelineStagesLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if m.pipelineView.stageLoading != nil {
		m.pipelineView.stageLoading[msg.pipelineID] = false
	}
	if msg.err != nil {
		if m.pipelineView.stageErr == nil {
			m.pipelineView.stageErr = make(map[int]error)
		}
		m.pipelineView.stageErr[msg.pipelineID] = msg.err
		return m, nil
	}
	if m.pipelineView.stageCache == nil {
		m.pipelineView.stageCache = make(map[int][]gitlab.PipelineStage)
	}
	m.pipelineView.stageCache[msg.pipelineID] = msg.stages
	if m.pipelineView.stageErr != nil {
		delete(m.pipelineView.stageErr, msg.pipelineID)
	}

	// Update stage table with new data
	m.updateStageTable()

	return m, m.queuePipelineLogPreview()
}

func (m Model) handlePipelineJobsLoaded(msg pipelineJobsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if m.pipelineView.jobsLoading != nil {
		m.pipelineView.jobsLoading[msg.pipelineID] = false
	}
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			m.pipelineView.jobsCache[msg.pipelineID] = nil
			return m, m.queuePipelineLogPreview()
		}
		if m.pipelineView.jobsErr == nil {
			m.pipelineView.jobsErr = make(map[int]error)
		}
		m.pipelineView.jobsErr[msg.pipelineID] = msg.err
		return m, nil
	}
	if m.pipelineView.jobsCache == nil {
		m.pipelineView.jobsCache = make(map[int][]gitlab.PipelineJob)
	}
	m.pipelineView.jobsCache[msg.pipelineID] = msg.jobs
	if m.pipelineView.jobsErr != nil {
		delete(m.pipelineView.jobsErr, msg.pipelineID)
	}

	// Update stage table with new job data
	m.updateStageTable()

	return m, m.queuePipelineLogPreview()
}

func (m Model) handlePipelineLogLoaded(msg pipelineLogLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if m.pipelineView.logLoading != nil {
		m.pipelineView.logLoading[msg.jobID] = false
	}
	if msg.err != nil {
		if m.pipelineView.logErr == nil {
			m.pipelineView.logErr = make(map[int]error)
		}
		m.pipelineView.logErr[msg.jobID] = msg.err
		if msg.jobID == m.pipelineView.pendingLogJobID {
			m.pipelineView.pendingLogJobID = 0
		}
		if msg.jobID == m.pipelineView.logJobID && m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{err: msg.err}
		}
		return m, nil
	}
	if m.pipelineView.logCache == nil {
		m.pipelineView.logCache = make(map[int]string)
	}

	// Truncate oversized logs and evict old entries to prevent OOM
	truncated := truncateLogContent(msg.content)
	m.pipelineView.logCache[msg.jobID] = truncated
	m.evictOldLogs()
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, msg.jobID)
	}
	if msg.jobID != m.pipelineView.logJobID && msg.jobID != m.pipelineView.pendingLogJobID {
		return m, nil
	}
	if !m.pipelineView.logAutoFollow {
		return m, nil
	}
	if msg.jobID == m.pipelineView.pendingLogJobID {
		m.pipelineView.pendingLogJobID = 0
	}
	m.pipelineView.logPreview = previewState{
		path:    fmt.Sprintf("job-%d", msg.jobID),
		content: msg.content,
		raw:     msg.content,
		loading: false,
	}
	m.pipelineView.logViewport.SetContent(msg.content)
	m.pipelineView.logJobID = msg.jobID
	if m.pipelineView.logAutoFollow {
		m.pipelineView.logViewport.GotoBottom()
	}
	return m, nil
}

func (m Model) handlePipelineRetried(msg pipelineRetriedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.pipelineView.retrying = false
	m.pipelineView.confirmRetry = false
	m.pipelineView.confirmRetryID = 0
	m.pipelineView.confirmRetryRef = ""
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryJobID = 0
	m.pipelineView.confirmRetryJobName = ""
	m.pipelineView.confirmRetryJobStage = ""
	if msg.err != nil {
		m.pipelineView.retryErr = msg.err
		m.status = fmt.Sprintf("Failed to retry pipeline #%d", msg.pipelineID)
		return m, nil
	}
	m.pipelineView.retryErr = nil
	if msg.pipeline.ID != 0 {
		m.pipelineView.pendingSelectID = msg.pipeline.ID
		if msg.pipeline.ID == msg.pipelineID {
			m.status = fmt.Sprintf("Retried pipeline #%d", msg.pipeline.ID)
		} else {
			m.status = fmt.Sprintf("Triggered pipeline #%d", msg.pipeline.ID)
		}
	} else if msg.pipelineID != 0 {
		m.pipelineView.pendingSelectID = msg.pipelineID
		m.status = fmt.Sprintf("Retried pipeline #%d", msg.pipelineID)
	} else {
		m.status = "Pipeline retriggered"
	}
	m.pipelineView.page = 1
	return m.reloadPipelineView()
}

func (m Model) handlePipelineJobRetried(msg pipelineJobRetriedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.pipelineView.retrying = false
	m.pipelineView.confirmRetry = false
	m.pipelineView.confirmRetryID = 0
	m.pipelineView.confirmRetryRef = ""
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryJobID = 0
	m.pipelineView.confirmRetryJobName = ""
	m.pipelineView.confirmRetryJobStage = ""
	if msg.err != nil {
		m.pipelineView.retryErr = msg.err
		m.status = fmt.Sprintf("Failed to retry job #%d", msg.jobID)
		return m, nil
	}
	m.pipelineView.retryErr = nil
	if msg.job.ID != 0 {
		if msg.job.Name != "" {
			m.status = fmt.Sprintf("Retried job %s (#%d)", msg.job.Name, msg.job.ID)
		} else {
			m.status = fmt.Sprintf("Retried job #%d", msg.job.ID)
		}
	} else if msg.jobID != 0 {
		m.status = fmt.Sprintf("Retried job #%d", msg.jobID)
	} else {
		m.status = "Job retried"
	}
	var cmds []tea.Cmd
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
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineTick() (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeProjects:
		cmd := (&m).queuePipelineFetchForSelection(false)
		return m, cmd
	case modePipelines:
		cmd := (&m).queuePipelineViewRefresh()
		return m, cmd
	default:
		return m, nil
	}
}

func (m Model) handlePipelineDebounceTickMsg(msg pipelineDebounceTickMsg) (tea.Model, tea.Cmd) {
	// Ignore stale ticks
	if m.pipelineDebounceTimer == nil || !msg.timestamp.Equal(*m.pipelineDebounceTimer) {
		return m, nil
	}

	// Verify project still selected
	if m.pipelinePendingFetch == nil || m.pipelinePendingFetch.ID != msg.projectID {
		return m, nil
	}

	// Execute fetch
	m.pipelineDebounceTimer = nil
	project := *m.pipelinePendingFetch
	m.pipelinePendingFetch = nil

	return m, (&m).queuePipelineFetch(project, true)
}

func (m Model) handleBatchPipelineStatus(msg batchPipelineStatusMsg) (tea.Model, tea.Cmd) {
	if m.pipelineStatus == nil {
		m.pipelineStatus = make(map[int]pipelineState)
	}

	selectedID := 0
	if m.mode == modeProjects {
		visible := m.visibleProjects()
		if m.selected >= 0 && m.selected < len(visible) {
			selectedID = visible[m.selected].ID
		}
	}

	now := time.Now()
	for projectID, result := range msg.results {
		state := m.pipelineStatus[projectID]
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

		m.pipelineStatus[projectID] = state
		if projectID == selectedID {
			(&m).invalidateDetailCache()
		}
	}

	// Evict old cache entries if needed
	(&m).evictOldPipelineStatusCache()

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
