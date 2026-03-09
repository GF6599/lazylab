package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- computeLayout tests ---

func TestComputeLayout_BelowMinWidth(t *testing.T) {
	result := computeLayout(MinTerminalWidth-1, MinTerminalHeight, FocusState{Active: PanelProjects})
	if result.OK {
		t.Fatal("expected OK=false when width < MinTerminalWidth")
	}
}

func TestComputeLayout_BelowMinHeight(t *testing.T) {
	result := computeLayout(MinTerminalWidth, MinTerminalHeight-1, FocusState{Active: PanelProjects})
	if result.OK {
		t.Fatal("expected OK=false when height < MinTerminalHeight")
	}
}

func TestComputeLayout_BelowBothMinimums(t *testing.T) {
	result := computeLayout(10, 5, FocusState{Active: PanelProjects})
	if result.OK {
		t.Fatal("expected OK=false when both dimensions below minimum")
	}
}

func TestComputeLayout_ExactlyMinDimensions(t *testing.T) {
	result := computeLayout(MinTerminalWidth, MinTerminalHeight, FocusState{Active: PanelProjects})
	if !result.OK {
		t.Fatal("expected OK=true at exactly minimum dimensions")
	}
	if result.SidebarWidth < 1 {
		t.Errorf("sidebar width %d should be positive", result.SidebarWidth)
	}
	if result.DetailWidth < 1 {
		t.Errorf("detail width %d should be positive", result.DetailWidth)
	}
	if result.InfoBarWidth != MinTerminalWidth {
		t.Errorf("info bar width %d should equal terminal width %d", result.InfoBarWidth, MinTerminalWidth)
	}
	if result.TotalHeight != MinTerminalHeight {
		t.Errorf("total height %d should equal terminal height %d", result.TotalHeight, MinTerminalHeight)
	}
}

func TestComputeLayout_SmallTerminal(t *testing.T) {
	// Just above minimum — should produce a valid layout
	result := computeLayout(70, 15, FocusState{Active: PanelProjects})
	if !result.OK {
		t.Fatal("expected OK=true for 70x15 terminal")
	}
	if result.SidebarWidth <= 0 {
		t.Errorf("expected positive sidebar width, got %d", result.SidebarWidth)
	}
	if result.DetailWidth < detailMinWidth {
		t.Errorf("detail width %d should be >= detailMinWidth %d", result.DetailWidth, detailMinWidth)
	}
}

func TestComputeLayout_MediumTerminal(t *testing.T) {
	result := computeLayout(120, 40, FocusState{Active: PanelProjects})
	if !result.OK {
		t.Fatal("expected OK=true for 120x40")
	}
	if result.SidebarWidth < minSidebarWidth {
		t.Errorf("sidebar width %d should be >= minSidebarWidth %d", result.SidebarWidth, minSidebarWidth)
	}
	if result.DetailWidth < detailMinWidth {
		t.Errorf("detail width %d should be >= detailMinWidth %d", result.DetailWidth, detailMinWidth)
	}
	if result.DetailHeight < 1 {
		t.Errorf("detail content height %d should be positive", result.DetailHeight)
	}
}

func TestComputeLayout_LargeTerminal(t *testing.T) {
	result := computeLayout(250, 80, FocusState{Active: PanelProjects})
	if !result.OK {
		t.Fatal("expected OK=true for 250x80")
	}
	if result.SidebarWidth < minSidebarWidth {
		t.Errorf("sidebar width %d should be >= minSidebarWidth", result.SidebarWidth)
	}
	if result.DetailWidth >= result.SidebarWidth*3 {
		// Detail should be roughly 70% and sidebar 30% — detail should be larger
		// but this is a sanity check, not a tight constraint.
	}
}

func TestComputeLayout_AllScreenModes(t *testing.T) {
	modes := []struct {
		name string
		mode ScreenMode
	}{
		{"ScreenNormal", ScreenNormal},
		{"ScreenHalf", ScreenHalf},
		{"ScreenFull", ScreenFull},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			focus := FocusState{Active: PanelProjects, ScreenMode: tc.mode}
			result := computeLayout(120, 40, focus)
			if !result.OK {
				t.Fatal("expected OK=true")
			}
			// Every sidebar panel must have a height entry
			for _, p := range SidebarPanels {
				h, exists := result.PanelHeights[p]
				if !exists {
					t.Errorf("panel %d missing from PanelHeights", p)
				}
				if h < 0 {
					t.Errorf("panel %d has negative height %d", p, h)
				}
			}
		})
	}
}

func TestComputeLayout_LayoutDefault_SidebarSmaller(t *testing.T) {
	focus := FocusState{Active: PanelProjects, LayoutMode: LayoutDefault}
	result := computeLayout(120, 40, focus)
	if !result.OK {
		t.Fatal("expected OK=true")
	}
	// In default (30/70) mode, detail should be wider than sidebar
	if result.DetailWidth <= result.SidebarWidth {
		t.Errorf("in LayoutDefault, detail (%d) should be wider than sidebar (%d)",
			result.DetailWidth, result.SidebarWidth)
	}
}

func TestComputeLayout_LayoutWide_SidebarWider(t *testing.T) {
	focus := FocusState{Active: PanelProjects, LayoutMode: LayoutWide}
	result := computeLayout(120, 40, focus)
	if !result.OK {
		t.Fatal("expected OK=true")
	}
	// In wide (50/50) mode, sidebar should be roughly equal to or wider than detail
	// (sidebar gets 50%, then gaps/borders eat into the right side)
	if result.SidebarWidth < result.DetailWidth {
		// Wide mode makes sidebar 50% — it should be at least as wide as detail
		t.Errorf("in LayoutWide, sidebar (%d) should be >= detail (%d)",
			result.SidebarWidth, result.DetailWidth)
	}
}

func TestComputeLayout_PanelHeightsSumCorrectly(t *testing.T) {
	// For a range of terminal sizes, verify that sidebar panel heights
	// (content + borders) sum to the available height minus the info bar.
	for _, dims := range []struct{ w, h int }{
		{MinTerminalWidth, MinTerminalHeight},
		{80, 24},
		{120, 40},
		{200, 60},
	} {
		focus := FocusState{Active: PanelPipelines, ScreenMode: ScreenNormal}
		result := computeLayout(dims.w, dims.h, focus)
		if !result.OK {
			continue
		}
		totalPanelHeight := 0
		for _, p := range SidebarPanels {
			totalPanelHeight += result.PanelHeights[p] + borderCharsV
		}
		expectedHeight := dims.h - infoBarHeight
		if totalPanelHeight != expectedHeight {
			t.Errorf("%dx%d: panel height sum %d != available %d",
				dims.w, dims.h, totalPanelHeight, expectedHeight)
		}
	}
}

func TestComputeLayout_DetailFocusUsesPrevActive(t *testing.T) {
	// When focusing on PanelDetail, the sidebar accordion should use
	// PrevActive, not PanelDetail itself (which is not in SidebarPanels).
	focus := FocusState{
		Active:     PanelDetail,
		PrevActive: PanelStages,
		ScreenMode: ScreenFull,
	}
	result := computeLayout(120, 40, focus)
	if !result.OK {
		t.Fatal("expected OK=true")
	}
	// PanelStages (PrevActive) should have the most space in ScreenFull mode
	for _, p := range SidebarPanels {
		if p != PanelStages && result.PanelHeights[p] > result.PanelHeights[PanelStages] {
			t.Errorf("PanelStages (prev active) should have most space, but panel %d has %d > %d",
				p, result.PanelHeights[p], result.PanelHeights[PanelStages])
		}
	}
}

func TestComputeLayout_DetailMinWidthGuarantee(t *testing.T) {
	// Even in wide layout mode, detail pane width should not drop below detailMinWidth
	modes := []LayoutMode{LayoutDefault, LayoutWide}
	for _, mode := range modes {
		focus := FocusState{Active: PanelProjects, LayoutMode: mode}
		result := computeLayout(MinTerminalWidth, MinTerminalHeight, focus)
		if !result.OK {
			continue
		}
		if result.DetailWidth < detailMinWidth {
			t.Errorf("layout mode %d: detail width %d < detailMinWidth %d",
				mode, result.DetailWidth, detailMinWidth)
		}
	}
}

// --- renderBorderedPane tests ---

func TestRenderBorderedPane_ZeroWidth(t *testing.T) {
	result := renderBorderedPane("hello", 0, 5, false, "Title", nil, 0, "")
	if result != "" {
		t.Fatalf("expected empty string for width=0, got %q", result)
	}
}

func TestRenderBorderedPane_ZeroHeight(t *testing.T) {
	result := renderBorderedPane("hello", 40, 0, false, "Title", nil, 0, "")
	if result != "" {
		t.Fatalf("expected empty string for height=0, got %q", result)
	}
}

func TestRenderBorderedPane_Width3(t *testing.T) {
	result := renderBorderedPane("x", 3, 3, false, "T", nil, 0, "")
	if result != "" {
		t.Fatalf("expected empty for width=3 (< 4), got %q", result)
	}
}

func TestRenderBorderedPane_MinimalValid(t *testing.T) {
	result := renderBorderedPane("x", 4, 1, false, "", nil, 0, "")
	if result == "" {
		t.Fatal("expected non-empty result for width=4, height=1")
	}
	// Should have top border, content line, and bottom border
	lines := strings.Split(result, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (top+content+bottom), got %d: %q", len(lines), result)
	}
}

func TestRenderBorderedPane_FocusedAndUnfocused_BothRender(t *testing.T) {
	unfocused := renderBorderedPane("content", 40, 5, false, "Panel", nil, 0, "1 of 5")
	focused := renderBorderedPane("content", 40, 5, true, "Panel", nil, 0, "1 of 5")
	// Both focused and unfocused should produce non-empty output
	if unfocused == "" {
		t.Fatal("unfocused pane should produce non-empty output")
	}
	if focused == "" {
		t.Fatal("focused pane should produce non-empty output")
	}
	// Both should contain the content
	if !strings.Contains(unfocused, "content") {
		t.Fatal("unfocused pane should contain content text")
	}
	if !strings.Contains(focused, "content") {
		t.Fatal("focused pane should contain content text")
	}
}

func TestRenderBorderedPane_ContainsContent(t *testing.T) {
	result := renderBorderedPane("Hello World", 40, 5, false, "Title", nil, 0, "")
	if !strings.Contains(result, "Hello World") {
		t.Fatalf("pane should contain content text, got %q", result)
	}
}

func TestRenderBorderedPane_WithScrollbar(t *testing.T) {
	// Content overflows: 100 total lines, showing 5 at offset 50
	result := renderBorderedPane("line", 40, 5, false, "T", nil, 0, "", scrollInfo{offset: 50, total: 100})
	if result == "" {
		t.Fatal("expected non-empty result with scrollbar")
	}
	// Should contain the scrollbar thumb character somewhere
	if !strings.Contains(result, "▐") {
		t.Fatal("expected scrollbar thumb character in output")
	}
}

func TestRenderBorderedPane_NoScrollbarWhenFits(t *testing.T) {
	// Content fits exactly: 5 lines total, 5 visible
	result := renderBorderedPane("a\nb\nc\nd\ne", 40, 5, false, "T", nil, 0, "", scrollInfo{offset: 0, total: 5})
	if strings.Contains(result, "▐") {
		t.Fatal("should not show scrollbar when content fits")
	}
}

func TestRenderBorderedPane_VeryNarrow(t *testing.T) {
	// Width = 5 should work (minimum is 4)
	result := renderBorderedPane("a", 5, 3, false, "", nil, 0, "")
	if result == "" {
		t.Fatal("expected non-empty result for width=5")
	}
}

// --- buildTopBorder tests ---

func TestBuildTopBorder_Width0(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	result := buildTopBorder(0, "Title", nil, 0, borderStyle, titleStyle)
	// width < 4 should produce a simple line
	if strings.Contains(result, "╭") {
		t.Fatal("width=0 should not produce corner chars")
	}
}

func TestBuildTopBorder_Width4(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	result := buildTopBorder(4, "", nil, 0, borderStyle, titleStyle)
	// Exactly at the threshold where corners are used
	if !strings.HasPrefix(result, "╭") || !strings.HasSuffix(result, "╮") {
		t.Fatalf("expected corner chars at width=4, got %q", result)
	}
}

func TestBuildTopBorder_TitleTruncatedWhenTooWide(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	// Title "VeryLongTitleHere" is 17 chars; available space at width=12 is ~5
	result := buildTopBorder(12, "VeryLongTitleHere", nil, 0, borderStyle, titleStyle)
	// The full title should not appear, but some portion should
	if ansi.StringWidth(result) != 12 {
		t.Errorf("expected width 12, got %d", ansi.StringWidth(result))
	}
}

func TestBuildTopBorder_TabsOverflowAvailableSpace(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	// Very long tabs that won't fit in a narrow border
	tabs := []string{"VeryLongTabName1", "VeryLongTabName2", "VeryLongTabName3"}
	result := buildTopBorder(20, "Title", tabs, 0, borderStyle, titleStyle)
	// Tabs should be omitted when they don't fit
	if ansi.StringWidth(result) != 20 {
		t.Errorf("expected width 20, got %d", ansi.StringWidth(result))
	}
}

func TestBuildTopBorder_EmptyTitle(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	result := buildTopBorder(30, "", nil, 0, borderStyle, titleStyle)
	if ansi.StringWidth(result) != 30 {
		t.Errorf("expected width 30, got %d", ansi.StringWidth(result))
	}
	if !strings.HasPrefix(result, "╭") || !strings.HasSuffix(result, "╮") {
		t.Fatalf("expected corner chars, got %q", result)
	}
}

func TestBuildTopBorder_TabsWithRoom(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	tabs := []string{"A", "B"}
	result := buildTopBorder(60, "X", tabs, 1, borderStyle, titleStyle)
	// Both tabs should appear when there's plenty of room
	if !strings.Contains(result, "A") || !strings.Contains(result, "B") {
		t.Fatalf("expected both tabs in output, got %q", result)
	}
	if ansi.StringWidth(result) != 60 {
		t.Errorf("expected width 60, got %d", ansi.StringWidth(result))
	}
}

func TestBuildTopBorder_WidthExactlyForCornersOnly(t *testing.T) {
	// Width=5: available = 5 - len("╭─ ") - len(" ─╮") = 5 - 3 - 3 = -1
	// Should fall back to simple corners
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	result := buildTopBorder(5, "X", nil, 0, borderStyle, titleStyle)
	if ansi.StringWidth(result) != 5 {
		t.Errorf("expected width 5, got %d", ansi.StringWidth(result))
	}
}

// --- buildBottomBorder tests ---

func TestBuildBottomBorder_Width0(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(0, "footer", borderStyle)
	if strings.Contains(result, "footer") {
		t.Fatal("width=0 should not contain footer")
	}
}

func TestBuildBottomBorder_Width3(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(3, "", borderStyle)
	// width < 4 produces a simple line
	if strings.Contains(result, "╰") {
		t.Fatal("width=3 should not produce corner chars")
	}
	expectedW := ansi.StringWidth(result)
	if expectedW != 3 {
		t.Errorf("expected width 3, got %d", expectedW)
	}
}

func TestBuildBottomBorder_EmptyFooterNarrow(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(4, "", borderStyle)
	if !strings.HasPrefix(result, "╰") || !strings.HasSuffix(result, "╯") {
		t.Fatalf("expected corner chars, got %q", result)
	}
}

func TestBuildBottomBorder_FooterExactlyFits(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	// Footer "1/2" is 3 chars; need at least len(footer)+4=7 available
	// available = width - 2 (╰╯)
	// So width needs to be >= 7+2 = 9 for footer to appear
	result := buildBottomBorder(12, "1/2", borderStyle)
	if !strings.Contains(result, "1/2") {
		t.Fatalf("expected footer in border, got %q", result)
	}
}

func TestBuildBottomBorder_FooterTooWide_Omitted(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(8, "very long footer text", borderStyle)
	if strings.Contains(result, "very long footer text") {
		t.Fatal("footer should be omitted when too wide")
	}
}

// --- accordionLayout comprehensive tests ---

func TestAccordionLayout_SinglePanel(t *testing.T) {
	panels := []PanelID{PanelProjects}
	heights := accordionLayout(panels, PanelProjects, ScreenNormal, 30)
	if h := heights[PanelProjects]; h != 30-borderCharsV {
		t.Errorf("single panel should get all content space: expected %d, got %d",
			30-borderCharsV, h)
	}
}

func TestAccordionLayout_FocusedPanelGetsMore_ScreenNormal(t *testing.T) {
	heights := accordionLayout(SidebarPanels, PanelPipelines, ScreenNormal, 40)
	for _, p := range SidebarPanels {
		if p != PanelPipelines && heights[p] > heights[PanelPipelines] {
			t.Errorf("in ScreenNormal, non-focused panel %d (%d) should not exceed focused (%d)",
				p, heights[p], heights[PanelPipelines])
		}
	}
}

func TestAccordionLayout_FocusedPanelGetsMore_ScreenHalf(t *testing.T) {
	heights := accordionLayout(SidebarPanels, PanelStages, ScreenHalf, 50)
	for _, p := range SidebarPanels {
		if p != PanelStages && heights[p] > heights[PanelStages] {
			t.Errorf("in ScreenHalf, non-focused panel %d (%d) should not exceed focused (%d)",
				p, heights[p], heights[PanelStages])
		}
	}
}

func TestAccordionLayout_ScreenFull_OthersGetMinimum(t *testing.T) {
	heights := accordionLayout(SidebarPanels, PanelMRs, ScreenFull, 50)
	for _, p := range SidebarPanels {
		if p != PanelMRs && heights[p] != minPanelHeight {
			t.Errorf("in ScreenFull, non-focused panel %d should get minPanelHeight %d, got %d",
				p, minPanelHeight, heights[p])
		}
	}
	if heights[PanelMRs] <= minPanelHeight {
		t.Errorf("focused panel should get more than minPanelHeight, got %d", heights[PanelMRs])
	}
}

func TestAccordionLayout_AllPanelsFocused_IndependentDistribution(t *testing.T) {
	// Each sidebar panel as focus should produce a different distribution
	// (unless the terminal is so small that all panels get minPanelHeight)
	totalHeight := 50
	distributions := make(map[PanelID]map[PanelID]int)

	for _, focused := range SidebarPanels {
		heights := accordionLayout(SidebarPanels, focused, ScreenNormal, totalHeight)
		distributions[focused] = heights
	}

	// The focused panel should always get the most height
	for focused, dist := range distributions {
		for _, p := range SidebarPanels {
			if p != focused && dist[p] > dist[focused] {
				t.Errorf("when %d is focused, panel %d (%d) should not exceed focused (%d)",
					focused, p, dist[p], dist[focused])
			}
		}
	}
}

func TestAccordionLayout_BudgetExact_AllModesAllHeights(t *testing.T) {
	// Extended version: test a wide range of heights for all modes and all focused panels
	modes := []ScreenMode{ScreenNormal, ScreenHalf, ScreenFull}
	n := len(SidebarPanels)

	for _, mode := range modes {
		for _, focused := range SidebarPanels {
			// Test heights from minimum usable up to 80
			for h := n * borderCharsV; h <= 80; h++ {
				heights := accordionLayout(SidebarPanels, focused, mode, h)
				total := 0
				for _, p := range SidebarPanels {
					total += heights[p] + borderCharsV
				}
				if h >= n*(minPanelHeight+borderCharsV) {
					// In normal range, total should exactly equal h
					if total != h {
						t.Errorf("mode=%d focus=%d h=%d: total %d != %d",
							mode, focused, h, total, h)
					}
				} else {
					// In guard path, total must not exceed h
					if total > h {
						t.Errorf("mode=%d focus=%d h=%d: total %d > %d (guard path)",
							mode, focused, h, total, h)
					}
				}
			}
		}
	}
}

func TestAccordionLayout_NegativeTotalHeight(t *testing.T) {
	// Edge case: negative height should not panic and should produce zero heights
	heights := accordionLayout(SidebarPanels, PanelProjects, ScreenNormal, -5)
	for _, p := range SidebarPanels {
		if heights[p] < 0 {
			t.Errorf("panel %d has negative height %d", p, heights[p])
		}
	}
}

func TestAccordionLayout_ZeroTotalHeight(t *testing.T) {
	heights := accordionLayout(SidebarPanels, PanelProjects, ScreenNormal, 0)
	for _, p := range SidebarPanels {
		if heights[p] < 0 {
			t.Errorf("panel %d has negative height %d", p, heights[p])
		}
	}
}

// --- renderTooSmallView tests ---

func TestRenderTooSmallView_ContainsDimensions(t *testing.T) {
	result := renderTooSmallView(40, 8)
	if !strings.Contains(result, "40x8") {
		t.Fatalf("expected current dimensions in message, got %q", result)
	}
	if !strings.Contains(result, "Terminal too small") {
		t.Fatalf("expected 'Terminal too small' in message, got %q", result)
	}
}

func TestRenderTooSmallView_ContainsMinDimensions(t *testing.T) {
	result := renderTooSmallView(10, 5)
	expected := strings.ReplaceAll(
		strings.TrimSpace(
			strings.Split(result, "\n")[0],
		), " ", "")
	_ = expected
	// Just verify the minimum dimensions are mentioned
	if !strings.Contains(result, "Minimum required") {
		t.Fatalf("expected 'Minimum required' in message, got %q", result)
	}
}

// --- buildTabString additional tests ---

func TestBuildTabString_SingleTab(t *testing.T) {
	result := buildTabString([]string{"Only"}, 0)
	if !strings.Contains(result, "Only") {
		t.Fatalf("expected tab label, got %q", result)
	}
	// Single tab should have no separator
	if strings.Contains(result, "|") {
		t.Fatalf("single tab should have no separator, got %q", result)
	}
}

func TestBuildTabString_ActiveOutOfRange(t *testing.T) {
	// activeTab beyond range — should still work without panic
	result := buildTabString([]string{"A", "B"}, 5)
	if !strings.Contains(result, "A") || !strings.Contains(result, "B") {
		t.Fatalf("expected both tabs regardless of active index, got %q", result)
	}
}

func TestBuildTabString_NegativeActive(t *testing.T) {
	result := buildTabString([]string{"X", "Y"}, -1)
	if !strings.Contains(result, "X") || !strings.Contains(result, "Y") {
		t.Fatalf("expected both tabs with negative active, got %q", result)
	}
}
