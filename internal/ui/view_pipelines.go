// view_pipelines.go renders the pipeline view: the pipeline list pane,
// the stages/jobs matrix pane, the log preview pane, and the retry
// confirmation modal.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// pipelinePaneLayout calculates inner widths and content height for the
// three-pane pipeline view (pipelines, stages, log). Same height budget
// as explorerPaneLayout. Uses treeParent/treeCurrentWidthPct constants
// since the pipeline view shares the tree-style navigation layout.
func pipelinePaneLayout(width, height int) (pipelineInner, stagesInner, logInner, contentHeight int, ok bool) {
	if width <= 0 {
		width = 80
	}
	minInner := 10
	minTotalWidth := minInner*3 + 6 + paneGap*2
	if width < minTotalWidth {
		return 0, 0, 0, 0, false
	}
	if height <= 5 {
		height = 5
	}
	contentHeight = height - 4
	innerTotal := width - paneGap*2 - 6
	pipelineInner = max(minInner, innerTotal*treeParentWidthPct/100)
	stagesInner = max(minInner, innerTotal*treeCurrentWidthPct/100)
	logInner = innerTotal - pipelineInner - stagesInner
	if logInner < minInner {
		logInner = minInner
		stagesInner = max(minInner, innerTotal-pipelineInner-logInner)
	}
	return pipelineInner, stagesInner, logInner, contentHeight, true
}

// renderPipelineView assembles the three-pane pipeline layout: pipeline list,
// stages/jobs table, and log preview. Focus state determines which pane has
// highlighted borders and receives keyboard input.
func renderPipelineView(m Model, width int) string {
	pipelineInner, stagesInner, logInner, contentHeight, ok := pipelinePaneLayout(width, m.height)
	if !ok {
		return renderTooSmallView(width, m.height)
	}
	pipelinesFocused := m.pipelineView.focus == pipelineFocusPipelines
	stagesFocused := m.pipelineView.focus == pipelineFocusStages
	parentPane := renderPane(renderPipelineListPane(m, pipelineInner, contentHeight, pipelinesFocused), pipelineInner, contentHeight, pipelinesFocused)
	currentPane := renderPane(renderPipelineStagesPane(m, stagesInner, contentHeight, stagesFocused), stagesInner, contentHeight, stagesFocused)
	previewPane := renderPane(renderPipelineLogPane(m, logInner, false), logInner, contentHeight, false)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, parentPane, gap, currentPane, gap, previewPane)
}

// renderPipelineListPane renders the left pane of the pipeline view: a
// scrollable list of pipelines for the current project with page navigation.
// Status indicators (loading, retry errors) appear above the list, and a
// key hint bar is pinned at the bottom.
func renderPipelineListPane(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	page := max(1, m.pipelineView.page)
	total := max(1, m.pipelineView.totalPages)
	title := fmt.Sprintf("Pipelines · %s · Page %d/%d", m.pipelineView.project.PathWithNamespace, page, total)
	if m.pipelineView.loading && len(m.pipelineView.pipelines) > 0 {
		title += " (refreshing)"
	}
	b.WriteString(paneHeaderStyle(focused).Render(clampLine(title, width)))
	b.WriteString("\n")
	if m.pipelineView.loading && len(m.pipelineView.pipelines) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading pipelines...", width)))
		b.WriteString("\n")
	}
	if m.pipelineView.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+m.pipelineView.err.Error(), width)))
		b.WriteString("\n")
	}
	if m.pipelineView.retrying {
		b.WriteString(explorerHintStyle.Render(clampLine(" Retrying...", width)))
		b.WriteString("\n")
	}
	if m.pipelineView.retryErr != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" Retry failed: "+m.pipelineView.retryErr.Error(), width)))
		b.WriteString("\n")
	}
	if len(m.pipelineView.pipelines) == 0 && !m.pipelineView.loading && m.pipelineView.err == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" "+msgNoPipelines+".", width)))
		b.WriteString("\n")
	}
	// Render pipeline list using bubbles list component
	if len(m.pipelineView.pipelines) > 0 {
		// Calculate available height for list
		headerLines := 1 // title
		if m.pipelineView.loading && len(m.pipelineView.pipelines) > 0 {
			headerLines++
		}
		if m.pipelineView.err != nil {
			headerLines++
		}
		if m.pipelineView.retrying {
			headerLines++
		}
		if m.pipelineView.retryErr != nil {
			headerLines++
		}

		listHeight := max(1, height-headerLines-1) // -1 for hint at bottom
		m.pipelineView.pipelineList.SetSize(width, listHeight)
		b.WriteString(m.pipelineView.pipelineList.View())
		b.WriteString("\n")
	}
	content := lipgloss.NewStyle().Width(width).Render(strings.TrimSuffix(b.String(), "\n"))
	hint := explorerHintStyle.Render(clampLine(" ← back · → stages · R retry · [ ] page · r refresh · Ctrl+O copy", width))
	return renderWithBottomHint(content, hint, height)
}

// renderPipelineStagesPane renders the middle pane: stages and jobs for the
// selected pipeline, displayed as a navigable table. Shows pipeline ref and
// ID as context, and loads stage/job data asynchronously.
func renderPipelineStagesPane(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	pipeline := m.selectedPipeline()
	title := "Stages"
	if pipeline != nil {
		stages, _ := m.pipelineView.stages.Get(pipeline.ID)
		if m.pipelineView.stages.IsLoading(pipeline.ID) && len(stages) > 0 {
			title += " (refreshing)"
		}
	}
	b.WriteString(paneHeaderStyle(focused).Render(clampLine(title, width)))
	b.WriteString("\n")
	hint := explorerHintStyle.Render(clampLine(" j/k stages · ← back · J/K logs · R retry · C cancel · P play · Ctrl+O copy", width))
	finalize := func() string {
		content := lipgloss.NewStyle().Width(width).Render(strings.TrimSuffix(b.String(), "\n"))
		return renderWithBottomHint(content, hint, height)
	}
	if pipeline == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a pipeline to see stages.", width)))
		b.WriteString("\n")
		return finalize()
	}
	b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Pipeline: #%d", pipeline.ID), width)))
	b.WriteString("\n")
	if pipeline.Ref != "" {
		b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Ref: %s", pipeline.Ref), width)))
		b.WriteString("\n")
	}
	stages, _ := m.pipelineView.stages.Get(pipeline.ID)
	jobs, _ := m.pipelineView.jobs.Get(pipeline.ID)
	if m.pipelineView.stages.IsLoading(pipeline.ID) && len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading stages...", width)))
		b.WriteString("\n")
		return finalize()
	}
	if m.pipelineView.jobs.IsLoading(pipeline.ID) && len(jobs) == 0 && len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading jobs...", width)))
		b.WriteString("\n")
	}
	if err := m.pipelineView.jobs.Err(pipeline.ID); err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+err.Error(), width)))
		b.WriteString("\n")
	}
	if err := m.pipelineView.stages.Err(pipeline.ID); err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+err.Error(), width)))
		b.WriteString("\n")
		if len(stages) == 0 {
			return finalize()
		}
	}
	if len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" No stage data available.", width)))
		b.WriteString("\n")
		return finalize()
	}

	// Render the table
	b.WriteString(m.pipelineView.stageTable.View())
	b.WriteString("\n")

	return finalize()
}

// renderPipelineLogPane renders the right pane: job log output in a scrollable
// viewport. The header shows [LIVE] when auto-following new output or [PAUSED]
// when the user has scrolled away from the bottom.
func renderPipelineLogPane(m Model, width int, focused bool) string {
	b := &strings.Builder{}
	title := "Log Preview"
	job := m.pipelineLogJob()
	if job != nil {
		title = fmt.Sprintf("Log · %s", job.Name)
	}
	if job != nil {
		if m.pipelineView.logAutoFollow {
			title += " [LIVE]"
		} else {
			title += " [PAUSED]"
		}
	}
	if m.pipelineView.logPreview.loading && m.pipelineView.logPreview.content != "" {
		title += " (refreshing)"
	}
	b.WriteString(paneHeaderStyle(focused).Render(clampLine(title, width)))
	b.WriteString("\n")
	preview := m.pipelineView.logPreview
	if preview.loading && preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading job log...", width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.err != nil && preview.content == "" {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+preview.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a stage to preview logs.", width)))
		b.WriteString("\n")
		return b.String()
	}

	// Use viewport for scrolling
	b.WriteString(m.pipelineView.logViewport.View())
	return b.String()
}

// renderPipelineRetryConfirmModal renders the confirmation dialog before
// retrying a pipeline or individual job. Shows different context (job name,
// stage, downstream project) depending on whether it is a job or pipeline retry.
func renderPipelineRetryConfirmModal(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	innerWidth := min(68, width-10)
	if innerWidth < 24 {
		innerWidth = max(12, width-6)
	}
	b := &strings.Builder{}
	isDownstream := m.pipelineView.confirmRetryProjectID != 0
	title := fmt.Sprintf("Retry Pipeline · %s", m.pipelineView.project.PathWithNamespace)
	if m.pipelineView.confirmRetryIsJob {
		if isDownstream {
			title = fmt.Sprintf("Retry Downstream Job · %s", m.pipelineView.project.PathWithNamespace)
		} else {
			title = fmt.Sprintf("Retry Job · %s", m.pipelineView.project.PathWithNamespace)
		}
	}
	b.WriteString(detailHeaderStyle.Render(clampLine(title, innerWidth)))
	b.WriteString("\n")
	if m.pipelineView.confirmRetryIsJob {
		jobLabel := "Job: (unknown)"
		if m.pipelineView.confirmRetryJobID != 0 {
			if name := strings.TrimSpace(m.pipelineView.confirmRetryJobName); name != "" {
				jobLabel = fmt.Sprintf("Job: %s (#%d)", name, m.pipelineView.confirmRetryJobID)
			} else {
				jobLabel = fmt.Sprintf("Job: #%d", m.pipelineView.confirmRetryJobID)
			}
		}
		b.WriteString(explorerPathStyle.Render(clampLine(jobLabel, innerWidth)))
		b.WriteString("\n")
		if stage := strings.TrimSpace(m.pipelineView.confirmRetryJobStage); stage != "" {
			b.WriteString(explorerPathStyle.Render(clampLine("Stage: "+stage, innerWidth)))
			b.WriteString("\n")
		}
		if m.pipelineView.confirmRetryID != 0 {
			b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Pipeline: #%d", m.pipelineView.confirmRetryID), innerWidth)))
			b.WriteString("\n")
		}
		if isDownstream {
			b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Project: %d (downstream)", m.pipelineView.confirmRetryProjectID), innerWidth)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		hint := "This will retry the selected job only."
		if isDownstream {
			hint = "This will retry the downstream pipeline job."
		}
		b.WriteString(explorerHintStyle.Render(clampLine(hint, innerWidth)))
		b.WriteString("\n\n")
		b.WriteString(explorerHintStyle.Render(clampLine("Enter to retry job · Esc to cancel", innerWidth)))
	} else {
		pipelineLabel := "Pipeline: (unknown)"
		if m.pipelineView.confirmRetryID != 0 {
			pipelineLabel = fmt.Sprintf("Pipeline: #%d", m.pipelineView.confirmRetryID)
		}
		b.WriteString(explorerPathStyle.Render(clampLine(pipelineLabel, innerWidth)))
		b.WriteString("\n")
		if ref := strings.TrimSpace(m.pipelineView.confirmRetryRef); ref != "" {
			b.WriteString(explorerPathStyle.Render(clampLine("Ref: "+ref, innerWidth)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(explorerHintStyle.Render(clampLine("This will retry failed jobs or start a new pipeline run.", innerWidth)))
		b.WriteString("\n\n")
		b.WriteString(explorerHintStyle.Render(clampLine("Enter to retry pipeline · Esc to cancel", innerWidth)))
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rosePineSubtle).
		Padding(1, 2).
		Width(innerWidth).
		Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}
