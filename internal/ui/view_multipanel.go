// view_multipanel.go is the top-level View function for the multi-panel mode.
//
// It composes the full screen from four sidebar panels (stacked vertically on
// the left), a detail pane (right), and an info bar (bottom). Each panel's
// content is rendered independently by a dispatcher that routes to the
// appropriate content renderer based on PanelID.
//
// The detail pane is context-sensitive: its content depends on which sidebar
// panel the user is in (or came from, when the detail pane itself is focused).
// detailContextPanel resolves this so renderers don't need to check focus state.

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/GF6599/lazylab/internal/redacting"
)

// renderMultiPanelView composes the full screen: sidebar | gap | detail + info bar.
func renderMultiPanelView(m Model, width, height int) string {
	layout := computeLayout(width, height, m.focus)
	if !layout.OK {
		return renderTooSmallView(width, height)
	}

	// Render sidebar panels
	sidebar := renderSidebar(m, layout)

	// Render right area (detail pane)
	rightArea := renderRightArea(m, layout)

	// Join sidebar and right area
	gap := renderPaneGap(paneGap, layout.TotalHeight-infoBarHeight)
	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, gap, rightArea)

	// Info bar at bottom
	infoBar := renderInfoBar(&m, layout.InfoBarWidth)

	return main + "\n" + infoBar
}

// renderSidebar renders all left-side panels stacked vertically.
func renderSidebar(m Model, layout layoutResult) string {
	var panels []string
	for _, panelID := range SidebarPanels {
		h := layout.PanelHeights[panelID]
		focused := m.focus.Active == panelID
		content := renderSidebarPanelContent(m, panelID, layout.SidebarWidth, h)
		tabs, activeTab := panelTabs(panelID, &m)
		footer := panelFooter(panelID, &m)
		scroll := panelScrollInfo(panelID, &m)
		rendered := renderBorderedPane(content, layout.SidebarWidth+borderCharsH, h, focused, panelLabel(panelID), tabs, activeTab, footer, scroll)
		if rendered != "" {
			panels = append(panels, rendered)
		}
	}
	return strings.Join(panels, "\n")
}

// renderSidebarPanelContent renders the content for a sidebar panel.
func renderSidebarPanelContent(m Model, panel PanelID, width, height int) string {
	switch panel {
	case PanelProjects:
		return renderProjectsPanelContent(m, width, height)
	case PanelPipelines:
		return renderPipelinesPanelContent(m, width, height)
	case PanelStages:
		return renderStagesPanelContent(m, width, height)
	case PanelMRs:
		return renderMRsPanel(&m, width, height)
	default:
		return ""
	}
}

// renderProjectsPanelContent renders the projects list for the sidebar.
func renderProjectsPanelContent(m Model, width, height int) string {
	if m.loading && len(m.allProjects) == 0 {
		return explorerHintStyle.Render(clampLine(fmt.Sprintf(" %s Loading projects...", m.spinner.View()), width))
	}
	if m.err != nil {
		return explorerErrorStyle.Render(clampLine(" "+redacting.Redact(m.err.Error()), width))
	}
	visible := m.visibleProjects()
	if len(visible) == 0 && !m.loading {
		if m.projectTab == projectTabFavorites {
			return explorerHintStyle.Render(clampLine(" No favorites yet (press f)", width))
		}
		return explorerHintStyle.Render(clampLine(" No projects found", width))
	}

	content := m.projectList.View()

	// Add search bar at bottom if active
	if m.search.active || m.search.query != "" {
		searchBar := renderSearchBar(m, width)
		return renderWithBottomHint(content, searchBar, height)
	}
	return content
}

// renderPipelinesPanelContent renders the pipelines list for the sidebar.
func renderPipelinesPanelContent(m Model, width, height int) string {
	if m.pipelineView.project.ID == 0 {
		return explorerHintStyle.Render(clampLine(" Select a project", width))
	}
	if m.pipelineView.loading && len(m.pipelineView.pipelines) == 0 {
		return explorerHintStyle.Render(clampLine(" Loading pipelines...", width))
	}
	if m.pipelineView.err != nil {
		return explorerErrorStyle.Render(clampLine(" "+formatLoadErr("pipelines", m.pipelineView.err), width))
	}
	if len(m.pipelineView.pipelines) == 0 {
		return explorerHintStyle.Render(clampLine(" "+msgNoPipelines, width))
	}
	return m.pipelineView.pipelineList.View()
}

// renderStagesPanelContent renders the stages for the sidebar.
func renderStagesPanelContent(m Model, width, height int) string {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return explorerHintStyle.Render(clampLine(" Select a pipeline", width))
	}

	stages, _ := m.pipelineView.stages.Get(pipeline.ID)
	if m.pipelineView.stages.IsLoading(pipeline.ID) && len(stages) == 0 {
		return explorerHintStyle.Render(clampLine(" Loading stages...", width))
	}
	if err := m.pipelineView.stages.Err(pipeline.ID); err != nil {
		return explorerErrorStyle.Render(clampLine(" "+formatLoadErr("stages", err), width))
	}
	if len(stages) == 0 {
		return explorerHintStyle.Render(clampLine(" "+msgNoStages, width))
	}

	// Reserve 1 line for the selected job name hint when it would be truncated.
	hint := stageTableSelectedHint(&m, width)
	content := styleStageTable(m.pipelineView.stageTable.View(), m.pipelineView.stageTable.SelectedRow())
	if hint != "" {
		return renderWithBottomHint(content, hint, height)
	}
	return content
}

// renderRightArea renders the detail pane.
func renderRightArea(m Model, layout layoutResult) string {
	detailContent := renderDetailContent(m, layout.DetailWidth, layout.DetailHeight)
	detailFocused := m.focus.Active == PanelDetail
	detailTitle := detailPaneTitle(&m)
	detailTabs, detailActiveTab := detailPaneTabs(&m)
	scroll := detailScrollInfo(&m)
	return renderBorderedPane(detailContent, layout.DetailWidth+borderCharsH, layout.DetailHeight, detailFocused, detailTitle, detailTabs, detailActiveTab, "", scroll)
}

// detailContextPanel resolves which sidebar panel determines what the detail
// pane renders. When the detail pane is focused, the user got there via l/right
// from a sidebar panel, so PrevActive tells us the context.
func detailContextPanel(m *Model) PanelID {
	if m.focus.Active == PanelDetail {
		return m.focus.PrevActive
	}
	return m.focus.Active
}

// renderDetailContent dispatches to the correct renderer based on context panel.
func renderDetailContent(m Model, width, height int) string {
	switch detailContextPanel(&m) {
	case PanelProjects:
		return m.renderDetailCached(width, height)
	case PanelPipelines, PanelStages:
		return renderPipelineDetailContent(m, width, height)
	case PanelMRs:
		return renderMRDetailContent(&m, width, height)
	default:
		return m.renderDetailCached(width, height)
	}
}

// renderPipelineDetailContent dispatches to the correct tab content.
func renderPipelineDetailContent(m Model, width, height int) string {
	switch m.pipelineView.detailTab {
	case detailTabTests:
		return renderTestReportContent(&m, width)
	case detailTabArtifacts:
		return renderArtifactsContent(&m, width)
	default:
		return renderPipelineLogContent(m, width, height)
	}
}

// renderPipelineLogContent renders the job log in the detail pane.
func renderPipelineLogContent(m Model, width, height int) string {
	preview := m.pipelineView.logPreview
	b := &strings.Builder{}

	job := m.pipelineLogJob()
	if job != nil {
		title := fmt.Sprintf("Job: %s (#%d)", job.Name, job.ID)
		b.WriteString(detailHeaderStyle.Render(clampLine(title, width)))
		b.WriteString("\n")
		writeDetailKV(b, "Stage", job.Stage, width)
		writeDetailKV(b, "Status", job.Status, width)
		if job.FailureReason != "" {
			writeDetailKV(b, "Failure", job.FailureReason, width)
		}
		writeDetailKV(b, "Elapsed", jobElapsed(*job, time.Now()), width)
		writeDetailKV(b, "Pipeline", m.pipelineElapsed(time.Now()), width)
		writeDetailDivider(b, width)
	}

	if m.pipelineView.logAutoFollow && job != nil {
		b.WriteString(infoBarStatusStyle.Render("[LIVE] "))
	}

	if preview.loading && preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading job log...", width)))
		return b.String()
	}
	if preview.err != nil && preview.content == "" {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+formatLoadErr("job log", preview.err), width)))
		return b.String()
	}
	if preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a stage to preview logs", width)))
		return b.String()
	}

	b.WriteString(m.pipelineView.logViewport.View())
	return b.String()
}

// renderTestReportContent renders the pipeline test report in the detail pane.
func renderTestReportContent(m *Model, width int) string {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return explorerHintStyle.Render(clampLine(" Select a pipeline", width))
	}
	if m.pipelineView.testReportLoading {
		return explorerHintStyle.Render(clampLine(" Loading test report...", width))
	}
	if m.pipelineView.testReportErr != nil {
		return explorerErrorStyle.Render(clampLine(" "+formatLoadErr("test report", m.pipelineView.testReportErr), width))
	}
	if m.pipelineView.testReport == nil || m.pipelineView.testReportPipelineID != pipeline.ID {
		return explorerHintStyle.Render(clampLine(" Press 't' to load test report", width))
	}

	report := m.pipelineView.testReport
	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine(fmt.Sprintf("Test Report · Pipeline #%d", pipeline.ID), width)))
	b.WriteString("\n")
	writeDetailKV(b, "Total", fmt.Sprintf("%d", report.TotalCount), width)
	writeDetailKV(b, "Passed", fmt.Sprintf("%d", report.SuccessCount), width)
	writeDetailKV(b, "Failed", fmt.Sprintf("%d", report.FailedCount), width)
	writeDetailKV(b, "Skipped", fmt.Sprintf("%d", report.SkippedCount), width)
	writeDetailKV(b, "Errors", fmt.Sprintf("%d", report.ErrorCount), width)
	if report.TotalTime > 0 {
		writeDetailKV(b, "Time", fmt.Sprintf("%.2fs", report.TotalTime), width)
	}

	// Show failing test cases
	for _, suite := range report.Suites {
		if suite.FailedCount == 0 && suite.ErrorCount == 0 {
			continue
		}
		writeDetailDivider(b, width)
		writeDetailSection(b, fmt.Sprintf("%s (%d failed)", suite.Name, suite.FailedCount), width)
		for _, tc := range suite.Cases {
			if tc.Status != "failed" && tc.Status != "error" {
				continue
			}
			b.WriteString(explorerErrorStyle.Render(clampLine(fmt.Sprintf("  %s %s", pipelineStatusIcon("failed"), tc.Name), width)))
			b.WriteString("\n")
			if tc.Classname != "" {
				b.WriteString(explorerHintStyle.Render(clampLine(fmt.Sprintf("    class: %s", tc.Classname), width)))
				b.WriteString("\n")
			}
			if tc.SystemOutput != "" {
				lines := strings.Split(tc.SystemOutput, "\n")
				for i, line := range lines {
					if i >= 5 {
						b.WriteString(explorerHintStyle.Render(clampLine("    ...", width)))
						b.WriteString("\n")
						break
					}
					b.WriteString(clampLine("    "+line, width))
					b.WriteString("\n")
				}
			}
		}
	}
	return b.String()
}

// renderArtifactsContent renders job artifacts in the detail pane.
func renderArtifactsContent(m *Model, width int) string {
	job := m.pipelineLogJob()
	if job == nil {
		return explorerHintStyle.Render(clampLine(" Select a stage to view artifacts", width))
	}
	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine(fmt.Sprintf("Artifacts · %s (#%d)", job.Name, job.ID), width)))
	b.WriteString("\n")

	if job.ArtifactsCount == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" No artifacts for this job", width)))
		return b.String()
	}

	writeDetailKV(b, "Count", fmt.Sprintf("%d", job.ArtifactsCount), width)
	if !job.ArtifactsExpireAt.IsZero() {
		writeDetailKV(b, "Expires", formatTimeAgo(job.ArtifactsExpireAt), width)
	}
	writeDetailDivider(b, width)

	for _, a := range job.Artifacts {
		sizeStr := formatBytes(a.Size)
		line := fmt.Sprintf("  %s  %s (%s)", a.FileType, a.Filename, sizeStr)
		b.WriteString(clampLine(line, width))
		b.WriteString("\n")
	}

	// Show bridges (child pipelines) if any
	pipeline := m.selectedPipeline()
	if pipeline != nil {
		if bridges, _ := m.pipelineView.bridges.Get(pipeline.ID); len(bridges) > 0 {
			writeDetailDivider(b, width)
			writeDetailSection(b, "Child Pipelines", width)
			frames := m.statusFrames()
			for _, bridge := range bridges {
				icon := frames.icon(bridge.Status)
				line := fmt.Sprintf("  %s %s", icon, bridge.Name)
				if bridge.DownstreamPipeline != nil {
					line += fmt.Sprintf(" -> #%d (%s)", bridge.DownstreamPipeline.ID, bridge.DownstreamPipeline.Status)
				}
				b.WriteString(clampLine(line, width))
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

// renderMRDetailContent renders MR detail in the right pane, dispatching by tab.
func renderMRDetailContent(m *Model, width, height int) string {
	switch m.mrView.detailTab {
	case mrDetailTabComments:
		return renderMRCommentsPane(m, width, height)
	case mrDetailTabDiff:
		return renderMRDiffPane(m, width, height)
	default:
		return renderMRInfoContent(m, width)
	}
}

// renderMRInfoContent renders basic MR metadata (the original Info tab).
func renderMRInfoContent(m *Model, width int) string {
	if len(m.mrView.mrs) == 0 || m.mrView.selected >= len(m.mrView.mrs) {
		return explorerHintStyle.Render(clampLine(" Select a merge request", width))
	}
	mr := m.mrView.mrs[m.mrView.selected]
	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine(fmt.Sprintf("!%d %s", mr.IID, mr.Title), width)))
	b.WriteString("\n")
	writeDetailKV(b, "State", mr.State, width)
	writeDetailKV(b, "Author", mr.Author, width)
	writeDetailKV(b, "Source", mr.SourceBranch, width)
	writeDetailKV(b, "Target", mr.TargetBranch, width)
	if mr.WebURL != "" {
		writeDetailKV(b, "URL", clampLine(mr.WebURL, width-10), width)
	}
	return b.String()
}

// renderMRCommentsPane renders the MR comments/discussions tab.
func renderMRCommentsPane(m *Model, width, height int) string {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return explorerHintStyle.Render(clampLine(" Select a merge request", width))
	}
	if m.mrView.discussions.IsLoading(mr.IID) {
		return explorerHintStyle.Render(clampLine(" Loading discussions...", width))
	}
	if err := m.mrView.discussions.Err(mr.IID); err != nil {
		return explorerErrorStyle.Render(clampLine(" "+formatLoadErr("discussions", err), width))
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return explorerHintStyle.Render(clampLine(" Press 't' to load comments", width))
	}
	if len(discussions) == 0 {
		return explorerHintStyle.Render(clampLine(" No discussions", width))
	}
	return m.mrView.mrViewport.View()
}

// renderMRDiffPane renders the MR diff tab.
func renderMRDiffPane(m *Model, width, height int) string {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return explorerHintStyle.Render(clampLine(" Select a merge request", width))
	}
	if m.mrView.diffs.IsLoading(mr.IID) {
		return explorerHintStyle.Render(clampLine(" Loading diffs...", width))
	}
	if err := m.mrView.diffs.Err(mr.IID); err != nil {
		return explorerErrorStyle.Render(clampLine(" "+formatLoadErr("diff", err), width))
	}
	diffs, ok := m.mrView.diffs.Get(mr.IID)
	if !ok {
		return explorerHintStyle.Render(clampLine(" Press 't' to load diff", width))
	}
	if len(diffs) == 0 {
		return explorerHintStyle.Render(clampLine(" No changes", width))
	}
	return m.mrView.mrViewport.View()
}

// detailPaneTitle returns the title for the detail pane based on context.
func detailPaneTitle(m *Model) string {
	switch detailContextPanel(m) {
	case PanelProjects:
		// The rule carries the path and the body carries the heading, so the two
		// never spend their space saying the same word.
		if proj, ok := m.selectedProject(); ok {
			return clampLine(proj.PathWithNamespace, 40)
		}
		return panelLabel(PanelDetail)
	case PanelPipelines, PanelStages:
		tab := pipelineDetailTabLabels[m.pipelineView.detailTab]
		switch m.pipelineView.detailTab {
		case detailTabLog:
			if job := m.pipelineLogJob(); job != nil {
				suffix := " [LIVE]"
				if !m.pipelineView.logAutoFollow {
					suffix = " [PAUSED]"
				}
				return tab + " · " + job.Name + suffix
			}
			return tab
		case detailTabTests:
			if p := m.selectedPipeline(); p != nil {
				return fmt.Sprintf("%s · #%d", tab, p.ID)
			}
			return tab
		case detailTabArtifacts:
			if job := m.pipelineLogJob(); job != nil {
				return fmt.Sprintf("%s · %s", tab, job.Name)
			}
			return tab
		default:
			return tab
		}
	case PanelMRs:
		tab := mrDetailTabLabels[m.mrView.detailTab]
		mr := m.mrView.selectedMR()
		if mr != nil {
			return fmt.Sprintf("%s · !%d", tab, mr.IID)
		}
		return tab
	default:
		return "Detail"
	}
}

// detailPaneTabs returns tabs for the detail pane if applicable.
func detailPaneTabs(m *Model) ([]string, int) {
	switch detailContextPanel(m) {
	case PanelPipelines, PanelStages:
		return pipelineDetailTabLabels, int(m.pipelineView.detailTab)
	case PanelMRs:
		return mrDetailTabLabels, int(m.mrView.detailTab)
	default:
		return nil, 0
	}
}

// panelScrollInfo returns the scroll position for a sidebar panel.
func panelScrollInfo(panel PanelID, m *Model) scrollInfo {
	switch panel {
	case PanelProjects:
		return scrollInfo{offset: m.projectList.Index(), total: len(m.visibleProjects())}
	case PanelPipelines:
		return scrollInfo{offset: m.pipelineView.selected, total: len(m.pipelineView.pipelines)}
	case PanelStages:
		return scrollInfo{offset: m.pipelineView.stageSelected, total: len(m.pipelineView.stageJobRows)}
	case PanelMRs:
		return scrollInfo{offset: m.mrView.selected, total: len(m.mrView.mrs)}
	default:
		return scrollInfo{}
	}
}

// detailScrollInfo returns the scroll position for the detail pane.
func detailScrollInfo(m *Model) scrollInfo {
	switch detailContextPanel(m) {
	case PanelPipelines, PanelStages:
		vp := m.pipelineView.logViewport
		total := vp.TotalLineCount()
		if total > vp.Height {
			return scrollInfo{offset: vp.YOffset, total: total}
		}
	case PanelMRs:
		vp := m.mrView.mrViewport
		total := vp.TotalLineCount()
		if total > vp.Height {
			return scrollInfo{offset: vp.YOffset, total: total}
		}
	}
	return scrollInfo{}
}

// panelTabs returns the tab labels and active tab for a sidebar panel.
func panelTabs(panel PanelID, m *Model) ([]string, int) {
	switch panel {
	case PanelProjects:
		return projectTabLabels, int(m.projectTab)
	case PanelPipelines:
		page := max(1, m.pipelineView.page)
		total := max(1, m.pipelineView.totalPages)
		if total > 1 {
			return []string{fmt.Sprintf("Page %d/%d", page, total)}, 0
		}
		return nil, 0
	case PanelMRs:
		return mrTabLabels, int(m.mrView.tab)
	default:
		return nil, 0
	}
}

// styleStageTable colors the status icons in rendered table output and marks the
// current row, which the caller supplies as the table's selected row.
//
// The bracket pair cannot come from the row data here the way it does in every
// other list, because the table widget owns its own row rendering. It is cut
// into the row's outer cell padding instead, which keeps the line the width the
// pane laid out for it.
func styleStageTable(s string, selected table.Row) string {
	replacements := []struct {
		plain   string
		colored string
	}{
		{iconSuccess + " SUCCESS", pipelineStatusStyle("success").Render(iconSuccess) + " SUCCESS"},
		{iconFailed + " FAILED", pipelineStatusStyle("failed").Render(iconFailed) + " FAILED"},
		{iconRunning + " RUNNING", pipelineStatusStyle("running").Render(iconRunning) + " RUNNING"},
		{iconPending + " PENDING", pipelineStatusStyle("pending").Render(iconPending) + " PENDING"},
		{iconPending + " CREATED", pipelineStatusStyle("pending").Render(iconPending) + " CREATED"},
		{iconCanceled + " CANCELED", pipelineStatusStyle("canceled").Render(iconCanceled) + " CANCELED"},
		{iconSkipped + " SKIPPED", pipelineStatusStyle("skipped").Render(iconSkipped) + " SKIPPED"},
		{iconManual + " MANUAL", pipelineStatusStyle("manual").Render(iconManual) + " MANUAL"},
		{iconBlocked + " BLOCKED", pipelineStatusStyle("manual").Render(iconBlocked) + " BLOCKED"},
	}
	lines := strings.Split(s, "\n")
	selectedLine := stageRowLine(lines, selected)
	for i, line := range lines {
		if i == selectedLine {
			lines[i] = markStageRow(line)
			continue
		}
		for _, r := range replacements {
			line = strings.ReplaceAll(line, r.plain, r.colored)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// stageRowLine returns the index of the rendered line carrying row, or -1 when
// the table has scrolled it out of view. The table shifts its window as the
// cursor moves, so the row's line cannot be derived from the cursor index.
func stageRowLine(lines []string, row table.Row) int {
	var wanted []string
	for _, cell := range row {
		if cell = strings.TrimSpace(cell); cell != "" {
			wanted = append(wanted, cell)
		}
	}
	if len(wanted) == 0 {
		return -1
	}
	for i, line := range lines {
		if rowOnLine(ansi.Strip(line), wanted) {
			return i
		}
	}
	return -1
}

// rowOnLine reports whether every cell appears on the line in order.
func rowOnLine(plain string, cells []string) bool {
	for _, cell := range cells {
		at := strings.Index(plain, cell)
		if at < 0 {
			return false
		}
		plain = plain[at+len(cell):]
	}
	return true
}

// markStageRow replaces the row's first and last cell with the bracket pair.
// Both cells are the table's own padding, so the row keeps its width and the
// text between the brackets does not shift.
func markStageRow(line string) string {
	width := ansi.StringWidth(line)
	if width < 3 {
		return line
	}
	// The table writes a foreground into every cell, and a style wrapped around
	// the finished row cannot override a colour already in the string. So drop
	// the cell styling and render the label in the marked colour here.
	plain := ansi.Strip(line)
	inner := ansi.TruncateLeft(ansi.Truncate(plain, width-1, ""), 1, "")
	return markerStyle.Render(markerFlat[0]) + selectedItemStyle.Render(inner) + markerStyle.Render(markerFlat[1])
}
