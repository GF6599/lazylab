package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// newBareList creates a list.Model with all chrome (status bar, pagination,
// help, filtering) disabled — the common baseline for every list in the UI.
func newBareList(items []list.Item, delegate list.ItemDelegate, w, h int) list.Model {
	l := list.New(items, delegate, w, h)
	l.Title = ""
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	return l
}

// The bracket pieces a marked row draws. A row that fits on one line takes the
// flat pair. A row that wraps takes the corner pieces on its first and last
// lines and the extension pieces between, so one tall pair encloses the whole
// block instead of a flat pair repeating on each line as its own row.
var (
	markerFlat   = [2]string{"[", "]"}
	markerTop    = [2]string{"⎡", "⎤"}
	markerMiddle = [2]string{"⎢", "⎥"}
	markerBottom = [2]string{"⎣", "⎦"}
)

// rowMarker is the bracket pair a list row draws around its label, with the
// style the label itself takes. Both brackets occupy a cell on every row and go
// blank when the row is not current, so no label moves as the marker travels.
type rowMarker struct {
	left, right string
	label       lipgloss.Style
}

// markerFor returns the marker a row draws, given its index and the current one.
func markerFor(index int, listIndex int) rowMarker {
	if index == listIndex {
		return rowMarker{left: markerFlat[0], right: markerFlat[1], label: selectedItemStyle}
	}
	return rowMarker{left: " ", right: " ", label: itemStyle}
}

// spanning returns the marker for line i of an n-line row.
func (mk rowMarker) spanning(i, n int) rowMarker {
	if n < 2 {
		return mk
	}
	pieces := markerMiddle
	switch i {
	case 0:
		pieces = markerTop
	case n - 1:
		pieces = markerBottom
	}
	mk.left, mk.right = pieces[0], pieces[1]
	return mk
}

// render wraps inner in the bracket pair. The brackets take the marker colour
// and the label its own, because the brackets are chrome and the label is the
// item they point at.
func (mk rowMarker) render(inner string) string {
	return markerStyle.Render(mk.left) + mk.label.Render(inner) + markerStyle.Render(mk.right)
}

// Commonly reused strings.
const (
	unknownStatus   = "unknown"
	timestampFormat = "01-02 15:04"
	msgNoPipeline   = "No pipeline selected"
	msgNoStages     = "No stages available"
	msgNoPipelines  = "No pipelines found"
)

// Layout constants for UI calculations
const (
	// headerFooterLines is the number of lines reserved for header and footer
	headerFooterLines = 6

	// halfPageScrollFactor is the divisor for half-page scrolling
	halfPageScrollFactor = 2

	// Explorer view width percentages. The preview column is the
	// remainder (100 - parent - current), so no explicit constant.
	explorerParentWidthPct  = 25
	explorerCurrentWidthPct = 45

	// Tree view widths for navigation (when in nested directory).
	treeParentWidthPct  = 30
	treeCurrentWidthPct = 25

	// Minimum widths to prevent layout collapse.
	minParentWidth  = 6
	minCurrentWidth = 6
	minTreeParent   = 12
	minTreeCurrent  = 12
)

// stageTableColumns computes column widths for the pipeline stage table based
// on available pane width. The bubbles table applies 1-char padding per side
// per cell (2 chars/cell), so 3 columns consume 6 chars of padding.
func stageTableColumns(width int) []table.Column {
	const cellPadding = 2
	const numCols = 3
	const statusWidth = 12
	const minStage = 8
	const minJob = 8

	usable := width - numCols*cellPadding
	if usable < minJob+minStage+statusWidth {
		usable = max(usable, numCols)
		third := usable / numCols
		return []table.Column{
			{Title: "Job", Width: max(1, usable-third*2)},
			{Title: "Stage", Width: third},
			{Title: "Status", Width: third},
		}
	}
	remaining := usable - statusWidth
	stageW := max(minStage, remaining*25/100)
	jobW := remaining - stageW
	return []table.Column{
		{Title: "Job", Width: jobW},
		{Title: "Stage", Width: stageW},
		{Title: "Status", Width: statusWidth},
	}
}

// stageTableSelectedHint returns a styled hint line showing the full job name
// of the selected stage row when it would be truncated by the table column
// width. Returns "" when the name fits or no row is selected.
//
// Unlike list delegates (which can emit extra lines), the bubbles table
// enforces single-line cells with Inline(true), so we show the full name
// in a separate hint line below the table instead.
func stageTableSelectedHint(m *Model, width int) string {
	rows := m.pipelineView.stageTable.Rows()
	cursor := m.pipelineView.stageTable.Cursor()
	if cursor < 0 || cursor >= len(rows) {
		return ""
	}
	jobName := rows[cursor][0]
	// Strip tree-drawing prefixes so the hint shows only the meaningful name.
	clean := strings.TrimLeft(jobName, " ")
	for _, p := range []string{"├─ ", "└─ ", iconTreeExpanded + " ", iconTreeCollapsed + " "} {
		clean = strings.TrimPrefix(clean, p)
	}
	cols := stageTableColumns(width)
	if len(cols) == 0 || lipgloss.Width(jobName) <= cols[0].Width {
		return ""
	}
	return explorerHintStyle.Render(clampLine(" "+clean, width))
}

// needsAnimation reports whether anything on screen should be moving. Update keeps the
// spinner tick chain alive only while this holds, so a state that animates but is not
// named here freezes on its first frame.
func (m *Model) needsAnimation() bool {
	return m.isLoading() || m.hasLivePipeline()
}

// hasLivePipeline reports whether a pipeline on screen is still working. This is
// deliberately separate from isLoading: a running pipeline is the state the user watches
// longest, and it is precisely the state in which no fetch is in flight. Both sources are
// read because the two panels draw from different ones, and either can be on screen alone.
func (m *Model) hasLivePipeline() bool {
	if m.mode != modePipelines && m.mode != modeMultiPanel {
		return false
	}
	for _, pipeline := range m.pipelineView.pipelines {
		if isLivePipelineStatus(pipeline.Status) {
			return true
		}
	}
	if m.pipelineStatus == nil {
		return false
	}
	live := false
	m.pipelineStatus.Range(func(_ int, state pipelineState) bool {
		if state.hasInfo && isLivePipelineStatus(state.info.Status) {
			live = true
		}
		return !live
	})
	return live
}

// livePipelineStatuses are the statuses GitLab still advances on its own. Terminal
// statuses are absent, and so are manual and blocked: those wait for a person, so
// animating them would spin against nothing until somebody acts.
var livePipelineStatuses = map[string]bool{
	"created":              true,
	"waiting_for_resource": true,
	"preparing":            true,
	"pending":              true,
	"running":              true,
	"scheduled":            true,
}

func isLivePipelineStatus(status string) bool {
	return livePipelineStatuses[strings.ToLower(status)]
}

// isLoading returns true if anything is currently loading that should animate the spinner.
func (m *Model) isLoading() bool {
	// Project list loading
	if m.loading || m.backgroundLoading {
		return true
	}

	// Pipeline/stage/job loading (applies to both modePipelines and modeMultiPanel)
	if m.mode == modePipelines || m.mode == modeMultiPanel {
		if m.pipelineView.loading {
			return true
		}
		if m.pipelineView.logPreview.loading {
			return true
		}
		if pipeline := m.selectedPipeline(); pipeline != nil {
			if m.pipelineView.stages.IsLoading(pipeline.ID) {
				return true
			}
			if m.pipelineView.jobs.IsLoading(pipeline.ID) {
				return true
			}
		}
	}

	// Explorer tree or file loading
	if m.mode == modeExplorer || (m.mode == modeMultiPanel && m.explorer.project.ID != 0) {
		if cur := m.currentDirState(); cur != nil && cur.loading {
			return true
		}
		if m.explorer.preview.loading {
			return true
		}
	}

	return false
}

var pipelineStatusStyles map[string]lipgloss.Style

// rebuildPipelineStatusStyles populates the pipelineStatusStyles map from the
// current pipeline style vars. Called from rebuildStyles() on every theme change.
func rebuildPipelineStatusStyles() {
	pipelineStatusStyles = map[string]lipgloss.Style{
		"success":              pipelineSuccess,
		"failed":               pipelineFailed,
		"running":              pipelineRunning,
		"pending":              pipelinePending,
		"created":              pipelinePending,
		"waiting_for_resource": pipelinePending,
		"scheduled":            pipelinePending,
		"canceled":             pipelineCanceled,
		"canceled?":            pipelineCanceled,
		"skipped":              pipelineSkipped,
		"manual":               pipelinePending,
		"blocked":              pipelinePending,
	}
}

func pipelineStatusStyle(status string) lipgloss.Style {
	if s, ok := pipelineStatusStyles[strings.ToLower(status)]; ok {
		return s
	}
	return pipelineUnknown
}

func pipelineRefLabel(project gitlab.ProjectNode, state pipelineState) string {
	if strings.TrimSpace(state.ref) != "" {
		if state.ref == pipelineAllRefsRef {
			if state.hasInfo && strings.TrimSpace(state.info.Ref) != "" {
				return strings.TrimSpace(state.info.Ref)
			}
			return pipelineAllRefsLabel
		}
		return state.ref
	}
	if strings.TrimSpace(project.DefaultBranch) != "" {
		return strings.TrimSpace(project.DefaultBranch)
	}
	return "all refs"
}

func (m *Model) pipelineLogJob() *gitlab.PipelineJob {
	if m.pipelineView.logJobID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	jobs, _ := m.pipelineView.jobs.Get(pipeline.ID)
	for i := range jobs {
		if jobs[i].ID == m.pipelineView.logJobID {
			return &jobs[i]
		}
	}
	return nil
}

// displayRef returns the git ref shown in the explorer header, defaulting
// to "main" when no explicit ref was provided.
func displayRef(ex explorerState) string {
	if ex.ref == "" {
		return "main"
	}
	return ex.ref
}

func (m *Model) findDirIndex(path string) int {
	for i := range m.explorer.stack {
		if m.explorer.stack[i].path == path {
			return i
		}
	}
	return -1
}

// previewContentWidth estimates the inner width of the explorer preview pane
// for viewport and syntax highlighting sizing. Mirrors explorerPaneLayout's
// width calculation minus 2 border chars.
func previewContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(minParentWidth, width*explorerParentWidthPct/100)
	currentWidth := max(minCurrentWidth, width*explorerCurrentWidthPct/100)
	previewWidth := width - parentWidth - currentWidth
	previewWidth = max(previewWidth, minParentWidth)
	contentWidth := max(previewWidth-2, 1)
	return contentWidth
}

// previewContentHeight returns the viewport height for the explorer preview pane.
// Reserves 6 lines: 4 (layout: 2 borders + helpbar + newline) + 2 (pane header + newline).
func previewContentHeight(height int) int {
	return max(height-6, 1)
}

// pipelineLogContentWidth estimates the inner width of the pipeline log pane
// for viewport sizing. Mirrors pipelinePaneLayout's width calculation minus
// 2 border chars.
func pipelineLogContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(minTreeParent, width*treeParentWidthPct/100)
	currentWidth := max(minTreeCurrent, width*treeCurrentWidthPct/100)
	previewWidth := width - parentWidth - currentWidth
	previewWidth = max(previewWidth, minTreeParent)
	contentWidth := max(previewWidth-2, 1)
	return contentWidth
}

// pipelineLogContentHeight returns the viewport height for the pipeline log pane.
// Reserves 6 lines: 4 (layout: 2 borders + helpbar + newline) + 2 (pane header + newline).
func pipelineLogContentHeight(height int) int {
	return max(height-6, 1)
}

func normalizePipelineLogContent(content string) string {
	if content == "" {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if !strings.Contains(content, "\t") {
		return content
	}
	return strings.ReplaceAll(content, "\t", "    ")
}

// setLogViewportContent normalizes and wraps content to fit the viewport width.
func (m *Model) setLogViewportContent(content string) {
	w := m.pipelineView.logViewport.Width
	if w <= 0 {
		w = 80
	}
	normalized := normalizePipelineLogContent(content)
	wrapped := ansi.Wrap(normalized, w, "")
	m.pipelineView.logViewport.SetContent(wrapped)
}

// listPageStep calculates how many items to skip for half-page scrolling,
// based on the visible terminal height minus chrome.
func listPageStep(height int) int {
	visible := max(height-headerFooterLines, 1)
	step := max(visible/halfPageScrollFactor, 1)
	return step
}

// bigStepIdx computes the new selection index for the "big-step" navigation
// keys (ctrl+u, ctrl+d, g/<, G/>) shared by every panel. It returns the
// proposed new index and whether the key was handled. Returning handled=true
// with newIdx==idx means the key matched but the index didn't change (e.g.,
// already at the boundary) — call sites should still treat it as "consumed"
// to avoid falling through to other branches.
//
// j/k navigation is intentionally omitted because each panel routes it
// through its own widget (bubbles/list, bubbles/table, or a plain int) with
// widget-specific wrapping behavior that this helper would have to mimic.
func bigStepIdx(key string, idx, length, height int) (newIdx int, handled bool) {
	if length <= 0 {
		switch key {
		case "ctrl+d", "ctrl+u", "<", "g", ">", "G":
			return idx, true
		}
		return idx, false
	}
	switch key {
	case "ctrl+d":
		return min(idx+listPageStep(height), length-1), true
	case "ctrl+u":
		return max(idx-listPageStep(height), 0), true
	case "<", "g":
		return 0, true
	case ">", "G":
		return length - 1, true
	}
	return idx, false
}

// moveTableCursor exists because table.SetCursor moves the cursor and leaves the
// scroll offset where it was, which drops the current row off screen on any jump
// wider than the visible window. Only MoveUp and MoveDown carry the offset, so
// every jump goes through them. Prefer this to SetCursor everywhere: the offset
// is only ever correct if nothing bypasses it.
func moveTableCursor(t *table.Model, idx int) {
	idx = max(0, min(idx, len(t.Rows())-1))
	switch delta := idx - t.Cursor(); {
	case delta > 0:
		t.MoveDown(delta)
	case delta < 0:
		t.MoveUp(-delta)
	}
}

// logError logs an error if the logger is configured. This eliminates the
// repeated `if m.opts.Logger != nil { m.opts.Logger.Error(...) }` pattern.
func (m *Model) logError(msg string, args ...any) {
	if m.opts.Logger != nil {
		m.opts.Logger.Error(msg, args...)
	}
}

// logDebug logs a debug message if the logger is configured.
func (m *Model) logDebug(msg string, args ...any) {
	if m.opts.Logger != nil {
		m.opts.Logger.Debug(msg, args...)
	}
}
