// view_explorer.go renders the three-pane file browser: parent directory
// listing, current directory listing, and file/directory preview with
// syntax highlighting.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

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
		return renderTooSmallView(width, m.height)
	}
	parentPane := renderPane(renderExplorerParents(m, parentInner, contentHeight, false), parentInner, contentHeight, false)
	currentPane := renderPane(renderExplorerCurrent(m, currentInner, contentHeight, true), currentInner, contentHeight, true)
	previewPane := renderPane(renderExplorerPreview(m, previewInner, false), previewInner, contentHeight, false)
	gap := renderPaneGap(paneGap, contentHeight+2)
	return lipgloss.JoinHorizontal(lipgloss.Top, parentPane, gap, currentPane, gap, previewPane)
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

// renderExplorerCurrent renders the center pane: the current directory's entries
// as a scrollable list with path breadcrumb. The list height is constrained to
// (height - headerLines - 1) so the bubbles list handles internal scrolling
// rather than rendering all entries at once.
func renderExplorerCurrent(m Model, width, height int, focused bool) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Explorer · %s @ %s", m.explorer.project.PathWithNamespace, displayRef(m.explorer))
	b.WriteString(paneHeaderStyle(focused).Render(clampLine(title, width)))
	b.WriteString("\n")
	hint := explorerHintStyle.Render(clampLine("Enter/→ descend · ←/Esc up · J/K preview · r refresh · Ctrl+O copy", width))
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

// renderExplorerPreview renders the right pane: file content (syntax-highlighted
// if possible) or directory listing in a scrollable viewport.
func renderExplorerPreview(m Model, width int, focused bool) string {
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
