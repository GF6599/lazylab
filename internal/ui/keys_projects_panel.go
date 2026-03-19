// keys_projects_panel.go contains key handling for the Projects panel
// in the multi-panel layout.

package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleProjectsPanelKey handles keys when the Projects panel is focused.
func (m Model) handleProjectsPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
	key := msg.String()

	if m.search.active {
		return m.handleProjectSearchKey(msg)
	}

	switch key {
	case "/":
		return m.startSearch()
	case "e":
		// Open explorer overlay
		if project, ok := m.selectedProject(); ok {
			return m.openExplorer(project)
		}
	case "enter":
		// Drill into Pipelines panel for selected project
		if project, ok := m.selectedProject(); ok {
			m.focus.Active = PanelPipelines
			return m, m.loadProjectPipelines(project)
		}
	case "l", "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelProjects
		m.focus.Active = PanelDetail
		return m, nil
	case "down", "j", "up", "k":
		m.projectList, _ = m.projectList.Update(msg)
		m.selected = m.projectList.Index()
	case "ctrl+d":
		visible := m.visibleProjects()
		if len(visible) > 0 {
			step := listPageStep(m.height)
			newIdx := min(m.projectList.Index()+step, len(visible)-1)
			m.projectList.Select(newIdx)
			m.selected = newIdx
		}
	case "ctrl+u":
		visible := m.visibleProjects()
		if len(visible) > 0 {
			step := listPageStep(m.height)
			newIdx := max(m.projectList.Index()-step, 0)
			m.projectList.Select(newIdx)
			m.selected = newIdx
		}
	case "<", "g":
		if len(m.visibleProjects()) > 0 {
			m.projectList.Select(0)
			m.selected = 0
		}
	case ">", "G":
		visible := m.visibleProjects()
		if len(visible) > 0 {
			m.projectList.Select(len(visible) - 1)
			m.selected = len(visible) - 1
		}
	case "h", "left":
		// Projects is the topmost panel — no-op (nothing to go back to)
		return m, nil
	case "[":
		pageCmd := m.movePage(-1)
		prefetchCmd := (&m).queueBatchPrefetchPipelineStatus()
		return m, tea.Batch(pageCmd, prefetchCmd)
	case "]":
		pageCmd := m.movePage(1)
		prefetchCmd := (&m).queueBatchPrefetchPipelineStatus()
		return m, tea.Batch(pageCmd, prefetchCmd)
	case "r", "ctrl+r":
		m.loading = true
		m.err = nil
		m.status = "Refreshing projects..."
		m.backgroundLoading = false
		m.page = 1
		m.paginator.Page = 0
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	case "f":
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
			m.projectList.SetDelegate(projectDelegate{
				pipelineStatus: m.pipelineStatus,
				favorites:      m.favorites,
			})
			m.invalidateVisibleCache()
			m.updateProjectList()
			m.ensureSelectionBounds()
			var saveCmd tea.Cmd
			if m.favStore != nil {
				saveCmd = saveFavoritesCmd(m.favStore, m.favOrder)
			}
			return m, saveCmd
		}
	case "t":
		m.projectTab = (m.projectTab + 1) % projectTabCount
		m.selected = 0
		m.invalidateVisibleCache()
		m.updateProjectList()
		m.ensureSelectionBounds()
		if m.selected >= 0 {
			m.projectList.Select(m.selected)
		}
		(&m).invalidateDetailCache()
		var cmds []tea.Cmd
		if cmd := (&m).queueBatchPrefetchPipelineStatus(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := (&m).autoLoadSelectedProjectData(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil
	case "{":
		if m.projectTab == projectTabFavorites {
			return m.moveFavorite(-1)
		}
	case "}":
		if m.projectTab == projectTabFavorites {
			return m.moveFavorite(1)
		}
	case "ctrl+o":
		m.copyCloneCommand()
	}

	// Auto-load sidebar panels when selection changes
	currID, currOK := m.currentSelectedProjectID()
	if prevID != currID || prevOK != currOK {
		(&m).invalidateDetailCache()
		if cmd := (&m).autoLoadSelectedProjectData(); cmd != nil {
			return m, cmd
		}
	}
	return m, nil
}

// loadProjectPipelines loads pipeline data for a project into the pipeline view.
func (m *Model) loadProjectPipelines(project gitlab.ProjectNode) tea.Cmd {
	if m.pipelineView.project.ID == project.ID && len(m.pipelineView.pipelines) > 0 {
		return nil // Already loaded
	}

	// Initialize pipeline view state
	m.pipelineView.project = project
	m.pipelineView.loading = true
	m.pipelineView.err = nil
	m.pipelineView.pipelines = nil
	m.pipelineView.selected = 0
	m.pipelineView.page = 1
	m.pipelineView.totalPages = 1
	m.pipelineView.perPage = pipelinePerPage
	m.pipelineView.stages = NewAsyncCache[int, []gitlab.PipelineStage]()
	m.pipelineView.jobs = NewAsyncCache[int, []gitlab.PipelineJob]()
	m.pipelineView.logs = NewAsyncCache[int, string]()
	m.pipelineView.logAutoFollow = true
	m.pipelineView.focus = pipelineFocusPipelines
	m.pipelineView.bridges = NewAsyncCache[int, []gitlab.PipelineBridge]()
	m.pipelineView.childJobs = NewAsyncCache[int, []gitlab.PipelineJob]()
	m.pipelineView.testReport = nil
	m.pipelineView.testReportLoading = false
	m.pipelineView.testReportErr = nil
	m.pipelineView.testReportPipelineID = 0
	m.pipelineView.detailTab = detailTabLog

	// Initialize bubbles list for pipeline display
	delegate := pipelineDelegate{}
	m.pipelineView.pipelineList = newBareList(nil, delegate, 0, 0)
	m.pipelineView.pipelineList.Styles.Title = titleStyle

	// Initialize stage table (job-per-row layout)
	stageWidth := 56
	if layout := computeLayout(m.width, m.height, m.focus); layout.OK {
		stageWidth = layout.SidebarWidth
	}
	columns := stageTableColumns(stageWidth)
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(false),
		table.WithHeight(stageTableDefaultHeight),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(rosePineSubtle).
		BorderBottom(true).
		BorderTop(false).
		BorderLeft(false).
		BorderRight(false).
		Bold(false).
		Foreground(rosePineSubtle)
	s.Selected = s.Selected.
		Foreground(rosePineText).
		Background(rosePineHighlightMed).
		Bold(false)
	s.Cell = s.Cell.
		Foreground(rosePineText)
	t.SetStyles(s)
	m.pipelineView.stageTable = t

	// Initialize log viewport with layout-computed dimensions
	vpWidth := pipelineLogContentWidth(m.width)
	vpHeight := pipelineLogContentHeight(m.height)
	if m.mode == modeMultiPanel {
		layout := computeLayout(m.width, m.height, m.focus)
		if layout.OK {
			vpWidth = layout.DetailWidth
			vpHeight = layout.DetailHeight
		}
	}
	m.pipelineView.logViewport = viewport.New(vpWidth, vpHeight)

	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, 1, pipelinePerPage)
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
		return nil
	}
	// Skip debounce on first load for instant startup feedback
	if m.pipelineView.project.ID == 0 {
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
	if _, cached := m.commitCache[project.ID]; !cached && !m.commitLoading[project.ID] {
		m.commitLoading[project.ID] = true
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
		cmds = append(cmds, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, mrTabStateString(m.mrView.tab), 1, mrPerPage))
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
