// keys_multipanel.go contains the top-level keyboard router for the
// multi-panel layout and shared utilities used by multiple panel handlers.
//
// Key events flow through a two-level dispatch:
//
//  1. handleMultiPanelKey handles global keys that work in any panel
//     (quit, Tab/Shift-Tab cycling, number-key panel jumps, layout toggles).
//  2. It then delegates to a panel-specific handler based on focus.Active.
//
// Each panel handler follows the same contract: it receives a tea.KeyMsg,
// mutates the model as needed, and returns (tea.Model, tea.Cmd). Panel
// handlers own their own navigation, selection, and action keys. The Detail
// pane handler uses focus.PrevActive to decide whether to scroll the pipeline
// log viewport or the MR viewport.
//
// Panel-specific handlers live in separate files:
//   - keys_projects_panel.go
//   - keys_pipelines_panel.go
//   - keys_stages_panel.go
//   - keys_detail_panel.go
//   - keys_mrs_panel.go

package ui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleMultiPanelKey is the top-level key router for the multi-panel layout.
// Global keys (quit, Tab, number shortcuts, layout toggles) are handled first;
// everything else is delegated to the focused panel's handler.
func (m Model) handleMultiPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global keys that work regardless of focused panel
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		// Only quit if not in search mode
		if m.focus.Active != PanelProjects || !m.search.active {
			return m, tea.Quit
		}
	case "tab", "shift+tab":
		if m.focus.Active == PanelDetail {
			m.focus.Active = m.focus.PrevActive
			return m, nil
		}
		if key == "tab" {
			m.focus.Active = nextSidebarPanel(m.focus.Active)
		} else {
			m.focus.Active = prevSidebarPanel(m.focus.Active)
		}
		return m, m.onPanelFocusChanged()
	case "+", "-":
		m.focus.ToggleLayoutMode()
		return m, nil
	case "=":
		m.focus.NextScreenMode()
		return m, nil
	case "1", "2", "3", "4", "5":
		n := int(key[0] - '0')
		if panel, ok := panelByShortcut(n); ok {
			m.focus.Active = panel
			return m, m.onPanelFocusChanged()
		}
	}

	// Delegate to focused panel handler
	switch m.focus.Active {
	case PanelProjects:
		return m.handleProjectsPanelKey(msg)
	case PanelPipelines:
		return m.handlePipelinesPanelKey(msg)
	case PanelStages:
		return m.handleStagesPanelKey(msg)
	case PanelMRs:
		return m.handleMRsPanelKey(msg)
	case PanelDetail:
		return m.handleDetailPanelKey(msg)
	default:
		return m, nil
	}
}

// onPanelFocusChanged triggers lazy data loading when a panel gains focus.
// Currently only the MRs panel uses this; pipelines and stages are pre-loaded
// by autoLoadSelectedProjectData when the project selection changes.
func (m *Model) onPanelFocusChanged() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		return nil
	}

	switch m.focus.Active {
	case PanelMRs:
		if m.mrView.project.ID != project.ID {
			mrVp := viewport.New(0, 0)
			if layout := computeLayout(m.width, m.height, m.focus); layout.OK {
				mrVp = viewport.New(layout.DetailWidth, layout.DetailHeight)
			}
			m.mrView = mrViewState{
				project:     project,
				loading:     true,
				discussions: NewAsyncCache[int, []gitlab.MRDiscussion](),
				diffs:       NewAsyncCache[int, []gitlab.MRDiffFile](),
				mrViewport:  mrVp,
			}
			return fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, mrTabStateString(m.mrView.tab), 1, mrPerPage)
		}
	}
	return nil
}

// cycleDetailTab cycles through detail pane tabs based on context.
func (m Model) cycleDetailTab() (tea.Model, tea.Cmd) {
	ctx := detailContextPanel(&m)
	if ctx == PanelMRs {
		return m.cycleMRDetailTab()
	}
	m.pipelineView.detailTab = (m.pipelineView.detailTab + 1) % 3
	// Fetch test report when switching to Tests tab
	if m.pipelineView.detailTab == detailTabTests {
		pipeline := m.selectedPipeline()
		if pipeline != nil && m.pipelineView.testReportPipelineID != pipeline.ID {
			m.pipelineView.testReportLoading = true
			m.pipelineView.testReport = nil
			m.pipelineView.testReportErr = nil
			return m, fetchTestReportCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
		}
	}
	return m, nil
}

// cycleDetailTabReverse cycles detail pane tabs backward.
func (m Model) cycleDetailTabReverse() (tea.Model, tea.Cmd) {
	ctx := detailContextPanel(&m)
	if ctx == PanelMRs {
		return m.cycleMRDetailTabReverse()
	}
	m.pipelineView.detailTab = (m.pipelineView.detailTab + 2) % 3
	if m.pipelineView.detailTab == detailTabTests {
		pipeline := m.selectedPipeline()
		if pipeline != nil && m.pipelineView.testReportPipelineID != pipeline.ID {
			m.pipelineView.testReportLoading = true
			m.pipelineView.testReport = nil
			m.pipelineView.testReportErr = nil
			return m, fetchTestReportCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
		}
	}
	return m, nil
}

// cycleMRDetailTab cycles through MR detail pane tabs (Info → Comments → Diff).
func (m Model) cycleMRDetailTab() (tea.Model, tea.Cmd) {
	return m.setMRDetailTab((m.mrView.detailTab + 1) % 3)
}

// cycleMRDetailTabReverse cycles MR detail pane tabs backward (Diff → Comments → Info).
func (m Model) cycleMRDetailTabReverse() (tea.Model, tea.Cmd) {
	return m.setMRDetailTab((m.mrView.detailTab + 2) % 3)
}

// setMRDetailTab switches to the given MR detail tab, fetching data if needed.
func (m Model) setMRDetailTab(tab mrDetailTab) (tea.Model, tea.Cmd) {
	m.mrView.detailTab = tab
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	switch tab {
	case mrDetailTabComments:
		if _, cached := m.mrView.discussions.Get(mr.IID); !cached && !m.mrView.discussions.IsLoading(mr.IID) {
			m.mrView.discussions.SetLoading(mr.IID)
			return m, fetchMRDiscussionsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mr.IID)
		}
		if discussions, ok := m.mrView.discussions.Get(mr.IID); ok {
			m.setMRViewportContent(renderMRCommentsText(discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion))
			m.mrView.mrViewport.GotoTop()
		}
	case mrDetailTabDiff:
		if _, cached := m.mrView.diffs.Get(mr.IID); !cached && !m.mrView.diffs.IsLoading(mr.IID) {
			m.mrView.diffs.SetLoading(mr.IID)
			return m, fetchMRDiffsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mr.IID)
		}
		if diffs, ok := m.mrView.diffs.Get(mr.IID); ok {
			m.setMRViewportContent(renderMRDiffText(diffs, m.mrViewportWidth(), m.mrView.diffCursor))
			m.mrView.mrViewport.GotoTop()
		}
	}
	return m, nil
}
