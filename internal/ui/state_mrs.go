// state_mrs.go manages merge request state: clipboard operations for MR URLs
// and discussion comments.
package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// copyMRURL returns a Cmd that copies the selected merge request's web URL
// to the clipboard off the event loop. Guard paths still set m.status
// synchronously and return a nil Cmd.
func (m *Model) copyMRURL() tea.Cmd {
	if len(m.mrView.mrs) == 0 || m.mrView.selected >= len(m.mrView.mrs) {
		m.status = "No merge request selected"
		return nil
	}
	mr := m.mrView.mrs[m.mrView.selected]
	if mr.WebURL == "" {
		m.status = "Merge request has no URL"
		return nil
	}
	return writeClipboardCmd(mr.WebURL, fmt.Sprintf("Copied !%d URL", mr.IID))
}

// copyMRComment returns a Cmd that copies the selected discussion's comment
// body (with file reference if present) to the clipboard off the event loop.
// Guard paths short-circuit with a synchronous status update and nil Cmd.
func (m *Model) copyMRComment() tea.Cmd {
	if len(m.mrView.mrs) == 0 || m.mrView.selected >= len(m.mrView.mrs) {
		m.status = "No merge request selected"
		return nil
	}
	mr := m.mrView.mrs[m.mrView.selected]
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok || len(discussions) == 0 {
		m.status = "No discussions loaded"
		return nil
	}
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 || m.mrView.selectedDiscussion >= len(filtered) {
		m.status = "No discussion selected"
		return nil
	}
	disc := filtered[m.mrView.selectedDiscussion]
	var b strings.Builder
	for i, note := range disc.Notes {
		if note.System {
			continue
		}
		if i > 0 {
			b.WriteString("\n---\n")
		}
		if note.FilePath != "" {
			if note.Line > 0 {
				fmt.Fprintf(&b, "%s:%d\n", note.FilePath, note.Line)
			} else {
				fmt.Fprintf(&b, "%s\n", note.FilePath)
			}
		}
		fmt.Fprintf(&b, "%s: %s\n", note.Author, note.Body)
	}
	text := b.String()
	if text == "" {
		m.status = "No comment to copy"
		return nil
	}
	return writeClipboardCmd(text, "Copied comment to clipboard")
}
