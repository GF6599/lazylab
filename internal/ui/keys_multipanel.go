// keys_multipanel.go contains the keyboard routing for the multi-panel layout.
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
// Side-loading pattern: when the user navigates to a new project,
// autoLoadSelectedProjectData eagerly fetches pipelines, commits, and MRs
// so that sidebar panels are pre-populated before the user tabs into them.

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
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

// handleProjectsPanelKey handles keys when the Projects panel is focused.
func (m Model) handleProjectsPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
	key := msg.String()

	if m.search.active {
		return m.handleProjectSearchKey(msg)
	}

	switch key {
	case "/":
		m.search.active = true
		m.search.query = ""
		m.search.input.SetValue("")
		m.search.input.Focus()
		return m, textinput.Blink
	case "e":
		// Open explorer overlay
		if project, ok := m.selectedProject(); ok {
			return m.openExplorerOverlay(project)
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
		m.projectTab = (m.projectTab + 1) % 2
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

// autoLoadSelectedProjectData eagerly fetches pipelines, commits, and MRs for
// the currently selected project so sidebar panels are ready before the user
// navigates to them. Called on startup after cache/API load and whenever the
// project selection changes. Uses debouncing for pipeline status to avoid
// hammering the API during rapid j/k navigation.
func (m *Model) autoLoadSelectedProjectData() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		return nil
	}
	cmds := []tea.Cmd{m.loadProjectPipelines(project)}
	now := time.Now()
	m.pipelinePendingFetch = &project
	m.pipelineDebounceTimer = &now
	cmds = append(cmds, pipelineDebounceTickCmd(project.ID, now, pipelineDebounceDelay))
	// Eagerly load commits for selected project
	if _, cached := m.commitCache[project.ID]; !cached && !m.commitLoading[project.ID] {
		m.commitLoading[project.ID] = true
		cmds = append(cmds, fetchCommitsCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, project.DefaultBranch))
	}
	// Eagerly load MRs for selected project
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
	return tea.Batch(cmds...)
}

// openExplorerOverlay opens the explorer as a full-screen overlay.
func (m Model) openExplorerOverlay(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	ref := project.DefaultBranch
	if ref == "" {
		ref = "main"
	}

	delegate := treeEntryDelegate{}
	parentList := newBareList(nil, delegate, 0, 0)
	currentList := newBareList(nil, delegate, 0, 0)

	previewVp := viewport.New(previewContentWidth(m.width), previewContentHeight(m.height))

	m.explorer = explorerState{
		project:     project,
		ref:         ref,
		stack:       []dirState{{path: "", loading: true}},
		parentList:  parentList,
		currentList: currentList,
		preview:     previewState{viewport: previewVp},
	}
	m.status = fmt.Sprintf("Browsing %s", project.PathWithNamespace)
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, ref, "")
}

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
	case "ctrl+d":
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		jobCount := len(m.pipelineView.jobRows)
		step := listPageStep(m.height)
		if m.pipelineView.stageSelected < jobCount-1 {
			m.pipelineView.stageSelected = min(m.pipelineView.stageSelected+step, jobCount-1)
			m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
			m.resetPipelineLogPreview()
			return m, m.queuePipelineLogPreview()
		}
	case "ctrl+u":
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
		if m.pipelineView.stageSelected > 0 {
			step := listPageStep(m.height)
			m.pipelineView.stageSelected = max(m.pipelineView.stageSelected-step, 0)
			m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
			m.resetPipelineLogPreview()
			return m, m.queuePipelineLogPreview()
		}
	case "<", "g":
		m.pipelineView.logViewport.GotoTop()
		m.pipelineView.logAutoFollow = false
		if m.pipelineView.stageSelected != 0 {
			m.pipelineView.stageSelected = 0
			m.pipelineView.stageTable.SetCursor(0)
			m.resetPipelineLogPreview()
			return m, m.queuePipelineLogPreview()
		}
	case ">", "G":
		m.pipelineView.logViewport.GotoBottom()
		m.pipelineView.logAutoFollow = true
		jobCount := len(m.pipelineView.jobRows)
		if jobCount > 0 {
			last := jobCount - 1
			if m.pipelineView.stageSelected != last {
				m.pipelineView.stageSelected = last
				m.pipelineView.stageTable.SetCursor(last)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
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

// handleMRsPanelKey handles keys when the MRs panel is focused.
func (m Model) handleMRsPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "down", "j":
		if m.mrView.selected < len(m.mrView.mrs)-1 {
			m.mrView.selected++
		}
	case "up", "k":
		if m.mrView.selected > 0 {
			m.mrView.selected--
		}
	case "]":
		// Next page
		if m.mrView.nextPage > 0 {
			m.mrView.loading = true
			m.mrView.selected = 0
			return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), m.mrView.nextPage, mrPerPage)
		}
		return m, nil
	case "[":
		// Prev page
		if m.mrView.prevPage > 0 {
			m.mrView.loading = true
			m.mrView.selected = 0
			return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), m.mrView.prevPage, mrPerPage)
		}
		return m, nil
	case "<", "g":
		if len(m.mrView.mrs) > 0 {
			m.mrView.selected = 0
		}
		return m, nil
	case ">", "G":
		if len(m.mrView.mrs) > 0 {
			m.mrView.selected = len(m.mrView.mrs) - 1
		}
		return m, nil
	case "ctrl+d":
		step := listPageStep(m.height)
		if m.mrView.selected < len(m.mrView.mrs)-1 {
			m.mrView.selected = min(m.mrView.selected+step, len(m.mrView.mrs)-1)
		}
		return m, nil
	case "ctrl+u":
		step := listPageStep(m.height)
		if m.mrView.selected > 0 {
			m.mrView.selected = max(m.mrView.selected-step, 0)
		}
		return m, nil
	case "enter", "l", "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelMRs
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "left":
		// Back to Stages
		m.focus.Active = PanelStages
		return m, nil
	case "esc":
		m.focus.Active = PanelProjects
		return m, nil
	case "J":
		m.mrView.mrViewport.HalfPageDown()
		return m, nil
	case "K":
		m.mrView.mrViewport.HalfPageUp()
		return m, nil
	case "t":
		// Cycle MR sidebar tabs (Open → Merged → Closed)
		m.mrView.tab = (m.mrView.tab + 1) % 3
		m.mrView.loading = true
		m.mrView.mrs = nil
		m.mrView.selected = 0
		m.mrView.detailTab = mrDetailTabInfo
		return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), 1, mrPerPage)
	case "T":
		// Cycle MR sidebar tabs backward (Closed → Merged → Open)
		m.mrView.tab = (m.mrView.tab + 2) % 3
		m.mrView.loading = true
		m.mrView.mrs = nil
		m.mrView.selected = 0
		m.mrView.detailTab = mrDetailTabInfo
		return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), 1, mrPerPage)
	case "ctrl+o":
		m.copyMRURL()
	}
	return m, nil
}

// handleDetailPanelKey handles keys when the Detail pane is focused. It uses
// focus.PrevActive to determine context: scrolling targets either the pipeline
// log viewport or the MR viewport depending on which sidebar panel the user
// came from.
func (m Model) handleDetailPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	isMR := m.focus.PrevActive == PanelMRs
	isMRComments := isMR && m.mrView.detailTab == mrDetailTabComments

	// MR comments tab: j/k navigates discussions, r resolves, enter replies
	if isMRComments {
		switch key {
		case "left", "h":
			m.focus.Active = m.focus.PrevActive
			return m, nil
		case "esc":
			m.focus.Active = PanelProjects
			return m, nil
		case "down", "j":
			return m.moveDiscussionSelection(1)
		case "up", "k":
			return m.moveDiscussionSelection(-1)
		case "J", "ctrl+d":
			m.mrView.mrViewport.HalfPageDown()
			return m, nil
		case "K", "ctrl+u":
			m.mrView.mrViewport.HalfPageUp()
			return m, nil
		case "<", "g":
			m.mrView.selectedDiscussion = 0
			return m.refreshMRCommentsViewport()
		case ">", "G":
			return m.moveDiscussionToEnd()
		case "r":
			return m.toggleDiscussionResolved()
		case "enter":
			return m.openMRReplyModal()
		case "t":
			return m.cycleDetailTab()
		case "T":
			return m.cycleDetailTabReverse()
		case "ctrl+o":
			m.copyMRURL()
			return m, nil
		}
		return m, nil
	}

	switch key {
	case "left", "h":
		m.focus.Active = m.focus.PrevActive
		return m, nil
	case "esc":
		// Jump to Projects (home)
		m.focus.Active = PanelProjects
		return m, nil
	case "down", "j":
		if isMR {
			m.mrView.mrViewport.ScrollDown(1)
		} else {
			m.pipelineView.logViewport.ScrollDown(1)
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		}
		return m, nil
	case "up", "k":
		if isMR {
			m.mrView.mrViewport.ScrollUp(1)
		} else {
			m.pipelineView.logViewport.ScrollUp(1)
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case "J", "ctrl+d":
		if isMR {
			m.mrView.mrViewport.HalfPageDown()
		} else {
			m.pipelineView.logViewport.HalfPageDown()
			m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		}
		return m, nil
	case "K", "ctrl+u":
		if isMR {
			m.mrView.mrViewport.HalfPageUp()
		} else {
			m.pipelineView.logViewport.HalfPageUp()
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case "<", "g":
		if isMR {
			m.mrView.mrViewport.GotoTop()
		} else {
			m.pipelineView.logViewport.GotoTop()
			m.pipelineView.logAutoFollow = false
		}
		return m, nil
	case ">", "G":
		if isMR {
			m.mrView.mrViewport.GotoBottom()
		} else {
			m.pipelineView.logViewport.GotoBottom()
			m.pipelineView.logAutoFollow = true
		}
		return m, nil
	case "t":
		return m.cycleDetailTab()
	case "T":
		return m.cycleDetailTabReverse()
	case "R":
		switch m.focus.PrevActive {
		case PanelPipelines:
			return m.openRetryModal()
		case PanelStages:
			return m.openRetryModalForJob()
		}
	case "ctrl+o":
		switch m.focus.PrevActive {
		case PanelProjects:
			m.copyCloneCommand()
		case PanelPipelines:
			m.copyPipelineURL()
		case PanelStages:
			m.copyJobURL()
		case PanelMRs:
			m.copyMRURL()
		}
	}
	return m, nil
}

// moveDiscussionSelection moves the selected discussion index by delta,
// clamping to bounds, and re-renders the comments viewport.
func (m Model) moveDiscussionSelection(delta int) (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 {
		return m, nil
	}
	newIdx := m.mrView.selectedDiscussion + delta
	if newIdx < 0 {
		newIdx = 0
	}
	if newIdx >= len(filtered) {
		newIdx = len(filtered) - 1
	}
	if newIdx == m.mrView.selectedDiscussion {
		return m, nil
	}
	m.mrView.selectedDiscussion = newIdx
	return m.refreshMRCommentsViewport()
}

// moveDiscussionToEnd moves the selected discussion to the last one.
func (m Model) moveDiscussionToEnd() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 {
		return m, nil
	}
	m.mrView.selectedDiscussion = len(filtered) - 1
	return m.refreshMRCommentsViewport()
}

// refreshMRCommentsViewport re-renders the comments text with current selection
// and scrolls the viewport so the selected discussion is visible.
func (m Model) refreshMRCommentsViewport() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	w := m.mrViewportWidth()
	content := renderMRCommentsText(discussions, w, m.mrView.selectedDiscussion)
	m.setMRViewportContent(content)

	// Scroll so the selected discussion is visible
	startLine := discussionStartLine(discussions, w, m.mrView.selectedDiscussion)
	vpHeight := m.mrView.mrViewport.Height
	yOffset := m.mrView.mrViewport.YOffset
	if startLine < yOffset {
		m.mrView.mrViewport.SetYOffset(startLine)
	} else if startLine >= yOffset+vpHeight {
		m.mrView.mrViewport.SetYOffset(startLine - vpHeight/2)
	}
	return m, nil
}

// toggleDiscussionResolved toggles resolve/unresolve on the selected discussion.
func (m Model) toggleDiscussionResolved() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if m.mrView.selectedDiscussion >= len(filtered) {
		return m, nil
	}
	disc := filtered[m.mrView.selectedDiscussion]
	// Check if resolvable (first note's Resolvable flag)
	if len(disc.Notes) == 0 || !disc.Notes[0].Resolvable {
		m.status = "Discussion is not resolvable"
		return m, nil
	}
	currentResolved := disc.Notes[0].Resolved
	newResolved := !currentResolved

	// Optimistic update: toggle resolved state in cache
	m.optimisticToggleResolved(mr.IID, disc.ID, newResolved)

	// Re-render with updated state
	if updatedDiscs, ok2 := m.mrView.discussions.Get(mr.IID); ok2 {
		content := renderMRCommentsText(updatedDiscs, m.mrViewportWidth(), m.mrView.selectedDiscussion)
		m.setMRViewportContent(content)
	}

	if newResolved {
		m.status = "Resolving discussion..."
	} else {
		m.status = "Unresolving discussion..."
	}
	return m, resolveMRDiscussionCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mr.IID, disc.ID, newResolved)
}

// optimisticToggleResolved updates the cached discussion's Resolved flags in-place.
func (m *Model) optimisticToggleResolved(mrIID int, discussionID string, resolved bool) {
	discussions, ok := m.mrView.discussions.Get(mrIID)
	if !ok {
		return
	}
	for i := range discussions {
		if discussions[i].ID == discussionID {
			for j := range discussions[i].Notes {
				if discussions[i].Notes[j].Resolvable {
					discussions[i].Notes[j].Resolved = resolved
				}
			}
			break
		}
	}
	m.mrView.discussions.Set(mrIID, discussions)
}

// openMRReplyModal opens the reply textarea modal for the selected discussion.
func (m Model) openMRReplyModal() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if m.mrView.selectedDiscussion >= len(filtered) {
		return m, nil
	}
	disc := filtered[m.mrView.selectedDiscussion]

	ta := textarea.New()
	ta.Placeholder = "Type your reply..."
	ta.SetWidth(50)
	ta.SetHeight(5)
	ta.Focus()

	m.mrView.reply = mrReplyState{
		active:       true,
		discussionID: disc.ID,
		projectID:    m.mrView.project.ID,
		mrIID:        mr.IID,
		input:        ta,
	}
	return m, textarea.Blink
}

// handleMRReplyKey handles keys when the MR reply modal is active.
func (m Model) handleMRReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mrView.reply = mrReplyState{}
		return m, nil
	case "ctrl+s":
		body := strings.TrimSpace(m.mrView.reply.input.Value())
		if body == "" {
			m.mrView.reply.err = fmt.Errorf("reply cannot be empty")
			return m, nil
		}
		m.mrView.reply.sending = true
		m.mrView.reply.err = nil
		m.status = "Sending reply..."
		return m, replyMRDiscussionCmd(
			m.ctx, m.client, m.opts.APITimeout,
			m.mrView.reply.projectID,
			m.mrView.reply.mrIID,
			m.mrView.reply.discussionID,
			body,
		)
	default:
		var cmd tea.Cmd
		m.mrView.reply.input, cmd = m.mrView.reply.input.Update(msg)
		return m, cmd
	}
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
			m.setMRViewportContent(renderMRDiffText(diffs, m.mrViewportWidth()))
			m.mrView.mrViewport.GotoTop()
		}
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
	m.pipelineView.confirmRetry = true
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryID = pipeline.ID
	m.pipelineView.confirmRetryRef = pipeline.Ref
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
	m.pipelineView.confirmRetry = true
	m.pipelineView.confirmRetryIsJob = true
	m.pipelineView.confirmRetryID = pipeline.ID
	m.pipelineView.confirmRetryRef = ""
	m.pipelineView.confirmRetryJobID = job.ID
	m.pipelineView.confirmRetryJobName = job.Name
	m.pipelineView.confirmRetryJobStage = job.Stage
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridgeChild && row.ChildProjectID != 0 {
		m.pipelineView.confirmRetryProjectID = row.ChildProjectID
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
