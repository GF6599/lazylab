// text_helpers.go contains pure text manipulation functions used across
// multiple UI views: truncation, wrapping, fuzzy matching, ANSI-aware
// clamping, column normalization, and modal overlay compositing.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// wrapText word-wraps s at width visible cells. Word boundaries are preserved;
// words longer than width are placed on their own line and may overflow.
// Width is measured with lipgloss.Width so multi-byte runes, CJK, and emoji
// count by terminal cells rather than bytes.
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var lines []string
	line := words[0]
	lineW := lipgloss.Width(line)
	for _, word := range words[1:] {
		wordW := lipgloss.Width(word)
		if lineW+1+wordW > width {
			lines = append(lines, line)
			line = word
			lineW = wordW
		} else {
			line += " " + word
			lineW += 1 + wordW
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

// wrapSelectedItem hard-wraps text for a selected list item so that the
// full content is visible instead of being truncated with "…". The first
// line uses full width; continuation lines are indented by indent visible
// characters. Returns at most maxLines lines (default 2); the last line
// is truncated with "…" if the text still overflows.
//
// This is used by list delegates to expand only the selected item while
// keeping non-selected items single-line. The bubbles list does not enforce
// Height() in its render loop, so extra newlines are safe.
func wrapSelectedItem(text string, width, indent, maxLines int) []string {
	if width <= 0 || lipgloss.Width(text) <= width {
		return []string{text}
	}
	if maxLines <= 0 {
		maxLines = 2
	}
	runes := []rune(text)
	var lines []string
	pos := 0
	for pos < len(runes) && len(lines) < maxLines {
		prefix := ""
		if len(lines) > 0 {
			prefix = strings.Repeat(" ", indent)
		}
		isLast := len(lines) == maxLines-1
		if isLast {
			// Last allowed line: pass all remaining text through clampLine
			// so it truncates with "…" only if content overflows.
			lines = append(lines, clampLine(prefix+string(runes[pos:]), width))
			break
		}
		avail := width - lipgloss.Width(prefix)
		if avail <= 0 {
			avail = 1
		}
		// Measure how many runes fit.
		var b strings.Builder
		b.WriteString(prefix)
		w := 0
		consumed := 0
		for _, r := range runes[pos:] {
			rw := lipgloss.Width(string(r))
			if w+rw > avail {
				break
			}
			b.WriteRune(r)
			w += rw
			consumed++
		}
		if consumed == 0 && pos < len(runes) {
			b.WriteRune(runes[pos])
			consumed = 1
		}
		pos += consumed
		lines = append(lines, b.String())
	}
	if len(lines) == 0 {
		return []string{text}
	}
	return lines
}

// fuzzyMatch performs a case-insensitive subsequence match: every rune in
// pattern must appear in target in order, but not necessarily contiguously.
// For example, "llb" matches "lazylab". This is intentionally simple (no
// scoring or gap penalties) because the project list is small enough that
// subsequence filtering is sufficient.
func fuzzyMatch(target, pattern string) bool {
	targetRunes := []rune(strings.ToLower(target))
	patternRunes := []rune(strings.ToLower(pattern))
	if len(patternRunes) == 0 {
		return true
	}
	tIdx := 0
	for _, r := range patternRunes {
		found := false
		for tIdx < len(targetRunes) {
			if targetRunes[tIdx] == r {
				found = true
				tIdx++
				break
			}
			tIdx++
		}
		if !found {
			return false
		}
	}
	return true
}

// clampLine truncates a styled line to fit within width, appending "..." if
// needed. Uses lipgloss.Width to account for wide/combining characters.
func clampLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	currentWidth := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > width-1 {
			break
		}
		b.WriteRune(r)
		currentWidth += rw
	}
	return b.String() + "…"
}

func clampLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = clampLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// clampLineANSI truncates a line that may contain ANSI escape sequences,
// preserving escape codes while respecting visible character width.
func clampLineANSI(line string, width int) string {
	if width <= 0 {
		return line
	}
	if ansi.StringWidth(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(line, width, "…")
}

// normalizeColumn pads or truncates content to exactly width x height cells.
// This ensures all panes in a horizontal join have identical dimensions,
// preventing lipgloss.JoinHorizontal from producing ragged layouts.
func normalizeColumn(content string, width, height int) []string {
	width = max(width, 1)
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	result := make([]string, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		result[i] = fitLine(line, width)
	}
	return result
}

// fitLine truncates or right-pads a line to exactly width visible characters.
func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "")
	}
	pad := width - ansi.StringWidth(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

// overlayCentered composites a modal (overlay) on top of an existing rendered
// view (base), centering it both horizontally and vertically. Uses ANSI-aware
// string slicing so that base content styling is preserved around the overlay
// edges. This is how confirmation dialogs and action menus appear "on top of"
// the underlying view without re-rendering it.
func overlayCentered(base, overlay string, width int) string {
	base = strings.TrimSuffix(base, "\n")
	overlay = strings.TrimSuffix(overlay, "\n")
	baseLines := strings.Split(base, "\n")
	if len(baseLines) == 0 {
		baseLines = []string{""}
	}
	if width <= 0 {
		for _, line := range baseLines {
			width = max(width, ansi.StringWidth(line))
		}
		if width == 0 {
			width = 1
		}
	}
	for i, line := range baseLines {
		baseLines[i] = fitLine(line, width)
	}
	overlayLines := strings.Split(overlay, "\n")
	if len(overlayLines) == 0 {
		return strings.Join(baseLines, "\n")
	}
	overlayWidth := 0
	for _, line := range overlayLines {
		overlayWidth = max(overlayWidth, ansi.StringWidth(line))
	}
	if overlayWidth == 0 {
		return strings.Join(baseLines, "\n")
	}
	overlayWidth = min(overlayWidth, width)
	overlayHeight := min(len(overlayLines), len(baseLines))
	x := max(0, (width-overlayWidth)/2)
	y := max(0, (len(baseLines)-overlayHeight)/2)
	for i := 0; i < overlayHeight && y+i < len(baseLines); i++ {
		line := fitLine(overlayLines[i], overlayWidth)
		end := min(width, x+overlayWidth)
		if end <= x {
			continue
		}
		baseLine := baseLines[y+i]
		left := ansi.Cut(baseLine, 0, x)
		right := ansi.Cut(baseLine, end, width)
		baseLines[y+i] = left + fitLine(line, end-x) + right
	}
	return strings.Join(baseLines, "\n")
}

// renderWithBottomHint pins a single hint line at the bottom of a pane,
// truncating content from the middle if it exceeds the available height.
func renderWithBottomHint(content, hint string, height int) string {
	if hint == "" {
		return content
	}
	return renderWithBottomLines(content, []string{hint}, height)
}

// renderWithBottomLines pins multiple status/hint lines at the bottom of a
// pane. Content above is truncated to fit; empty lines fill any remaining
// gap so the hints are always flush with the pane bottom.
func renderWithBottomLines(content string, hints []string, height int) string {
	filtered := make([]string, 0, len(hints))
	for _, hint := range hints {
		if strings.TrimSpace(hint) != "" {
			filtered = append(filtered, hint)
		}
	}
	if len(filtered) == 0 {
		return content
	}
	if height <= 0 {
		trimmed := strings.TrimSuffix(content, "\n")
		lines := filtered
		if trimmed != "" {
			lines = append([]string{trimmed}, lines...)
		}
		return strings.Join(lines, "\n")
	}
	if height <= len(filtered) {
		return strings.Join(filtered[len(filtered)-height:], "\n")
	}
	trimmed := strings.TrimSuffix(content, "\n")
	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	available := height - len(filtered)
	if len(lines) > available {
		lines = lines[:available]
	}
	for len(lines) < available {
		lines = append(lines, "")
	}
	lines = append(lines, filtered...)
	return strings.Join(lines, "\n")
}

// joinLines joins non-empty lines with newlines.
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
