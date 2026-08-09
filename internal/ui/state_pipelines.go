// state_pipelines.go manages pipeline view state: opening/closing the view,
// page navigation, stage/job/bridge/log fetching, retry logic, and the
// auto-refresh cascade that keeps pipeline data live.
package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// newPipelineViewState returns a blank-but-valid pipeline view: every Bubble
// Tea sub-component (list, stage table, log viewport) is built with a non-nil
// delegate and the per-pipeline AsyncCaches are initialized, so the model is
// safe to size and render before any project is selected. This matters because
// the first WindowSizeMsg — which arrives before selection in modeMultiPanel —
// drives applyMultiPanelLayout into pipelineList.SetSize, and a zero-value
// list.Model panics there on a nil delegate. openPipelineView and
// loadProjectPipelines later overwrite this with project-scoped state.
func newPipelineViewState() pipelineViewState {
	return pipelineViewState{
		pipelineList:   newPipelineListModel(statusFrames{}),
		stageTable:     newStageTable(minSidebarWidth),
		page:           1,
		totalPages:     1,
		perPage:        pipelinePerPage,
		stages:         NewAsyncCache[int, []gitlab.PipelineStage](),
		jobs:           NewAsyncCache[int, []gitlab.PipelineJob](),
		logs:           NewAsyncCache[int, string](),
		bridges:        NewAsyncCache[int, []gitlab.PipelineBridge](),
		pipelineStarts: NewAsyncCache[int, time.Time](),
		childJobs:      NewAsyncCache[int, []gitlab.PipelineJob](),
		logAutoFollow:  true,
		focus:          pipelineFocusPipelines,
	}
}

// openPipelineView transitions to the pipeline list for a project. All
// pipeline caches (stages, jobs, logs, bridges, child jobs) are freshly
// initialized, and log auto-follow is enabled. Returns a command to fetch
// the first page of pipelines.
func (m Model) openPipelineView(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modePipelines

	// Initialize stage table (job-per-row layout)
	_, stagesInner, _, _, _ := pipelinePaneLayout(m.width, m.height)
	t := newStageTable(max(stagesInner, 56))

	// Initialize pipeline list
	pipelineList := newPipelineListModel(m.statusFrames())

	// Initialize log viewport with proper dimensions
	logVp := viewport.New(pipelineLogContentWidth(m.width), pipelineLogContentHeight(m.height))

	m.pipelineView = pipelineViewState{
		project:        project,
		pipelineList:   pipelineList,
		loading:        true,
		page:           1,
		totalPages:     1,
		perPage:        pipelinePerPage,
		stages:         NewAsyncCache[int, []gitlab.PipelineStage](),
		stageTable:     t,
		jobs:           NewAsyncCache[int, []gitlab.PipelineJob](),
		logs:           NewAsyncCache[int, string](),
		logViewport:    logVp,
		logAutoFollow:  true,
		focus:          pipelineFocusPipelines,
		bridges:        NewAsyncCache[int, []gitlab.PipelineBridge](),
		pipelineStarts: NewAsyncCache[int, time.Time](),
		childJobs:      NewAsyncCache[int, []gitlab.PipelineJob](),
	}
	m.status = fmt.Sprintf("Pipelines for %s", project.PathWithNamespace)
	return m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, m.pipelineView.page, m.pipelineView.perPage)
}

// clearRetryConfirm resets the retry confirmation modal fields only,
// without affecting the retrying flag or retry error.
func (m *Model) clearRetryConfirm() {
	m.pipelineView.retryConfirm = retryConfirmState{}
}

// clearAllRetryState resets all retry-related fields including the confirmation
// modal state, retrying flag, and retry error.
func (m *Model) clearAllRetryState() {
	m.clearRetryConfirm()
	m.pipelineView.retrying = false
	m.pipelineView.retryErr = nil
}

// handlePipelineRetryRequest opens the retry confirmation modal for either a
// pipeline or a specific job, depending on the current focus.
func (m Model) handlePipelineRetryRequest() (tea.Model, tea.Cmd) {
	if m.pipelineView.retrying {
		m.status = "Retry already in progress"
		return m, nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return m, nil
	}
	if m.pipelineView.focus == pipelineFocusStages {
		return m.requestStageRetry(pipeline)
	}
	m.pipelineView.retryConfirm = retryConfirmState{
		active: true,
		id:     pipeline.ID,
		ref:    pipeline.Ref,
	}
	return m, nil
}

// requestStageRetry prepares a job-scoped retry confirmation, queueing the
// stage/job fetch if the cached data is missing.
func (m Model) requestStageRetry(pipeline *gitlab.PipelineSummary) (tea.Model, tea.Cmd) {
	job := m.selectedPipelineJob()
	if job == nil {
		return m.queuePipelineDataForRetry()
	}
	m.pipelineView.retryConfirm = retryConfirmState{
		active:   true,
		isJob:    true,
		id:       pipeline.ID,
		jobID:    job.ID,
		jobName:  job.Name,
		jobStage: job.Stage,
	}
	if id := m.selectedStageJobRow().downstreamProjectID(); id != 0 {
		m.pipelineView.retryConfirm.projectID = id
	}
	return m, nil
}

// queuePipelineDataForRetry batches the stage and job fetches needed before a
// job-retry can resolve a target job.
func (m Model) queuePipelineDataForRetry() (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if cmd := m.queuePipelineStagesForSelection(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineJobsForSelection(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) > 0 {
		m.status = "Loading pipeline jobs..."
		return m, tea.Batch(cmds...)
	}
	m.status = "No job selected"
	return m, nil
}

// confirmPipelineRetry runs the modal's accept action: dispatches a job retry
// or a pipeline retry depending on the stored retryConfirmState.
func (m Model) confirmPipelineRetry() (tea.Model, tea.Cmd) {
	rc := m.pipelineView.retryConfirm
	(&m).clearRetryConfirm()
	if m.pipelineView.project.ID == 0 || m.pipelineView.retrying {
		return m, nil
	}
	if rc.isJob {
		return m.dispatchJobRetry(rc)
	}
	return m.dispatchPipelineRetry(rc)
}

// dispatchJobRetry issues a single-job retry. Falls back to the currently
// selected pipeline when the confirmation state lacks a pipeline ID (e.g.,
// the modal was opened on a stale selection).
func (m Model) dispatchJobRetry(rc retryConfirmState) (tea.Model, tea.Cmd) {
	if rc.jobID == 0 {
		return m, nil
	}
	pipelineID := rc.id
	if pipelineID == 0 {
		if pipeline := m.selectedPipeline(); pipeline != nil {
			pipelineID = pipeline.ID
		}
	}
	m.pipelineView.retrying = true
	m.pipelineView.retryErr = nil
	jobLabel := fmt.Sprintf("#%d", rc.jobID)
	if rc.jobName != "" {
		jobLabel = fmt.Sprintf("%s (#%d)", rc.jobName, rc.jobID)
	}
	m.status = fmt.Sprintf("Retrying job %s", jobLabel)
	return m, retryJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.jobActionTargetIn(rc.projectID), pipelineID, rc.jobID)
}

// dispatchPipelineRetry issues a whole-pipeline retry, defaulting ref to the
// project's default branch when the stored ref is empty.
func (m Model) dispatchPipelineRetry(rc retryConfirmState) (tea.Model, tea.Cmd) {
	if rc.id == 0 {
		return m, nil
	}
	ref := strings.TrimSpace(rc.ref)
	if ref == "" {
		ref = strings.TrimSpace(m.pipelineView.project.DefaultBranch)
	}
	m.pipelineView.retrying = true
	m.pipelineView.retryErr = nil
	m.status = fmt.Sprintf("Retrying pipeline #%d", rc.id)
	return m, retryPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, rc.id, ref)
}

// closePipelineView exits pipeline mode and returns to the project list,
// clearing all pipeline view state.
func (m *Model) closePipelineView() {
	m.mode = modeProjects
	// Reset to a blank-but-valid state (not the zero value) so a later layout
	// pass can size the pipeline list without dereferencing a nil delegate.
	m.pipelineView = newPipelineViewState()
}

// resetCaches clears all per-pipeline caches (stages, jobs, logs) in place so
// that stale data from a previous page or reload is not displayed. The caches
// are reused, not reallocated; matrixExpanded is intentionally preserved.
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

// initForProject prepares pipelineViewState for a fresh project load. Unlike
// resetCaches, this initializes new (empty) AsyncCaches rather than clearing
// existing ones, drops bridge expansion state, and resets all scalar fields
// to their first-load defaults. The caller still has to attach UI sub-models
// (pipelineList, stageTable, logViewport) since those depend on dimensions
// known only at the call site.
func (pv *pipelineViewState) initForProject(project gitlab.ProjectNode) {
	pv.project = project
	pv.loading = true
	pv.err = nil
	pv.pipelines = nil
	pv.selected = 0
	pv.page = 1
	pv.totalPages = 1
	pv.perPage = pipelinePerPage
	pv.stages = NewAsyncCache[int, []gitlab.PipelineStage]()
	pv.jobs = NewAsyncCache[int, []gitlab.PipelineJob]()
	pv.logs = NewAsyncCache[int, string]()
	pv.bridges = NewAsyncCache[int, []gitlab.PipelineBridge]()
	pv.childJobs = NewAsyncCache[int, []gitlab.PipelineJob]()
	pv.logAutoFollow = true
	pv.focus = pipelineFocusPipelines
	pv.testReport = nil
	pv.testReportLoading = false
	pv.testReportErr = nil
	pv.testReportPipelineID = 0
	pv.detailTab = detailTabLog
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
	if cmd := m.queuePipelineStartRefresh(); cmd != nil {
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

// A start time cannot move once a run has begun, so it is fetched once and never again, and never
// at all for a run that has already finished.
func (m *Model) queuePipelineStartRefresh() tea.Cmd {
	pipelineID, ok := m.shouldFetchPipelineData(m.pipelineView.pipelineStarts.LoadingMap())
	if !ok {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil || !isLivePipelineStatus(pipeline.Status) {
		return nil
	}
	if _, cached := m.pipelineView.pipelineStarts.Get(pipelineID); cached {
		return nil
	}
	m.pipelineView.pipelineStarts.SetLoading(pipelineID)
	return fetchPipelineStartCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID)
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
	row := m.selectedStageJobRow()
	// Bridge rows don't have job traces — show bridge info instead.
	if row != nil && row.Kind == rowKindBridge {
		content := bridgePreviewContent(row.Bridge, row.IsLast)
		m.pipelineView.logPreview = previewState{
			path: row.Bridge.Name, content: content, raw: content,
		}
		m.setLogViewportContent(content)
		m.pipelineView.logJobID = 0
		m.pipelineView.logViewport.GotoTop()
		return nil
	}
	// Bridge child rows have real jobs but may live in a different project.
	if row != nil && row.Kind == rowKindBridgeChild && row.Job != nil {
		projectID := row.ChildProjectID
		if projectID == 0 {
			projectID = m.pipelineView.project.ID
		}
		return m.showOrFetchJobLog(row.Job, projectID)
	}
	job := m.selectedPipelineJob()
	if job == nil {
		m.pipelineView.logPreview = previewState{content: "No jobs available."}
		return nil
	}
	return m.showOrFetchJobLog(job, m.pipelineView.project.ID)
}

// showOrFetchJobLog renders the cached log for job into the preview pane, or
// kicks off an async fetch if the log is not yet cached. Returns the fetch
// command (or nil if cached / already loading). Resets viewport scroll to top
// when the displayed job changes; auto-follows to bottom when logAutoFollow
// is enabled. The projectID parameter handles bridge-child jobs that live in
// a downstream project.
func (m *Model) showOrFetchJobLog(job *gitlab.PipelineJob, projectID int) tea.Cmd {
	if content, ok := m.pipelineView.logs.Get(job.ID); ok {
		prevJobID := m.pipelineView.logJobID
		m.pipelineView.logPreview = previewState{
			path: job.Name, content: content, raw: content,
		}
		m.setLogViewportContent(content)
		m.pipelineView.logJobID = job.ID
		switch {
		case m.pipelineView.logAutoFollow:
			m.pipelineView.logViewport.GotoBottom()
		case prevJobID != job.ID:
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

// copyPipelineURL returns a Cmd that copies the selected pipeline's web URL
// to the clipboard off the event loop. The result lands in m.status via
// clipboardWroteMsg. Guard paths (no selection, empty URL) still set
// m.status synchronously and return a nil Cmd.
func (m *Model) copyPipelineURL() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return nil
	}
	if pipeline.WebURL == "" {
		m.status = "Pipeline has no URL"
		return nil
	}
	return writeClipboardCmd(pipeline.WebURL, fmt.Sprintf("Copied pipeline #%d URL", pipeline.ID))
}

// copyJobURL returns a Cmd that copies the selected job's web URL to the
// clipboard off the event loop. Guard paths short-circuit with a synchronous
// status update and nil Cmd.
func (m *Model) copyJobURL() tea.Cmd {
	job := m.selectedPipelineJob()
	if job == nil {
		m.status = "No job selected"
		return nil
	}
	if job.WebURL == "" {
		m.status = "Job has no URL"
		return nil
	}
	return writeClipboardCmd(job.WebURL, fmt.Sprintf("Copied job %s URL", job.Name))
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
		m.clearStageTable()
		return
	}

	stages, _ := m.pipelineView.stages.Get(pipeline.ID)
	jobs, _ := m.pipelineView.jobs.Get(pipeline.ID)
	bridges, _ := m.pipelineView.bridges.Get(pipeline.ID)
	stages = injectBridgeOnlyStages(stages, bridges)

	if len(stages) == 0 || (len(jobs) == 0 && len(bridges) == 0) {
		m.clearStageTable()
		return
	}

	if m.pipelineView.matrixExpanded == nil {
		m.pipelineView.matrixExpanded = make(map[string]bool)
	}

	childJobsMap := m.collectExpandedBridgeChildren(bridges)
	richRows := buildStageJobRows(stages, jobs, bridges, m.pipelineView.matrixExpanded, childJobsMap)
	m.pipelineView.stageJobRows = richRows

	tableRows, jobRows := m.renderStageJobRows(richRows)
	m.pipelineView.jobRows = jobRows
	m.pipelineView.stageTable.SetRows(tableRows)

	if m.pipelineView.stageSelected >= len(tableRows) {
		m.pipelineView.stageSelected = max(0, len(tableRows)-1)
	}
	if m.pipelineView.stageSelected >= 0 && m.pipelineView.stageSelected < len(tableRows) {
		moveTableCursor(&m.pipelineView.stageTable, m.pipelineView.stageSelected)
	}
}

// refreshStageTableFrames reuses the structure updateStageTable worked out, so a tick costs one
// re-render rather than a rebuild from three caches, and SetRows leaves the cursor where it was.
func (m *Model) refreshStageTableFrames() {
	// The tick fires for anything on screen that moves, including a pipeline in another panel,
	// so most ticks reach a table with nothing in it to animate. Rebuilding one anyway costs
	// about 84 KB per tick on a 200 job pipeline and redraws it byte for byte the same.
	if !slices.ContainsFunc(m.pipelineView.stageJobRows, func(row stageJobRow) bool {
		return isLivePipelineStatus(row.Status)
	}) {
		return
	}
	// Only the status label moves between frames, so the job rows this rebuilds are identical
	// to the ones already held and the cursor keeps resolving to the same job.
	tableRows, _ := m.renderStageJobRows(m.pipelineView.stageJobRows)
	m.pipelineView.stageTable.SetRows(tableRows)
}

// clearStageTable empties the stage table and its parallel row slices, used
// when there is no selected pipeline or when a pipeline has no jobs/bridges.
func (m *Model) clearStageTable() {
	m.pipelineView.stageTable.SetRows([]table.Row{})
	m.pipelineView.jobRows = nil
	m.pipelineView.stageJobRows = nil
}

// injectBridgeOnlyStages appends synthetic PipelineStage entries for any stage
// referenced by a bridge but missing from the regular stages slice. Without
// this, bridge-only stages would be invisible because PipelineStages is built
// from ListPipelineJobs (which excludes bridges).
func injectBridgeOnlyStages(stages []gitlab.PipelineStage, bridges []gitlab.PipelineBridge) []gitlab.PipelineStage {
	if len(bridges) == 0 {
		return stages
	}
	stageSet := make(map[string]bool, len(stages))
	for _, s := range stages {
		stageSet[s.Name] = true
	}
	for _, b := range bridges {
		if stageSet[b.Stage] {
			continue
		}
		stageSet[b.Stage] = true
		stages = append(stages, gitlab.PipelineStage{Name: b.Stage, Status: b.Status})
	}
	return stages
}

// collectExpandedBridgeChildren returns a map of downstream-pipeline-ID to
// child jobs, but only for bridges the user has expanded. Used by
// buildStageJobRows to flatten child jobs into the table when expanded.
func (m *Model) collectExpandedBridgeChildren(bridges []gitlab.PipelineBridge) map[int][]gitlab.PipelineJob {
	var out map[int][]gitlab.PipelineJob
	for _, b := range bridges {
		if b.DownstreamPipeline == nil {
			continue
		}
		if !m.pipelineView.matrixExpanded[fmt.Sprintf("bridge:%d", b.ID)] {
			continue
		}
		cJobs, ok := m.pipelineView.childJobs.Get(b.DownstreamPipeline.ID)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[int][]gitlab.PipelineJob)
		}
		out[b.DownstreamPipeline.ID] = cJobs
	}
	return out
}

// renderStageJobRows converts the rich row model into the parallel slices the
// bubbles/table widget needs (display rows) and the cursor-to-job map used
// for log preview and retry. The slices are kept in lock-step so an index
// into one is valid in the other.
func (m *Model) renderStageJobRows(richRows []stageJobRow) ([]table.Row, []gitlab.PipelineJob) {
	tableRows := make([]table.Row, 0, len(richRows))
	jobRows := make([]gitlab.PipelineJob, 0, len(richRows))
	frames := m.statusFrames()
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
		statusLabel := frames.icon(status) + " " + strings.ToUpper(status)
		tableRow, job := m.stageJobTableRow(row, stageCol, statusLabel)
		tableRows = append(tableRows, tableRow)
		jobRows = append(jobRows, job)
	}
	return tableRows, jobRows
}

// stageJobTableRow formats a single stageJobRow for the table widget and
// returns the PipelineJob the cursor should resolve to when this row is
// selected. Bridge rows synthesize a PipelineJob so log/retry still work.
func (m *Model) stageJobTableRow(row stageJobRow, stageCol, statusLabel string) (table.Row, gitlab.PipelineJob) {
	switch row.Kind {
	case rowKindJob:
		return table.Row{row.Job.Name, stageCol, statusLabel}, *row.Job
	case rowKindMatrixGroup:
		name := fmt.Sprintf("%s %s [%d]", iconTreeExpanded, row.BaseName, len(row.Jobs))
		// Map group header to first sub-job for log preview fallback.
		return table.Row{name, stageCol, statusLabel}, row.Jobs[0]
	case rowKindMatrixChild:
		name := fmt.Sprintf("  %s %s", treeBranchPrefix(row.IsLast), row.Vars)
		// Children never show a stage column.
		return table.Row{name, "", statusLabel}, *row.Job
	case rowKindBridgeChild:
		name := fmt.Sprintf("  %s %s", treeBranchPrefix(row.IsLast), row.Job.Name)
		return table.Row{name, "", statusLabel}, *row.Job
	case rowKindBridge:
		return m.bridgeTableRow(row, stageCol, statusLabel)
	}
	return table.Row{}, gitlab.PipelineJob{}
}

// bridgeTableRow renders a bridge header row (collapsed/expanded) or a
// placeholder child-pipeline row when the bridge is expanded but child jobs
// haven't loaded yet.
func (m *Model) bridgeTableRow(row stageJobRow, stageCol, statusLabel string) (table.Row, gitlab.PipelineJob) {
	b := row.Bridge
	job := gitlab.PipelineJob{ID: b.ID, Name: b.Name, Stage: b.Stage, Status: b.Status}
	if row.IsLast && b.DownstreamPipeline != nil {
		name := fmt.Sprintf("  └─ child #%d", b.DownstreamPipeline.ID)
		return table.Row{name, "", statusLabel}, job
	}
	icon := iconTreeCollapsed
	if m.pipelineView.matrixExpanded[row.GroupKey] {
		icon = iconTreeExpanded
	}
	suffix := ""
	if b.DownstreamPipeline != nil {
		suffix = fmt.Sprintf(" → #%d", b.DownstreamPipeline.ID)
	}
	name := fmt.Sprintf("%s %s%s", icon, b.Name, suffix)
	return table.Row{name, stageCol, statusLabel}, job
}

func treeBranchPrefix(isLast bool) string {
	if isLast {
		return "└─"
	}
	return "├─"
}
