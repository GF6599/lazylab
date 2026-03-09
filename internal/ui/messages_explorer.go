// Message handlers for explorer-mode concerns: directory tree loading and
// file content loading with syntax highlighting.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// handleTreeLoaded processes a fetched directory listing. It serves two purposes:
//   - Directory preview: if msg.path matches the preview path, format entries
//     as a listing and display in the preview viewport.
//   - Directory navigation: if msg.path matches a stack entry, populate its
//     entries and update the corresponding bubbles list.
func (m Model) handleTreeLoaded(msg treeLoadedMsg) (tea.Model, tea.Cmd) {
	isExplorer := m.mode == modeExplorer || (m.mode == modeMultiPanel && m.explorer.project.ID != 0)
	if !isExplorer || m.explorer.project.ID != msg.projectID {
		return m, nil
	}
	// If this was triggered for directory preview (path matches preview.path), format preview.
	if m.explorer.preview.path != "" && m.explorer.preview.path == msg.path {
		if msg.err != nil {
			vp := m.explorer.preview.viewport
			m.explorer.preview = previewState{path: msg.path, err: msg.err, viewport: vp}
			m.status = "Failed to load directory preview"
			return m, nil
		}
		builder := &strings.Builder{}
		builder.WriteString(fmt.Sprintf("%s/\n", msg.path))
		for _, entry := range msg.entries {
			name := entry.Name
			if entry.IsDir() {
				name += "/"
			}
			builder.WriteString(fmt.Sprintf("%s %s", explorerEntryIcon(entry), name))
			builder.WriteString("\n")
		}
		content := builder.String()
		vp := m.explorer.preview.viewport
		m.explorer.preview = previewState{
			path:     msg.path,
			content:  content,
			loading:  false,
			viewport: vp,
		}
		m.explorer.preview.viewport.SetContent(content)
		m.explorer.preview.viewport.GotoTop()
		return m, nil
	}
	idx := m.findDirIndex(msg.path)
	if idx == -1 {
		return m, nil
	}
	dir := &m.explorer.stack[idx]
	if msg.err != nil {
		dir.loading = false
		dir.entries = nil
		dir.err = msg.err
		m.status = "Failed to load directory"
		return m, nil
	}
	dir.loading = false
	dir.err = nil
	dir.entries = msg.entries
	if dir.selected >= len(dir.entries) {
		dir.selected = max(0, len(dir.entries)-1)
	}

	// Update bubbles lists with new entries
	items := make([]list.Item, len(msg.entries))
	for i, entry := range msg.entries {
		items[i] = treeEntryItem{entry: entry}
	}

	// If this is the current directory, update currentList
	if idx == len(m.explorer.stack)-1 {
		m.explorer.currentList.SetItems(items)
		if dir.selected >= 0 && dir.selected < len(items) {
			m.explorer.currentList.Select(dir.selected)
		}
		return m, m.queueExplorerPreview()
	}

	// If this is the parent directory, update parentList
	if idx == len(m.explorer.stack)-2 {
		m.explorer.parentList.SetItems(items)
		if dir.selected >= 0 && dir.selected < len(items) {
			m.explorer.parentList.Select(dir.selected)
		}
	}

	return m, nil
}

func (m Model) handleFileLoaded(msg fileLoadedMsg) (tea.Model, tea.Cmd) {
	isExplorer := m.mode == modeExplorer || (m.mode == modeMultiPanel && m.explorer.project.ID != 0)
	if !isExplorer || m.explorer.project.ID != msg.projectID {
		return m, nil
	}
	if msg.path != m.explorer.preview.path {
		return m, nil
	}
	m.explorer.preview.loading = false
	if msg.err != nil {
		m.explorer.preview.err = msg.err
		m.explorer.preview.content = ""
		m.explorer.preview.raw = ""
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
		m.status = "Failed to load file"
		return m, nil
	}
	width := previewContentWidth(m.width)
	highlighted, isHighlighted, err := (&m).highlightPreview(msg.path, msg.content, width)
	if err != nil {
		// Surface syntax highlighting errors to the user
		m.logDebug("highlight preview", "err", err, "path", msg.path)
		m.status = fmt.Sprintf("Syntax highlighting unavailable: %v", err)
		// Fall back to plain text
		m.explorer.preview.err = nil
		m.explorer.preview.raw = msg.content
		m.explorer.preview.content = msg.content
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
		m.explorer.preview.viewport.SetContent(msg.content)
		m.explorer.preview.viewport.GotoTop()
		return m, nil
	}
	m.explorer.preview.err = nil
	m.explorer.preview.raw = msg.content
	if isHighlighted {
		m.explorer.preview.content = highlighted
		m.explorer.preview.highlighted = true
		m.explorer.preview.highlightWidth = width
	} else {
		m.explorer.preview.content = msg.content
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
	}
	m.explorer.preview.viewport.SetContent(m.explorer.preview.content)
	m.explorer.preview.viewport.GotoTop()
	return m, nil
}
