package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazylab/internal/gitlab"
)

// View renders the UI to the terminal.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	switch m.mode {
	case modeExplorer:
		return renderExplorerView(m, width)
	case modeProjectActions:
		base := renderProjectsView(m, width)
		modal := renderProjectActionModal(m, width)
		return overlayCentered(base, modal, width)
	case modePipelines:
		base := renderPipelineView(m, width)
		if m.pipelineView.confirmRetry {
			modal := renderPipelineRetryConfirmModal(m, width)
			return overlayCentered(base, modal, width)
		}
		return base
	}
	return renderProjectsView(m, width)
}

func renderProjectsView(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	if width < 50 {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for project view.")
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	listWidth := max(24, width*45/100)
	detailWidth := width - listWidth
	if detailWidth < 24 {
		detailWidth = 24
		listWidth = max(24, width-detailWidth)
	}
	contentHeight := height - 2
	listLines := normalizeColumn(renderListPane(m, listWidth-2, contentHeight), listWidth-2, contentHeight)
	detailLines := normalizeColumn(renderDetailPane(m, detailWidth-2, contentHeight), detailWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("╔" + strings.Repeat("═", listWidth-2) + "╦" + strings.Repeat("═", detailWidth-2) + "╗"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s\n",
			explorerBorderStyle.Render("║"),
			listLines[i],
			explorerBorderStyle.Render("║"),
			detailLines[i],
			explorerBorderStyle.Render("║"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("╚" + strings.Repeat("═", listWidth-2) + "╩" + strings.Repeat("═", detailWidth-2) + "╝"))
	return b.String()
}

func renderListPane(m Model, width, height int) string {
	b := &strings.Builder{}
	title := titleStyle.Render(clampLine(renderListTitle(m), width))
	b.WriteString(title)
	b.WriteString("\n")
	visible := m.visibleProjects()
	for i, p := range visible {
		cursor := " "
		style := itemStyle
		if i == m.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(fmt.Sprintf("%s %s", cursor, p.PathWithNamespace), width)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	content := lipgloss.NewStyle().Width(width).Render(strings.TrimSuffix(b.String(), "\n"))
	bottomLines := make([]string, 0, 5)
	switch {
	case m.err != nil:
		bottomLines = append(bottomLines, errorStyle.Render(clampLine(" "+m.err.Error(), width)))
	case len(m.allProjects) == 0 && !m.loading:
		bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine(" No projects found.", width)))
	case m.loading && len(m.allProjects) == 0:
		bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine(" Loading projects...", width)))
	case m.search.query == "" && !m.pagesReady[m.page] && !m.loading:
		bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine(fmt.Sprintf(" Page %d is still loading...", m.page), width)))
	}
	if progress := renderProgressBar(m, width); progress != "" {
		bottomLines = append(bottomLines, progress)
	}
	if m.status != "" {
		bottomLines = append(bottomLines, statusStyle.Render(clampLine(" "+m.status, width)))
	}
	bottomLines = append(bottomLines, renderSearchBar(m, width))
	bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine("Enter actions · / search · Ctrl+D/U page · </> jump", width)))
	return renderWithBottomLines(content, bottomLines, height)
}

func renderDetailPane(m Model, width, height int) string {
	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine("Details", width)))
	b.WriteString("\n\n")
	visible := m.visibleProjects()
	if len(visible) == 0 {
		b.WriteString(clampLine(" Select a project to see more information.", width))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	project := visible[m.selected]
	writeDetailSection(b, "Project", width)
	writeDetailKV(b, "Name", project.Name, width)
	writeDetailKV(b, "Path", project.PathWithNamespace, width)
	writeDetailKV(b, "Visibility", project.Visibility, width)
	writeDetailKV(b, "Stars", fmt.Sprintf("%d", project.StarCount), width)
	if !project.LastActivityAt.IsZero() {
		writeDetailKV(b, "Last Activity", project.LastActivityAt.Format(time.RFC1123), width)
	}
	if project.DefaultBranch != "" {
		writeDetailKV(b, "Default Branch", project.DefaultBranch, width)
	}
	writeDetailDivider(b, width)

	writeDetailSection(b, "Links", width)
	writeDetailKV(b, "URL", project.WebURL, width)
	if project.SSHURLToRepo != "" {
		writeDetailKV(b, "Clone", fmt.Sprintf("git clone %s", project.SSHURLToRepo), width)
	}
	if project.Description != "" {
		writeDetailDivider(b, width)
		writeDetailSection(b, "Description", width)
		desc := clampLines(wrapText(project.Description, width), width)
		for _, line := range strings.Split(desc, "\n") {
			b.WriteString(detailValueStyle.Render(line))
			b.WriteString("\n")
		}
	}
	writeDetailDivider(b, width)
	writeDetailSection(b, "Pipeline", width)
	pipe := clampLines(renderPipelineSection(m, project, width), width)
	for _, line := range strings.Split(strings.TrimSuffix(pipe, "\n"), "\n") {
		b.WriteString(detailValueStyle.Render(line))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineListPane(m Model, width, height int) string {
	b := &strings.Builder{}
	page := max(1, m.pipelineView.page)
	total := max(1, m.pipelineView.totalPages)
	title := fmt.Sprintf("Pipelines · %s · Page %d/%d", m.pipelineView.project.PathWithNamespace, page, total)
	if m.pipelineView.loading && len(m.pipelineView.pipelines) > 0 {
		title += " (refreshing)"
	}
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusPipelines {
		header = explorerFocusHeaderStyle
	}
	b.WriteString(header.Render(clampLine(title, width)))
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
		b.WriteString(explorerHintStyle.Render(clampLine(" No pipelines found.", width)))
		b.WriteString("\n")
	}
	labelWidth := 0
	for _, p := range m.pipelineView.pipelines {
		labelWidth = max(labelWidth, ansi.StringWidth(pipelineStatusLabel(p.Status)))
	}
	if labelWidth == 0 {
		labelWidth = ansi.StringWidth(pipelineStatusLabel(""))
	}
	for i, p := range m.pipelineView.pipelines {
		cursor := " "
		if i == m.pipelineView.selected {
			cursor = ">"
		}
		statusBadge := pipelineStatusBadgeWithWidth(p.Status, labelWidth)
		ref := p.Ref
		if ref == "" {
			ref = "unknown-ref"
		}
		timestamp := "unknown-time"
		if !p.UpdatedAt.IsZero() {
			timestamp = p.UpdatedAt.Local().Format("01-02 15:04")
		}
		line := clampLineANSI(fmt.Sprintf("%s %s #%d %s %s", cursor, statusBadge, p.ID, timestamp, ref), width)
		b.WriteString(renderPipelineEntryLine(line, i == m.pipelineView.selected, m.pipelineView.focus == pipelineFocusPipelines))
		b.WriteString("\n")
	}
	content := lipgloss.NewStyle().Width(width).Render(strings.TrimSuffix(b.String(), "\n"))
	hint := explorerHintStyle.Render(clampLine(" ← back · → stages · r refresh · R retry · [ and ] page · Ctrl+D/U page · </> jump", width))
	return renderWithBottomHint(content, hint, height)
}

func renderPipelineStagesPane(m Model, width, height int) string {
	b := &strings.Builder{}
	pipeline := m.selectedPipeline()
	title := "Stages"
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusStages {
		header = explorerFocusHeaderStyle
	}
	if pipeline != nil {
		stages := m.pipelineView.stageCache[pipeline.ID]
		if m.pipelineView.stageLoading[pipeline.ID] && len(stages) > 0 {
			title += " (refreshing)"
		}
	}
	b.WriteString(header.Render(clampLine(title, width)))
	b.WriteString("\n")
	hint := explorerHintStyle.Render(clampLine(" ↑/↓ stages · ← pipelines · J/K scroll logs · Ctrl+D/U page · </> jump", width))
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
	stages := m.pipelineView.stageCache[pipeline.ID]
	jobs := m.pipelineView.jobsCache[pipeline.ID]
	if m.pipelineView.stageLoading[pipeline.ID] && len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading stages...", width)))
		b.WriteString("\n")
		return finalize()
	}
	if m.pipelineView.jobsLoading[pipeline.ID] && len(jobs) == 0 && len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading jobs...", width)))
		b.WriteString("\n")
	}
	if err := m.pipelineView.jobsErr[pipeline.ID]; err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+err.Error(), width)))
		b.WriteString("\n")
	}
	if err := m.pipelineView.stageErr[pipeline.ID]; err != nil {
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
	stageLabelWidth := 0
	for _, stage := range stages {
		stageLabelWidth = max(stageLabelWidth, ansi.StringWidth(pipelineStatusLabel(stage.Status)))
	}
	if stageLabelWidth == 0 {
		stageLabelWidth = ansi.StringWidth(pipelineStatusLabel(""))
	}
	for i, stage := range stages {
		cursor := " "
		if i == m.pipelineView.stageSelected {
			cursor = ">"
		}
		status := stage.Status
		if status == "" {
			status = "unknown"
		}
		summary := stageJobSummary(jobs, stage.Name)
		stageLine := fmt.Sprintf("%s %s %s%s", cursor, pipelineStatusBadgeWithWidth(status, stageLabelWidth), stage.Name, summary)
		b.WriteString(renderPipelineEntryLine(clampLineANSI(stageLine, width), i == m.pipelineView.stageSelected, m.pipelineView.focus == pipelineFocusStages))
		b.WriteString("\n")
	}
	return finalize()
}

func renderPipelineLogPane(m Model, width, height int) string {
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
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusStages {
		header = explorerFocusHeaderStyle
	}
	b.WriteString(header.Render(clampLine(title, width)))
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
	contentLines := pipelineLogContentLines(preview, width)
	visibleHeight := max(0, height-1)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	offset := preview.offset
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if visibleHeight > 0 && len(contentLines) > visibleHeight {
		contentLines = contentLines[offset:min(offset+visibleHeight, len(contentLines))]
	}
	for _, line := range contentLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderPipelineSection(m Model, project gitlab.ProjectNode, width int) string {
	state, ok := m.pipelineStatus[project.ID]
	refLabel := pipelineRefLabel(project, state)
	if refLabel == "" {
		refLabel = "all refs"
	}
	var b strings.Builder
	fmt.Fprintf(&b, " Pipeline (%s):\n", refLabel)
	switch {
	case state.loading && !state.hasInfo:
		b.WriteString("  Loading latest pipeline...\n")
	case state.err != nil:
		b.WriteString("  Error: " + state.err.Error() + "\n")
	case state.empty:
		fmt.Fprintf(&b, "  No pipelines found for %s.\n", refLabel)
	case state.hasInfo:
		fmt.Fprintf(&b, "  Status: %s (#%d)\n", state.info.Status, state.info.ID)
		if state.info.SHA != "" {
			fmt.Fprintf(&b, "  SHA: %s\n", truncate(state.info.SHA, 12))
		}
		if !state.info.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, "  Updated: %s\n", state.info.UpdatedAt.Format(time.RFC1123))
		}
		if state.info.WebURL != "" {
			urlWidth := width - 4
			if urlWidth < 4 {
				urlWidth = width
			}
			fmt.Fprintf(&b, "  URL: %s\n", truncate(state.info.WebURL, urlWidth))
		}
		if len(state.info.Stages) > 0 {
			stageWidth := width - 8
			if stageWidth < 8 {
				stageWidth = width
			}
			b.WriteString("  Stages:\n")
			for _, stage := range state.info.Stages {
				stageName := truncate(stage.Name, stageWidth)
				stageStatus := truncate(stage.Status, stageWidth)
				fmt.Fprintf(&b, "   - %s: %s\n", stageName, stageStatus)
			}
		}
		if state.loading {
			b.WriteString("  Refreshing...\n")
		}
	default:
		if !ok {
			b.WriteString("  Pipeline status pending...\n")
		} else if state.loading {
			b.WriteString("  Refreshing pipeline status...\n")
		} else {
			b.WriteString("  Pipeline status pending...\n")
		}
	}
	if !state.lastFetched.IsZero() {
		fmt.Fprintf(&b, "  Checked: %s\n", state.lastFetched.Format(time.RFC1123))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderExplorerView(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	if width < 18 {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for explorer view.")
	}
	parentWidth := max(6, width*20/100)
	currentWidth := max(6, width*40/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 6 {
		previewWidth = 6
		currentWidth = max(6, width-parentWidth-previewWidth)
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	parentLines := normalizeColumn(renderExplorerParents(m, parentWidth-2), parentWidth-2, contentHeight)
	currentLines := normalizeColumn(renderExplorerCurrent(m, currentWidth-2, contentHeight), currentWidth-2, contentHeight)
	previewLines := normalizeColumn(renderExplorerPreview(m, previewWidth-2, contentHeight), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("╔" + strings.Repeat("═", parentWidth-2) + "╦" + strings.Repeat("═", currentWidth-2) + "╦" + strings.Repeat("═", previewWidth-2) + "╗"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s%s%s\n",
			explorerBorderStyle.Render("║"),
			parentLines[i],
			explorerBorderStyle.Render("║"),
			currentLines[i],
			explorerBorderStyle.Render("║"),
			previewLines[i],
			explorerBorderStyle.Render("║"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("╚" + strings.Repeat("═", parentWidth-2) + "╩" + strings.Repeat("═", currentWidth-2) + "╩" + strings.Repeat("═", previewWidth-2) + "╝"))
	return b.String()
}

func renderProjectActionModal(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	innerWidth := min(60, width-10)
	if innerWidth < 20 {
		innerWidth = max(12, width-6)
	}
	b := &strings.Builder{}
	title := fmt.Sprintf("Project Actions · %s", m.actionMenu.project.PathWithNamespace)
	b.WriteString(detailHeaderStyle.Render(clampLine(title, innerWidth)))
	b.WriteString("\n")
	b.WriteString(explorerHintStyle.Render(clampLine("Choose what to open:", innerWidth)))
	b.WriteString("\n\n")
	for i, option := range projectActionOptions {
		cursor := " "
		style := itemStyle
		if i == m.actionMenu.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(fmt.Sprintf("%s %s", cursor, option), innerWidth)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(explorerHintStyle.Render(clampLine("Enter to select · Esc to cancel", innerWidth)))
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(rosePineSubtle).
		Padding(1, 2).
		Width(innerWidth).
		Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}

func renderPipelineView(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	if width < 24 {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for pipeline view.")
	}
	parentWidth := max(12, width*20/100)
	currentWidth := max(12, width*40/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 12 {
		previewWidth = 12
		currentWidth = max(12, width-parentWidth-previewWidth)
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	parentLines := normalizeColumn(renderPipelineListPane(m, parentWidth-2, contentHeight), parentWidth-2, contentHeight)
	currentLines := normalizeColumn(renderPipelineStagesPane(m, currentWidth-2, contentHeight), currentWidth-2, contentHeight)
	previewLines := normalizeColumn(renderPipelineLogPane(m, previewWidth-2, contentHeight), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("╔" + strings.Repeat("═", parentWidth-2) + "╦" + strings.Repeat("═", currentWidth-2) + "╦" + strings.Repeat("═", previewWidth-2) + "╗"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s%s%s\n",
			explorerBorderStyle.Render("║"),
			parentLines[i],
			explorerBorderStyle.Render("║"),
			currentLines[i],
			explorerBorderStyle.Render("║"),
			previewLines[i],
			explorerBorderStyle.Render("║"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("╚" + strings.Repeat("═", parentWidth-2) + "╩" + strings.Repeat("═", currentWidth-2) + "╩" + strings.Repeat("═", previewWidth-2) + "╝"))
	return b.String()
}

func renderPipelineRetryConfirmModal(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	innerWidth := min(68, width-10)
	if innerWidth < 24 {
		innerWidth = max(12, width-6)
	}
	b := &strings.Builder{}
	title := fmt.Sprintf("Retry Pipeline · %s", m.pipelineView.project.PathWithNamespace)
	b.WriteString(detailHeaderStyle.Render(clampLine(title, innerWidth)))
	b.WriteString("\n")
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
	b.WriteString(explorerHintStyle.Render(clampLine("Enter to retry · Esc to cancel", innerWidth)))
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(rosePineSubtle).
		Padding(1, 2).
		Width(innerWidth).
		Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}

func renderExplorerParents(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString(explorerHeaderStyle.Render(clampLine("Parents", width)))
	b.WriteString("\n")
	parent := m.parentDirState()
	if parent == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" (root)", width)))
		b.WriteString("\n")
		return b.String()
	}
	pathLabel := parent.path
	if pathLabel == "" {
		pathLabel = "/"
	}
	b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Path: %s", pathLabel), width)))
	b.WriteString("\n")
	if parent.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading...", width)))
		b.WriteString("\n")
	}
	if parent.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+parent.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if len(parent.entries) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" (empty)", width)))
		b.WriteString("\n")
		return b.String()
	}
	for i, entry := range parent.entries {
		cursor := " "
		if i == parent.selected {
			cursor = ">"
		}
		name := entry.Name
		if entry.IsDir() {
			name += "/"
		}
		line := clampLine(fmt.Sprintf("%s%s %s", cursor, explorerEntryIcon(entry), name), width)
		b.WriteString(renderExplorerEntryLine(line, entry.IsDir(), i == parent.selected))
		b.WriteString("\n")
	}
	return b.String()
}

func renderExplorerCurrent(m Model, width, height int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Explorer · %s @ %s", m.explorer.project.PathWithNamespace, displayRef(m.explorer))
	b.WriteString(explorerFocusHeaderStyle.Render(clampLine(title, width)))
	b.WriteString("\n")
	hint := explorerHintStyle.Render(clampLine("Enter/→ descend · ←/Esc up · J/K preview · Ctrl+D/U page · </> jump", width))
	finalize := func() string {
		content := strings.TrimSuffix(b.String(), "\n")
		return renderWithBottomHint(content, hint, height)
	}
	cur := m.currentDirState()
	if cur == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" No directory selected.", width)))
		b.WriteString("\n")
		return finalize()
	}
	pathLabel := cur.path
	if pathLabel == "" {
		pathLabel = "/"
	}
	b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Path: %s", pathLabel), width)))
	b.WriteString("\n")
	if cur.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading directory...", width)))
		b.WriteString("\n")
	}
	if cur.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+cur.err.Error(), width)))
		b.WriteString("\n")
		return finalize()
	}
	if len(cur.entries) == 0 && !cur.loading && cur.err == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" Directory is empty.", width)))
		b.WriteString("\n")
	}
	for i, entry := range cur.entries {
		cursor := " "
		if i == cur.selected {
			cursor = ">"
		}
		name := entry.Name
		if entry.IsDir() {
			name += "/"
		}
		line := clampLine(fmt.Sprintf("%s%s %s", cursor, explorerEntryIcon(entry), name), width)
		b.WriteString(renderExplorerEntryLine(line, entry.IsDir(), i == cur.selected))
		b.WriteString("\n")
	}
	return finalize()
}

func renderExplorerPreview(m Model, width, height int) string {
	b := &strings.Builder{}
	b.WriteString(explorerHeaderStyle.Render(clampLine("Preview", width)))
	b.WriteString("\n")
	preview := m.explorer.preview
	if preview.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading file preview...", width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+preview.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a file to preview.", width)))
		b.WriteString("\n")
		return b.String()
	}
	contentLines := previewContentLines(preview, width)
	visibleHeight := max(0, height-1)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	offset := preview.offset
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if visibleHeight > 0 && len(contentLines) > visibleHeight {
		contentLines = contentLines[offset:min(offset+visibleHeight, len(contentLines))]
	}
	for _, line := range contentLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderExplorerEntryLine(line string, isDir, selected bool) string {
	style := explorerFileStyle
	if isDir {
		style = explorerDirStyle
	}
	if selected {
		style = explorerSelectedStyle
	}
	return style.Render(line)
}

func explorerEntryIcon(entry gitlab.TreeNode) string {
	switch entry.Type {
	case "tree":
		return "📁"
	case "commit":
		return "🔗"
	case "blob":
		return "📄"
	default:
		return "•"
	}
}

func renderListTitle(m Model) string {
	if m.search.query != "" {
		return fmt.Sprintf("Projects · Search “%s” (%d matches)", truncate(m.search.query, 20), len(m.visibleProjects()))
	}
	total := max(1, m.totalPages)
	page := max(1, m.page)
	return fmt.Sprintf("Projects · Page %d/%d", page, total)
}

func renderSearchBar(m Model, width int) string {
	var line string
	if m.search.active {
		line = m.search.input.View()
	} else if m.search.query != "" {
		line = fmt.Sprintf("/ %s", m.search.query)
	} else {
		line = "Press / to search"
	}
	return searchStyle.Render(clampLine(line, width))
}

func renderProgressBar(m Model, width int) string {
	if len(m.pagesReady) == 0 || m.pagesLoaded >= len(m.pagesReady) {
		if m.cache != nil {
			return progressStyle.Render("Cache warm")
		}
		return ""
	}
	if m.pagesLoaded < 0 {
		return ""
	}
	loaded := m.pagesLoaded
	total := len(m.pagesReady)
	if total <= 0 {
		return ""
	}
	barWidth := width - 20
	if barWidth < 8 {
		barWidth = 8
	}
	filled := int(float64(barWidth) * (float64(loaded) / float64(total)))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return progressStyle.Render(clampLine(fmt.Sprintf("Caching %d/%d pages [%s]", loaded, total, bar), width))
}

func writeDetailLine(b *strings.Builder, line string, width int) {
	b.WriteString(clampLine(line, width))
	b.WriteString("\n")
}

func writeDetailSection(b *strings.Builder, title string, width int) {
	label := " " + title
	b.WriteString(detailHeaderStyle.Render(clampLine(label, width)))
	b.WriteString("\n")
	writeDetailDivider(b, width)
}

func writeDetailDivider(b *strings.Builder, width int) {
	if width <= 0 {
		return
	}
	b.WriteString(detailDividerStyle.Render(strings.Repeat("─", max(1, width))))
	b.WriteString("\n")
}

func writeDetailKV(b *strings.Builder, label, value string, width int) {
	if value == "" {
		return
	}
	prefix := detailLabelStyle.Render(label + ":")
	line := fmt.Sprintf(" %s %s", prefix, detailValueStyle.Render(value))
	b.WriteString(clampLine(line, width))
	b.WriteString("\n")
}
