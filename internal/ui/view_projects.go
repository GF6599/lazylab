// view_projects.go renders the project list mode: the filterable project list
// pane, the detail sidebar (project metadata + pipeline status), the search
// bar, the action menu modal, and the pagination progress bar.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

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

// renderProjectsView renders the two-pane project view: a scrollable project
// list on the left (45% width) and a detail pane on the right showing metadata,
// pipeline status, and recent commits for the selected project.
func renderProjectsView(m Model, width int) string {
	listInner, detailInner, contentHeight, ok := projectPaneLayout(width, m.height)
	if !ok {
		return renderTooSmallView(width, m.height)
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
func renderDetailPane(m *Model, width int) string {
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
		for line := range strings.SplitSeq(desc, "\n") {
			b.WriteString(detailValueStyle.Render(line))
			b.WriteString("\n")
		}
	}
	writeDetailDivider(b, width)
	writeDetailSection(b, "Pipeline", width)
	pipe := clampLines(renderPipelineSection(m, project, width), width)
	for line := range strings.SplitSeq(strings.TrimSuffix(pipe, "\n"), "\n") {
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

// renderPipelineSection builds the pipeline summary shown in the project detail
// pane: status, SHA, timestamp, web URL, and per-stage breakdown. Reflects
// the tri-state loading model (loading/error/ready) from pipelineStatus cache.
func renderPipelineSection(m *Model, project gitlab.ProjectNode, width int) string {
	state, ok := m.pipelineStatus.Get(project.ID)
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

func renderListTitle(m Model) string {
	if m.search.query != "" {
		return fmt.Sprintf("Projects · Search \u201c%s\u201d (%d matches)", truncate(m.search.query, 20), len(m.visibleProjects()))
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

// renderProgressBar shows background page-loading progress as a block-character
// bar. Visible only while pagesLoaded < total pages and pagesLoaded >= 0.
// Returns "Cache warm" when loading is complete and a cache exists, or empty
// string when there's nothing to show.
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
	barWidth := max(width-20, 8)
	filled := min(int(float64(barWidth)*(float64(loaded)/float64(total))), barWidth)
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
