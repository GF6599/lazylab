// Package ui contains the Bubble Tea models, views, and styles for the TUI.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
	key := msg.String()
	if m.search.active {
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
			// Batch prefetch for new visible projects after search clear
			return m, (&m).queueBatchPrefetchPipelineStatus()
		case tea.KeyEnter:
			// Apply search immediately on Enter
			m.search.active = false
			m.search.query = m.search.input.Value()
			m.search.pendingQuery = ""
			m.search.debounceTimer = nil
			m.search.input.Blur()
			m.status = fmt.Sprintf("Search: %s", m.search.query)
			m.invalidateVisibleCache()
			m.ensureSelectionBounds()
			m.updateProjectList()
			// Batch prefetch for search results
			return m, (&m).queueBatchPrefetchPipelineStatus()
		case tea.KeyCtrlC:
			return m, tea.Quit
		default:
			// Update input and queue debounced filter
			m.search.input, cmd = m.search.input.Update(msg)
			inputValue := m.search.input.Value()

			// Queue debounced search
			now := time.Now()
			m.search.pendingQuery = inputValue
			m.search.debounceTimer = &now

			// Return input cmd + debounce tick cmd
			debounceCmd := searchDebounceTickCmd(inputValue, now)
			if cmd != nil {
				return m, tea.Batch(cmd, debounceCmd)
			}
			return m, debounceCmd
		}
	}

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "/":
		m.search.active = true
		m.search.input.SetValue(m.search.query)
		m.search.input.CursorEnd()
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
		m.movePage(1)
		// Batch prefetch pipeline status for new page
		return m, (&m).queueBatchPrefetchPipelineStatus()
	case "h", "left":
		m.movePage(-1)
		// Batch prefetch pipeline status for new page
		return m, (&m).queueBatchPrefetchPipelineStatus()
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
		m.explorer.preview.viewport.HalfViewDown()
		return m, nil
	case "K":
		m.explorer.preview.viewport.HalfViewUp()
		return m, nil
	case "ctrl+d":
		m.explorer.preview.viewport.HalfViewDown()
		if cur.selected < len(cur.entries)-1 {
			step := listPageStep(m.height)
			cur.selected = min(cur.selected+step, len(cur.entries)-1)
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "ctrl+u":
		m.explorer.preview.viewport.HalfViewUp()
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
		step := listPageStep(m.height)
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
				m.pipelineView.selected = min(m.pipelineView.selected+step, len(m.pipelineView.pipelines)-1)
				m.pipelineView.stageSelected = 0
				m.pipelineView.stageTable.SetCursor(0)
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			stages := m.selectedPipelineStages()
			if m.pipelineView.stageSelected < len(stages)-1 {
				m.pipelineView.stageSelected = min(m.pipelineView.stageSelected+step, len(stages)-1)
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "ctrl+u":
		m.pipelineView.logViewport.HalfViewUp()
		m.pipelineView.logAutoFollow = false
		step := listPageStep(m.height)
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected > 0 {
				m.pipelineView.selected = max(m.pipelineView.selected-step, 0)
				m.pipelineView.stageSelected = 0
				m.pipelineView.stageTable.SetCursor(0)
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
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
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
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
					m.pipelineView.stageSelected = 0
					m.resetPipelineLogPreview()
					cmd := m.queuePipelineStagesForSelection()
					return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
				}
			}
		} else {
			stages := m.selectedPipelineStages()
			if len(stages) > 0 {
				last := len(stages) - 1
				if m.pipelineView.stageSelected != last {
					m.pipelineView.stageSelected = last
					m.pipelineView.stageTable.SetCursor(last)
					m.resetPipelineLogPreview()
					return m, m.queuePipelineLogPreview()
				}
			}
		}
	case "down", "j", "up", "k":
		if m.pipelineView.focus == pipelineFocusPipelines {
			// Let Bubbles list handle navigation
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
		} else {
			// Let Bubbles table handle navigation
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
	case "r", "ctrl+r":
		return m.reloadPipelineView()
	case "R":
		if m.pipelineView.retrying {
			m.status = "Retry already in progress"
			return m, nil
		}
		pipeline := m.selectedPipeline()
		if pipeline == nil {
			m.status = "No pipeline selected"
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
			return m, nil
		}
		m.pipelineView.confirmRetry = true
		m.pipelineView.confirmRetryIsJob = false
		m.pipelineView.confirmRetryID = pipeline.ID
		m.pipelineView.confirmRetryRef = pipeline.Ref
		m.pipelineView.confirmRetryJobID = 0
		m.pipelineView.confirmRetryJobName = ""
		m.pipelineView.confirmRetryJobStage = ""
	}
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
			return m, retryJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID, jobID)
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
