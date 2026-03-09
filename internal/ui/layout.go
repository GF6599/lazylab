// layout.go computes the multi-panel geometry for the lazygit-style layout.
//
// The screen is split into a left sidebar (stacked panels) and a right detail
// pane. Horizontally, the sidebar gets 30% of terminal width (with a minimum
// floor), with the detail pane taking the remainder. The user can toggle
// between 30/70 and 50/50 splits with +/-.
//
// Vertically, sidebar panels use an "accordion" layout: the focused panel
// expands to consume most of the height while unfocused panels shrink to a
// minimum. Three screen modes (Normal, Half, Full) control how aggressively
// the focused panel dominates. This keeps all panels visible for context while
// giving the active panel enough room to be useful.
//
// renderBorderedPane draws custom Unicode borders with an embedded title, tabs,
// and footer counter. It avoids lipgloss.Border because we need per-character
// control over the top/bottom lines to embed interactive elements.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Layout constants for the multi-panel view.
const (
	sidebarWidthPct = 30 // Sidebar takes 30% of terminal width
	minSidebarWidth = 28 // Minimum sidebar width in columns
	minPanelHeight  = 4  // Minimum content height for a non-focused panel
	infoBarHeight   = 1  // Bottom info bar
	detailMinWidth  = 30 // Detail pane won't shrink below this
	borderCharsH    = 2  // Left + right border chars per pane
	borderCharsV    = 2  // Top + bottom border chars per pane

	// MinTerminalWidth and MinTerminalHeight define the smallest terminal
	// dimensions where the TUI can render usefully. Below these thresholds
	// all modes show a "too small" message and block key input.
	MinTerminalWidth  = 63 // minSidebarWidth + detailMinWidth + paneGap + borderCharsH*2
	MinTerminalHeight = 10 // practical floor for any useful rendering
)

// layoutResult holds the computed dimensions for every element of the
// multi-panel layout. Content dimensions exclude borders; callers can
// render into these sizes directly.
type layoutResult struct {
	SidebarWidth int
	DetailWidth  int
	PanelHeights map[PanelID]int // Height for each sidebar panel (content area, excludes borders)
	DetailHeight int             // Content height for detail pane
	InfoBarWidth int             // Width for the info bar
	TotalHeight  int             // Total terminal height
	OK           bool            // False if terminal too small
}

// computeLayout calculates sidebar/detail widths and per-panel heights for the
// current terminal size and focus state. Returns OK=false if the terminal is
// too small to render the layout meaningfully.
// renderTooSmallView returns a centered, styled message telling the user that
// the terminal is below the minimum usable dimensions.
func renderTooSmallView(width, height int) string {
	msg := fmt.Sprintf("Terminal too small: %dx%d\nMinimum required:  %dx%d", width, height, MinTerminalWidth, MinTerminalHeight)
	styled := lipgloss.NewStyle().
		Foreground(rosePineGold).
		Bold(true).
		Align(lipgloss.Center).
		Render(msg)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, styled)
}

func computeLayout(width, height int, focus FocusState) layoutResult {
	if width < MinTerminalWidth || height < MinTerminalHeight {
		return layoutResult{OK: false}
	}

	// Sidebar width: percentage depends on layout mode
	var sidebarInner int
	if focus.LayoutMode == LayoutWide {
		sidebarInner = max(minSidebarWidth, width*50/100)
	} else {
		sidebarInner = max(minSidebarWidth, width*sidebarWidthPct/100)
	}
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
	detailOuter = max(detailOuter, borderCharsV+3)
	detailContent := detailOuter - borderCharsV

	// Sidebar accordion layout: when detail pane is focused, keep the
	// sidebar expanded as if the previous sidebar panel is still active.
	sidebarFocus := focus.Active
	if sidebarFocus == PanelDetail {
		sidebarFocus = focus.PrevActive
	}
	panelHeights := accordionLayout(SidebarPanels, sidebarFocus, focus.ScreenMode, availableHeight)

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

// accordionLayout distributes vertical space among sidebar panels using an
// accordion metaphor: the focused panel expands while others compress.
// ScreenMode controls the ratio — Full gives everything to the focused panel,
// Half splits evenly, Normal gives 40% to focused and distributes the rest.
func accordionLayout(panels []PanelID, focused PanelID, mode ScreenMode, totalHeight int) map[PanelID]int {
	heights := make(map[PanelID]int, len(panels))
	n := len(panels)
	if n == 0 {
		return heights
	}
	// When total height can't satisfy minimum constraints, distribute what
	// we have so rendered panels (content + borders) fit within totalHeight.
	// The focused panel gets leftover rows; in extreme cases only it renders.
	if totalHeight < n*(minPanelHeight+borderCharsV) {
		budget := totalHeight - n*borderCharsV // content rows after borders
		if budget < n {
			// Can't give every panel even 1 row; focus gets everything.
			for _, p := range panels {
				heights[p] = 0
			}
			heights[focused] = max(0, budget)
		} else {
			perPanel := budget / n
			leftover := budget - perPanel*n
			for _, p := range panels {
				if p == focused {
					heights[p] = perPanel + leftover
				} else {
					heights[p] = perPanel
				}
			}
		}
		return heights
	}

	// Total available for content after accounting for borders
	totalBorders := n * borderCharsV
	contentBudget := totalHeight - totalBorders
	contentBudget = max(contentBudget, n*minPanelHeight)

	switch mode {
	case ScreenFull:
		// Focused panel gets everything, others get minimum
		otherTotal := (n - 1) * minPanelHeight
		focusHeight := max(contentBudget-otherTotal, minPanelHeight)
		for _, p := range panels {
			if p == focused {
				heights[p] = focusHeight
			} else {
				heights[p] = minPanelHeight
			}
		}

	case ScreenHalf:
		// Focused panel gets half, rest distributed evenly
		focusHeight := max(contentBudget/2, minPanelHeight)
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
		focusHeight := max(contentBudget*40/100, minPanelHeight)
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

	// Reconcile: ensure sum of panel heights exactly equals contentBudget.
	// Integer division rounding and minPanelHeight clamping can cause the
	// total to drift. Absorb the difference into the focused panel.
	total := 0
	for _, p := range panels {
		total += heights[p]
	}
	if diff := total - contentBudget; diff != 0 {
		heights[focused] = max(0, heights[focused]-diff)
	}

	return heights
}

// scrollInfo describes the scroll position of a panel's content, used to render
// a mini scrollbar on the right border when content overflows.
type scrollInfo struct {
	offset int // first visible item/line index (0-based)
	total  int // total items/lines
}

// renderBorderedPane draws content inside a Unicode box border with an embedded
// title, optional tab bar, and footer counter. We build borders manually rather
// than using lipgloss.Border because the top line interleaves styled title text,
// tab indicators, and border characters that need independent coloring.
func renderBorderedPane(content string, width, height int, focused bool, title string, tabs []string, activeTab int, footer string, scroll ...scrollInfo) string {
	if width < 4 || height < 1 {
		return ""
	}

	borderFg := rosePineSubtle
	titleFg := rosePineIris
	if focused {
		borderFg = rosePineFoam
		titleFg = rosePineFoam
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderFg)
	titleStyleLocal := lipgloss.NewStyle().Bold(true).Foreground(titleFg)

	// Top border: ╭─ Title ── Tab1 | Tab2 ───╮
	topLine := buildTopBorder(width, title, tabs, activeTab, borderStyle, titleStyleLocal)

	// Compute scrollbar thumb range when content overflows.
	// Thumb size is proportional: (visible / total) * height, clamped to ≥1.
	// Thumb position maps the scroll offset to the available track (height - thumbSize).
	hasScrollbar := false
	thumbStart, thumbEnd := -1, -1
	if len(scroll) > 0 && scroll[0].total > height && height > 0 {
		s := scroll[0]
		hasScrollbar = true
		thumbSize := max(1, height*height/s.total)
		maxOffset := s.total - height
		if maxOffset > 0 {
			thumbStart = s.offset * (height - thumbSize) / maxOffset
		}
		thumbEnd = thumbStart + thumbSize
	}

	contentWidth := width - borderCharsH
	contentLines := normalizeColumn(content, contentWidth, height)

	thumbStyle := lipgloss.NewStyle().Foreground(rosePineFoam)

	var body strings.Builder
	for i, line := range contentLines {
		body.WriteString(borderStyle.Render("│"))
		body.WriteString(fitLine(line, contentWidth))
		if hasScrollbar && i >= thumbStart && i < thumbEnd {
			body.WriteString(thumbStyle.Render("▐"))
		} else {
			body.WriteString(borderStyle.Render("│"))
		}
		body.WriteString("\n")
	}

	// Bottom border: ╰──────────── 3 of 47 ──╯
	bottomLine := buildBottomBorder(width, footer, borderStyle)

	return topLine + "\n" + body.String() + bottomLine
}

// buildTopBorder constructs the top border with title and optional tabs.
func buildTopBorder(width int, title string, tabs []string, activeTab int, borderStyle, titleStyleLocal lipgloss.Style) string {
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
		tabStr := buildTabString(tabs, activeTab)
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
func buildTabString(tabs []string, activeTab int) string {
	if len(tabs) == 0 {
		return ""
	}
	var parts []string
	for i, tab := range tabs {
		if i == activeTab {
			style := lipgloss.NewStyle().Bold(true).Foreground(rosePineIris)
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
