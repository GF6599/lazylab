package ui

import (
	"lazylab/internal/gitlab"
)

// mrTabState tracks which tab filter is active.
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

// mrViewState holds state for the Merge Requests panel.
type mrViewState struct {
	project  gitlab.ProjectNode
	mrs      []gitlab.MergeRequestSummary
	selected int
	loading  bool
	err      error
	tab      mrTab
	page     int
	total    int
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

	var lines []string
	for i, mr := range m.mrView.mrs {
		cursor := " "
		style := itemStyle
		if i == m.mrView.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(cursor+" !"+itoa(mr.IID)+" "+mr.Title, width)
		lines = append(lines, style.Render(line))
		if len(lines) >= height {
			break
		}
	}
	return joinLines(lines)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
