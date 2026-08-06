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

	"github.com/GF6599/lazylab/internal/diffutil"
	"github.com/GF6599/lazylab/internal/gitlab"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// toDiffutilFiles adapts the gitlab MR-diff slice to the diffutil package's
// minimal FileDiff so the parser stays decoupled from the API client.
func toDiffutilFiles(diffs []gitlab.MRDiffFile) []diffutil.FileDiff {
	out := make([]diffutil.FileDiff, len(diffs))
	for i, d := range diffs {
		out[i] = diffutil.FileDiff{OldPath: d.OldPath, NewPath: d.NewPath, Diff: d.Diff}
	}
	return out
}

// mrSnippetStyles returns the snippet styles used when rendering inline diff
// context. Built fresh per call so theme changes propagate without caching.
func mrSnippetStyles() diffutil.SnippetStyles {
	return diffutil.SnippetStyles{
		Add:     diffAddStyle,
		Del:     diffDelStyle,
		Hunk:    diffHunkStyle,
		Context: itemStyle,
	}
}

// mrPerPage matches GitLab's web UI default of 25 MRs per page, keeping the
// TUI's pagination aligned with what users see in the browser.
const mrPerPage = 25

// mrTab filters the MR list by state. Maps to GitLab API state parameters.
type mrTab int

const (
	mrTabOpen mrTab = iota
	mrTabMerged
	mrTabClosed
)

var mrTabLabels = []string{"open", "merged", "closed"}

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
// Discussions and diffs are cached per MR IID (via AsyncCache keyed by IID) to
// survive tab switching without redundant API calls.
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
	selectedDiscussion int // Index into filtered (non-system) discussions, not raw discussions
	reply              mrReplyState
	createMR           createMRState

	// Diff cursor state
	diffLineMap []diffutil.LineInfo
	diffCursor  int
	diffRefs    gitlab.MRDiffRefs // fetched alongside diffs for positioned comments
}

// mrReplyState holds state for the reply-to-discussion modal. The modal
// lifecycle is: inactive → active (textarea shown) → sending (API call in
// flight) → inactive. A fresh textarea.Model is created each time the modal
// opens to avoid carrying over stale draft text.
type mrReplyState struct {
	active       bool
	discussionID string
	projectID    int
	mrIID        int
	input        textarea.Model
	sending      bool
	err          error
	isNew        bool                      // true = new discussion (not reply)
	position     *gitlab.MRCommentPosition // non-nil = line-level comment
}

// createMRState holds state for the create-merge-request modal. The lifecycle
// mirrors mrReplyState: inactive → active (form shown) → sending → inactive.
type createMRState struct {
	active       bool
	projectID    int
	title        textinput.Model // field 0
	sourceBranch textinput.Model // field 1
	targetBranch textinput.Model // field 2
	description  textarea.Model  // field 3
	focusIndex   int             // 0=title, 1=source, 2=target, 3=description
	sending      bool
	err          error
	branchPicker branchPickerState
}

// branchPickerState holds state for the branch picker overlay nested inside
// the create-MR modal. Activated via Ctrl+B when a branch field has focus.
type branchPickerState struct {
	active   bool
	forField int             // which field triggered: 1=source, 2=target
	branches []string        // all fetched branches
	filtered []string        // after search filter
	search   textinput.Model // filter input
	selected int             // cursor in filtered list
	loading  bool
	err      error
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
		return explorerErrorStyle.Render(clampLine(" "+formatLoadErr("merge requests", m.mrView.err), width))
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
	offset = max(offset, 0)

	var lines []string
	end := offset + height
	end = min(end, total)
	for i := offset; i < end; i++ {
		mr := m.mrView.mrs[i]
		mk := markerFor(i, m.mrView.selected)
		line := clampLine("!"+strconv.Itoa(mr.IID)+" "+mr.Title, max(0, width-2))
		lines = append(lines, mk.render(line))
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
//
// When diffs is non-nil and contextLines > 0, positioned comments (DiffNotes)
// include an inline diff snippet between the header and body, showing the
// surrounding code context so the reader can understand the comment without
// switching to the Diff tab. Pass nil diffs to render without context (e.g.,
// when diffs haven't loaded yet — the comments will re-render when they arrive).
//
// Width controls truncation; pass 0 to skip.
func renderMRCommentsText(discussions []gitlab.MRDiscussion, width, selectedIdx int, diffs []gitlab.MRDiffFile, contextLines int) string {
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

			// Inline diff context for positioned comments (first note only)
			if j == 0 && note.FilePath != "" && contextLines > 0 && len(diffs) > 0 {
				locLine := fmt.Sprintf("  %s:%d", note.FilePath, note.Line)
				if width > 0 {
					locLine = ansi.Truncate(locLine, width, "…")
				}
				b.WriteString(detailLabelStyle.Render(locLine))
				b.WriteString("\n")
				snippet := diffutil.ExtractContext(toDiffutilFiles(diffs), note.FilePath, note.OldLine, note.NewLine, contextLines)
				if len(snippet) > 0 {
					b.WriteString(diffutil.RenderSnippet(snippet, width, mrSnippetStyles()))
				}
			}

			// Indent body with tree-style prefix
			prefix := "│ "
			if j == len(d.Notes)-1 {
				prefix = "└ "
			} else if j > 0 {
				prefix = "├ "
			}
			for line := range strings.SplitSeq(note.Body, "\n") {
				out := "  " + prefix + line
				if width > 0 {
					out = ansi.Truncate(out, width, "…")
				}
				b.WriteString(itemStyle.Render(out) + "\n")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderMRDiffText builds a styled unified diff view with per-line coloring
// (green for additions, red for deletions, dimmed for hunk headers).
// Width controls line truncation; pass 0 to skip. cursorLine highlights the
// specified rendered line index with a distinct background; pass -1 to disable.
func renderMRDiffText(diffs []gitlab.MRDiffFile, width, cursorLine int) string {
	if len(diffs) == 0 {
		return explorerHintStyle.Render("No changes")
	}
	cursorBg := diffCursorBgStyle
	lineNum := 0
	var b strings.Builder
	for i, d := range diffs {
		if i > 0 {
			b.WriteString("\n")
			lineNum++
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
		lineNum++
		divW := 40
		if width > 0 && width < divW {
			divW = width
		}
		b.WriteString(detailDividerStyle.Render(strings.Repeat("─", divW)))
		b.WriteString("\n")
		lineNum++

		// Render diff lines with color, truncating to viewport width
		for line := range strings.SplitSeq(d.Diff, "\n") {
			// Normalize tabs and carriage returns before measuring
			line = strings.ReplaceAll(line, "\t", "    ")
			line = strings.ReplaceAll(line, "\r", "")
			if width > 0 {
				line = ansi.Truncate(line, width, "…")
			}
			var styled string
			switch {
			case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++"):
				styled = diffHunkStyle.Render(line)
			case strings.HasPrefix(line, "+"):
				styled = diffAddStyle.Render(line)
			case strings.HasPrefix(line, "-"):
				styled = diffDelStyle.Render(line)
			case strings.HasPrefix(line, "@@"):
				styled = diffHunkStyle.Render(line)
			default:
				styled = itemStyle.Render(line)
			}
			if lineNum == cursorLine {
				styled = cursorBg.Render(styled)
			}
			b.WriteString(styled)
			b.WriteString("\n")
			lineNum++
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
	title := "reply"
	if m.mrView.reply.isNew {
		if m.mrView.reply.position != nil {
			title = fmt.Sprintf("Comment on %s:%d", m.mrView.reply.position.NewPath, m.mrView.reply.position.NewLine)
			if m.mrView.reply.position.NewLine == 0 && m.mrView.reply.position.OldLine != 0 {
				title = fmt.Sprintf("Comment on %s:%d", m.mrView.reply.position.OldPath, m.mrView.reply.position.OldLine)
			}
		} else {
			title = "new comment"
		}
	}
	b.WriteString(detailHeaderStyle.Render(clampLine(title, innerWidth)))
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

	modal := modalBorderStyle.Width(innerWidth).Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}

// renderCreateMRModal renders the create-merge-request form as a centered overlay.
func renderCreateMRModal(m Model, width int) string {
	innerWidth := width / 2
	if innerWidth < 50 {
		innerWidth = min(width-4, 70)
	}
	if innerWidth < 30 {
		innerWidth = max(20, width-6)
	}
	fieldWidth := innerWidth - 2

	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine("Create Merge Request", innerWidth)))
	b.WriteString("\n\n")

	type field struct {
		label string
		view  string
		idx   int
	}
	m.mrView.createMR.title.Width = fieldWidth
	m.mrView.createMR.sourceBranch.Width = fieldWidth
	m.mrView.createMR.targetBranch.Width = fieldWidth
	m.mrView.createMR.description.SetWidth(fieldWidth)

	fields := []field{
		{"Title", m.mrView.createMR.title.View(), 0},
		{"Source Branch", m.mrView.createMR.sourceBranch.View(), 1},
		{"Target Branch", m.mrView.createMR.targetBranch.View(), 2},
		{"Description", m.mrView.createMR.description.View(), 3},
	}

	labelStyle := modalLabelStyle
	focusLabelStyle := modalFocusLabelStyle

	for _, f := range fields {
		ls := labelStyle
		if f.idx == m.mrView.createMR.focusIndex {
			ls = focusLabelStyle
		}
		label := f.label
		if (f.idx == 1 || f.idx == 2) && !m.mrView.createMR.branchPicker.active {
			label += "  (Ctrl+B to pick)"
		}
		b.WriteString(ls.Render(label))
		b.WriteString("\n")
		b.WriteString(f.view)
		b.WriteString("\n")

		// Render branch picker inline below the active branch field
		if m.mrView.createMR.branchPicker.active && m.mrView.createMR.branchPicker.forField == f.idx {
			b.WriteString(renderBranchPicker(m.mrView.createMR.branchPicker, fieldWidth))
		}
		b.WriteString("\n")
	}

	if m.mrView.createMR.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(m.mrView.createMR.err.Error(), innerWidth)))
		b.WriteString("\n")
	}
	if m.mrView.createMR.sending {
		b.WriteString(explorerHintStyle.Render("Creating merge request..."))
		b.WriteString("\n")
	}

	b.WriteString(explorerHintStyle.Render(clampLine("Tab cycle · Ctrl+B branches · Ctrl+S create · Esc cancel", innerWidth)))

	modal := modalBorderStyle.Width(innerWidth).Render(strings.TrimSuffix(b.String(), "\n"))
	return modal
}

// renderBranchPicker renders the branch picker sub-panel inline within the
// create-MR modal.
func renderBranchPicker(bp branchPickerState, width int) string {
	b := &strings.Builder{}
	searchLabel := modalLabelStyle.Render("Filter: ")
	bp.search.Width = width - lipgloss.Width(searchLabel) - 2
	b.WriteString(searchLabel)
	b.WriteString(bp.search.View())
	b.WriteString("\n")

	if bp.loading {
		b.WriteString(explorerHintStyle.Render("  Loading branches..."))
		b.WriteString("\n")
	} else if bp.err != nil {
		b.WriteString(explorerErrorStyle.Render("  " + bp.err.Error()))
		b.WriteString("\n")
	} else if len(bp.filtered) == 0 {
		b.WriteString(explorerHintStyle.Render("  No matching branches"))
		b.WriteString("\n")
	} else {
		maxVisible := 8
		start := 0
		if bp.selected >= maxVisible {
			start = bp.selected - maxVisible + 1
		}
		end := min(start+maxVisible, len(bp.filtered))
		for i := start; i < end; i++ {
			mk := markerFor(i, bp.selected)
			b.WriteString(mk.render(clampLine(bp.filtered[i], max(0, width-2))))
			b.WriteString("\n")
		}
	}
	b.WriteString(explorerHintStyle.Render(clampLine("Enter select · Esc close", width)))
	return b.String()
}

// discussionStartLine returns the 0-based line number at which the given
// filtered discussion index begins in the rendered comments text.
//
// This must exactly mirror the line-emission logic in renderMRCommentsText
// (including the optional diff context snippet) so the viewport can scroll
// to keep the selected discussion visible. If the two functions diverge,
// selection scrolling breaks — keep them in sync when changing either.
func discussionStartLine(discussions []gitlab.MRDiscussion, selectedIdx int, diffs []gitlab.MRDiffFile, contextLines int) int {
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
			line++ // header line
			// Diff context for positioned comments (first note only)
			if j == 0 && note.FilePath != "" && contextLines > 0 && len(diffs) > 0 {
				line++ // file location line
				snippet := diffutil.ExtractContext(toDiffutilFiles(diffs), note.FilePath, note.OldLine, note.NewLine, contextLines)
				line += len(snippet) // snippet lines (each emits a \n)
			}
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

// refreshMRViewportContent re-renders the MR viewport for the current detail
// tab using the active theme. Called after a theme change so cached lipgloss
// styling matches the new palette without waiting for the next user keystroke.
func (m *Model) refreshMRViewportContent() {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return
	}
	switch m.mrView.detailTab {
	case mrDetailTabComments:
		discussions, ok := m.mrView.discussions.Get(mr.IID)
		if !ok {
			return
		}
		diffs, _ := m.mrView.diffs.Get(mr.IID)
		content := renderMRCommentsText(discussions, m.mrViewportWidth(), m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines)
		m.setMRViewportContent(content)
	case mrDetailTabDiff:
		diffs, ok := m.mrView.diffs.Get(mr.IID)
		if !ok {
			return
		}
		content := renderMRDiffText(diffs, m.mrViewportWidth(), m.mrView.diffCursor)
		m.setMRViewportContent(content)
	}
}
