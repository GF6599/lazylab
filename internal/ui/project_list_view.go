package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
)

// View is the top-level Bubble Tea render entry point. It dispatches to the
// active mode's renderer and composites modal overlays (help, retry confirm)
// on top of the base view when needed.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	// Render help overlay if requested
	if m.showHelp {
		return m.renderHelpView(width)
	}

	// Multi-panel mode: the new default
	if m.mode == modeMultiPanel {
		// Explorer overlay on top of multi-panel
		if m.mode == modeMultiPanel && m.explorer.project.ID != 0 && len(m.explorer.stack) > 0 {
			return renderExplorerView(m, width)
		}
		// Reply modal overlay
		if m.mrView.reply.active {
			base := renderMultiPanelView(&m, width, m.height)
			modal := renderMRReplyModal(m, width)
			return overlayCentered(base, modal, width)
		}
		// Retry confirmation modal overlay
		if m.pipelineView.confirmRetry {
			base := renderMultiPanelView(&m, width, m.height)
			modal := renderPipelineRetryConfirmModal(m, width)
			return overlayCentered(base, modal, width)
		}
		return renderMultiPanelView(&m, width, m.height)
	}

	// Legacy modes (kept for backward compatibility during transition)
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

// renderHelpView shows a full-screen help overlay with mode-aware key bindings
// arranged in a 3-column grid. Replaces (not overlays) the entire view.
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

// renderHelpBar returns a single-line hint bar for the bottom of legacy mode views.
func (m Model) renderHelpBar() string {
	return m.help.ShortHelpView(m.keys.ShortHelp())
}

// paneGap is the horizontal spacing between adjacent panes (in characters).
const paneGap = 1

// projectPaneLayout calculates inner widths and content height for the
// two-pane project view (list + detail). Returns (listInner, detailInner,
// contentHeight, ok). ok is false if the terminal is too narrow.
//
// Height budget: terminal height - 4 = content height.
// The 4 reserved lines are: 2 pane borders (top+bottom) + 1 help bar + 1 newline separator.
func projectPaneLayout(width, height int) (int, int, int, bool) {
	if width <= 0 {
		width = 80
	}
	minInnerWidth := 22
	minTotalWidth := minInnerWidth*2 + 4 + paneGap
	if width < minTotalWidth {
		return 0, 0, 0, false
	}
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 4
	innerTotal := width - paneGap - 4
	listInner := max(minInnerWidth, innerTotal*45/100)
	detailInner := innerTotal - listInner
	if detailInner < minInnerWidth {
		detailInner = minInnerWidth
		listInner = max(minInnerWidth, innerTotal-detailInner)
	}
	return listInner, detailInner, contentHeight, true
}

// paneHeaderStyle returns the header style for a pane, using a distinct
// color for the focused pane so users can tell which pane has input focus.
func paneHeaderStyle(focused bool) lipgloss.Style {
	if focused {
		return explorerFocusHeaderStyle
	}
	return explorerHeaderStyle
}

// renderPane wraps content in a bordered box, normalizing it to exact
// width x height so all panes align when joined horizontally.
func renderPane(content string, width, height int, focused bool) string {
	lines := normalizeColumn(content, width, height)
	style := paneBorderStyle
	if focused {
		style = paneBorderFocusStyle
	}
	return style.Render(strings.Join(lines, "\n"))
}

func renderPaneGap(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render("")
}

// renderProjectsView renders the two-pane project view: a scrollable project
// list on the left (45% width) and a detail pane on the right showing metadata,
// pipeline status, and recent commits for the selected project.
func renderProjectsView(m Model, width int) string {
	listInner, detailInner, contentHeight, ok := projectPaneLayout(width, m.height)
	if !ok {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for project view.")
	}
	listPane := renderPane(renderListPane(m, listInner, contentHeight, true), listInner, contentHeight, true)
	detailPane := renderPane((&m).cachedDetailPane(detailInner, contentHeight), detailInner, contentHeight, false)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, listPane, gap, detailPane)
}

// renderListPane builds the project list pane content: header with page/search
// info, the bubbles list component, and bottom status lines (errors, loading
// indicators, paginator, search bar). Bottom lines are pinned via
// renderWithBottomLines so they stay visible regardless of list length.
func renderListPane(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	title := paneHeaderStyle(focused).Render(clampLine(renderListTitle(m), width))
	b.WriteString(title)
	b.WriteString("\n")

	// Set list size and render
	list := m.projectList
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

// renderDetailPane renders project metadata (name, visibility, links),
// latest pipeline status with stage breakdown, and recent commits.
// Takes a pointer receiver because it reads from async caches that may
// be populated by background commands.
func renderDetailPane(m *Model, width, height int) string {
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
	// Recent commits section
	writeDetailDivider(b, width)
	writeDetailSection(b, "Recent Commits", width)
	if m.commitLoading[project.ID] {
		b.WriteString(detailValueStyle.Render(" Loading commits..."))
		b.WriteString("\n")
	} else if commits, ok := m.commitCache[project.ID]; ok && len(commits) > 0 {
		for _, c := range commits {
			timeAgo := formatTimeAgo(c.CreatedAt)
			// sha + 2 spaces + title + 2 spaces + (time ago) = need to fit in width
			maxTitle := width - len(c.ShortID) - len(timeAgo) - 7 // " sha  title  (ago)"
			title := c.Title
			if maxTitle > 0 && len(title) > maxTitle {
				title = title[:maxTitle-1] + "…"
			}
			line := fmt.Sprintf(" %s  %s  (%s)", c.ShortID, title, timeAgo)
			b.WriteString(detailValueStyle.Render(clampLine(line, width)))
			b.WriteString("\n")
		}
	} else {
		b.WriteString(detailValueStyle.Render(" No commits loaded."))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
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
	hint := explorerHintStyle.Render(clampLine(" ← back · → stages · r refresh · R retry · [ and ] page · Ctrl+D/U page · </> jump", width))
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
func renderPipelineLogPane(m Model, width, height int, focused bool) string {
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

// renderPipelineSection builds the pipeline summary shown in the project detail
// pane: status, SHA, timestamp, web URL, and per-stage breakdown. Reflects
// the tri-state loading model (loading/error/ready) from pipelineStatus cache.
func renderPipelineSection(m *Model, project gitlab.ProjectNode, width int) string {
	state, ok := m.pipelineStatus[project.ID]
	refLabel := pipelineRefLabel(project, state)
	if refLabel == "" {
		refLabel = "all refs"
	}
	var b strings.Builder
	b.WriteString(detailLabelStyle.Render(fmt.Sprintf(" Pipeline (%s):", refLabel)) + "\n")
	switch {
	case state.loading && !state.hasInfo:
		b.WriteString(progressStyle.Render("  Loading latest pipeline...") + "\n")
	case state.err != nil:
		b.WriteString(errorStyle.Render("  Error: "+state.err.Error()) + "\n")
	case state.empty:
		b.WriteString(progressStyle.Render(fmt.Sprintf("  %s for %s.", msgNoPipelines, refLabel)) + "\n")
	case state.hasInfo:
		b.WriteString(detailLabelStyle.Render("  Status: ") + pipelineStatusStyle(state.info.Status).Render(fmt.Sprintf("%s (#%d)", state.info.Status, state.info.ID)) + "\n")
		if state.info.SHA != "" {
			b.WriteString(detailLabelStyle.Render("  SHA: ") + detailValueStyle.Render(truncate(state.info.SHA, 12)) + "\n")
		}
		if !state.info.UpdatedAt.IsZero() {
			b.WriteString(detailLabelStyle.Render("  Updated: ") + detailValueStyle.Render(state.info.UpdatedAt.Format(time.RFC1123)) + "\n")
		}
		if state.info.WebURL != "" {
			urlWidth := width - 4
			if urlWidth < 4 {
				urlWidth = width
			}
			b.WriteString(detailLabelStyle.Render("  URL: ") + detailValueStyle.Render(truncate(state.info.WebURL, urlWidth)) + "\n")
		}
		if len(state.info.Stages) > 0 {
			stageWidth := width - 8
			if stageWidth < 8 {
				stageWidth = width
			}
			b.WriteString(detailLabelStyle.Render("  Stages:") + "\n")
			for _, stage := range state.info.Stages {
				stageName := truncate(stage.Name, stageWidth)
				stageStatus := truncate(stage.Status, stageWidth)
				b.WriteString(detailLabelStyle.Render("   - "+stageName+": ") + pipelineStatusStyle(stageStatus).Render(stageStatus) + "\n")
			}
		}
		if state.loading {
			b.WriteString(progressStyle.Render("  Refreshing...") + "\n")
		}
	default:
		if !ok {
			b.WriteString(progressStyle.Render("  Pipeline status pending...") + "\n")
		} else if state.loading {
			b.WriteString(progressStyle.Render("  Refreshing pipeline status...") + "\n")
		} else {
			b.WriteString(progressStyle.Render("  Pipeline status pending...") + "\n")
		}
	}
	if !state.lastFetched.IsZero() {
		b.WriteString(detailLabelStyle.Render("  Checked: ") + detailValueStyle.Render(state.lastFetched.Format(time.RFC1123)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// explorerPaneLayout calculates inner widths and content height for the
// three-pane explorer view (parent, current, preview). Returns ok=false
// if the terminal is too narrow.
//
// Height budget matches projectPaneLayout: terminal height - 4.
// Width budget: total - 2 gaps - 6 border chars (3 panes x 2 borders each).
func explorerPaneLayout(width, height int) (parentInner, currentInner, previewInner, contentHeight int, ok bool) {
	if width <= 0 {
		width = 80
	}
	minInner := 4
	minTotalWidth := minInner*3 + 6 + paneGap*2
	if width < minTotalWidth {
		return 0, 0, 0, 0, false
	}
	if height <= 5 {
		height = 5
	}
	contentHeight = height - 4
	innerTotal := width - paneGap*2 - 6
	parentInner = max(minInner, innerTotal*explorerParentWidthPct/100)
	currentInner = max(minInner, innerTotal*explorerCurrentWidthPct/100)
	previewInner = innerTotal - parentInner - currentInner
	if previewInner < minInner {
		previewInner = minInner
		currentInner = max(minInner, innerTotal-parentInner-previewInner)
	}
	return parentInner, currentInner, previewInner, contentHeight, true
}

// renderExplorerView renders the three-pane file explorer (ranger/yazi style):
// parent directory on the left, current directory in the center, and file
// preview on the right. Pane widths follow explorerPaneLayout percentages.
func renderExplorerView(m Model, width int) string {
	parentInner, currentInner, previewInner, contentHeight, ok := explorerPaneLayout(width, m.height)
	if !ok {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for explorer view.")
	}
	parentPane := renderPane(renderExplorerParents(m, parentInner, contentHeight, false), parentInner, contentHeight, false)
	currentPane := renderPane(renderExplorerCurrent(m, currentInner, contentHeight, true), currentInner, contentHeight, true)
	previewPane := renderPane(renderExplorerPreview(m, previewInner, contentHeight, false), previewInner, contentHeight, false)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, parentPane, gap, currentPane, gap, previewPane)
}

// renderProjectActionModal renders the "View pipelines / Browse files" chooser
// as a centered bordered box, intended to be composited via overlayCentered.
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
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rosePineSubtle).
		Padding(1, 2).
		Width(innerWidth).
		Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}

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
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for pipeline view.")
	}
	pipelinesFocused := m.pipelineView.focus == pipelineFocusPipelines
	stagesFocused := m.pipelineView.focus == pipelineFocusStages
	parentPane := renderPane(renderPipelineListPane(m, pipelineInner, contentHeight, pipelinesFocused), pipelineInner, contentHeight, pipelinesFocused)
	currentPane := renderPane(renderPipelineStagesPane(m, stagesInner, contentHeight, stagesFocused), stagesInner, contentHeight, stagesFocused)
	previewPane := renderPane(renderPipelineLogPane(m, logInner, contentHeight, false), logInner, contentHeight, false)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, parentPane, gap, currentPane, gap, previewPane)
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

// renderExplorerParents renders the left pane showing the parent directory
// entries. The height parameter is the pane's contentHeight from layout;
// the list is constrained to height-1 to leave room for the header line,
// enabling scrolling when entries exceed available space.
func renderExplorerParents(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	b.WriteString(paneHeaderStyle(focused).Render(clampLine("Parents", width)))
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

	// Use bubbles list for rendering entries
	m.explorer.parentList.SetSize(width, max(1, height-1))
	b.WriteString(m.explorer.parentList.View())
	return b.String()
}

func renderExplorerCurrent(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Explorer · %s @ %s", m.explorer.project.PathWithNamespace, displayRef(m.explorer))
	b.WriteString(paneHeaderStyle(focused).Render(clampLine(title, width)))
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
	} else if len(cur.entries) > 0 {
		// Constrain list height to available pane space so bubbles handles
		// scrolling internally. Without this, SetSize(width, len(entries))
		// allocates enough height for all items, preventing scroll.
		headerLines := 2 // title + path
		if cur.loading {
			headerLines++
		}
		listHeight := max(1, height-headerLines-1)
		m.explorer.currentList.SetSize(width, listHeight)
		b.WriteString(m.explorer.currentList.View())
	}
	return finalize()
}

func renderExplorerPreview(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	b.WriteString(paneHeaderStyle(focused).Render(clampLine("Preview", width)))
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
