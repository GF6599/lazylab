// This file contains all message handlers — methods that process the typed
// messages produced by commands in project_list_cmds.go. Each handler
// updates Model state and optionally returns follow-up commands for
// cascading fetches (e.g., loading stages after pipelines arrive).
//
// Common patterns across handlers:
//   - Guard clause: discard the message if the mode or project ID no longer
//     matches (the user may have navigated away while the fetch was in flight).
//   - Selection persistence: after reloading a list, try to restore the cursor
//     to the same item by matching on ID/IID to avoid jarring jumps.
//   - Cascade: when parent data arrives (pipelines), automatically fetch
//     child data (stages, jobs) for the selected item.

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

// handleCacheLoaded processes the on-disk project cache result. On a cache hit,
// all pages are marked ready immediately and the pipeline refresh ticker starts.
// On a miss (or error), it falls back to a foreground API fetch. In multi-panel
// mode it also triggers sidebar data loading for the initially selected project.
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

// handleTreeLoaded processes a fetched directory listing. It serves two purposes:
//   - Directory preview: if msg.path matches the preview path, format entries
//     as a listing and display in the preview viewport.
//   - Directory navigation: if msg.path matches a stack entry, populate its
//     entries and update the corresponding bubbles list.
func (m Model) handleTreeLoaded(msg treeLoadedMsg) (tea.Model, tea.Cmd) {
	isExplorer := m.mode == modeExplorer || (m.mode == modeMultiPanel && m.explorer.project.ID != 0)
	if !isExplorer || m.explorer.project.ID != msg.projectID {
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
	isExplorer := m.mode == modeExplorer || (m.mode == modeMultiPanel && m.explorer.project.ID != 0)
	if !isExplorer || m.explorer.project.ID != msg.projectID {
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
		if m.opts.Logger != nil {
			m.opts.Logger.Error("load projects", "err", msg.err, "background", msg.background)
		}
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

// handlePipelinesLoaded updates the pipeline list for the viewed project.
// Pipelines are sorted by UpdatedAt descending (newest first), then by ID as
// a tiebreaker. After sorting, it attempts to preserve the user's cursor
// position by matching on pipeline ID — first checking pendingSelectID (set
// after a retry), then the previously selected ID. This prevents the cursor
// from jumping during auto-refresh. Finally, it cascades into fetching stages
// and jobs for the (possibly new) selected pipeline.
func (m Model) handlePipelinesLoaded(msg pipelinesLoadedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
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
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.pipelineView.stages.SetErr(msg.pipelineID, msg.err)
		return m, nil
	}
	m.pipelineView.stages.Set(msg.pipelineID, msg.stages)

	// Update stage table with new data
	m.updateStageTable()

	// Fetch bridges for this pipeline (always re-fetch to pick up status changes)
	var cmds []tea.Cmd
	if cmd := m.queuePipelineLogPreview(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if !m.pipelineView.bridges.IsLoading(msg.pipelineID) {
		m.pipelineView.bridges.SetLoading(msg.pipelineID)
		cmds = append(cmds, fetchBridgesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, msg.pipelineID))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineJobsLoaded(msg pipelineJobsLoadedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			m.pipelineView.jobs.Set(msg.pipelineID, nil)
			return m, m.queuePipelineLogPreview()
		}
		m.pipelineView.jobs.SetErr(msg.pipelineID, msg.err)
		return m, nil
	}
	m.pipelineView.jobs.Set(msg.pipelineID, msg.jobs)

	// Update stage table with new job data
	m.updateStageTable()

	return m, m.queuePipelineLogPreview()
}

// handlePipelineLogLoaded stores a fetched job log trace and updates the log
// preview viewport. Logs are truncated to maxLogSizeBytes to prevent OOM, and
// old entries are evicted via LRU. The viewport only updates when logAutoFollow
// is true — if the user has manually scrolled, the scroll position is
// preserved and the content is silently cached for the next auto-follow toggle.
func (m Model) handlePipelineLogLoaded(msg pipelineLogLoadedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.pipelineView.logs.SetErr(msg.jobID, msg.err)
		if msg.jobID == m.pipelineView.pendingLogJobID {
			m.pipelineView.pendingLogJobID = 0
		}
		if msg.jobID == m.pipelineView.logJobID && m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{err: msg.err}
		}
		return m, nil
	}

	// Truncate oversized logs and evict old entries to prevent OOM
	truncated := truncateLogContent(msg.content)
	m.pipelineView.logs.Set(msg.jobID, truncated)
	m.evictOldLogs()
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
	m.setLogViewportContent(msg.content)
	m.pipelineView.logJobID = msg.jobID
	if m.pipelineView.logAutoFollow {
		m.pipelineView.logViewport.GotoBottom()
	}
	return m, nil
}

// handlePipelineRetried processes the result of retrying an entire pipeline.
// On success it sets pendingSelectID so the cursor follows the new (or same)
// pipeline after reload, then triggers a full pipeline view reload from page 1.
func (m Model) handlePipelineRetried(msg pipelineRetriedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.clearAllRetryState()
	if msg.err != nil {
		m.pipelineView.retryErr = msg.err
		m.status = fmt.Sprintf("Failed to retry pipeline #%d", msg.pipelineID)
		return m, nil
	}
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

// handlePipelineJobRetried processes a single job retry result. Unlike pipeline
// retry, it does not reload the full pipeline list — it only refreshes stages,
// jobs, bridges, and the log for the affected pipeline to show the updated
// job status.
func (m Model) handlePipelineJobRetried(msg pipelineJobRetriedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.clearAllRetryState()
	if msg.err != nil {
		m.pipelineView.retryErr = msg.err
		m.status = fmt.Sprintf("Failed to retry job #%d", msg.jobID)
		return m, nil
	}
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
		return m, nil
	}
	return m, tea.Batch(cmds...)
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

// handleBatchPipelineStatus processes the results from batchFetchPipelineStatusCmd,
// updating the pipelineStatus map for each project in a single pass. If the
// currently selected project was among the results, the detail pane cache is
// invalidated so the updated status icon renders immediately.
func (m Model) handleBatchPipelineStatus(msg batchPipelineStatusMsg) (tea.Model, tea.Cmd) {
	if m.pipelineStatus == nil {
		m.pipelineStatus = make(map[int]pipelineState)
	}

	selectedID := 0
	if m.mode == modeProjects || m.mode == modeMultiPanel {
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
			content := renderMRCommentsText(msg.discussions, m.mrViewportWidth())
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
	if m.mrView.detailTab == mrDetailTabDiff {
		mr := m.mrView.selectedMR()
		if mr != nil && mr.IID == msg.mrIID {
			content := renderMRDiffText(msg.diffs, m.mrViewportWidth())
			m.setMRViewportContent(content)
			m.mrView.mrViewport.GotoTop()
		}
	}
	return m, nil
}

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

func (m Model) handlePipelineCanceled(msg pipelineCanceledMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("Failed to cancel pipeline #%d: %v", msg.pipelineID, msg.err)
		return m, nil
	}
	m.status = fmt.Sprintf("Canceled pipeline #%d", msg.pipelineID)
	return m.reloadPipelineView()
}

func (m Model) handleJobCanceled(msg jobCanceledMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("Failed to cancel job #%d: %v", msg.jobID, msg.err)
		return m, nil
	}
	m.status = fmt.Sprintf("Canceled job #%d", msg.jobID)
	var cmds []tea.Cmd
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
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleJobPlayed(msg jobPlayedMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("Failed to play job #%d: %v", msg.jobID, msg.err)
		return m, nil
	}
	if msg.job.Name != "" {
		m.status = fmt.Sprintf("Triggered job %s (#%d)", msg.job.Name, msg.job.ID)
	} else {
		m.status = fmt.Sprintf("Triggered job #%d", msg.jobID)
	}
	var cmds []tea.Cmd
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
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleChildPipelineJobsLoaded(msg childPipelineJobsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines && m.mode != modeMultiPanel {
		return m, nil
	}
	if msg.err != nil {
		m.pipelineView.childJobs.SetErr(msg.childPipelineID, msg.err)
		return m, nil
	}
	m.pipelineView.childJobs.Set(msg.childPipelineID, msg.jobs)
	m.updateStageTable()
	return m, m.queuePipelineLogPreview()
}

func (m Model) handleBridgesLoaded(msg bridgesLoadedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.pipelineView.bridges.SetErr(msg.pipelineID, msg.err)
		return m, nil
	}
	m.pipelineView.bridges.Set(msg.pipelineID, msg.bridges)
	// Rebuild stage table so bridge rows appear immediately
	m.updateStageTable()
	// Re-fetch child jobs for any expanded bridges so their statuses update
	if cmd := m.queueExpandedChildJobsRefresh(); cmd != nil {
		return m, cmd
	}
	return m, nil
}

func (m Model) handleTestReportLoaded(msg testReportLoadedMsg) (tea.Model, tea.Cmd) {
	if (m.mode != modePipelines && m.mode != modeMultiPanel) || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.pipelineView.testReportLoading = false
	if msg.err != nil {
		m.pipelineView.testReportErr = msg.err
		return m, nil
	}
	m.pipelineView.testReport = msg.report
	m.pipelineView.testReportErr = nil
	m.pipelineView.testReportPipelineID = msg.pipelineID
	return m, nil
}

// handleCommitsLoaded stores fetched commits and invalidates the detail pane
// cache so the "Recent Commits" section renders on the next frame. Errors are
// logged but not surfaced to the user — missing commits are non-critical.
func (m Model) handleCommitsLoaded(msg commitsLoadedMsg) (tea.Model, tea.Cmd) {
	m.commitLoading[msg.projectID] = false
	if msg.err != nil {
		if m.opts.Logger != nil {
			m.opts.Logger.Error("load commits", "err", msg.err, "project", msg.projectID)
		}
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
