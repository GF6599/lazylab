// panel_mrs.go implements the Merge Requests sidebar panel and the MR-specific
// detail pane content (info, threaded comments, unified diffs).
//
// MR data is fetched lazily: the list loads when the MR panel gains focus or
// when the project selection changes. Comments and diffs are fetched on demand
// when the user switches to those detail tabs, and cached in AsyncCache by MR
// IID to avoid redundant API calls.
//
// The comments renderer uses a tree-style layout (│, ├, └) to visually nest
// reply threads, with resolved/unresolved badges on the first note of
// resolvable discussions.

package ui

import (
	"fmt"
	"strconv"
	"strings"

	"lazylab/internal/gitlab"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const mrPerPage = 25

// mrTab filters the MR list by state. Maps to GitLab API state parameters.
type mrTab int

const (
	mrTabOpen mrTab = iota
	mrTabMerged
	mrTabClosed
)

var mrTabLabels = []string{"Open", "Merged", "Closed"}

func mrTabStateString(t mrTab) string {
	switch t {
	case mrTabOpen:
		return "opened"
	case mrTabMerged:
		return "merged"
	case mrTabClosed:
		return "closed"
	default:
		return "opened"
	}
}

// mrViewState holds all state for the MR panel and its detail tabs. Each
// project gets its own mrViewState (reset when the project selection changes).
// Discussions and diffs are cached per MR IID to survive tab switching.
type mrViewState struct {
	project    gitlab.ProjectNode
	mrs        []gitlab.MergeRequestSummary
	selected   int
	loading    bool
	err        error
	tab        mrTab
	page       int
	prevPage   int
	nextPage   int
	totalPages int

	// Detail pane state
	detailTab          mrDetailTab
	discussions        AsyncCache[int, []gitlab.MRDiscussion]
	diffs              AsyncCache[int, []gitlab.MRDiffFile]
	mrViewport         viewport.Model
	selectedDiscussion int // Index into filtered (non-system) discussions
	reply              mrReplyState
}

// mrReplyState holds state for the reply-to-discussion modal.
type mrReplyState struct {
	active       bool
	discussionID string
	projectID    int
	mrIID        int
	input        textarea.Model
	sending      bool
	err          error
}

// selectedMR returns a pointer to the currently selected merge request,
// or nil if none is selected.
func (s *mrViewState) selectedMR() *gitlab.MergeRequestSummary {
	if len(s.mrs) == 0 || s.selected >= len(s.mrs) {
		return nil
	}
	return &s.mrs[s.selected]
}

// renderMRsPanel renders the MRs sidebar panel content.
func renderMRsPanel(m *Model, width, height int) string {
	if m.mrView.project.ID == 0 {
		return explorerHintStyle.Render(clampLine(" Select a project", width))
	}
	if m.mrView.loading && len(m.mrView.mrs) == 0 {
		return explorerHintStyle.Render(clampLine(" Loading merge requests...", width))
	}
	if m.mrView.err != nil {
		return explorerErrorStyle.Render(clampLine(" "+m.mrView.err.Error(), width))
	}
	if len(m.mrView.mrs) == 0 {
		return explorerHintStyle.Render(clampLine(" No merge requests found", width))
	}

	// Compute scroll offset so the selected item stays visible
	total := len(m.mrView.mrs)
	offset := 0
	if m.mrView.selected >= height {
		offset = m.mrView.selected - height + 1
	}
	if offset > total-height {
		offset = total - height
	}
	if offset < 0 {
		offset = 0
	}

	var lines []string
	end := offset + height
	if end > total {
		end = total
	}
	for i := offset; i < end; i++ {
		mr := m.mrView.mrs[i]
		cursor := " "
		style := itemStyle
		if i == m.mrView.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(cursor+" !"+strconv.Itoa(mr.IID)+" "+mr.Title, width)
		lines = append(lines, style.Render(line))
	}
	return joinLines(lines)
}

// filterUserDiscussions returns only discussions that contain at least one
// non-system note (i.e., user-authored discussions).
func filterUserDiscussions(discussions []gitlab.MRDiscussion) []gitlab.MRDiscussion {
	var filtered []gitlab.MRDiscussion
	for _, d := range discussions {
		for _, n := range d.Notes {
			if !n.System {
				filtered = append(filtered, d)
				break
			}
		}
	}
	return filtered
}

// renderMRCommentsText builds a styled string of MR discussions for the
// viewport. System-only discussions (automated notes) are filtered out.
// Notes within a discussion are rendered with tree-line prefixes (│/├/└)
// to show the reply structure. The selectedIdx highlights one discussion.
// Width controls truncation; pass 0 to skip.
func renderMRCommentsText(discussions []gitlab.MRDiscussion, width, selectedIdx int) string {
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 {
		return explorerHintStyle.Render("No discussions")
	}

	authorStyle := detailHeaderStyle // Iris, bold
	timestampStyle := detailLabelStyle
	resolvedStyle := diffAddStyle   // Pine (green-ish)
	unresolvedStyle := diffDelStyle // Love (red)

	var b strings.Builder
	for i, d := range filtered {
		if i > 0 {
			divW := 40
			if width > 0 && width < divW {
				divW = width
			}
			b.WriteString(detailDividerStyle.Render(strings.Repeat("─", divW)))
			b.WriteString("\n\n")
		}

		// Selection indicator
		selPrefix := "  "
		if i == selectedIdx {
			selPrefix = "▶ "
		}

		for j, note := range d.Notes {
			if note.System {
				continue
			}
			author := authorStyle.Render(note.Author)
			ts := timestampStyle.Render(formatTimeAgo(note.CreatedAt))
			var header string
			if j == 0 {
				header = fmt.Sprintf("%s%s · %s", selPrefix, author, ts)
			} else {
				header = fmt.Sprintf("  %s · %s", author, ts)
			}

			// Show resolved badge on first note of resolvable discussions
			if j == 0 && note.Resolvable {
				if note.Resolved {
					header += " " + resolvedStyle.Render("[resolved]")
				} else {
					header += " " + unresolvedStyle.Render("[unresolved]")
				}
			}

			if width > 0 {
				header = ansi.Truncate(header, width, "…")
			}
			b.WriteString(header)
			b.WriteString("\n")

			// Indent body with tree-style prefix
			prefix := "│ "
			if j == len(d.Notes)-1 {
				prefix = "└ "
			} else if j > 0 {
				prefix = "├ "
			}
			bodyLines := strings.Split(note.Body, "\n")
			for _, line := range bodyLines {
				out := "  " + prefix + line
				if width > 0 {
					out = ansi.Truncate(out, width, "…")
				}
				b.WriteString(out + "\n")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderMRDiffText builds a styled unified diff view with per-line coloring
// (green for additions, red for deletions, dimmed for hunk headers).
// Width controls line truncation; pass 0 to skip.
func renderMRDiffText(diffs []gitlab.MRDiffFile, width int) string {
	if len(diffs) == 0 {
		return explorerHintStyle.Render("No changes")
	}
	var b strings.Builder
	for i, d := range diffs {
		if i > 0 {
			b.WriteString("\n")
		}
		// File header with change type
		var changeType string
		switch {
		case d.NewFile:
			changeType = "new file"
		case d.DeletedFile:
			changeType = "deleted"
		case d.RenamedFile:
			changeType = fmt.Sprintf("renamed: %s → %s", d.OldPath, d.NewPath)
		default:
			changeType = "modified"
		}
		header := fmt.Sprintf(" %s: %s", changeType, d.NewPath)
		if width > 0 {
			header = ansi.Truncate(header, width, "…")
		}
		b.WriteString(detailHeaderStyle.Render(header))
		b.WriteString("\n")
		divW := 40
		if width > 0 && width < divW {
			divW = width
		}
		b.WriteString(detailDividerStyle.Render(strings.Repeat("─", divW)))
		b.WriteString("\n")

		// Render diff lines with color, truncating to viewport width
		lines := strings.Split(d.Diff, "\n")
		for _, line := range lines {
			// Normalize tabs and carriage returns before measuring
			line = strings.ReplaceAll(line, "\t", "    ")
			line = strings.ReplaceAll(line, "\r", "")
			if width > 0 {
				line = ansi.Truncate(line, width, "…")
			}
			switch {
			case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++"):
				b.WriteString(diffHunkStyle.Render(line))
			case strings.HasPrefix(line, "+"):
				b.WriteString(diffAddStyle.Render(line))
			case strings.HasPrefix(line, "-"):
				b.WriteString(diffDelStyle.Render(line))
			case strings.HasPrefix(line, "@@"):
				b.WriteString(diffHunkStyle.Render(line))
			default:
				b.WriteString(line)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderMRReplyModal renders the reply textarea modal as a centered overlay.
func renderMRReplyModal(m Model, width int) string {
	innerWidth := width / 2
	if innerWidth < 40 {
		innerWidth = min(width-4, 60)
	}
	if innerWidth < 20 {
		innerWidth = max(12, width-6)
	}

	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine("Reply to Discussion", innerWidth)))
	b.WriteString("\n\n")

	m.mrView.reply.input.SetWidth(innerWidth - 2)
	b.WriteString(m.mrView.reply.input.View())
	b.WriteString("\n\n")

	if m.mrView.reply.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(m.mrView.reply.err.Error(), innerWidth)))
		b.WriteString("\n")
	}
	if m.mrView.reply.sending {
		b.WriteString(explorerHintStyle.Render("Sending..."))
		b.WriteString("\n")
	}

	b.WriteString(explorerHintStyle.Render(clampLine("Ctrl+S to send · Esc to cancel", innerWidth)))

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rosePineSubtle).
		Padding(1, 2).
		Width(innerWidth).
		Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}

// discussionStartLine returns the 0-based line number at which the given
// filtered discussion index begins in the rendered comments text. This mirrors
// the line-counting logic in renderMRCommentsText so the viewport can scroll
// to keep the selected discussion visible.
func discussionStartLine(discussions []gitlab.MRDiscussion, width, selectedIdx int) int {
	filtered := filterUserDiscussions(discussions)
	line := 0
	for i, d := range filtered {
		if i == selectedIdx {
			return line
		}
		// Divider between discussions: styled line + blank line
		if i > 0 {
			line += 2 // divider + blank
		}
		// Count lines for each non-system note
		for j, note := range d.Notes {
			if note.System {
				continue
			}
			_ = j
			line++ // header line
			bodyLines := strings.Split(note.Body, "\n")
			line += len(bodyLines) // body lines (each with prefix)
			line++                 // blank line after note
		}
	}
	return line
}

// mrViewportWidth returns the MR viewport width, with a fallback of 80.
func (m *Model) mrViewportWidth() int {
	w := m.mrView.mrViewport.Width
	if w <= 0 {
		return 80
	}
	return w
}

// setMRViewportContent normalizes and hard-wraps content to fit the MR viewport width.
func (m *Model) setMRViewportContent(content string) {
	w := m.mrViewportWidth()
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.ReplaceAll(normalized, "\t", "    ")
	wrapped := ansi.Hardwrap(normalized, w, false)
	m.mrView.mrViewport.SetContent(wrapped)
}
