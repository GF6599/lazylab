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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleProjectSearchKey handles key events while the search input is focused.
// Esc clears the search and restores the full project list; Enter commits the
// query. All other keys are forwarded to the textinput component, with a
// debounce timer that triggers incremental filtering after a short pause.
func (m Model) handleProjectSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m.cancelProjectSearch()
	case tea.KeyEnter:
		return m.commitProjectSearch()
	case tea.KeyCtrlC:
		return m, tea.Quit
	}
	return m.typeIntoProjectSearch(msg)
}

// cancelProjectSearch clears the search input and reloads the full visible set.
func (m Model) cancelProjectSearch() (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
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
	return m, m.searchPostUpdateCmd(prevID, prevOK)
}

// commitProjectSearch finalizes the search query and reloads dependent data.
func (m Model) commitProjectSearch() (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
	m.search.active = false
	m.search.query = m.search.input.Value()
	m.search.pendingQuery = ""
	m.search.debounceTimer = nil
	m.search.input.Blur()
	m.status = fmt.Sprintf("Search: %s", m.search.query)
	m.invalidateVisibleCache()
	m.ensureSelectionBounds()
	m.updateProjectList()
	return m, m.searchPostUpdateCmd(prevID, prevOK)
}

// typeIntoProjectSearch forwards a keystroke to the search textinput and
// schedules a debounced filter update.
func (m Model) typeIntoProjectSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
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

// searchPostUpdateCmd batches the work shared by every search transition:
// prefetching pipeline status for the new visible set and reloading sidebar
// data when the selected project changed.
func (m Model) searchPostUpdateCmd(prevID int, prevOK bool) tea.Cmd {
	var cmds []tea.Cmd
	if cmd := (&m).queueBatchPrefetchPipelineStatus(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := (&m).handleSelectedProjectChange(prevID, prevOK); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// handleExplorerKey handles the ranger-style file explorer. J/K scroll the
// preview pane independently of directory navigation. Enter/right descends
// into directories; left/backspace ascends. Each navigation change triggers
// an async preview fetch for the newly selected entry.
func (m Model) handleExplorerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.currentDirState()
	if cur == nil {
		m = m.closeExplorer("Back to projects")
		return m, ensurePipelineTickCmd(&m)
	}
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m = m.closeExplorer("Back to projects")
		return m, ensurePipelineTickCmd(&m)
	case "J":
		m.explorer.preview.viewport.HalfPageDown()
		return m, nil
	case "K":
		m.explorer.preview.viewport.HalfPageUp()
		return m, nil
	case "ctrl+d", "ctrl+u", "<", ">":
		return m.handleExplorerBigStep(key, cur)
	case "down", "j", "up", "k":
		return m.handleExplorerListNav(msg, cur)
	case "enter", "right", "l":
		entry := m.selectedEntry()
		if entry != nil && entry.IsDir() {
			return m.descendDirectory(*entry)
		}
	case "left", "h", "backspace":
		return m.navigateExplorerUp()
	case "r", "ctrl+r":
		return m.reloadExplorerPath()
	case "ctrl+o":
		m = m.copyExplorerURL()
		return m, nil
	}
	return m, nil
}

// handleExplorerBigStep applies ctrl+d / ctrl+u / < / > to both the preview
// viewport and the directory selection cursor.
func (m Model) handleExplorerBigStep(key string, cur *dirState) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+d":
		m.explorer.preview.viewport.HalfPageDown()
	case "ctrl+u":
		m.explorer.preview.viewport.HalfPageUp()
	case "<":
		m.explorer.preview.viewport.GotoTop()
	case ">":
		m.explorer.preview.viewport.GotoBottom()
	}
	newIdx, handled := bigStepIdx(key, cur.selected, len(cur.entries), m.height)
	if !handled || newIdx == cur.selected {
		return m, nil
	}
	cur.selected = newIdx
	return m, m.queueExplorerPreview()
}

// handleExplorerListNav forwards j/k/up/down to the current bubbles list and
// fires an async preview fetch when the selection actually moves.
func (m Model) handleExplorerListNav(msg tea.KeyMsg, cur *dirState) (tea.Model, tea.Cmd) {
	prevIdx := m.explorer.currentList.Index()
	var cmd tea.Cmd
	m.explorer.currentList, cmd = m.explorer.currentList.Update(msg)
	newIdx := m.explorer.currentList.Index()
	cur.selected = newIdx
	if newIdx != prevIdx {
		return m, tea.Batch(cmd, m.queueExplorerPreview())
	}
	return m, cmd
}

// handlePipelineViewKey handles the dual-focus pipeline view. Left/right
// switches focus between the pipeline list and stages pane. J/K scrolls
// the log preview. Navigation within each pane triggers async loading of
// stages, jobs, and log traces. R opens the retry confirmation modal.
func (m Model) handlePipelineViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.retryConfirm.active {
		return m.handlePipelineRetryConfirmKey(msg)
	}
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		return m.handlePipelineBack()
	case "right", "l":
		m.pipelineView.focus = pipelineFocusStages
		return m, m.queuePipelineLogPreview()
	case "enter":
		return m.handlePipelineEnter()
	case "]":
		return m.changePipelinePageOnFocus(1)
	case "[":
		return m.changePipelinePageOnFocus(-1)
	case "J", "K":
		return m.handlePipelineLogScroll(key)
	case "ctrl+d", "ctrl+u", "<", ">":
		return m.handlePipelineNavigation(key)
	case "down", "j", "up", "k":
		return m.handlePipelineItemNavigation(msg)
	case "r", "ctrl+r":
		return m.reloadPipelineView()
	case "R":
		return m.handlePipelineRetryRequest()
	case "ctrl+o":
		return m.copyPipelineSelectionURL()
	}
	return m, nil
}

// handlePipelineLogScroll scrolls the log viewport by a half page and updates
// the auto-follow flag based on whether the user is still pinned to the bottom.
func (m Model) handlePipelineLogScroll(key string) (tea.Model, tea.Cmd) {
	if key == "J" {
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
	} else {
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
	}
	return m, nil
}

// copyPipelineSelectionURL copies the URL for whatever is currently selected
// (a job when focused on stages, otherwise the pipeline itself).
func (m Model) copyPipelineSelectionURL() (tea.Model, tea.Cmd) {
	if m.pipelineView.focus == pipelineFocusStages {
		m.copyJobURL()
	} else {
		m.copyPipelineURL()
	}
	return m, nil
}

// handlePipelineBack collapses focus from stages → pipelines, or closes the
// pipeline view when already at the leftmost pane.
func (m Model) handlePipelineBack() (tea.Model, tea.Cmd) {
	if m.pipelineView.focus == pipelineFocusStages {
		m.pipelineView.focus = pipelineFocusPipelines
		return m, nil
	}
	m.closePipelineView()
	return m, nil
}

// handlePipelineEnter toggles bridge expand/collapse when focused on stages.
func (m Model) handlePipelineEnter() (tea.Model, tea.Cmd) {
	if m.pipelineView.focus != pipelineFocusStages {
		return m, nil
	}
	row := m.selectedStageJobRow()
	if row == nil || row.Kind != rowKindBridge || row.IsLast {
		return m, nil
	}
	if m.pipelineView.matrixExpanded == nil {
		m.pipelineView.matrixExpanded = make(map[string]bool)
	}
	expanding := !m.pipelineView.matrixExpanded[row.GroupKey]
	m.pipelineView.matrixExpanded[row.GroupKey] = expanding
	m.updateStageTable()
	if !expanding || row.Bridge == nil || row.Bridge.DownstreamPipeline == nil {
		return m, nil
	}
	ds := row.Bridge.DownstreamPipeline
	if ds.ProjectID == 0 || m.pipelineView.childJobs.IsLoading(ds.ID) {
		return m, nil
	}
	if _, cached := m.pipelineView.childJobs.Get(ds.ID); cached {
		return m, nil
	}
	m.pipelineView.childJobs.SetLoading(ds.ID)
	return m, fetchChildPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, ds.ProjectID, ds.ID)
}

// changePipelinePageOnFocus advances the pipeline list page when focus is on
// the pipelines pane; otherwise a no-op.
func (m Model) changePipelinePageOnFocus(delta int) (tea.Model, tea.Cmd) {
	if m.pipelineView.focus != pipelineFocusPipelines {
		return m, nil
	}
	if cmd := m.changePipelinePage(delta); cmd != nil {
		return m, cmd
	}
	return m, nil
}

// handlePipelineNavigation handles half-page and jump-to-end navigation in
// the pipeline view (ctrl+d, ctrl+u, <, >). Moves both the list/table cursor
// and the log viewport simultaneously so the log preview stays in sync with
// the selected item.
func (m Model) handlePipelineNavigation(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+d":
		m.pipelineView.logViewport.HalfPageDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
	case "ctrl+u":
		m.pipelineView.logViewport.HalfPageUp()
		m.pipelineView.logAutoFollow = false
	case "<":
		m.pipelineView.logViewport.GotoTop()
		m.pipelineView.logAutoFollow = false
	case ">":
		m.pipelineView.logViewport.GotoBottom()
		m.pipelineView.logAutoFollow = true
	}
	if m.pipelineView.focus == pipelineFocusPipelines {
		newIdx, handled := bigStepIdx(key, m.pipelineView.selected, len(m.pipelineView.pipelines), m.height)
		if !handled || newIdx == m.pipelineView.selected {
			return m, nil
		}
		m.pipelineView.selected = newIdx
		return m, m.selectPipelineAndLoadStages()
	}
	newIdx, handled := bigStepIdx(key, m.pipelineView.stageSelected, len(m.pipelineView.jobRows), m.height)
	if !handled || newIdx == m.pipelineView.stageSelected {
		return m, nil
	}
	m.pipelineView.stageSelected = newIdx
	m.pipelineView.stageTable.SetCursor(newIdx)
	m.resetPipelineLogPreview()
	return m, m.queuePipelineLogPreview()
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
		return m.requestStageRetry(pipeline)
	}
	m.pipelineView.retryConfirm = retryConfirmState{
		active: true,
		id:     pipeline.ID,
		ref:    pipeline.Ref,
	}
	return m, nil
}

// requestStageRetry prepares a job-scoped retry confirmation, queueing the
// stage/job fetch if the cached data is missing.
func (m Model) requestStageRetry(pipeline *gitlab.PipelineSummary) (tea.Model, tea.Cmd) {
	job := m.selectedPipelineJob()
	if job == nil {
		return m.queuePipelineDataForRetry()
	}
	m.pipelineView.retryConfirm = retryConfirmState{
		active:   true,
		isJob:    true,
		id:       pipeline.ID,
		jobID:    job.ID,
		jobName:  job.Name,
		jobStage: job.Stage,
	}
	if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridgeChild && row.ChildProjectID != 0 {
		m.pipelineView.retryConfirm.projectID = row.ChildProjectID
	}
	return m, nil
}

// queuePipelineDataForRetry batches the stage and job fetches needed before a
// job-retry can resolve a target job.
func (m Model) queuePipelineDataForRetry() (tea.Model, tea.Cmd) {
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

// handlePipelineRetryConfirmKey processes input on the retry confirmation modal.
// Uses clearRetryConfirm (not clearAllRetryState) because the retrying/retryErr
// flags must only be set after the user confirms — dismissing the modal should
// not clear an in-progress retry or its error.
func (m Model) handlePipelineRetryConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		(&m).clearRetryConfirm()
		return m, nil
	case "enter":
		return m.confirmPipelineRetry()
	}
	return m, nil
}

// confirmPipelineRetry runs the modal's accept action: dispatches a job retry
// or a pipeline retry depending on the stored retryConfirmState.
func (m Model) confirmPipelineRetry() (tea.Model, tea.Cmd) {
	rc := m.pipelineView.retryConfirm
	(&m).clearRetryConfirm()
	if m.pipelineView.project.ID == 0 || m.pipelineView.retrying {
		return m, nil
	}
	if rc.isJob {
		return m.dispatchJobRetry(rc)
	}
	return m.dispatchPipelineRetry(rc)
}

// dispatchJobRetry issues a single-job retry. Falls back to the currently
// selected pipeline when the confirmation state lacks a pipeline ID (e.g.,
// the modal was opened on a stale selection).
func (m Model) dispatchJobRetry(rc retryConfirmState) (tea.Model, tea.Cmd) {
	if rc.jobID == 0 {
		return m, nil
	}
	pipelineID := rc.id
	if pipelineID == 0 {
		if pipeline := m.selectedPipeline(); pipeline != nil {
			pipelineID = pipeline.ID
		}
	}
	m.pipelineView.retrying = true
	m.pipelineView.retryErr = nil
	jobLabel := fmt.Sprintf("#%d", rc.jobID)
	if rc.jobName != "" {
		jobLabel = fmt.Sprintf("%s (#%d)", rc.jobName, rc.jobID)
	}
	m.status = fmt.Sprintf("Retrying job %s", jobLabel)
	projectID := m.pipelineView.project.ID
	if rc.projectID != 0 {
		projectID = rc.projectID
	}
	return m, retryJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, projectID, pipelineID, rc.jobID)
}

// dispatchPipelineRetry issues a whole-pipeline retry, defaulting ref to the
// project's default branch when the stored ref is empty.
func (m Model) dispatchPipelineRetry(rc retryConfirmState) (tea.Model, tea.Cmd) {
	if rc.id == 0 {
		return m, nil
	}
	ref := strings.TrimSpace(rc.ref)
	if ref == "" {
		ref = strings.TrimSpace(m.pipelineView.project.DefaultBranch)
	}
	m.pipelineView.retrying = true
	m.pipelineView.retryErr = nil
	m.status = fmt.Sprintf("Retrying pipeline #%d", rc.id)
	return m, retryPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, rc.id, ref)
}
