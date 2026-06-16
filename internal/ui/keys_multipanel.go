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
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleMultiPanelKey is the top-level key router for the multi-panel layout.
// Global keys (quit, Tab, number shortcuts, layout toggles) are handled first;
// everything else is delegated to the focused panel's handler.
func (m Model) handleMultiPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global keys that work regardless of focused panel.
	// Quit covers both "q" and "ctrl+c"; suppress the printable "q" while
	// typing in the project search so the user can spell project names freely.
	// Ctrl+C always quits.
	if key.Matches(msg, m.keys.Quit) {
		s := msg.String()
		if s == "q" && m.focus.Active == PanelProjects && m.search.active {
			// fall through — let the search handler consume "q"
		} else {
			return m, tea.Quit
		}
	}
	if key.Matches(msg, m.keys.NextPanel) {
		if m.focus.Active == PanelDetail {
			m.focus.Active = m.focus.PrevActive
			return m, nil
		}
		m.focus.Active = nextSidebarPanel(m.focus.Active)
		return m, m.onPanelFocusChanged()
	}
	if key.Matches(msg, m.keys.PrevPanel) {
		if m.focus.Active == PanelDetail {
			m.focus.Active = m.focus.PrevActive
			return m, nil
		}
		m.focus.Active = prevSidebarPanel(m.focus.Active)
		return m, m.onPanelFocusChanged()
	}
	if key.Matches(msg, m.keys.ToggleLayout) {
		m.focus.ToggleLayoutMode()
		var cmd tea.Cmd
		if m.prefStore != nil {
			cmd = savePreferencesCmd(m.prefStore, m.focus.LayoutMode, m.focus.ScreenMode, currentTheme)
		}
		return m, cmd
	}
	if key.Matches(msg, m.keys.NextScreenMode) {
		m.focus.NextScreenMode()
		var cmd tea.Cmd
		if m.prefStore != nil {
			cmd = savePreferencesCmd(m.prefStore, m.focus.LayoutMode, m.focus.ScreenMode, currentTheme)
		}
		return m, cmd
	}
	if key.Matches(msg, m.keys.JumpPanel) {
		s := msg.String()
		n := int(s[0] - '0')
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
	m.pipelineView.detailTab = (m.pipelineView.detailTab + 1) % pipelineDetailTabCount
	if cmd := m.ensureTestReportLoaded(); cmd != nil {
		return m, cmd
	}
	return m, nil
}

// cycleDetailTabReverse cycles detail pane tabs backward.
func (m Model) cycleDetailTabReverse() (tea.Model, tea.Cmd) {
	ctx := detailContextPanel(&m)
	if ctx == PanelMRs {
		return m.cycleMRDetailTabReverse()
	}
	m.pipelineView.detailTab = (m.pipelineView.detailTab + pipelineDetailTabCount - 1) % pipelineDetailTabCount
	if cmd := m.ensureTestReportLoaded(); cmd != nil {
		return m, cmd
	}
	return m, nil
}

// ensureTestReportLoaded fetches the test report if the Tests tab is active
// and the report hasn't been loaded for the current pipeline yet.
func (m *Model) ensureTestReportLoaded() tea.Cmd {
	if m.pipelineView.detailTab != detailTabTests {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil || m.pipelineView.testReportPipelineID == pipeline.ID {
		return nil
	}
	m.pipelineView.testReportLoading = true
	m.pipelineView.testReport = nil
	m.pipelineView.testReportErr = nil
	return fetchTestReportCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
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
		var cmds []tea.Cmd
		if _, cached := m.mrView.discussions.Get(mr.IID); !cached && !m.mrView.discussions.IsLoading(mr.IID) {
			m.mrView.discussions.SetLoading(mr.IID)
			cmds = append(cmds, fetchMRDiscussionsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mr.IID))
		}
		// Also fetch diffs (needed for inline diff context) if not cached
		if _, cached := m.mrView.diffs.Get(mr.IID); !cached && !m.mrView.diffs.IsLoading(mr.IID) {
			m.mrView.diffs.SetLoading(mr.IID)
			cmds = append(cmds, fetchMRDiffsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mr.IID))
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		if discussions, ok := m.mrView.discussions.Get(mr.IID); ok {
			diffs, _ := m.mrView.diffs.Get(mr.IID)
			m.setMRViewportContent(renderMRCommentsText(discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines))
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
