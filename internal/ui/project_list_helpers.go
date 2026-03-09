package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
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

// listCursorStyle returns the cursor prefix and style for a list item based
// on whether it is the currently selected item.
func listCursorStyle(index int, listIndex int) (string, lipgloss.Style) {
	if index == listIndex {
		return ">", selectedItemStyle
	}
	return " ", itemStyle
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

	// Explorer view width percentages
	explorerParentWidthPct  = 25 // Parent directory listing width
	explorerCurrentWidthPct = 45 // Current directory listing width
	explorerPreviewWidthPct = 30 // File preview width

	// Tree view widths for navigation (when in nested directory)
	treeParentWidthPct  = 30 // Parent directory listing
	treeCurrentWidthPct = 25 // Current directory listing
	treePreviewWidthPct = 45 // Remaining for preview

	// Minimum widths to prevent layout collapse
	minParentWidth  = 6
	minCurrentWidth = 6
	minTreeParent   = 12
	minTreeCurrent  = 12
	minInnerWidth   = 20
)

// isLoading returns true if anything is currently loading that should animate the spinner.
func (m *Model) isLoading() bool {
	// Project list loading
	if m.loading || m.backgroundLoading {
		return true
	}

	// Mode-specific loading states
	switch m.mode {
	case modeExplorer:
		// Explorer tree or file loading
		if cur := m.currentDirState(); cur != nil && cur.loading {
			return true
		}
		if m.explorer.preview.loading {
			return true
		}
	case modePipelines:
		// Pipeline view loading states
		if m.pipelineView.loading {
			return true
		}
		if m.pipelineView.logPreview.loading {
			return true
		}
		// Check if any stages or jobs are loading for selected pipeline
		if pipeline := m.selectedPipeline(); pipeline != nil {
			if m.pipelineView.stages.IsLoading(pipeline.ID) {
				return true
			}
			if m.pipelineView.jobs.IsLoading(pipeline.ID) {
				return true
			}
		}
	}

	return false
}

var pipelineStatusStyles = map[string]lipgloss.Style{
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

func parentDir(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		return ""
	}
	return path[:idx]
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

// Pipeline log scrolling now handled by viewport directly in key handlers

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

// logInfo logs an info message if the logger is configured.
func (m *Model) logInfo(msg string, args ...any) {
	if m.opts.Logger != nil {
		m.opts.Logger.Info(msg, args...)
	}
}
