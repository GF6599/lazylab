// Package diffutil parses unified-diff text and renders snippets. It is UI- and
// GitLab-agnostic so it can be reused outside the MR panel.
package diffutil

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// FileDiff is the minimal view of a changed file the diff parser needs.
// Callers pass an adapter from their domain type (e.g. gitlab.MRDiffFile) so
// this package never has to know about a specific API client.
type FileDiff struct {
	OldPath string
	NewPath string
	// Diff must be a raw unified diff (the "@@", "+", "-", " " line stream),
	// not pre-rendered text: the parsers in this package match on those line
	// prefixes. CRLF is tolerated — every parser strips "\r" before inspecting
	// a line — so callers need not normalize line endings first.
	Diff string
}

// LineInfo maps a rendered diff line back to its source file and line number.
// Kind is one of: '+', '-', ' ' (context), '@' (hunk), 'H' (header), 'D' (divider).
type LineInfo struct {
	FileIdx int
	OldLine int // 0 = not applicable (additions)
	NewLine int // 0 = not applicable (deletions)
	Kind    byte
}

// SnippetStyles bundles the lipgloss styles RenderSnippet applies. Passing
// styles in (rather than importing UI theme globals) keeps this package
// independent of the host application's theme wiring.
type SnippetStyles struct {
	Add     lipgloss.Style
	Del     lipgloss.Style
	Hunk    lipgloss.Style
	Context lipgloss.Style
}

// BuildLineMap creates a mapping from rendered line index to source file/line
// info, mirroring the line-emission logic in the MR diff renderer.
//
// The returned slice is 1:1 and order-aligned with the rendered diff lines:
// element i describes rendered line i, so callers can index into it directly
// using a cursor position. To stay aligned it must emit one LineInfo per
// rendered line in the exact same order the renderer produces them — for each
// file: an inter-file blank separator ('D', skipped for the first file), the
// file header ('H'), a divider ('D'), then every line of d.Diff. Changing this
// emission order without matching the renderer breaks cursor-to-source lookups.
func BuildLineMap(diffs []FileDiff) []LineInfo {
	var m []LineInfo
	for i, d := range diffs {
		if i > 0 {
			m = append(m, LineInfo{FileIdx: i, Kind: 'D'}) // blank separator
		}
		// File header line
		m = append(m, LineInfo{FileIdx: i, Kind: 'H'})
		// Divider line
		m = append(m, LineInfo{FileIdx: i, Kind: 'D'})
		// Diff lines
		oldLine, newLine := 0, 0
		for _, line := range strings.Split(d.Diff, "\n") {
			line = strings.ReplaceAll(line, "\r", "")
			switch {
			case strings.HasPrefix(line, "@@"):
				oldLine, newLine = ParseHunkHeader(line)
				m = append(m, LineInfo{FileIdx: i, Kind: '@', OldLine: oldLine, NewLine: newLine})
			case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++"):
				m = append(m, LineInfo{FileIdx: i, Kind: 'H'})
			case strings.HasPrefix(line, "+"):
				m = append(m, LineInfo{FileIdx: i, Kind: '+', NewLine: newLine})
				newLine++
			case strings.HasPrefix(line, "-"):
				m = append(m, LineInfo{FileIdx: i, Kind: '-', OldLine: oldLine})
				oldLine++
			default:
				m = append(m, LineInfo{FileIdx: i, Kind: ' ', OldLine: oldLine, NewLine: newLine})
				oldLine++
				newLine++
			}
		}
	}
	return m
}

// ParseHunkHeader extracts old and new starting line numbers from a unified
// diff hunk header. It returns (1, 1) only when the line has no "@@"-delimited
// body at all. Otherwise it parses each side independently, so a header missing
// (or carrying an unparseable number for) one side leaves just that side at 0
// while the other keeps its parsed value — the fallback is per-side, not
// all-or-nothing.
func ParseHunkHeader(line string) (oldLine, newLine int) {
	// Format: @@ -old,count +new,count @@
	parts := strings.SplitN(line, "@@", 3)
	if len(parts) < 2 {
		return 1, 1
	}
	inner := strings.TrimSpace(parts[1])
	fields := strings.Fields(inner)
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			nums := strings.SplitN(f[1:], ",", 2)
			fmt.Sscanf(nums[0], "%d", &oldLine)
		} else if strings.HasPrefix(f, "+") {
			nums := strings.SplitN(f[1:], ",", 2)
			fmt.Sscanf(nums[0], "%d", &newLine)
		}
	}
	return oldLine, newLine
}

// ExtractContext returns a slice of raw unified-diff lines surrounding a
// positioned comment, giving the reader visual context.
//
// Matching strategy: newLine is preferred for additions and context lines
// (the common case), while oldLine is only used for pure deletions where
// newLine is 0. This mirrors how GitLab's Position API populates the fields.
//
// The returned window is clamped to hunk boundaries (@@, ---, +++ lines) so
// the snippet never bleeds into an unrelated hunk or file header. Returns nil
// when no match is found, diffs is nil, or contextLines is 0.
func ExtractContext(diffs []FileDiff, filePath string, oldLine, newLine, contextLines int) []string {
	if len(diffs) == 0 || filePath == "" || contextLines <= 0 {
		return nil
	}
	var diffText string
	for _, d := range diffs {
		if d.NewPath == filePath || d.OldPath == filePath {
			diffText = d.Diff
			break
		}
	}
	if diffText == "" {
		return nil
	}

	lines := strings.Split(diffText, "\n")
	targetIdx := FindTargetLine(lines, oldLine, newLine)
	if targetIdx < 0 {
		return nil
	}

	start := max(0, targetIdx-contextLines)
	end := min(len(lines), targetIdx+contextLines+1)
	// Clamp start: don't cross a hunk header going backwards
	for i := targetIdx - 1; i >= start; i-- {
		if strings.HasPrefix(lines[i], "---") || strings.HasPrefix(lines[i], "+++") {
			start = i + 1
			break
		}
	}
	// Clamp end: don't cross a hunk header going forwards (but include @@ if at start)
	for i := targetIdx + 1; i < end; i++ {
		if strings.HasPrefix(lines[i], "@@") || strings.HasPrefix(lines[i], "---") || strings.HasPrefix(lines[i], "+++") {
			end = i
			break
		}
	}
	if start >= end {
		return nil
	}
	return lines[start:end]
}

// FindTargetLine walks a unified diff's lines, tracking old/new line counters
// through hunk headers, and returns the index of the line matching the
// comment's position. Returns -1 if no line matches.
//
// When newLine > 0 it matches additions (+) and context lines on the new side.
// When newLine == 0 && oldLine > 0 it matches deletions (-) and context on
// the old side. This split handles the asymmetry in GitLab's position model:
// additions only have NewLine, deletions only have OldLine, and context lines
// have both (but NewLine takes priority for matching).
func FindTargetLine(lines []string, oldLine, newLine int) int {
	curOld, curNew := 0, 0
	for i, line := range lines {
		line = strings.ReplaceAll(line, "\r", "")
		switch {
		case strings.HasPrefix(line, "@@"):
			curOld, curNew = ParseHunkHeader(line)
		case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++"):
			// skip diff file headers
		case strings.HasPrefix(line, "+"):
			if newLine > 0 && curNew == newLine {
				return i
			}
			curNew++
		case strings.HasPrefix(line, "-"):
			if oldLine > 0 && newLine == 0 && curOld == oldLine {
				return i
			}
			curOld++
		default:
			if newLine > 0 && curNew == newLine {
				return i
			}
			if oldLine > 0 && newLine == 0 && curOld == oldLine {
				return i
			}
			curOld++
			curNew++
		}
	}
	return -1
}

// RenderSnippet applies diff styling (green for additions, red for deletions,
// dimmed for hunk headers) to a slice of raw diff lines. Each line is indented
// by 2 spaces so the snippet sits inside surrounding tree-line layout without
// visual collision. Styles are passed in so this package stays decoupled from
// any host theme.
//
// width is the available column budget: width <= 0 disables truncation
// entirely (lines render at full length), while a positive width truncates to
// width-4 columns. The -4 reserves room for the 2-space indent plus the
// truncation ellipsis, so it must track the indent above if either changes.
func RenderSnippet(rawLines []string, width int, styles SnippetStyles) string {
	var b strings.Builder
	for _, line := range rawLines {
		line = strings.ReplaceAll(line, "\t", "    ")
		line = strings.ReplaceAll(line, "\r", "")
		if width > 0 {
			line = ansi.Truncate(line, width-4, "…")
		}
		var styled string
		switch {
		case strings.HasPrefix(line, "+"):
			styled = styles.Add.Render("  " + line)
		case strings.HasPrefix(line, "-"):
			styled = styles.Del.Render("  " + line)
		case strings.HasPrefix(line, "@@"):
			styled = styles.Hunk.Render("  " + line)
		default:
			styled = styles.Context.Render("  " + line)
		}
		b.WriteString(styled + "\n")
	}
	return b.String()
}
