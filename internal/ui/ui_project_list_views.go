package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"gitlab-tui-codex/internal/gitlab"
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
		return renderProjectActionView(m, width)
	case modePipelines:
		return renderPipelineView(m, width)
	}
	listWidth := width / 2
	detailWidth := width - listWidth

	left := renderListPane(m, listWidth)
	right := renderDetailPane(m, detailWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func renderListPane(m Model, width int) string {
	b := &strings.Builder{}
	title := titleStyle.Render(clampLine(renderListTitle(m), width))
	b.WriteString(title)
	b.WriteString("\n")
	if m.loading {
		b.WriteString(clampLine(" Loading projects...", width))
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString(errorStyle.Render(clampLine(" "+m.err.Error(), width)))
		b.WriteString("\n")
	}
	if len(m.allProjects) == 0 && !m.loading && m.err == nil {
		b.WriteString(clampLine(" No projects found.", width))
		b.WriteString("\n")
	}
	visible := m.visibleProjects()
	if m.search.query == "" && !m.pagesReady[m.page] && !m.loading {
		b.WriteString(clampLine(fmt.Sprintf(" Page %d is still loading...", m.page), width))
		b.WriteString("\n")
	}
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
	progress := renderProgressBar(m, width)
	if progress != "" {
		b.WriteString(progress)
		b.WriteString("\n")
	}
	b.WriteString(renderSearchBar(m, width))
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(statusStyle.Render(clampLine(" "+m.status, width)))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderDetailPane(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString(titleStyle.Render(clampLine("Details", width)))
	b.WriteString("\n")
	visible := m.visibleProjects()
	if len(visible) == 0 {
		b.WriteString(clampLine(" Select a project to see more information.", width))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	project := visible[m.selected]
	writeDetailLine(b, fmt.Sprintf(" Name: %s", project.Name), width)
	writeDetailLine(b, fmt.Sprintf(" Path: %s", project.PathWithNamespace), width)
	writeDetailLine(b, fmt.Sprintf(" Visibility: %s", project.Visibility), width)
	writeDetailLine(b, fmt.Sprintf(" Stars: %d", project.StarCount), width)
	if !project.LastActivityAt.IsZero() {
		writeDetailLine(b, fmt.Sprintf(" Last Activity: %s", project.LastActivityAt.Format(time.RFC1123)), width)
	}
	writeDetailLine(b, fmt.Sprintf(" URL: %s", project.WebURL), width)
	if project.DefaultBranch != "" {
		writeDetailLine(b, fmt.Sprintf(" Default Branch: %s", project.DefaultBranch), width)
	}
	if project.SSHURLToRepo != "" {
		writeDetailLine(b, fmt.Sprintf(" Clone: git clone %s", project.SSHURLToRepo), width)
	}
	if project.Description != "" {
		b.WriteString("\n")
		b.WriteString(clampLines(wrapText(project.Description, width), width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(clampLines(renderPipelineSection(m, project, width), width))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineListPane(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Pipelines · %s", m.pipelineView.project.PathWithNamespace)
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusPipelines {
		header = explorerHeaderStyle.Bold(true)
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
	if len(m.pipelineView.pipelines) == 0 && !m.pipelineView.loading && m.pipelineView.err == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" No pipelines found.", width)))
		b.WriteString("\n")
	}
	for i, p := range m.pipelineView.pipelines {
		cursor := " "
		if i == m.pipelineView.selected {
			cursor = ">"
		}
		statusBadge := pipelineStatusBadge(p.Status)
		ref := p.Ref
		if ref == "" {
			ref = "unknown-ref"
		}
		line := clampLine(fmt.Sprintf("%s %s #%d %s", cursor, statusBadge, p.ID, ref), width)
		b.WriteString(renderPipelineEntryLine(line, i == m.pipelineView.selected, m.pipelineView.focus == pipelineFocusPipelines))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(explorerHintStyle.Render(clampLine(" ← back · → stages · r refresh", width)))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineStagesPane(m Model, width int) string {
	b := &strings.Builder{}
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusStages {
		header = explorerHeaderStyle.Bold(true)
	}
	b.WriteString(header.Render(clampLine("Stages", width)))
	b.WriteString("\n")
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a pipeline to see stages.", width)))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
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
		return lipgloss.NewStyle().Width(width).Render(b.String())
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
			return lipgloss.NewStyle().Width(width).Render(b.String())
		}
	}
	if len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" No stage data available.", width)))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
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
		stageLine := fmt.Sprintf("%s %s %s%s", cursor, pipelineStatusBadge(status), stage.Name, summary)
		b.WriteString(renderPipelineEntryLine(clampLine(stageLine, width), i == m.pipelineView.stageSelected, m.pipelineView.focus == pipelineFocusStages))
		b.WriteString("\n")
	}
	b.WriteString(explorerHintStyle.Render(clampLine(" ↑/↓ stages · ← pipelines · J/K scroll logs", width)))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineLogPane(m Model, width, height int) string {
	b := &strings.Builder{}
	title := "Log Preview"
	if job := m.pipelineLogJob(); job != nil {
		title = fmt.Sprintf("Log · %s", job.Name)
	}
	b.WriteString(explorerHeaderStyle.Render(clampLine(title, width)))
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
	currentLines := normalizeColumn(renderExplorerCurrent(m, currentWidth-2), currentWidth-2, contentHeight)
	previewLines := normalizeColumn(renderExplorerPreview(m, previewWidth-2, contentHeight), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("┌" + strings.Repeat("─", parentWidth-2) + "┬" + strings.Repeat("─", currentWidth-2) + "┬" + strings.Repeat("─", previewWidth-2) + "┐"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s%s%s\n",
			explorerBorderStyle.Render("│"),
			parentLines[i],
			explorerBorderStyle.Render("│"),
			currentLines[i],
			explorerBorderStyle.Render("│"),
			previewLines[i],
			explorerBorderStyle.Render("│"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("└" + strings.Repeat("─", parentWidth-2) + "┴" + strings.Repeat("─", currentWidth-2) + "┴" + strings.Repeat("─", previewWidth-2) + "┘"))
	return b.String()
}

func renderProjectActionView(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Project Actions · %s", m.actionMenu.project.PathWithNamespace)
	b.WriteString(titleStyle.Render(clampLine(title, width)))
	b.WriteString("\n")
	b.WriteString(clampLine("Choose what to open:", width))
	b.WriteString("\n\n")
	for i, option := range projectActionOptions {
		cursor := " "
		style := itemStyle
		if i == m.actionMenu.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(fmt.Sprintf("%s %s", cursor, option), width)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(statusStyle.Render(clampLine(" Enter to select · Esc to cancel", width)))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
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
	parentLines := normalizeColumn(renderPipelineListPane(m, parentWidth-2), parentWidth-2, contentHeight)
	currentLines := normalizeColumn(renderPipelineStagesPane(m, currentWidth-2), currentWidth-2, contentHeight)
	previewLines := normalizeColumn(renderPipelineLogPane(m, previewWidth-2, contentHeight), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("┌" + strings.Repeat("─", parentWidth-2) + "┬" + strings.Repeat("─", currentWidth-2) + "┬" + strings.Repeat("─", previewWidth-2) + "┐"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s%s%s\n",
			explorerBorderStyle.Render("│"),
			parentLines[i],
			explorerBorderStyle.Render("│"),
			currentLines[i],
			explorerBorderStyle.Render("│"),
			previewLines[i],
			explorerBorderStyle.Render("│"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("└" + strings.Repeat("─", parentWidth-2) + "┴" + strings.Repeat("─", currentWidth-2) + "┴" + strings.Repeat("─", previewWidth-2) + "┘"))
	return b.String()
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

func renderExplorerCurrent(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Explorer · %s @ %s", m.explorer.project.PathWithNamespace, displayRef(m.explorer))
	b.WriteString(explorerHeaderStyle.Render(clampLine(title, width)))
	b.WriteString("\n")
	cur := m.currentDirState()
	if cur == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" No directory selected.", width)))
		b.WriteString("\n")
		return b.String()
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
		return b.String()
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
	b.WriteString(explorerHintStyle.Render(clampLine("Enter/→ descend · ←/Esc up", width)))
	b.WriteString("\n")
	return b.String()
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
	return fmt.Sprintf("Projects · Page %d/%d · Cached %d/%d pages", page, total, m.pagesLoaded, total)
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
