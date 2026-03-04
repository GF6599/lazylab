// Key handlers for each UI mode.
//
// Each handler returns (tea.Model, tea.Cmd) per Bubble Tea convention. State
// mutations happen on the value-receiver copy of Model, and any async work
// (API calls, debounce ticks) is returned as a tea.Cmd for the runtime to
// execute. Handlers are kept mode-specific so the top-level Update can
// dispatch by mode without growing unbounded.

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// handleProjectSearchKey handles key events while the search input is focused.
// Esc clears the search and restores the full project list; Enter commits the
// query. All other keys are forwarded to the textinput component, with a
// debounce timer that triggers incremental filtering after a short pause.
func (m Model) handleProjectSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg.Type {
	case tea.KeyEsc:
		m.search.active = false
		m.search.query = ""
		m.search.pendingQuery = ""
		m.search.debounceTimer = nil
		m.search.input.Reset()
		m.search.input.Blur()
		m.invalidateVisibleCache()
		m.ensureSelectionBounds()
		m.updateProjectList()
		m.status = "Search cleared"
		return m, (&m).queueBatchPrefetchPipelineStatus()
	case tea.KeyEnter:
		m.search.active = false
		m.search.query = m.search.input.Value()
		m.search.pendingQuery = ""
		m.search.debounceTimer = nil
		m.search.input.Blur()
		m.status = fmt.Sprintf("Search: %s", m.search.query)
		m.invalidateVisibleCache()
		m.ensureSelectionBounds()
		m.updateProjectList()
		return m, (&m).queueBatchPrefetchPipelineStatus()
	case tea.KeyCtrlC:
		return m, tea.Quit
	default:
		m.search.input, cmd = m.search.input.Update(msg)
		inputValue := m.search.input.Value()
		now := time.Now()
		m.search.pendingQuery = inputValue
		m.search.debounceTimer = &now
		debounceCmd := searchDebounceTickCmd(inputValue, now)
		if cmd != nil {
			return m, tea.Batch(cmd, debounceCmd)
		}
		return m, debounceCmd
	}
}

// handleProjectKey handles navigation and actions in the project list mode.
// Selection changes trigger a debounced pipeline status fetch so the detail
// pane stays current without overwhelming the API during fast scrolling.
// Page changes (h/l) batch a page-load command with pipeline prefetch.
func (m Model) handleProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
	key := msg.String()
	if m.search.active {
		return m.handleProjectSearchKey(msg)
	}

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "/":
		m.search.active = true
		m.search.query = ""
		m.search.input.SetValue("")
		m.search.input.Focus()
		return m, textinput.Blink
	case "enter":
		if project, ok := m.selectedProject(); ok {
			return m.openProjectActions(project)
		}
	case "down", "j", "up", "k":
		// Let Bubbles list handle navigation
		var cmd tea.Cmd
		m.projectList, cmd = m.projectList.Update(msg)
		m.selected = m.projectList.Index()
		return m, cmd
	case "ctrl+d":
		// Page down - move by half screen
		visible := m.visibleProjects()
		if len(visible) > 0 {
			step := listPageStep(m.height)
			newIdx := min(m.projectList.Index()+step, len(visible)-1)
			m.projectList.Select(newIdx)
			m.selected = newIdx
		}
	case "ctrl+u":
		// Page up - move by half screen
		visible := m.visibleProjects()
		if len(visible) > 0 {
			step := listPageStep(m.height)
			newIdx := max(m.projectList.Index()-step, 0)
			m.projectList.Select(newIdx)
			m.selected = newIdx
		}
	case "<", "g":
		// Go to start
		if len(m.visibleProjects()) > 0 {
			m.projectList.Select(0)
			m.selected = 0
		}
	case ">", "G":
		// Go to end
		visible := m.visibleProjects()
		if len(visible) > 0 {
			m.projectList.Select(len(visible) - 1)
			m.selected = len(visible) - 1
		}
	case "l", "right":
		pageCmd := m.movePage(1)
		prefetchCmd := (&m).queueBatchPrefetchPipelineStatus()
		return m, tea.Batch(pageCmd, prefetchCmd)
	case "h", "left":
		pageCmd := m.movePage(-1)
		prefetchCmd := (&m).queueBatchPrefetchPipelineStatus()
		return m, tea.Batch(pageCmd, prefetchCmd)
	case "r", "ctrl+r":
		m.loading = true
		m.err = nil
		m.status = "Refreshing projects..."
		m.backgroundLoading = false
		m.page = 1
		m.paginator.Page = 0 // Reset to first page (0-indexed)
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	case "ctrl+o":
		m.copyCloneCommand()
	}
	currID, currOK := m.currentSelectedProjectID()
	if prevID != currID || prevOK != currOK {
		(&m).invalidateDetailCache()
		currProject, ok := m.selectedProject()
		if !ok {
			return m, nil
		}
		now := time.Now()
		m.pipelinePendingFetch = &currProject
		m.pipelineDebounceTimer = &now
		return m, pipelineDebounceTickCmd(currProject.ID, now, pipelineDebounceDelay)
	}
	return m, nil
}

// handleProjectActionKey handles the project action modal (pipelines vs files).
// Enter opens the selected action; Esc/left returns to the project list.
func (m Model) handleProjectActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		m.closeActionMenu()
		return m, nil
	case "down", "j", "up", "k":
		// Let Bubbles list handle navigation
		var cmd tea.Cmd
		m.actionMenu.menuList, cmd = m.actionMenu.menuList.Update(msg)
		m.actionMenu.selected = m.actionMenu.menuList.Index()
		return m, cmd
	case "enter":
		// Use Bubbles list selection index
		switch m.actionMenu.menuList.Index() {
		case 0:
			return m.openPipelineView(m.actionMenu.project)
		case 1:
			return m.openExplorer(m.actionMenu.project)
		}
	}
	return m, nil
}

// handleExplorerKey handles the ranger-style file explorer. J/K scroll the
// preview pane independently of directory navigation. Enter/right descends
// into directories; left/backspace ascends. Each navigation change triggers
// an async preview fetch for the newly selected entry.
func (m Model) handleExplorerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	cur := m.currentDirState()
	if cur == nil {
		m.closeExplorer("Back to projects")
		return m, nil
	}
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.closeExplorer("Back to projects")
		return m, nil
	case "J":
		m.explorer.preview.viewport.HalfPageDown()
		return m, nil
	case "K":
		m.explorer.preview.viewport.HalfPageUp()
		return m, nil
	case "ctrl+d":
		m.explorer.preview.viewport.HalfPageDown()
		if cur.selected < len(cur.entries)-1 {
			step := listPageStep(m.height)
			cur.selected = min(cur.selected+step, len(cur.entries)-1)
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "ctrl+u":
		m.explorer.preview.viewport.HalfPageUp()
		if cur.selected > 0 {
			step := listPageStep(m.height)
			cur.selected = max(cur.selected-step, 0)
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "<":
		m.explorer.preview.viewport.GotoTop()
		if cur.selected > 0 {
			cur.selected = 0
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case ">":
		m.explorer.preview.viewport.GotoBottom()
		if cur.selected < len(cur.entries)-1 {
			cur.selected = len(cur.entries) - 1
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "down", "j", "up", "k":
		// Let Bubbles list handle navigation
		prevIdx := m.explorer.currentList.Index()
		var cmd tea.Cmd
		m.explorer.currentList, cmd = m.explorer.currentList.Update(msg)
		newIdx := m.explorer.currentList.Index()
		cur.selected = newIdx
		if newIdx != prevIdx {
			return m, tea.Batch(cmd, m.queueExplorerPreview())
		}
		return m, cmd
	case "enter", "right", "l":
		entry := m.selectedEntry()
		if entry != nil && entry.IsDir() {
			return m.descendDirectory(*entry)
		}
	case "left", "h", "backspace":
		return m.navigateExplorerUp()
	case "r", "ctrl+r":
		return m.reloadExplorerPath()
	}
	return m, nil
}

// handlePipelineViewKey handles the dual-focus pipeline view. Left/right
// switches focus between the pipeline list and stages pane. J/K scrolls
// the log preview. Navigation within each pane triggers async loading of
// stages, jobs, and log traces. R opens the retry confirmation modal.
func (m Model) handlePipelineViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.confirmRetry {
		return m.handlePipelineRetryConfirmKey(msg)
	}
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		if m.pipelineView.focus == pipelineFocusStages {
			m.pipelineView.focus = pipelineFocusPipelines
			return m, nil
		}
		m.closePipelineView()
		return m, nil
	case "right", "l":
		m.pipelineView.focus = pipelineFocusStages
		return m, m.queuePipelineLogPreview()
	case "enter":
		if m.pipelineView.focus == pipelineFocusStages {
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
		}
	case "]":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if cmd := m.changePipelinePage(1); cmd != nil {
				return m, cmd
			}
		}
	case "[":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if cmd := m.changePipelinePage(-1); cmd != nil {
				return m, cmd
			}
		}
	case "J":
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K":
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "ctrl+d", "ctrl+u", "<", ">":
		return m.handlePipelineNavigation(key)
	case "down", "j", "up", "k":
		return m.handlePipelineItemNavigation(msg)
	case "r", "ctrl+r":
		return m.reloadPipelineView()
	case "R":
		return m.handlePipelineRetryRequest()
	}
	return m, nil
}

// handlePipelineNavigation handles half-page and jump-to-end navigation in
// the pipeline view (ctrl+d, ctrl+u, <, >). Moves both the list/table cursor
// and the log viewport simultaneously so the log preview stays in sync with
// the selected item.
func (m Model) handlePipelineNavigation(key string) (tea.Model, tea.Cmd) {
	step := listPageStep(m.height)
	switch key {
	case "ctrl+d":
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
				m.pipelineView.selected = min(m.pipelineView.selected+step, len(m.pipelineView.pipelines)-1)
				return m, m.selectPipelineAndLoadStages()
			}
		} else {
			jobCount := len(m.pipelineView.jobRows)
			if m.pipelineView.stageSelected < jobCount-1 {
				m.pipelineView.stageSelected = min(m.pipelineView.stageSelected+step, jobCount-1)
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "ctrl+u":
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected > 0 {
				m.pipelineView.selected = max(m.pipelineView.selected-step, 0)
				return m, m.selectPipelineAndLoadStages()
			}
		} else {
			if m.pipelineView.stageSelected > 0 {
				m.pipelineView.stageSelected = max(m.pipelineView.stageSelected-step, 0)
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "<":
		m.pipelineView.logViewport.GotoTop()
		m.pipelineView.logAutoFollow = false
		if m.pipelineView.focus == pipelineFocusPipelines {
			if len(m.pipelineView.pipelines) > 0 && m.pipelineView.selected != 0 {
				m.pipelineView.selected = 0
				return m, m.selectPipelineAndLoadStages()
			}
		} else if m.pipelineView.stageSelected != 0 {
			m.pipelineView.stageSelected = 0
			m.pipelineView.stageTable.SetCursor(0)
			m.resetPipelineLogPreview()
			return m, m.queuePipelineLogPreview()
		}
	case ">":
		m.pipelineView.logViewport.GotoBottom()
		m.pipelineView.logAutoFollow = true
		if m.pipelineView.focus == pipelineFocusPipelines {
			if len(m.pipelineView.pipelines) > 0 {
				last := len(m.pipelineView.pipelines) - 1
				if m.pipelineView.selected != last {
					m.pipelineView.selected = last
					return m, m.selectPipelineAndLoadStages()
				}
			}
		} else {
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
		}
	}
	return m, nil
}

// selectPipelineAndLoadStages resets stage selection and triggers stage + job
// loading for the currently selected pipeline. Used after changing the pipeline
// selection via half-page scroll or jump-to-end.
func (m *Model) selectPipelineAndLoadStages() tea.Cmd {
	m.pipelineView.stageSelected = 0
	m.pipelineView.stageTable.SetCursor(0)
	m.resetPipelineLogPreview()
	cmd := m.queuePipelineStagesForSelection()
	return tea.Batch(cmd, m.queuePipelineJobsForSelection())
}

// handlePipelineItemNavigation handles single-item j/k navigation in the
// pipeline view, delegating to either the bubbles list or table component.
func (m Model) handlePipelineItemNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.focus == pipelineFocusPipelines {
		prevIdx := m.pipelineView.pipelineList.Index()
		var cmd tea.Cmd
		m.pipelineView.pipelineList, cmd = m.pipelineView.pipelineList.Update(msg)
		newIdx := m.pipelineView.pipelineList.Index()
		m.pipelineView.selected = newIdx
		if newIdx != prevIdx {
			return m, tea.Batch(cmd, m.selectPipelineAndLoadStages())
		}
		return m, cmd
	}
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
}

// handlePipelineRetryRequest opens the retry confirmation modal for either a
// pipeline or a specific job, depending on the current focus.
func (m Model) handlePipelineRetryRequest() (tea.Model, tea.Cmd) {
	if m.pipelineView.retrying {
		m.status = "Retry already in progress"
		return m, nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.status = msgNoPipeline
		return m, nil
	}
	if m.pipelineView.focus == pipelineFocusStages {
		job := m.selectedPipelineJob()
		if job == nil {
			var cmds []tea.Cmd
			if cmd := m.queuePipelineStagesForSelection(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if cmd := m.queuePipelineJobsForSelection(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if len(cmds) > 0 {
				m.status = "Loading pipeline jobs..."
				return m, tea.Batch(cmds...)
			}
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
	m.pipelineView.confirmRetry = true
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryID = pipeline.ID
	m.pipelineView.confirmRetryRef = pipeline.Ref
	m.pipelineView.confirmRetryJobID = 0
	m.pipelineView.confirmRetryJobName = ""
	m.pipelineView.confirmRetryJobStage = ""
	return m, nil
}

// handlePipelineRetryConfirmKey processes input on the retry confirmation modal.
// Uses clearRetryConfirm (not clearAllRetryState) because the retrying/retryErr
// flags must only be set after the user confirms — dismissing the modal should
// not clear an in-progress retry or its error.
func (m Model) handlePipelineRetryConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		(&m).clearRetryConfirm()
		return m, nil
	case "enter":
		isJob := m.pipelineView.confirmRetryIsJob
		pipelineID := m.pipelineView.confirmRetryID
		ref := strings.TrimSpace(m.pipelineView.confirmRetryRef)
		jobID := m.pipelineView.confirmRetryJobID
		jobName := m.pipelineView.confirmRetryJobName
		retryProjectID := m.pipelineView.confirmRetryProjectID
		(&m).clearRetryConfirm()
		if m.pipelineView.project.ID == 0 || m.pipelineView.retrying {
			return m, nil
		}
		if isJob {
			if jobID == 0 {
				return m, nil
			}
			if pipelineID == 0 {
				if pipeline := m.selectedPipeline(); pipeline != nil {
					pipelineID = pipeline.ID
				}
			}
			m.pipelineView.retrying = true
			m.pipelineView.retryErr = nil
			jobLabel := fmt.Sprintf("#%d", jobID)
			if jobName != "" {
				jobLabel = fmt.Sprintf("%s (#%d)", jobName, jobID)
			}
			m.status = fmt.Sprintf("Retrying job %s", jobLabel)
			projectID := m.pipelineView.project.ID
			if retryProjectID != 0 {
				projectID = retryProjectID
			}
			return m, retryJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, projectID, pipelineID, jobID)
		}
		if pipelineID == 0 {
			return m, nil
		}
		if ref == "" {
			ref = strings.TrimSpace(m.pipelineView.project.DefaultBranch)
		}
		m.pipelineView.retrying = true
		m.pipelineView.retryErr = nil
		m.status = fmt.Sprintf("Retrying pipeline #%d", pipelineID)
		return m, retryPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID, ref)
	}
	return m, nil
}
