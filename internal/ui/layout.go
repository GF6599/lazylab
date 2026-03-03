package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Layout constants for the multi-panel view.
const (
	sidebarWidthPct = 30 // Sidebar takes 30% of terminal width
	minSidebarWidth = 28 // Minimum sidebar width
	maxSidebarWidth = 50 // Maximum sidebar width
	minPanelHeight  = 4  // Minimum height for a non-focused panel
	infoBarHeight   = 1  // Bottom info bar
	detailMinWidth  = 30 // Minimum detail pane width
	borderCharsH    = 2  // Left + right border chars per pane
	borderCharsV    = 2  // Top + bottom border chars per pane
)

// layoutResult holds the computed dimensions for the multi-panel layout.
type layoutResult struct {
	SidebarWidth int
	DetailWidth  int
	PanelHeights map[PanelID]int // Height for each sidebar panel (content area, excludes borders)
	DetailHeight int             // Content height for detail pane
	InfoBarWidth int             // Width for the info bar
	TotalHeight  int             // Total terminal height
	OK           bool            // False if terminal too small
}

// computeLayout calculates the full multi-panel layout based on terminal dimensions.
func computeLayout(width, height int, focus FocusState) layoutResult {
	if width < minSidebarWidth+detailMinWidth+paneGap+borderCharsH*2 || height < 10 {
		return layoutResult{OK: false}
	}

	// Sidebar width: 30% of terminal, clamped to min/max
	sidebarInner := max(minSidebarWidth, min(maxSidebarWidth, width*sidebarWidthPct/100))
	sidebarOuter := sidebarInner + borderCharsH

	// Detail area
	rightOuter := width - sidebarOuter - paneGap
	detailInner := rightOuter - borderCharsH
	if detailInner < detailMinWidth {
		detailInner = detailMinWidth
		rightOuter = detailInner + borderCharsH
		sidebarOuter = width - rightOuter - paneGap
		sidebarInner = sidebarOuter - borderCharsH
	}

	// Vertical layout: sidebar panels + info bar
	availableHeight := height - infoBarHeight

	// Detail pane gets all remaining vertical space
	detailOuter := availableHeight
	if detailOuter < borderCharsV+3 {
		detailOuter = borderCharsV + 3
	}
	detailContent := detailOuter - borderCharsV

	// Sidebar accordion layout
	panelHeights := accordionLayout(SidebarPanels, focus.Active, focus.ScreenMode, availableHeight)

	return layoutResult{
		SidebarWidth: sidebarInner,
		DetailWidth:  detailInner,
		PanelHeights: panelHeights,
		DetailHeight: detailContent,
		InfoBarWidth: width,
		TotalHeight:  height,
		OK:           true,
	}
}

// accordionLayout distributes height among sidebar panels. The focused panel
// gets the majority of space; other panels get the minimum.
func accordionLayout(panels []PanelID, focused PanelID, mode ScreenMode, totalHeight int) map[PanelID]int {
	heights := make(map[PanelID]int, len(panels))
	n := len(panels)
	if n == 0 || totalHeight < n*(minPanelHeight+borderCharsV) {
		for _, p := range panels {
			heights[p] = minPanelHeight
		}
		return heights
	}

	// Total available for content after accounting for borders
	totalBorders := n * borderCharsV
	contentBudget := totalHeight - totalBorders
	if contentBudget < n*minPanelHeight {
		contentBudget = n * minPanelHeight
	}

	switch mode {
	case ScreenFull:
		// Focused panel gets everything, others get minimum
		otherTotal := (n - 1) * minPanelHeight
		focusHeight := contentBudget - otherTotal
		if focusHeight < minPanelHeight {
			focusHeight = minPanelHeight
		}
		for _, p := range panels {
			if p == focused {
				heights[p] = focusHeight
			} else {
				heights[p] = minPanelHeight
			}
		}

	case ScreenHalf:
		// Focused panel gets half, rest distributed evenly
		focusHeight := contentBudget / 2
		if focusHeight < minPanelHeight {
			focusHeight = minPanelHeight
		}
		remaining := contentBudget - focusHeight
		otherHeight := minPanelHeight
		if n > 1 {
			otherHeight = max(minPanelHeight, remaining/(n-1))
		}
		for _, p := range panels {
			if p == focused {
				heights[p] = focusHeight
			} else {
				heights[p] = otherHeight
			}
		}

	default: // ScreenNormal
		// Focused panel gets 40% of budget, rest distributed evenly
		focusHeight := contentBudget * 40 / 100
		if focusHeight < minPanelHeight {
			focusHeight = minPanelHeight
		}
		remaining := contentBudget - focusHeight
		otherHeight := minPanelHeight
		if n > 1 {
			otherHeight = max(minPanelHeight, remaining/(n-1))
		}
		for _, p := range panels {
			if p == focused {
				heights[p] = focusHeight
			} else {
				heights[p] = otherHeight
			}
		}
	}

	return heights
}

// renderBorderedPane renders content with custom borders including title/tabs and footer counter.
func renderBorderedPane(content string, width, height int, focused bool, title string, tabs []string, activeTab int, footer string) string {
	if width < 4 || height < 1 {
		return ""
	}

	borderFg := rosePineSubtle
	titleFg := rosePineIris
	if focused {
		borderFg = rosePineRose
		titleFg = rosePineRose
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderFg)
	titleStyleLocal := lipgloss.NewStyle().Bold(true).Foreground(titleFg)

	// Top border: ╭─ Title ── Tab1 | Tab2 ───╮
	topLine := buildTopBorder(width, title, tabs, activeTab, borderStyle, titleStyleLocal, focused)

	// Content area: pad/truncate to exact dimensions
	contentLines := normalizeColumn(content, width-borderCharsH, height)
	var body strings.Builder
	for _, line := range contentLines {
		body.WriteString(borderStyle.Render("│"))
		body.WriteString(fitLine(line, width-borderCharsH))
		body.WriteString(borderStyle.Render("│"))
		body.WriteString("\n")
	}

	// Bottom border: ╰──────────── 3 of 47 ──╯
	bottomLine := buildBottomBorder(width, footer, borderStyle)

	return topLine + "\n" + body.String() + bottomLine
}

// buildTopBorder constructs the top border with title and optional tabs.
func buildTopBorder(width int, title string, tabs []string, activeTab int, borderStyle, titleStyleLocal lipgloss.Style, focused bool) string {
	if width < 4 {
		return borderStyle.Render(strings.Repeat("─", width))
	}

	left := "╭─ "
	right := " ─╮"
	available := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if available < 1 {
		return borderStyle.Render("╭" + strings.Repeat("─", max(0, width-2)) + "╮")
	}

	var middle strings.Builder
	if title != "" {
		rendered := titleStyleLocal.Render(title)
		titleWidth := ansi.StringWidth(rendered)
		if titleWidth <= available {
			middle.WriteString(rendered)
			available -= titleWidth
		} else {
			truncTitle := ansi.Truncate(rendered, available, "")
			middle.WriteString(truncTitle)
			available -= ansi.StringWidth(truncTitle)
		}
	}

	if len(tabs) > 0 && available > 4 {
		tabStr := buildTabString(tabs, activeTab, focused)
		tabWidth := ansi.StringWidth(tabStr)
		if tabWidth+3 <= available { // 3 for " ── " separator
			sep := " ── "
			middle.WriteString(borderStyle.Render(sep))
			middle.WriteString(tabStr)
			available -= ansi.StringWidth(sep) + tabWidth
		}
	}

	// Fill remaining with horizontal line
	if available > 0 {
		middle.WriteString(borderStyle.Render(strings.Repeat("─", available)))
	}

	return borderStyle.Render(left) + middle.String() + borderStyle.Render(right)
}

// buildTabString constructs the tab portion: "Tab1 | Tab2"
func buildTabString(tabs []string, activeTab int, focused bool) string {
	if len(tabs) == 0 {
		return ""
	}
	var parts []string
	for i, tab := range tabs {
		if i == activeTab {
			style := lipgloss.NewStyle().Bold(true).Foreground(rosePineRose)
			parts = append(parts, style.Render(tab))
		} else {
			style := lipgloss.NewStyle().Foreground(rosePineMuted)
			parts = append(parts, style.Render(tab))
		}
	}
	sep := lipgloss.NewStyle().Foreground(rosePineSubtle).Render(" | ")
	return strings.Join(parts, sep)
}

// buildBottomBorder constructs the bottom border with optional footer counter.
func buildBottomBorder(width int, footer string, borderStyle lipgloss.Style) string {
	if width < 4 {
		return borderStyle.Render(strings.Repeat("─", width))
	}

	left := "╰"
	right := "╯"
	available := width - ansi.StringWidth(left) - ansi.StringWidth(right)

	if footer == "" || available < len(footer)+4 {
		return borderStyle.Render(left + strings.Repeat("─", max(0, available)) + right)
	}

	footerRendered := lipgloss.NewStyle().Foreground(rosePineMuted).Render(footer)
	footerWidth := ansi.StringWidth(footerRendered)
	fillLeft := available - footerWidth - 2 // 2 for " " padding around footer
	if fillLeft < 1 {
		return borderStyle.Render(left + strings.Repeat("─", max(0, available)) + right)
	}

	return borderStyle.Render(left+strings.Repeat("─", fillLeft)+" ") + footerRendered + borderStyle.Render(" "+right)
}
