// keys_stages_panel.go contains key handling for the Stages panel
// in the multi-panel layout.

package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleStagesPanelKey handles keys when the Stages panel is focused.
func (m Model) handleStagesPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ensure table accepts key events
	m.pipelineView.stageTable.Focus()

	key := msg.String()
	switch key {
	case "enter":
		// Toggle bridge expand/collapse
		row := m.selectedStageJobRow()
		if row != nil && row.Kind == rowKindBridge && !row.IsLast {
			if m.pipelineView.matrixExpanded == nil {
				m.pipelineView.matrixExpanded = make(map[string]bool)
			}
			expanding := !m.pipelineView.matrixExpanded[row.GroupKey]
			m.pipelineView.matrixExpanded[row.GroupKey] = expanding
			m.updateStageTable()
			// Fetch child pipeline jobs when expanding
			if expanding && row.Bridge != nil && row.Bridge.DownstreamPipeline != nil {
				ds := row.Bridge.DownstreamPipeline
				if ds.ProjectID != 0 && !m.pipelineView.childJobs.IsLoading(ds.ID) {
					if _, cached := m.pipelineView.childJobs.Get(ds.ID); !cached {
						m.pipelineView.childJobs.SetLoading(ds.ID)
						return m, fetchChildPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, ds.ProjectID, ds.ID)
					}
				}
			}
			return m, nil
		}
	case "down", "j", "up", "k":
		prevIdx := m.pipelineView.stageTable.Cursor()
		var cmd tea.Cmd
		m.pipelineView.stageTable, cmd = m.pipelineView.stageTable.Update(msg)
		newIdx := m.pipelineView.stageTable.Cursor()
		m.pipelineView.stageSelected = newIdx
		if newIdx != prevIdx {
			m.resetPipelineLogPreview()
			return m, tea.Batch(cmd, m.queuePipelineLogPreview())
		}
		return m, cmd
	case "l", "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelStages
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "left":
		// Back to Pipelines
		m.focus.Active = PanelPipelines
		return m, nil
	case "esc":
		// Jump to Projects (home)
		m.focus.Active = PanelProjects
		return m, nil
	case "J":
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K":
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "ctrl+d", "ctrl+u", "<", "g", ">", "G":
		switch key {
		case "ctrl+d":
			m.pipelineView.logViewport.HalfPageDown()
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		case "ctrl+u":
			m.pipelineView.logViewport.HalfPageUp()
			m.pipelineView.logAutoFollow = false
		case "<", "g":
			m.pipelineView.logViewport.GotoTop()
			m.pipelineView.logAutoFollow = false
		case ">", "G":
			m.pipelineView.logViewport.GotoBottom()
			m.pipelineView.logAutoFollow = true
		}
		newIdx, handled := bigStepIdx(key, m.pipelineView.stageSelected, len(m.pipelineView.jobRows), m.height)
		if !handled || newIdx == m.pipelineView.stageSelected {
			return m, nil
		}
		m.pipelineView.stageSelected = newIdx
		m.pipelineView.stageTable.SetCursor(newIdx)
		m.resetPipelineLogPreview()
		return m, m.queuePipelineLogPreview()
	case "R":
		return m.openRetryModalForJob()
	case "C":
		return m.cancelJobAction()
	case "P":
		return m.playManualJob()
	case "r", "ctrl+r":
		return m.reloadPipelineView()
	case "t":
		return m.cycleDetailTab()
	case "T":
		return m.cycleDetailTabReverse()
	case "ctrl+o":
		m.copyJobURL()
	}
	return m, nil
}

// openRetryModalForJob opens the retry confirmation modal for the selected job.
func (m Model) openRetryModalForJob() (tea.Model, tea.Cmd) {
	if row := m.selectedStageJobRow(); row != nil && (row.Kind == rowKindMatrixGroup || row.Kind == rowKindBridge) {
		m.status = "Select an individual job to perform this action"
		return m, nil
	}
	if m.pipelineView.retrying {
		m.status = "Retry already in progress"
		return m, nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return m, nil
	}
	job := m.selectedPipelineJob()
	if job == nil {
		m.status = "No job selected"
		return m, nil
	}
	m.pipelineView.retryConfirm = retryConfirmState{
		active:   true,
		isJob:    true,
		id:       pipeline.ID,
		jobID:    job.ID,
		jobName:  job.Name,
		jobStage: job.Stage,
	}
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridgeChild && row.ChildProjectID != 0 {
		m.pipelineView.retryConfirm.projectID = row.ChildProjectID
	}
	return m, nil
}

// cancelJobAction cancels the selected job.
func (m Model) cancelJobAction() (tea.Model, tea.Cmd) {
	if row := m.selectedStageJobRow(); row != nil && (row.Kind == rowKindMatrixGroup || row.Kind == rowKindBridge) {
		m.status = "Select an individual job to perform this action"
		return m, nil
	}
	job := m.selectedPipelineJob()
	if job == nil {
		m.status = "No job selected"
		return m, nil
	}
	if !strings.EqualFold(job.Status, "running") && !strings.EqualFold(job.Status, "pending") {
		m.status = "Job is not running or pending"
		return m, nil
	}
	m.status = fmt.Sprintf("Canceling job %s (#%d)...", job.Name, job.ID)
	return m, cancelJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}

// playManualJob triggers a manual job.
func (m Model) playManualJob() (tea.Model, tea.Cmd) {
	if row := m.selectedStageJobRow(); row != nil && (row.Kind == rowKindMatrixGroup || row.Kind == rowKindBridge) {
		m.status = "Select an individual job to perform this action"
		return m, nil
	}
	job := m.selectedPipelineJob()
	if job == nil {
		m.status = "No job selected"
		return m, nil
	}
	if !strings.EqualFold(job.Status, "manual") {
		m.status = "Job is not manual"
		return m, nil
	}
	m.status = fmt.Sprintf("Playing job %s (#%d)...", job.Name, job.ID)
	return m, playJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}
