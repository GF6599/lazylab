// keys_mrs_panel.go contains key handling for the MRs panel and MR reply
// modal in the multi-panel layout.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// handleMRsPanelKey handles keys when the MRs panel is focused.
func (m Model) handleMRsPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "down", "j":
		if m.mrView.selected < len(m.mrView.mrs)-1 {
			m.mrView.selected++
		}
	case "up", "k":
		if m.mrView.selected > 0 {
			m.mrView.selected--
		}
	case "]":
		// Next page
		if m.mrView.nextPage > 0 {
			m.mrView.loading = true
			m.mrView.selected = 0
			return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), m.mrView.nextPage, mrPerPage)
		}
		return m, nil
	case "[":
		// Prev page
		if m.mrView.prevPage > 0 {
			m.mrView.loading = true
			m.mrView.selected = 0
			return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), m.mrView.prevPage, mrPerPage)
		}
		return m, nil
	case "<", "g":
		if len(m.mrView.mrs) > 0 {
			m.mrView.selected = 0
		}
		return m, nil
	case ">", "G":
		if len(m.mrView.mrs) > 0 {
			m.mrView.selected = len(m.mrView.mrs) - 1
		}
		return m, nil
	case "ctrl+d":
		step := listPageStep(m.height)
		if m.mrView.selected < len(m.mrView.mrs)-1 {
			m.mrView.selected = min(m.mrView.selected+step, len(m.mrView.mrs)-1)
		}
		return m, nil
	case "ctrl+u":
		step := listPageStep(m.height)
		if m.mrView.selected > 0 {
			m.mrView.selected = max(m.mrView.selected-step, 0)
		}
		return m, nil
	case "enter", "l", "right":
		// Focus Detail pane
		m.focus.PrevActive = PanelMRs
		m.focus.Active = PanelDetail
		return m, nil
	case "h", "left":
		// Back to Stages
		m.focus.Active = PanelStages
		return m, nil
	case "esc":
		m.focus.Active = PanelProjects
		return m, nil
	case "J":
		m.mrView.mrViewport.HalfPageDown()
		return m, nil
	case "K":
		m.mrView.mrViewport.HalfPageUp()
		return m, nil
	case "t":
		// Cycle MR sidebar tabs (Open → Merged → Closed)
		m.mrView.tab = (m.mrView.tab + 1) % 3
		m.mrView.loading = true
		m.mrView.mrs = nil
		m.mrView.selected = 0
		m.mrView.detailTab = mrDetailTabInfo
		return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), 1, mrPerPage)
	case "T":
		// Cycle MR sidebar tabs backward (Closed → Merged → Open)
		m.mrView.tab = (m.mrView.tab + 2) % 3
		m.mrView.loading = true
		m.mrView.mrs = nil
		m.mrView.selected = 0
		m.mrView.detailTab = mrDetailTabInfo
		return m, fetchMRsCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mrTabStateString(m.mrView.tab), 1, mrPerPage)
	case "c":
		return m.openMRNewCommentModal()
	case "ctrl+o":
		m.copyMRURL()
	}
	return m, nil
}

// handleMRReplyKey handles keys when the MR reply modal is active.
func (m Model) handleMRReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mrView.reply = mrReplyState{}
		return m, nil
	case "ctrl+s":
		body := strings.TrimSpace(m.mrView.reply.input.Value())
		if body == "" {
			m.mrView.reply.err = fmt.Errorf("comment cannot be empty")
			return m, nil
		}
		m.mrView.reply.sending = true
		m.mrView.reply.err = nil
		if m.mrView.reply.isNew {
			m.status = "Posting comment..."
			return m, createMRDiscussionCmd(
				m.ctx, m.client, m.opts.APITimeout,
				m.mrView.reply.projectID,
				m.mrView.reply.mrIID,
				body,
				m.mrView.reply.position,
			)
		}
		m.status = "Sending reply..."
		return m, replyMRDiscussionCmd(
			m.ctx, m.client, m.opts.APITimeout,
			m.mrView.reply.projectID,
			m.mrView.reply.mrIID,
			m.mrView.reply.discussionID,
			body,
		)
	default:
		var cmd tea.Cmd
		m.mrView.reply.input, cmd = m.mrView.reply.input.Update(msg)
		return m, cmd
	}
}

// moveDiscussionSelection moves the selected discussion index by delta,
// clamping to bounds, and re-renders the comments viewport.
func (m Model) moveDiscussionSelection(delta int) (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 {
		return m, nil
	}
	newIdx := m.mrView.selectedDiscussion + delta
	newIdx = max(newIdx, 0)
	if newIdx >= len(filtered) {
		newIdx = len(filtered) - 1
	}
	if newIdx == m.mrView.selectedDiscussion {
		return m, nil
	}
	m.mrView.selectedDiscussion = newIdx
	return m.refreshMRCommentsViewport()
}

// moveDiscussionToEnd moves the selected discussion to the last one.
func (m Model) moveDiscussionToEnd() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 {
		return m, nil
	}
	m.mrView.selectedDiscussion = len(filtered) - 1
	return m.refreshMRCommentsViewport()
}

// refreshMRCommentsViewport re-renders the comments text with current selection
// and scrolls the viewport so the selected discussion is visible.
func (m Model) refreshMRCommentsViewport() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	w := m.mrViewportWidth()
	diffs, _ := m.mrView.diffs.Get(mr.IID)
	content := renderMRCommentsText(discussions, w, m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines)
	m.setMRViewportContent(content)

	// Scroll so the selected discussion is visible
	startLine := discussionStartLine(discussions, m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines)
	vpHeight := m.mrView.mrViewport.Height
	yOffset := m.mrView.mrViewport.YOffset
	if startLine < yOffset {
		m.mrView.mrViewport.SetYOffset(startLine)
	} else if startLine >= yOffset+vpHeight {
		m.mrView.mrViewport.SetYOffset(startLine - vpHeight/2)
	}
	return m, nil
}

// toggleDiscussionResolved toggles resolve/unresolve on the selected discussion.
func (m Model) toggleDiscussionResolved() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if m.mrView.selectedDiscussion >= len(filtered) {
		return m, nil
	}
	disc := filtered[m.mrView.selectedDiscussion]
	// Check if resolvable (first note's Resolvable flag)
	if len(disc.Notes) == 0 || !disc.Notes[0].Resolvable {
		m.status = "Discussion is not resolvable"
		return m, nil
	}
	currentResolved := disc.Notes[0].Resolved
	newResolved := !currentResolved

	// Optimistic update: toggle resolved state in cache
	m.optimisticToggleResolved(mr.IID, disc.ID, newResolved)

	// Re-render with updated state
	if updatedDiscs, ok2 := m.mrView.discussions.Get(mr.IID); ok2 {
		diffs, _ := m.mrView.diffs.Get(mr.IID)
		content := renderMRCommentsText(updatedDiscs, m.mrViewportWidth(), m.mrView.selectedDiscussion, diffs, m.opts.DiffContextLines)
		m.setMRViewportContent(content)
	}

	if newResolved {
		m.status = "Resolving discussion..."
	} else {
		m.status = "Unresolving discussion..."
	}
	return m, resolveMRDiscussionCmd(m.ctx, m.client, m.opts.APITimeout, m.mrView.project.ID, mr.IID, disc.ID, newResolved)
}

// optimisticToggleResolved updates the cached discussion's Resolved flags in-place.
func (m *Model) optimisticToggleResolved(mrIID int, discussionID string, resolved bool) {
	discussions, ok := m.mrView.discussions.Get(mrIID)
	if !ok {
		return
	}
	for i := range discussions {
		if discussions[i].ID == discussionID {
			for j := range discussions[i].Notes {
				if discussions[i].Notes[j].Resolvable {
					discussions[i].Notes[j].Resolved = resolved
				}
			}
			break
		}
	}
	m.mrView.discussions.Set(mrIID, discussions)
}

// openMRReplyModal opens the reply textarea modal for the selected discussion.
func (m Model) openMRReplyModal() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok {
		return m, nil
	}
	filtered := filterUserDiscussions(discussions)
	if m.mrView.selectedDiscussion >= len(filtered) {
		return m, nil
	}
	disc := filtered[m.mrView.selectedDiscussion]

	ta := m.newMRTextarea("Type your reply...")
	m.mrView.reply = mrReplyState{
		active:       true,
		discussionID: disc.ID,
		projectID:    m.mrView.project.ID,
		mrIID:        mr.IID,
		input:        ta,
	}
	return m, textarea.Blink
}

// openMRNewCommentModal opens a textarea modal for creating a general comment.
func (m Model) openMRNewCommentModal() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}

	ta := m.newMRTextarea("Type your comment...")
	m.mrView.reply = mrReplyState{
		active:    true,
		projectID: m.mrView.project.ID,
		mrIID:     mr.IID,
		input:     ta,
		isNew:     true,
	}
	return m, textarea.Blink
}

// openMRDiffCommentModal opens a textarea modal for creating a line-level diff comment.
func (m Model) openMRDiffCommentModal() (tea.Model, tea.Cmd) {
	mr := m.mrView.selectedMR()
	if mr == nil {
		return m, nil
	}
	if len(m.mrView.diffLineMap) == 0 {
		m.status = "No diff lines available"
		return m, nil
	}
	if m.mrView.diffCursor < 0 || m.mrView.diffCursor >= len(m.mrView.diffLineMap) {
		m.status = "Cursor out of range"
		return m, nil
	}
	info := m.mrView.diffLineMap[m.mrView.diffCursor]
	if info.kind != '+' && info.kind != '-' && info.kind != ' ' {
		m.status = "Cannot comment on this line (select a code line)"
		return m, nil
	}

	diffs, ok := m.mrView.diffs.Get(mr.IID)
	if !ok || info.fileIdx >= len(diffs) {
		m.status = "Diff data not available"
		return m, nil
	}
	d := diffs[info.fileIdx]

	if m.mrView.diffRefs.BaseSHA == "" {
		m.status = "Diff refs not loaded yet — try again shortly"
		return m, nil
	}
	pos := &gitlab.MRCommentPosition{
		OldPath:  d.OldPath,
		NewPath:  d.NewPath,
		OldLine:  info.oldLine,
		NewLine:  info.newLine,
		DiffRefs: m.mrView.diffRefs,
	}

	ta := m.newMRTextarea("Type your comment...")
	m.mrView.reply = mrReplyState{
		active:    true,
		projectID: m.mrView.project.ID,
		mrIID:     mr.IID,
		input:     ta,
		isNew:     true,
		position:  pos,
	}
	return m, textarea.Blink
}

// newMRTextarea creates a styled textarea for MR comment/reply modals.
func (m Model) newMRTextarea(placeholder string) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.SetWidth(50)
	ta.SetHeight(5)
	ta.FocusedStyle.Base = lipgloss.NewStyle().Foreground(colorText)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(colorText).Background(colorHighlightLow)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorSubtle)
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(colorActive)
	ta.BlurredStyle = ta.FocusedStyle
	ta.Focus()
	return ta
}
