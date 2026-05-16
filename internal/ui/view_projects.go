// view_projects.go renders the project list pane, the detail sidebar (project
// metadata + pipeline status), the search bar, and the pagination progress bar.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

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
	} else if commits, ok := m.commitCache.Get(project.ID); ok && len(commits) > 0 {
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
			b.WriteString(detailLabelStyle.Render("  SHA: ") + detailValueStyle.Render(clampLine(state.info.SHA, 12)) + "\n")
		}
		if !state.info.UpdatedAt.IsZero() {
			b.WriteString(detailLabelStyle.Render("  Updated: ") + detailValueStyle.Render(state.info.UpdatedAt.Format(time.RFC1123)) + "\n")
		}
		if state.info.WebURL != "" {
			urlWidth := width - 4
			if urlWidth < 4 {
				urlWidth = width
			}
			b.WriteString(detailLabelStyle.Render("  URL: ") + detailValueStyle.Render(clampLine(state.info.WebURL, urlWidth)) + "\n")
		}
		if len(state.info.Stages) > 0 {
			stageWidth := width - 8
			if stageWidth < 8 {
				stageWidth = width
			}
			b.WriteString(detailLabelStyle.Render("  Stages:") + "\n")
			for _, stage := range state.info.Stages {
				stageName := clampLine(stage.Name, stageWidth)
				stageStatus := clampLine(stage.Status, stageWidth)
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
