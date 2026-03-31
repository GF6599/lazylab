// Message handlers for pipeline-level concerns: pipeline list loading, stages,
// jobs, logs, retries, cancellation, play, child pipelines, bridges, and test
// reports.

package ui

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

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
	slices.SortStableFunc(m.pipelineView.pipelines, func(a, b gitlab.PipelineSummary) int {
		if !a.UpdatedAt.IsZero() && !b.UpdatedAt.IsZero() {
			if a.UpdatedAt.Equal(b.UpdatedAt) {
				return cmp.Compare(b.ID, a.ID)
			}
			return b.UpdatedAt.Compare(a.UpdatedAt)
		}
		if a.ID != b.ID {
			return cmp.Compare(b.ID, a.ID)
		}
		return cmp.Compare(b.Ref, a.Ref)
	})

	// Update pipeline list with sorted pipelines
	items := make([]list.Item, len(m.pipelineView.pipelines))
	for i, p := range m.pipelineView.pipelines {
		items[i] = pipelineItem{summary: p}
	}
	m.pipelineView.pipelineList.SetItems(items)

	selectedSame := false
	if m.pipelineView.pendingSelectID != 0 || prevSelectedID != 0 {
		for i, p := range m.pipelineView.pipelines {
			if m.pipelineView.pendingSelectID != 0 && p.ID == m.pipelineView.pendingSelectID {
				m.pipelineView.selected = i
				selectedSame = true
				m.pipelineView.pendingSelectID = 0
				break
			}
			if prevSelectedID != 0 && p.ID == prevSelectedID {
				m.pipelineView.selected = i
				selectedSame = true
				// Don't break — pendingSelectID takes priority if found later
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

	var cmds []tea.Cmd
	if cmd := m.queuePipelineStagesForSelection(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineJobsForSelection(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return m, nil
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
	if cmd := m.queuePipelineSubRefresh(); cmd != nil {
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

func (m Model) handlePipelineCanceled(msg pipelineCanceledMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("Failed to cancel pipeline #%d: %v", msg.pipelineID, RedactToken(msg.err.Error()))
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
		m.status = fmt.Sprintf("Failed to cancel job #%d: %v", msg.jobID, RedactToken(msg.err.Error()))
		return m, nil
	}
	m.status = fmt.Sprintf("Canceled job #%d", msg.jobID)
	return m, m.queuePipelineSubRefresh()
}

func (m Model) handleJobPlayed(msg jobPlayedMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("Failed to play job #%d: %v", msg.jobID, RedactToken(msg.err.Error()))
		return m, nil
	}
	if msg.job.Name != "" {
		m.status = fmt.Sprintf("Triggered job %s (#%d)", msg.job.Name, msg.job.ID)
	} else {
		m.status = fmt.Sprintf("Triggered job #%d", msg.jobID)
	}
	return m, m.queuePipelineSubRefresh()
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
