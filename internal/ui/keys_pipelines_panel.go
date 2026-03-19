// keys_pipelines_panel.go contains key handling for the Pipelines panel
// in the multi-panel layout.

package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handlePipelinesPanelKey handles keys when the Pipelines panel is focused.
func (m Model) handlePipelinesPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "down", "j", "up", "k":
		prevIdx := m.pipelineView.pipelineList.Index()
		var cmd tea.Cmd
		m.pipelineView.pipelineList, cmd = m.pipelineView.pipelineList.Update(msg)
		newIdx := m.pipelineView.pipelineList.Index()
		m.pipelineView.selected = newIdx
		if newIdx != prevIdx {
			m.pipelineView.stageSelected = 0
			m.pipelineView.stageTable.SetCursor(0)
			m.resetPipelineLogPreview()
			stagesCmd := m.queuePipelineStagesForSelection()
			jobsCmd := m.queuePipelineJobsForSelection()
			return m, tea.Batch(cmd, stagesCmd, jobsCmd)
		}
		return m, cmd
	case "enter":
		// Focus stages panel
		m.focus.Active = PanelStages
		return m, m.queuePipelineLogPreview()
	case "l", "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelPipelines
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "left":
		// Back to projects
		m.focus.Active = PanelProjects
		return m, nil
	case "esc":
		// Jump to Projects (home)
		m.focus.Active = PanelProjects
		return m, nil
	case "]":
		if cmd := m.changePipelinePage(1); cmd != nil {
			return m, cmd
		}
	case "[":
		if cmd := m.changePipelinePage(-1); cmd != nil {
			return m, cmd
		}
	case "ctrl+d":
		step := listPageStep(m.height)
		if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
			m.pipelineView.selected = min(m.pipelineView.selected+step, len(m.pipelineView.pipelines)-1)
			m.pipelineView.stageSelected = 0
			m.pipelineView.stageTable.SetCursor(0)
			m.resetPipelineLogPreview()
			return m, tea.Batch(m.queuePipelineStagesForSelection(), m.queuePipelineJobsForSelection())
		}
	case "ctrl+u":
		step := listPageStep(m.height)
		if m.pipelineView.selected > 0 {
			m.pipelineView.selected = max(m.pipelineView.selected-step, 0)
			m.pipelineView.stageSelected = 0
			m.pipelineView.stageTable.SetCursor(0)
			m.resetPipelineLogPreview()
			return m, tea.Batch(m.queuePipelineStagesForSelection(), m.queuePipelineJobsForSelection())
		}
	case "<", "g":
		if len(m.pipelineView.pipelines) > 0 && m.pipelineView.selected != 0 {
			m.pipelineView.selected = 0
			m.pipelineView.stageSelected = 0
			m.resetPipelineLogPreview()
			return m, tea.Batch(m.queuePipelineStagesForSelection(), m.queuePipelineJobsForSelection())
		}
	case ">", "G":
		if len(m.pipelineView.pipelines) > 0 {
			last := len(m.pipelineView.pipelines) - 1
			if m.pipelineView.selected != last {
				m.pipelineView.selected = last
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				return m, tea.Batch(m.queuePipelineStagesForSelection(), m.queuePipelineJobsForSelection())
			}
		}
	case "r", "ctrl+r":
		return m.reloadPipelineView()
	case "R":
		return m.openRetryModal()
	case "C":
		return m.cancelPipelineAction()
	case "J":
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K":
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "t":
		return m.cycleDetailTab()
	case "T":
		return m.cycleDetailTabReverse()
	case "ctrl+o":
		m.copyPipelineURL()
	}
	return m, nil
}

// openRetryModal opens the retry confirmation modal for the selected pipeline.
func (m Model) openRetryModal() (tea.Model, tea.Cmd) {
	if m.pipelineView.retrying {
		m.status = "Retry already in progress"
		return m, nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return m, nil
	}
	m.pipelineView.retryConfirm = retryConfirmState{
		active: true,
		id:     pipeline.ID,
		ref:    pipeline.Ref,
	}
	return m, nil
}

// cancelPipelineAction cancels the selected pipeline with confirmation.
func (m Model) cancelPipelineAction() (tea.Model, tea.Cmd) {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return m, nil
	}
	if !strings.EqualFold(pipeline.Status, "running") && !strings.EqualFold(pipeline.Status, "pending") {
		m.status = "Pipeline is not running or pending"
		return m, nil
	}
	m.status = fmt.Sprintf("Canceling pipeline #%d...", pipeline.ID)
	return m, cancelPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}
