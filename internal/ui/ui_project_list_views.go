package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
)

// View renders the UI to the terminal.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	// Render help overlay if requested
	if m.showHelp {
		return m.renderHelpView(width)
	}

	var mainView string
	switch m.mode {
	case modeExplorer:
		mainView = renderExplorerView(m, width)
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
		mainView = base
	default:
		mainView = renderProjectsView(m, width)
	}

	// Add help bar at bottom
	return mainView + "\n" + m.renderHelpBar()
}

// renderHelpView shows the full help overlay
func (m Model) renderHelpView(width int) string {
	var keys []key.Binding
	switch m.mode {
	case modeProjects:
		keys = projectsKeyMap()
	case modeExplorer:
		keys = explorerKeyMap()
	case modePipelines:
		keys = pipelinesKeyMap()
	default:
		keys = projectsKeyMap()
	}

	// Convert to 2D array for multi-column layout (3 columns)
	cols := 3
	var keyGroups [][]key.Binding
	for i := 0; i < len(keys); i += cols {
		end := min(i+cols, len(keys))
		keyGroups = append(keyGroups, keys[i:end])
	}

	helpView := m.help.FullHelpView(keyGroups)
	title := titleStyle.Render("Help - Press ? or Esc to close")

	content := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rosePineSubtle).
		Padding(1, 2).
		Width(width - 4).
		Render(title + "\n\n" + helpView)

	return content
}

// renderHelpBar shows the condensed help at the bottom
func (m Model) renderHelpBar() string {
	return m.help.ShortHelpView(m.keys.ShortHelp())
}

const paneGap = 1

func renderPane(content string, width, height int) string {
	lines := normalizeColumn(content, width, height)
	return paneBorderStyle.Render(strings.Join(lines, "\n"))
}

func renderPaneGap(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render("")
}

func renderProjectsView(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	minInnerWidth := 22
	minTotalWidth := minInnerWidth*2 + 4 + paneGap
	if width < minTotalWidth {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for project view.")
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	innerTotal := width - paneGap - 4
	listInner := max(minInnerWidth, innerTotal*45/100)
	detailInner := innerTotal - listInner
	if detailInner < minInnerWidth {
		detailInner = minInnerWidth
		listInner = max(minInnerWidth, innerTotal-detailInner)
	}
	listPane := renderPane(renderListPane(m, listInner, contentHeight), listInner, contentHeight)
	detailPane := renderPane((&m).cachedDetailPane(detailInner, contentHeight), detailInner, contentHeight)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, listPane, gap, detailPane)
}

func renderListPane(m Model, width, height int) string {
	b := &strings.Builder{}
	title := titleStyle.Render(clampLine(renderListTitle(m), width))
	b.WriteString(title)
	b.WriteString("\n")

	// Set list size and render
	list := m.projectList
	list.SetSize(width, height-2) // Reserve space for title
	list.SetWidth(width)
	listView := list.View()

	content := lipgloss.NewStyle().Width(width).Render(listView)
	bottomLines := make([]string, 0, 5)
	switch {
	case m.err != nil:
		bottomLines = append(bottomLines, errorStyle.Render(clampLine(" "+m.err.Error(), width)))
	case len(m.allProjects) == 0 && !m.loading:
		bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine(" No projects found.", width)))
	case m.loading && len(m.allProjects) == 0:
		loadMsg := fmt.Sprintf(" %s Loading projects...", m.spinner.View())
		bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine(loadMsg, width)))
	case m.search.query == "" && !m.pagesReady[m.page] && !m.loading:
		loadMsg := fmt.Sprintf(" %s Page %d is still loading...", m.spinner.View(), m.page)
		bottomLines = append(bottomLines, explorerHintStyle.Render(clampLine(loadMsg, width)))
	}
	if progress := renderProgressBar(m, width); progress != "" {
		bottomLines = append(bottomLines, progress)
	}
	if m.status != "" {
		bottomLines = append(bottomLines, statusStyle.Render(clampLine(" "+m.status, width)))
	}
	// Add paginator if multiple pages
	if m.totalPages > 1 && m.search.query == "" {
		paginatorView := lipgloss.NewStyle().Foreground(rosePineMuted).Render(" " + m.paginator.View())
		bottomLines = append(bottomLines, paginatorView)
	}
	bottomLines = append(bottomLines, renderSearchBar(m, width))
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
	visIcon := visibilityIcon(project.Visibility)
	writeDetailKV(b, "Visibility", fmt.Sprintf("%s %s", visIcon, project.Visibility), width)
	if project.StarCount > 0 {
		writeDetailKV(b, "Stars", fmt.Sprintf("%s %d", iconStar, project.StarCount), width)
	}
	if !project.LastActivityAt.IsZero() {
		writeDetailKV(b, "Last Activity", fmt.Sprintf("%s %s", iconClock, formatTimeAgo(project.LastActivityAt)), width)
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

	// Render the table
	b.WriteString(m.pipelineView.stageTable.View())
	b.WriteString("\n")

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

	// Use viewport for scrolling
	b.WriteString(m.pipelineView.logViewport.View())
	return b.String()
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
	minInnerWidth := 4
	minTotalWidth := minInnerWidth*3 + 6 + paneGap*2
	if width < minTotalWidth {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for explorer view.")
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	innerTotal := width - paneGap*2 - 6
	parentInner := max(minInnerWidth, innerTotal*20/100)
	currentInner := max(minInnerWidth, innerTotal*40/100)
	previewInner := innerTotal - parentInner - currentInner
	if previewInner < minInnerWidth {
		previewInner = minInnerWidth
		currentInner = max(minInnerWidth, innerTotal-parentInner-previewInner)
	}
	parentPane := renderPane(renderExplorerParents(m, parentInner), parentInner, contentHeight)
	currentPane := renderPane(renderExplorerCurrent(m, currentInner, contentHeight), currentInner, contentHeight)
	previewPane := renderPane(renderExplorerPreview(m, previewInner, contentHeight), previewInner, contentHeight)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, parentPane, gap, currentPane, gap, previewPane)
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

	// Render action menu using bubbles list
	m.actionMenu.menuList.SetSize(innerWidth, len(projectActionOptions))
	b.WriteString(m.actionMenu.menuList.View())

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
	minInnerWidth := 10
	minTotalWidth := minInnerWidth*3 + 6 + paneGap*2
	if width < minTotalWidth {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for pipeline view.")
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	innerTotal := width - paneGap*2 - 6
	parentInner := max(minInnerWidth, innerTotal*30/100)
	currentInner := max(minInnerWidth, innerTotal*25/100)
	previewInner := innerTotal - parentInner - currentInner
	if previewInner < minInnerWidth {
		previewInner = minInnerWidth
		currentInner = max(minInnerWidth, innerTotal-parentInner-previewInner)
	}
	parentPane := renderPane(renderPipelineListPane(m, parentInner, contentHeight), parentInner, contentHeight)
	currentPane := renderPane(renderPipelineStagesPane(m, currentInner, contentHeight), currentInner, contentHeight)
	previewPane := renderPane(renderPipelineLogPane(m, previewInner, contentHeight), previewInner, contentHeight)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, parentPane, gap, currentPane, gap, previewPane)
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
	if m.pipelineView.confirmRetryIsJob {
		title = fmt.Sprintf("Retry Job · %s", m.pipelineView.project.PathWithNamespace)
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
		b.WriteString("\n")
		b.WriteString(explorerHintStyle.Render(clampLine("This will retry the selected job only.", innerWidth)))
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

	// Use viewport for scrolling
	b.WriteString(m.explorer.preview.viewport.View())
	return b.String()
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
