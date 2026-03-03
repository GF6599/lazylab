package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
)

// handleMultiPanelKey is the key router for the multi-panel layout.
// It handles global keys (Tab, 1-5, +, ?) then delegates to the focused panel.
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
	case "tab":
		m.focus.Active = nextSidebarPanel(m.focus.Active)
		return m, m.onPanelFocusChanged()
	case "shift+tab":
		m.focus.Active = prevSidebarPanel(m.focus.Active)
		return m, m.onPanelFocusChanged()
	case "+":
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

// onPanelFocusChanged triggers lazy loading when a panel gains focus.
func (m *Model) onPanelFocusChanged() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		return nil
	}

	switch m.focus.Active {
	case PanelMRs:
		if m.mrView.project.ID != project.ID {
			m.mrView = mrViewState{
				project: project,
				loading: true,
			}
			return fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, mrTabStateString(m.mrView.tab), 1, 25)
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
	case "enter", "l":
		// Drill into Pipelines panel for selected project
		if project, ok := m.selectedProject(); ok {
			m.focus.Active = PanelPipelines
			return m, m.loadProjectPipelines(project)
		}
	case "right":
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
		m.movePage(-1)
		return m, (&m).queueBatchPrefetchPipelineStatus()
	case "[":
		m.movePage(-1)
		return m, (&m).queueBatchPrefetchPipelineStatus()
	case "]":
		m.movePage(1)
		return m, (&m).queueBatchPrefetchPipelineStatus()
	case "r", "ctrl+r":
		m.loading = true
		m.err = nil
		m.status = "Refreshing projects..."
		m.backgroundLoading = false
		m.page = 1
		m.paginator.Page = 0
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	case "ctrl+o":
		m.copyCloneCommand()
	}

	// Auto-load sidebar panels when selection changes
	currID, currOK := m.currentSelectedProjectID()
	if prevID != currID || prevOK != currOK {
		(&m).invalidateDetailCache()
		currProject, ok := m.selectedProject()
		if ok {
			cmds := []tea.Cmd{m.loadProjectPipelines(currProject)}
			now := time.Now()
			m.pipelinePendingFetch = &currProject
			m.pipelineDebounceTimer = &now
			cmds = append(cmds, pipelineDebounceTickCmd(currProject.ID, now, pipelineDebounceDelay))
			// Eagerly load commits for selected project
			if _, cached := m.commitCache[currProject.ID]; !cached && !m.commitLoading[currProject.ID] {
				m.commitLoading[currProject.ID] = true
				cmds = append(cmds, fetchCommitsCmd(m.ctx, m.client, m.opts.APITimeout, currProject.ID, currProject.DefaultBranch))
			}
			// Eagerly load MRs for selected project
			m.mrView = mrViewState{
				project: currProject,
				loading: true,
			}
			cmds = append(cmds, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, currProject.ID, mrTabStateString(m.mrView.tab), 1, 25))
			return m, tea.Batch(cmds...)
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
		Foreground(rosePineBase).
		Background(rosePineRose).
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
	case "enter", "l":
		// Focus stages panel
		m.focus.Active = PanelStages
		return m, m.queuePipelineLogPreview()
	case "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelPipelines
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "esc":
		// Back to projects
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
		m.pipelineView.logViewport.HalfViewDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K":
		m.pipelineView.logViewport.HalfViewUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "t":
		return m.cycleDetailTab()
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
	case "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelStages
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "esc":
		m.focus.Active = PanelPipelines
		return m, nil
	case "J":
		m.pipelineView.logViewport.HalfViewDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K":
		m.pipelineView.logViewport.HalfViewUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "ctrl+d":
		m.pipelineView.logViewport.HalfViewDown()
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
		m.pipelineView.logViewport.HalfViewUp()
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
		// Next tab
		m.mrView.tab = (m.mrView.tab + 1) % 3
		m.mrView.loading = true
		m.mrView.mrs = nil
		m.mrView.selected = 0
		return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), 1, 25)
	case "[":
		// Prev tab
		m.mrView.tab = (m.mrView.tab + 2) % 3
		m.mrView.loading = true
		m.mrView.mrs = nil
		m.mrView.selected = 0
		return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), 1, 25)
	case "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelMRs
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "esc":
		m.focus.Active = PanelProjects
		return m, nil
	case "ctrl+o":
		m.copyMRURL()
	}
	return m, nil
}

// handleDetailPanelKey handles keys when the Detail pane is focused.
func (m Model) handleDetailPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "left", "h", "esc":
		// Return to the sidebar panel we came from
		m.focus.Active = m.focus.PrevActive
		return m, nil
	case "down", "j":
		m.pipelineView.logViewport.LineDown(1)
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "up", "k":
		m.pipelineView.logViewport.LineUp(1)
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "J", "ctrl+d":
		m.pipelineView.logViewport.HalfViewDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K", "ctrl+u":
		m.pipelineView.logViewport.HalfViewUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "<", "g":
		m.pipelineView.logViewport.GotoTop()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case ">", "G":
		m.pipelineView.logViewport.GotoBottom()
		m.pipelineView.logAutoFollow = true
		return m, nil
	case "t":
		return m.cycleDetailTab()
	case "R":
		// Retry based on the sidebar panel we came from
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

// cycleDetailTab cycles through detail pane tabs (Log → Tests → Artifacts).
func (m Model) cycleDetailTab() (tea.Model, tea.Cmd) {
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
