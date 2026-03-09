// state_pipelines.go manages pipeline view state: opening/closing the view,
// page navigation, stage/job/bridge/log fetching, retry logic, and the
// auto-refresh cascade that keeps pipeline data live.
package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

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

// closePipelineView exits pipeline mode and returns to the project list,
// clearing all pipeline view state.
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

// reloadPipelineView performs a hard refresh: resets the pipeline list, all
// per-pipeline caches (stages, jobs, logs, bridges), log preview, and focus
// back to the pipelines column. Returns a command to re-fetch the current page.
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

// changePipelinePage navigates by delta pages (e.g., +1 for next, -1 for prev),
// clamping to [1, totalPages]. Returns nil if the target page equals the current
// page (no-op). Resets all per-pipeline caches for the new page.
func (m *Model) changePipelinePage(delta int) tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	target := page + delta
	target = max(target, 1)
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

// queuePipelineSubRefresh re-fetches stages, jobs, bridges, and expanded child
// jobs for the selected pipeline. Used after mutations (retry, cancel, play)
// that may change pipeline sub-resource state.
func (m *Model) queuePipelineSubRefresh() tea.Cmd {
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
		return nil
	}
	return tea.Batch(cmds...)
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

// queuePipelineLogPreview loads the log for the currently selected stage row.
// Dispatches differently by row kind: bridge rows show metadata (no trace),
// bridge child rows fetch from the downstream project, and regular rows fetch
// from the parent project. Uses the log cache when available; otherwise starts
// an async fetch. Resets the viewport to top on job change, or auto-scrolls
// to bottom when logAutoFollow is true.
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

// queuePipelineLogRefresh re-fetches the log for the current job without
// resetting the viewport or clearing existing content. Unlike queuePipelineLogPreview
// (which resets state for a new selection), this merges fresh data into the
// existing preview, preserving scroll position for live-tailing.
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

// resetPipelineLogPreview clears all log preview state and re-enables auto-follow.
// Call this on page changes or pipeline selection changes where showing stale
// log content would be confusing.
func (m *Model) resetPipelineLogPreview() {
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
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
		m.logError("copy clipboard", "err", err)
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
		m.logError("copy clipboard", "err", err)
		return
	}
	m.status = fmt.Sprintf("Copied job %s URL", job.Name)
}

// evictOldLogs removes oldest entries from logCache if it exceeds maxLogCacheEntries.
// This prevents unbounded memory growth when auto-refreshing pipeline logs.
func (m *Model) evictOldLogs() {
	if m.pipelineView.logs.Len() <= maxLogCacheEntries {
		return
	}

	// Find jobs to evict (oldest by job ID - assumes increasing IDs over time)
	jobIDs := m.pipelineView.logs.Keys()
	slices.Sort(jobIDs)

	// Keep the current job and the most recent ones, remove the oldest
	toRemove := len(jobIDs) - maxLogCacheEntries
	for i := range toRemove {
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
