package ui

import (
	"fmt"
	"strings"
)

// renderGlabPreviewModal draws the command-preview overlay: a list of the glab
// commands available for the focused entity, the highlighted one ready to copy.
// It follows the same border/style grammar as the other modals.
func renderGlabPreviewModal(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	innerWidth := min(72, width-10)
	if innerWidth < 24 {
		innerWidth = max(12, width-6)
	}

	b := &strings.Builder{}
	title := "glab commands"
	if m.glabPreview.project != "" {
		title = fmt.Sprintf("glab commands · %s", m.glabPreview.project)
	}
	b.WriteString(detailHeaderStyle.Render(clampLine(title, innerWidth)))
	b.WriteString("\n\n")

	for i, c := range m.glabPreview.commands {
		marker := "  "
		labelStyle := explorerPathStyle
		cmdStyle := explorerHintStyle
		if i == m.glabPreview.cursor {
			marker = "› "
			labelStyle = detailHeaderStyle
			cmdStyle = explorerPathStyle
		}
		b.WriteString(labelStyle.Render(clampLine(marker+c.Label, innerWidth)))
		b.WriteString("\n")
		b.WriteString(cmdStyle.Render(clampLine("  "+c.Cmd, innerWidth)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(explorerHintStyle.Render(clampLine("j/k move · enter or y copy · esc close", innerWidth)))

	return modalBorderStyle.Width(innerWidth).Render(strings.TrimSuffix(b.String(), "\n"))
}
