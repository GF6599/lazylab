// keys_projects_panel.go contains key handling for the Projects panel
// in the multi-panel layout.

package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleProjectsPanelKey handles keys when the Projects panel is focused.
func (m Model) handleProjectsPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()

	if m.search.active {
		return m.handleProjectSearchKey(msg)
	}

	switch {
	case key.Matches(msg, m.keys.Search):
		return m.startSearch()
	case key.Matches(msg, m.keys.Explorer):
		// Open explorer overlay
		if project, ok := m.selectedProject(); ok {
			return m.openExplorer(project)
		}
	case key.Matches(msg, m.keys.Enter):
		// Drill into Pipelines panel for selected project
		if project, ok := m.selectedProject(); ok {
			m.focus.Active = PanelPipelines
			return m, m.loadProjectPipelines(project)
		}
	case key.Matches(msg, m.keys.Right):
		// Focus Detail pane
		m.focus.PrevActive = PanelProjects
		m.focus.Active = PanelDetail
		return m, nil
	case key.Matches(msg, m.keys.Down) || key.Matches(msg, m.keys.Up):
		m.projectList, _ = m.projectList.Update(msg)
		m.selected = m.projectList.Index()
	case key.Matches(msg, m.keys.HalfDown) || key.Matches(msg, m.keys.HalfUp) ||
		key.Matches(msg, m.keys.Top) || key.Matches(msg, m.keys.Bottom):
		if newIdx, handled := bigStepIdx(msg.String(), m.projectList.Index(), len(m.visibleProjects()), m.height); handled {
			m.projectList.Select(newIdx)
			m.selected = newIdx
		}
	case key.Matches(msg, m.keys.Left):
		// Projects is the topmost panel — no-op (nothing to go back to)
		return m, nil
	case key.Matches(msg, m.keys.PrevPage):
		pageCmd := m.movePage(-1)
		prefetchCmd := (&m).queueBatchPrefetchPipelineStatus()
		selectionCmd := (&m).handleSelectedProjectChange(prevID, prevOK)
		return m, tea.Batch(pageCmd, prefetchCmd, selectionCmd)
	case key.Matches(msg, m.keys.NextPage):
		pageCmd := m.movePage(1)
		prefetchCmd := (&m).queueBatchPrefetchPipelineStatus()
		selectionCmd := (&m).handleSelectedProjectChange(prevID, prevOK)
		return m, tea.Batch(pageCmd, prefetchCmd, selectionCmd)
	case key.Matches(msg, m.keys.Refresh):
		m.loading = true
		m.err = nil
		m.status = "Refreshing projects..."
		m.backgroundLoading = false
		m.page = 1
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	case key.Matches(msg, m.keys.Favorite):
		project, ok := m.selectedProject()
		if ok {
			if m.favorites[project.ID] {
				delete(m.favorites, project.ID)
				// Remove from favOrder
				for i, id := range m.favOrder {
					if id == project.ID {
						m.favOrder = append(m.favOrder[:i], m.favOrder[i+1:]...)
						break
					}
				}
				m.status = fmt.Sprintf("Unfavorited %s", project.PathWithNamespace)
			} else {
				m.favorites[project.ID] = true
				m.favOrder = append(m.favOrder, project.ID)
				m.status = fmt.Sprintf("Favorited %s", project.PathWithNamespace)
			}
			m.projectList.SetDelegate((&m).rowDelegate())
			m.invalidateVisibleCache()
			m.ensureSelectionBounds()
			m.updateProjectList()
			var cmds []tea.Cmd
			if m.favStore != nil {
				cmds = append(cmds, saveFavoritesCmd(m.favStore, m.favOrder))
			}
			if cmd := (&m).handleSelectedProjectChange(prevID, prevOK); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if len(cmds) == 0 {
				return m, nil
			}
			return m, tea.Batch(cmds...)
		}
	case key.Matches(msg, m.keys.CycleTab):
		m.projectTab = (m.projectTab + 1) % projectTabCount
		m.selected = 0
		m.invalidateVisibleCache()
		m.ensureSelectionBounds()
		m.updateProjectList()
		var cmds []tea.Cmd
		if cmd := (&m).queueBatchPrefetchPipelineStatus(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := (&m).handleSelectedProjectChange(prevID, prevOK); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil
	case key.Matches(msg, m.keys.MoveFavUp):
		if m.projectTab == projectTabFavorites {
			return m.moveFavorite(-1)
		}
	case key.Matches(msg, m.keys.MoveFavDn):
		if m.projectTab == projectTabFavorites {
			return m.moveFavorite(1)
		}
	case key.Matches(msg, m.keys.Copy):
		clipCmd := m.copyCloneCommand()
		if loadCmd := (&m).handleSelectedProjectChange(prevID, prevOK); loadCmd != nil {
			return m, tea.Batch(clipCmd, loadCmd)
		}
		return m, clipCmd
	}

	// Auto-load sidebar panels when selection changes
	if cmd := (&m).handleSelectedProjectChange(prevID, prevOK); cmd != nil {
		return m, cmd
	}
	return m, nil
}

// loadProjectPipelines loads pipeline data for a project into the pipeline view.
func (m *Model) loadProjectPipelines(project gitlab.ProjectNode) tea.Cmd {
	if m.pipelineView.project.ID == project.ID && len(m.pipelineView.pipelines) > 0 {
		return nil // Already loaded
	}

	m.pipelineView.initForProject(project)
	m.pipelineView.pipelineList = newPipelineListModel(m.statusFrames())
	m.pipelineView.stageTable = newStageTable(m.stageTableWidth())
	m.pipelineView.logViewport = m.newLogViewport()
	// The sub-components above are freshly built at zero size; re-apply the
	// current layout so the new pipeline list (and table/viewport) get real
	// dimensions. Without this the list renders empty until the next resize,
	// because the multi-panel pane sizes the list from state, not in View.
	m.updateViewportSizes()

	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, 1, m.startPipelinePage())
}

// newPipelineListModel returns a freshly-initialized bubbles list for the
// pipeline column. Dimensions are zero — the layout pass sets the real size
// before the first render.
func newPipelineListModel(frames statusFrames) list.Model {
	pl := newBareList(nil, pipelineDelegate{frames: frames}, 0, 0)
	pl.Styles.Title = titleStyle
	return pl
}

// stageTableWidth returns the width to use when building the stage table:
// the sidebar width when a multi-panel layout fits, or a sensible default
// otherwise.
func (m *Model) stageTableWidth() int {
	if layout := computeLayout(m.width, m.height, m.focus); layout.OK {
		return layout.SidebarWidth
	}
	return 56
}

// newStageTable builds a job-per-row stage table styled to match the active
// theme. This is the single source of truth for stage-table construction —
// both the initial open and the theme-refresh path style through stageTableStyles.
func newStageTable(width int) table.Model {
	t := table.New(
		table.WithColumns(stageTableColumns(width)),
		table.WithFocused(false),
		table.WithHeight(stageTableDefaultHeight),
	)
	t.SetStyles(stageTableStyles())
	return t
}

// stageTableStyles returns the themed table.Styles used by every stage-table
// callsite. Selected takes the shared marked-row style, which both replaces the
// default Color("212") (bright pink) foreground and keeps the current row the
// same colour here as in every other list.
func stageTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(frameBorder).
		BorderForeground(colorSubtle).
		BorderBottom(true).
		Bold(false).
		Foreground(colorSubtle)
	s.Selected = selectedItemStyle
	s.Cell = s.Cell.
		Foreground(colorText)
	return s
}

// newLogViewport sizes the pipeline-log viewport from the current layout.
// In multi-panel mode it uses the detail-pane size; otherwise it falls back
// to the full-screen pipeline-log dimensions.
func (m *Model) newLogViewport() viewport.Model {
	w := pipelineLogContentWidth(m.width)
	h := pipelineLogContentHeight(m.height)
	if m.mode == modeMultiPanel {
		if layout := computeLayout(m.width, m.height, m.focus); layout.OK {
			w = layout.DetailWidth
			h = layout.DetailHeight
		}
	}
	return viewport.New(w, h)
}

// autoLoadSelectedProjectData debounces eager data loading (pipelines, commits,
// MRs) for the selected project. On rapid j/k navigation, only the final
// selection triggers API calls after pipelineDebounceDelay (300ms).
//
// When no project data has been loaded yet (first selection after startup),
// the debounce is skipped for instant feedback.
func (m *Model) autoLoadSelectedProjectData() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.clearSelectionDebounce()
		return nil
	}
	// Skip debounce on first load for instant startup feedback
	if m.pipelineView.project.ID == 0 {
		m.clearSelectionDebounce()
		return m.loadSelectedProjectData(project)
	}
	now := time.Now()
	m.selectionPending = &project
	m.selectionDebounce = &now
	return selectionDebounceTickCmd(project.ID, now)
}

// loadSelectedProjectData performs the actual data loading for a project.
// Called either directly (first load) or after the debounce timer fires.
func (m *Model) loadSelectedProjectData(project gitlab.ProjectNode) tea.Cmd {
	var cmds []tea.Cmd
	if cmd := m.loadProjectPipelines(project); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Pipeline status badge (uses its own lastFetched guard)
	if cmd := m.queuePipelineFetch(project, false); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if _, cached := m.commitCache.Get(project.ID); !cached && !m.commitCache.IsLoading(project.ID) {
		m.commitCache.SetLoading(project.ID)
		cmds = append(cmds, fetchCommitsCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, project.DefaultBranch))
	}
	// Only reload MRs if the project changed
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
		cmds = append(cmds, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, mrTabStateString(m.mrView.tab), 1, m.startMRPage()))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// moveFavorite swaps the currently selected favorite with an adjacent entry
// in favOrder. delta is -1 (move up) or +1 (move down). The selection follows
// the moved item so the user can keep pressing the key to "drag" it.
func (m Model) moveFavorite(delta int) (tea.Model, tea.Cmd) {
	visible := m.visibleProjects()
	if len(visible) == 0 {
		return m, nil
	}
	sel := m.projectList.Index()
	if sel < 0 || sel >= len(visible) {
		return m, nil
	}
	projectID := visible[sel].ID

	// Find position in favOrder
	idx := -1
	for i, id := range m.favOrder {
		if id == projectID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return m, nil
	}

	newIdx := idx + delta
	if newIdx < 0 || newIdx >= len(m.favOrder) {
		return m, nil
	}

	// Swap in favOrder
	m.favOrder[idx], m.favOrder[newIdx] = m.favOrder[newIdx], m.favOrder[idx]

	// Invalidate cache and rebuild list so the new order is visible
	m.invalidateVisibleCache()
	m.updateProjectList()

	// Follow the moved item
	newSel := sel + delta
	if newSel >= 0 && newSel < len(m.visibleProjects()) {
		m.projectList.Select(newSel)
		m.selected = newSel
	}

	var saveCmd tea.Cmd
	if m.favStore != nil {
		saveCmd = saveFavoritesCmd(m.favStore, m.favOrder)
	}
	return m, saveCmd
}
