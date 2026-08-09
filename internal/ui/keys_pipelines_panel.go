// keys_pipelines_panel.go contains key handling for the Pipelines panel
// in the multi-panel layout.

package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// handlePipelinesPanelKey handles keys when the Pipelines panel is focused.
func (m Model) handlePipelinesPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Down) || key.Matches(msg, m.keys.Up):
		prevIdx := m.pipelineView.pipelineList.Index()
		var cmd tea.Cmd
		m.pipelineView.pipelineList, cmd = m.pipelineView.pipelineList.Update(msg)
		newIdx := m.pipelineView.pipelineList.Index()
		m.pipelineView.selected = newIdx
		if newIdx != prevIdx {
			return m, tea.Batch(cmd, m.onPipelineSelectionChanged())
		}
		return m, cmd
	case key.Matches(msg, m.keys.Enter):
		// Focus stages panel
		m.focus.Active = PanelStages
		return m, m.queuePipelineLogPreview()
	case key.Matches(msg, m.keys.Right):
		// Focus Detail pane
		m.focus.PrevActive = PanelPipelines
		m.focus.Active = PanelDetail
		return m, nil
	case key.Matches(msg, m.keys.Left):
		// Back to projects
		m.focus.Active = PanelProjects
		return m, nil
	case key.Matches(msg, m.keys.Back):
		// Jump to Projects (home)
		m.focus.Active = PanelProjects
		return m, nil
	case key.Matches(msg, m.keys.NextPage):
		if cmd := m.changePipelinePage(1); cmd != nil {
			return m, cmd
		}
	case key.Matches(msg, m.keys.PrevPage):
		if cmd := m.changePipelinePage(-1); cmd != nil {
			return m, cmd
		}
	case key.Matches(msg, m.keys.HalfDown) || key.Matches(msg, m.keys.HalfUp) ||
		key.Matches(msg, m.keys.Top) || key.Matches(msg, m.keys.Bottom):
		s := msg.String()
		newIdx, handled := bigStepIdx(s, m.pipelineView.selected, len(m.pipelineView.pipelines), m.height)
		if !handled || newIdx == m.pipelineView.selected {
			return m, nil
		}
		m.pipelineView.selected = newIdx
		return m, m.onPipelineSelectionChanged()
	case key.Matches(msg, m.keys.Refresh):
		return m.reloadPipelineView()
	case key.Matches(msg, m.keys.Retry):
		return m.openRetryModal()
	case key.Matches(msg, m.keys.Cancel):
		return m.cancelPipelineAction()
	case key.Matches(msg, m.keys.ScrollDown):
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case key.Matches(msg, m.keys.ScrollUp):
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case key.Matches(msg, m.keys.CycleTab):
		return m.cycleDetailTab()
	case key.Matches(msg, m.keys.CycleTabRv):
		return m.cycleDetailTabReverse()
	case key.Matches(msg, m.keys.Copy):
		return m, m.copyPipelineURL()
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
	if !isCancelableStatus(pipeline.Status) {
		m.status = "This pipeline has nothing left to cancel"
		return m, nil
	}
	m.status = fmt.Sprintf("Canceling pipeline #%d...", pipeline.ID)
	return m, cancelPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}
