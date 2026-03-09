// state_mrs.go manages merge request state: clipboard operations for MR URLs
// and discussion comments.
package ui

import (
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
)

// copyMRURL copies the selected merge request's web URL to the clipboard.
func (m *Model) copyMRURL() {
	if len(m.mrView.mrs) == 0 || m.mrView.selected >= len(m.mrView.mrs) {
		m.status = "No merge request selected"
		return
	}
	mr := m.mrView.mrs[m.mrView.selected]
	if mr.WebURL == "" {
		m.status = "Merge request has no URL"
		return
	}
	if err := clipboard.WriteAll(mr.WebURL); err != nil {
		m.status = "Failed to copy MR URL"
		m.logError("copy clipboard", "err", err)
		return
	}
	m.status = fmt.Sprintf("Copied !%d URL", mr.IID)
}

// copyMRComment copies the selected discussion's comment body (with file
// reference if present) to the clipboard.
func (m *Model) copyMRComment() {
	if len(m.mrView.mrs) == 0 || m.mrView.selected >= len(m.mrView.mrs) {
		m.status = "No merge request selected"
		return
	}
	mr := m.mrView.mrs[m.mrView.selected]
	discussions, ok := m.mrView.discussions.Get(mr.IID)
	if !ok || len(discussions) == 0 {
		m.status = "No discussions loaded"
		return
	}
	filtered := filterUserDiscussions(discussions)
	if len(filtered) == 0 || m.mrView.selectedDiscussion >= len(filtered) {
		m.status = "No discussion selected"
		return
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
		return
	}
	if err := clipboard.WriteAll(text); err != nil {
		m.status = "Failed to copy comment"
		m.logError("copy clipboard", "err", err)
		return
	}
	m.status = "Copied comment to clipboard"
}
