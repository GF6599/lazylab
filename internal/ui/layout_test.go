package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- computeLayout tests ---

// TestComputeLayout_TerminalSizes: terminal dimensions map to a viable layout or an explicit too-small result.
// Given a terminal size and focus state, when the layout is computed, then undersized terminals report
// OK=false while every viable layout satisfies the shared width, height, and panel invariants plus any
// size-specific expectation.
// Why it matters: a layout that overflows the terminal or starves a pane corrupts every rendered frame.
func TestComputeLayout_TerminalSizes(t *testing.T) {
	// Given: terminal sizes and focus states with their expected outcomes
	tests := []struct {
		name   string
		width  int
		height int
		focus  FocusState
		wantOK bool
		check  func(t *testing.T, l layoutResult)
	}{
		{name: "below min width", width: MinTerminalWidth - 1, height: MinTerminalHeight, focus: FocusState{Active: PanelProjects}},
		{name: "below min height", width: MinTerminalWidth, height: MinTerminalHeight - 1, focus: FocusState{Active: PanelProjects}},
		{name: "below both minimums", width: 10, height: 5, focus: FocusState{Active: PanelProjects}},
		{name: "exactly min dimensions", width: MinTerminalWidth, height: MinTerminalHeight, focus: FocusState{Active: PanelProjects}, wantOK: true},
		{name: "wide split at min dimensions", width: MinTerminalWidth, height: MinTerminalHeight, focus: FocusState{Active: PanelProjects, LayoutMode: LayoutWide}, wantOK: true},
		{name: "small terminal", width: 70, height: 15, focus: FocusState{Active: PanelProjects}, wantOK: true},
		{name: "wide split on narrow terminal", width: 65, height: 20, focus: FocusState{Active: PanelProjects, LayoutMode: LayoutWide}, wantOK: true},
		{name: "default split at 80", width: 80, height: 24, focus: FocusState{Active: PanelProjects}, wantOK: true},
		{name: "wide split at 80", width: 80, height: 24, focus: FocusState{Active: PanelProjects, LayoutMode: LayoutWide}, wantOK: true},
		{
			name: "default split favors detail", width: 120, height: 40,
			focus: FocusState{Active: PanelProjects}, wantOK: true,
			check: func(t *testing.T, l layoutResult) {
				if l.DetailWidth <= l.SidebarWidth {
					t.Errorf("LayoutDefault: detail (%d) should be wider than sidebar (%d)", l.DetailWidth, l.SidebarWidth)
				}
			},
		},
		{
			name: "wide split favors sidebar", width: 120, height: 40,
			focus: FocusState{Active: PanelProjects, LayoutMode: LayoutWide}, wantOK: true,
			check: func(t *testing.T, l layoutResult) {
				if l.SidebarWidth < l.DetailWidth {
					t.Errorf("LayoutWide: sidebar (%d) should be at least detail (%d)", l.SidebarWidth, l.DetailWidth)
				}
			},
		},
		{name: "screen half at 120x40", width: 120, height: 40, focus: FocusState{Active: PanelProjects, ScreenMode: ScreenHalf}, wantOK: true},
		{name: "screen full at 120x40", width: 120, height: 40, focus: FocusState{Active: PanelProjects, ScreenMode: ScreenFull}, wantOK: true},
		{name: "default split at 160", width: 160, height: 40, focus: FocusState{Active: PanelProjects}, wantOK: true},
		{name: "wide split at 160", width: 160, height: 40, focus: FocusState{Active: PanelProjects, LayoutMode: LayoutWide}, wantOK: true},
		{
			name: "default sidebar is percentage based", width: 200, height: 40,
			focus: FocusState{Active: PanelProjects}, wantOK: true,
			check: func(t *testing.T, l layoutResult) {
				if want := 200 * sidebarWidthPct / 100; l.SidebarWidth != want {
					t.Errorf("default sidebar = %d, want %d (%d%% of 200)", l.SidebarWidth, want, sidebarWidthPct)
				}
			},
		},
		{
			name: "wide sidebar takes half uncapped", width: 200, height: 40,
			focus: FocusState{Active: PanelProjects, LayoutMode: LayoutWide}, wantOK: true,
			check: func(t *testing.T, l layoutResult) {
				if want := 200 * 50 / 100; l.SidebarWidth != want {
					t.Errorf("wide sidebar = %d, want %d (50%% of 200)", l.SidebarWidth, want)
				}
			},
		},
		{name: "200x60 terminal", width: 200, height: 60, focus: FocusState{Active: PanelPipelines}, wantOK: true},
		{name: "large terminal", width: 250, height: 80, focus: FocusState{Active: PanelProjects}, wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the layout is computed
			l := computeLayout(tc.width, tc.height, tc.focus)

			// Then: viability matches the terminal size
			if l.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", l.OK, tc.wantOK)
			}
			if !l.OK {
				return
			}

			// And: the invariants shared by every viable layout hold
			assertLayoutInvariants(t, l, tc.width, tc.height)

			// And: any size-specific expectation holds
			if tc.check != nil {
				tc.check(t, l)
			}
		})
	}
}

// assertLayoutInvariants checks the guarantees every viable layout must satisfy:
// panes respect their minimum widths, panes plus gap fit inside the terminal,
// the detail pane spans the full height above the info bar, and sidebar panel
// heights plus borders exactly consume the height above the info bar.
func assertLayoutInvariants(t *testing.T, l layoutResult, width, height int) {
	t.Helper()
	if l.SidebarWidth < minSidebarWidth {
		t.Errorf("sidebar width %d below minSidebarWidth %d", l.SidebarWidth, minSidebarWidth)
	}
	if l.DetailWidth < detailMinWidth {
		t.Errorf("detail width %d below detailMinWidth %d", l.DetailWidth, detailMinWidth)
	}
	totalWidth := (l.SidebarWidth + borderCharsH) + paneGap + (l.DetailWidth + borderCharsH)
	if totalWidth > width {
		t.Errorf("total width %d exceeds terminal width %d", totalWidth, width)
	}
	if want := height - infoBarHeight - borderCharsV; l.DetailHeight != want {
		t.Errorf("DetailHeight = %d, want %d", l.DetailHeight, want)
	}
	if l.InfoBarWidth != width || l.TotalHeight != height {
		t.Errorf("InfoBarWidth/TotalHeight = %d/%d, want %d/%d", l.InfoBarWidth, l.TotalHeight, width, height)
	}
	totalPanelHeight := 0
	for _, p := range SidebarPanels {
		h, ok := l.PanelHeights[p]
		if !ok || h < 0 {
			t.Errorf("panel %d height missing or negative: %d (present=%v)", p, h, ok)
		}
		totalPanelHeight += h + borderCharsV
	}
	if want := height - infoBarHeight; totalPanelHeight != want {
		t.Errorf("panel heights plus borders sum to %d, want %d", totalPanelHeight, want)
	}
}

// TestComputeLayout_DetailFocusUsesPrevActive: focusing the detail pane keeps the sidebar accordion unchanged.
// Given a layout focused on a sidebar panel, when focus moves to the detail pane with that panel as
// PrevActive, then every sidebar panel keeps its height and the previous panel stays expanded, in every
// screen mode.
// Why it matters: if the sidebar collapsed on detail focus, entering the detail pane would reshuffle the
// whole sidebar and lose the user's place.
func TestComputeLayout_DetailFocusUsesPrevActive(t *testing.T) {
	// Given: each screen mode with PanelStages focused in the sidebar
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
			sidebar := computeLayout(120, 40, FocusState{Active: PanelStages, PrevActive: PanelProjects, ScreenMode: tc.mode})
			if !sidebar.OK {
				t.Fatal("expected layout OK for sidebar focus")
			}

			// When: focus moves to the detail pane with PanelStages as PrevActive
			detail := computeLayout(120, 40, FocusState{Active: PanelDetail, PrevActive: PanelStages, ScreenMode: tc.mode})
			if !detail.OK {
				t.Fatal("expected layout OK for detail focus")
			}

			// Then: every sidebar panel keeps its height
			for _, p := range SidebarPanels {
				if sidebar.PanelHeights[p] != detail.PanelHeights[p] {
					t.Errorf("panel %d height changed on detail focus: %d -> %d",
						p, sidebar.PanelHeights[p], detail.PanelHeights[p])
				}
			}

			// And: the previously focused panel stays the expanded one
			for _, p := range SidebarPanels {
				if p != PanelStages && detail.PanelHeights[p] > detail.PanelHeights[PanelStages] {
					t.Errorf("panel %d (%d) taller than prev-active PanelStages (%d)",
						p, detail.PanelHeights[p], detail.PanelHeights[PanelStages])
				}
			}
			if detail.PanelHeights[PanelStages] <= minPanelHeight {
				t.Errorf("prev-active panel should stay expanded above minPanelHeight %d, got %d",
					minPanelHeight, detail.PanelHeights[PanelStages])
			}
		})
	}
}

// --- accordionLayout tests ---

// TestAccordionLayout_Distribution: specific panel sets and heights produce the promised distributions.
// Given a panel set, focused panel, screen mode, and height, when the accordion distributes space, then
// single panels get everything, degenerate inputs stay safe, and each screen mode shapes unfocused
// panels as promised.
// Why it matters: a wrong distribution collapses panels the user is reading or overflows the terminal.
func TestAccordionLayout_Distribution(t *testing.T) {
	// Given: panel and height scenarios with their distribution expectations
	tests := []struct {
		name    string
		panels  []PanelID
		focused PanelID
		mode    ScreenMode
		height  int
		check   func(t *testing.T, heights map[PanelID]int)
	}{
		{
			name: "single panel gets all content space", panels: []PanelID{PanelProjects},
			focused: PanelProjects, mode: ScreenNormal, height: 30,
			check: func(t *testing.T, heights map[PanelID]int) {
				if want := 30 - borderCharsV; heights[PanelProjects] != want {
					t.Errorf("single panel = %d, want %d", heights[PanelProjects], want)
				}
			},
		},
		{
			name: "nil panels yield empty map", panels: nil,
			focused: PanelProjects, mode: ScreenNormal, height: 50,
			check: func(t *testing.T, heights map[PanelID]int) {
				if len(heights) != 0 {
					t.Errorf("expected empty map, got %v", heights)
				}
			},
		},
		{
			name: "empty panels yield empty map", panels: []PanelID{},
			focused: PanelProjects, mode: ScreenNormal, height: 50,
			check: func(t *testing.T, heights map[PanelID]int) {
				if len(heights) != 0 {
					t.Errorf("expected empty map, got %v", heights)
				}
			},
		},
		{
			// 10 rows leave a 2-row budget after 4 panels' borders; focus takes it all.
			name: "very tiny terminal renders only the focused panel", panels: SidebarPanels,
			focused: PanelStages, mode: ScreenNormal, height: 10,
			check: func(t *testing.T, heights map[PanelID]int) {
				if heights[PanelStages] != 2 {
					t.Errorf("focused panel = %d, want 2", heights[PanelStages])
				}
				for _, p := range SidebarPanels {
					if p != PanelStages && heights[p] != 0 {
						t.Errorf("non-focused panel %d = %d, want 0", p, heights[p])
					}
				}
			},
		},
		{
			name: "ScreenFull compresses others below their ScreenNormal size", panels: SidebarPanels,
			focused: PanelMRs, mode: ScreenFull, height: 50,
			check: func(t *testing.T, heights map[PanelID]int) {
				normal := accordionLayout(SidebarPanels, PanelMRs, ScreenNormal, 50)
				for _, p := range SidebarPanels {
					if p == PanelMRs {
						continue
					}
					if heights[p] != minPanelHeight {
						t.Errorf("panel %d = %d, want minPanelHeight %d", p, heights[p], minPanelHeight)
					}
					if normal[p] <= heights[p] {
						t.Errorf("ScreenNormal panel %d (%d) should exceed ScreenFull (%d)", p, normal[p], heights[p])
					}
				}
				if heights[PanelMRs] <= minPanelHeight {
					t.Errorf("focused panel = %d, should exceed minPanelHeight", heights[PanelMRs])
				}
			},
		},
		{
			name: "ScreenNormal keeps unfocused panels usable", panels: SidebarPanels,
			focused: PanelProjects, mode: ScreenNormal, height: 50,
			check: func(t *testing.T, heights map[PanelID]int) {
				for _, p := range SidebarPanels {
					if p != PanelProjects && heights[p] <= minPanelHeight {
						t.Errorf("panel %d = %d, should exceed minPanelHeight %d when space allows",
							p, heights[p], minPanelHeight)
					}
				}
			},
		},
		{
			name: "negative height yields no negative panels", panels: SidebarPanels,
			focused: PanelProjects, mode: ScreenNormal, height: -5,
			check: assertNoNegativeHeights,
		},
		{
			name: "zero height yields no negative panels", panels: SidebarPanels,
			focused: PanelProjects, mode: ScreenNormal, height: 0,
			check: assertNoNegativeHeights,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the accordion distributes the available height
			heights := accordionLayout(tc.panels, tc.focused, tc.mode, tc.height)

			// Then: the distribution matches the scenario's expectation
			tc.check(t, heights)
		})
	}
}

func assertNoNegativeHeights(t *testing.T, heights map[PanelID]int) {
	t.Helper()
	for _, p := range SidebarPanels {
		if heights[p] < 0 {
			t.Errorf("panel %d has negative height %d", p, heights[p])
		}
	}
}

// TestAccordionLayout_BudgetAndFocusInvariants: every mode, focus, and height keeps the accordion in budget.
// Given all screen modes, focused panels, and heights from the border floor up to 80 rows, when the
// accordion runs, then rendered heights (content plus borders) exactly consume the available space in the
// normal range, never overflow in the degenerate range, are never negative, and the focused panel is never
// smaller than an unfocused one.
// Why it matters: a one-row budget error tears the whole multi-panel frame, and a shrunken focused panel
// hides exactly what the user is working in.
func TestAccordionLayout_BudgetAndFocusInvariants(t *testing.T) {
	// Given: the full grid of screen modes, focused panels, and heights
	modes := []ScreenMode{ScreenNormal, ScreenHalf, ScreenFull}
	n := len(SidebarPanels)
	for _, mode := range modes {
		for _, focused := range SidebarPanels {
			for h := n * borderCharsV; h <= 80; h++ {
				// When: the accordion distributes h rows
				heights := accordionLayout(SidebarPanels, focused, mode, h)

				// Then: heights are non-negative and the focused panel dominates
				total := 0
				for _, p := range SidebarPanels {
					if heights[p] < 0 {
						t.Fatalf("mode=%d focus=%d h=%d: panel %d negative height %d", mode, focused, h, p, heights[p])
					}
					if heights[p] > heights[focused] {
						t.Fatalf("mode=%d focus=%d h=%d: panel %d (%d) taller than focused (%d)",
							mode, focused, h, p, heights[p], heights[focused])
					}
					total += heights[p] + borderCharsV
				}

				// And: the rendered total matches the budget exactly, or fits within it below the minimum range
				if h >= n*(minPanelHeight+borderCharsV) {
					if total != h {
						t.Fatalf("mode=%d focus=%d h=%d: rendered total %d != available %d", mode, focused, h, total, h)
					}
				} else if total > h {
					t.Fatalf("mode=%d focus=%d h=%d: rendered total %d exceeds available %d", mode, focused, h, total, h)
				}
			}
		}
	}
}

// --- border builder tests ---

// TestBuildTopBorder_EmbedsContentWithinWidth: the top border spans its width and drops what cannot fit.
// Given a width, title, and tabs, when the top border is built, then the result spans exactly the requested
// width, embeds the title and tabs when there is room, and falls back to bare lines or corners when not.
// Why it matters: a border that miscounts width or keeps oversized content misaligns every pane on screen.
func TestBuildTopBorder_EmbedsContentWithinWidth(t *testing.T) {
	// Given: plain styles and width/title/tab combinations
	plain := lipgloss.NewStyle()
	tests := []struct {
		name            string
		width           int
		title           string
		tabs            []string
		active          int
		wantContains    []string
		wantNotContains []string
	}{
		{name: "width 0 renders bare line", width: 0, title: "Title", wantNotContains: []string{frameBorder.TopLeft, "Title"}},
		{name: "width 3 renders bare line", width: 3, title: "X", wantContains: []string{frameBorder.Top}, wantNotContains: []string{frameBorder.TopLeft}},
		{name: "width 4 renders corners only", width: 4, wantContains: []string{frameBorder.TopLeft, frameBorder.TopRight}},
		{name: "width 5 falls back to corners", width: 5, title: "X", wantNotContains: []string{"X"}},
		{name: "title embedded", width: 40, title: "Projects", wantContains: []string{"Projects", frameBorder.TopLeft, frameBorder.TopRight}},
		{name: "empty title renders corners", width: 30, wantContains: []string{frameBorder.TopLeft, frameBorder.TopRight}},
		{name: "long title truncated to width", width: 12, title: "VeryLongTitleHere", wantNotContains: []string{"VeryLongTitleHere"}},
		{name: "title and tabs embedded", width: 60, title: "Detail", tabs: []string{"Log", "Tests", "Artifacts"}, wantContains: []string{"Detail", "Log"}},
		{name: "tabs render when room allows", width: 60, title: "X", tabs: []string{"A", "B"}, active: 1, wantContains: []string{"A", "B"}},
		{
			name: "tabs omitted when they overflow", width: 20, title: "Title",
			tabs:            []string{"VeryLongTabName1", "VeryLongTabName2", "VeryLongTabName3"},
			wantNotContains: []string{"VeryLongTabName1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the top border is built
			got := buildTopBorder(tc.width, tc.title, tc.tabs, tc.active, plain, plain)

			// Then: the border spans exactly the requested width
			if w := ansi.StringWidth(got); w != tc.width {
				t.Errorf("width = %d, want %d", w, tc.width)
			}

			// And: expected fragments appear and oversized ones are dropped
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			for _, notWant := range tc.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("unexpected %q in %q", notWant, got)
				}
			}
		})
	}
}

// TestBuildBottomBorder_EmbedsFooterWithinWidth: the bottom border spans its width and drops oversized footers.
// Given a width and footer, when the bottom border is built, then the result spans exactly the requested
// width, embeds the footer when it fits, and renders a plain or bare border when it does not.
// Why it matters: an overflowing footer pushes the border past the pane edge and breaks the frame.
func TestBuildBottomBorder_EmbedsFooterWithinWidth(t *testing.T) {
	// Given: plain styles and width/footer combinations
	plain := lipgloss.NewStyle()
	tests := []struct {
		name            string
		width           int
		footer          string
		wantContains    []string
		wantNotContains []string
		wantPlainDashes bool
	}{
		{name: "width 0 renders empty line", width: 0, footer: "footer", wantNotContains: []string{"footer", frameBorder.BottomLeft}},
		{name: "width 3 renders bare line", width: 3, wantNotContains: []string{frameBorder.BottomLeft}},
		{name: "width 4 renders corners", width: 4, wantContains: []string{frameBorder.BottomLeft, frameBorder.BottomRight}},
		{name: "footer embedded", width: 40, footer: "3 of 47", wantContains: []string{"3 of 47", frameBorder.BottomLeft, frameBorder.BottomRight}},
		{name: "footer exactly fits", width: 12, footer: "1/2", wantContains: []string{"1/2"}},
		{name: "footer omitted when too wide", width: 8, footer: "very long footer text", wantNotContains: []string{"very long footer text"}},
		{name: "no footer renders plain border", width: 30, wantContains: []string{frameBorder.BottomLeft, frameBorder.BottomRight}, wantPlainDashes: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the bottom border is built
			got := buildBottomBorder(tc.width, tc.footer, plain)

			// Then: the border spans exactly the requested width
			if w := ansi.StringWidth(got); w != tc.width {
				t.Errorf("width = %d, want %d", w, tc.width)
			}

			// And: the footer appears only when it fits
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			for _, notWant := range tc.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("unexpected %q in %q", notWant, got)
				}
			}

			// And: a footer-less border is a solid line between corners
			if tc.wantPlainDashes {
				inner := strings.TrimSuffix(strings.TrimPrefix(got, frameBorder.BottomLeft), frameBorder.BottomRight)
				for _, r := range inner {
					if string(r) != frameBorder.Bottom {
						t.Errorf("expected only %q between corners, found %q in %q", frameBorder.Bottom, string(r), got)
						break
					}
				}
			}
		})
	}
}

// TestBuildTabString_RendersLabels: every tab label renders and bad active indexes stay safe.
// Given a tab list and an active index, when the tab string is built, then all labels appear, a single
// tab has no separator, empty input yields an empty string, and out-of-range active indexes do not panic.
// Why it matters: a dropped label or an index panic would hide or crash the detail pane's tab bar.
func TestBuildTabString_RendersLabels(t *testing.T) {
	// Given: tab lists with in-range, out-of-range, and missing active indexes
	tests := []struct {
		name            string
		tabs            []string
		active          int
		wantEmpty       bool
		wantContains    []string
		wantNotContains []string
	}{
		{name: "nil tabs render nothing", tabs: nil, wantEmpty: true},
		{name: "empty tabs render nothing", tabs: []string{}, wantEmpty: true},
		{name: "single tab has no separator", tabs: []string{"Only"}, wantContains: []string{"Only"}, wantNotContains: []string{"|"}},
		{name: "all labels rendered", tabs: []string{"Log", "Tests", "Artifacts"}, active: 1, wantContains: []string{"Log", "Tests", "Artifacts"}},
		{name: "active index beyond range", tabs: []string{"A", "B"}, active: 5, wantContains: []string{"A", "B"}},
		{name: "negative active index", tabs: []string{"X", "Y"}, active: -1, wantContains: []string{"X", "Y"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the tab string is built
			got := buildTabString(tc.tabs, tc.active)

			// Then: emptiness and label presence match the input
			if tc.wantEmpty && got != "" {
				t.Fatalf("expected empty string, got %q", got)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			for _, notWant := range tc.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("unexpected %q in %q", notWant, got)
				}
			}
		})
	}
}

// --- renderBorderedPane tests ---

// TestRenderBorderedPane_ZeroWidth: a zero-width pane renders as nothing.
// Given width 0, when the pane renders, then the result is the empty string.
// Why it matters: emitting border glyphs for a pane that was allocated no space would inject stray
// characters into the joined layout.
func TestRenderBorderedPane_ZeroWidth(t *testing.T) {
	// When/Then: rendering at width 0 produces no output
	result := renderBorderedPane("hello", 0, 5, false, "Title", nil, 0, "")
	if result != "" {
		t.Fatalf("expected empty string for width=0, got %q", result)
	}
}

// TestRenderBorderedPane_ZeroHeight: a zero-height pane renders as nothing.
// Given height 0, when the pane renders, then the result is the empty string.
// Why it matters: a pane squeezed to zero rows by the accordion must vanish cleanly instead of leaving a
// stray border line.
func TestRenderBorderedPane_ZeroHeight(t *testing.T) {
	// When/Then: rendering at height 0 produces no output
	result := renderBorderedPane("hello", 40, 0, false, "Title", nil, 0, "")
	if result != "" {
		t.Fatalf("expected empty string for height=0, got %q", result)
	}
}

// TestRenderBorderedPane_Width3: widths below the 4-cell minimum render nothing.
// Given width 3, when the pane renders, then the result is empty.
// Why it matters: a border needs two corners plus interior, and forcing one into 3 cells would emit a
// malformed frame fragment.
func TestRenderBorderedPane_Width3(t *testing.T) {
	// When/Then: rendering below the minimum width produces no output
	result := renderBorderedPane("x", 3, 3, false, "T", nil, 0, "")
	if result != "" {
		t.Fatalf("expected empty for width=3 (< 4), got %q", result)
	}
}

// TestRenderBorderedPane_MinimalValid: the smallest legal pane still renders top, content, and bottom rows.
// Given width 4 and height 1, when the pane renders, then the output is non-empty with at least three lines.
// Why it matters: the accordion squeezes unfocused panels down to tiny sizes, and the minimum pane
// collapsing to nothing would blank them entirely.
func TestRenderBorderedPane_MinimalValid(t *testing.T) {
	// When: the smallest legal pane renders
	result := renderBorderedPane("x", 4, 1, false, "", nil, 0, "")

	// Then: it produces a top border, a content line, and a bottom border
	if result == "" {
		t.Fatal("expected non-empty result for width=4, height=1")
	}
	lines := strings.Split(result, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (top+content+bottom), got %d: %q", len(lines), result)
	}
}

// TestRenderBorderedPane_FocusedAndUnfocused_BothRender: focus styling never drops the pane or its content.
// Given the same pane rendered unfocused and focused, when both render, then each is non-empty and
// contains the content text.
// Why it matters: focus only restyles the border, and losing content on a focus change would make panels
// flicker blank as the user tabs around.
func TestRenderBorderedPane_FocusedAndUnfocused_BothRender(t *testing.T) {
	// When: the same pane renders unfocused and focused
	unfocused := renderBorderedPane("content", 40, 5, false, "Panel", nil, 0, "1 of 5")
	focused := renderBorderedPane("content", 40, 5, true, "Panel", nil, 0, "1 of 5")

	// Then: both produce output
	if unfocused == "" {
		t.Fatal("unfocused pane should produce non-empty output")
	}
	if focused == "" {
		t.Fatal("focused pane should produce non-empty output")
	}

	// And: both keep the content text
	if !strings.Contains(unfocused, "content") {
		t.Fatal("unfocused pane should contain content text")
	}
	if !strings.Contains(focused, "content") {
		t.Fatal("focused pane should contain content text")
	}
}

// TestRenderBorderedPane_ContainsContent: pane output includes the content passed in.
// Given a pane with "Hello World", when it renders, then the text appears in the output.
// Why it matters: a pane that frames but drops its body renders as an empty box.
func TestRenderBorderedPane_ContainsContent(t *testing.T) {
	// When/Then: the rendered pane carries its content text
	result := renderBorderedPane("Hello World", 40, 5, false, "Title", nil, 0, "")
	if !strings.Contains(result, "Hello World") {
		t.Fatalf("pane should contain content text, got %q", result)
	}
}

// TestRenderBorderedPane_WithScrollbar: overflowing content renders a scrollbar thumb.
// Given scroll info reporting 100 total lines with 5 visible, when the pane renders, then the thumb glyph
// appears in the output.
// Why it matters: the thumb is the only cue that a panel hides more content below the fold.
func TestRenderBorderedPane_WithScrollbar(t *testing.T) {
	// When: overflowing content renders, 100 total lines showing 5 at offset 50
	result := renderBorderedPane("line", 40, 5, false, "T", nil, 0, "", scrollInfo{offset: 50, total: 100})

	// Then: the scrollbar thumb glyph appears
	if result == "" {
		t.Fatal("expected non-empty result with scrollbar")
	}
	if !strings.Contains(result, "▐") {
		t.Fatal("expected scrollbar thumb character in output")
	}
}

// TestRenderBorderedPane_NoScrollbarWhenFits: content that fits renders without a scrollbar.
// Given scroll info where the total equals the visible height, when the pane renders, then no thumb glyph
// appears.
// Why it matters: a phantom scrollbar invites scrolling that does nothing and eats a content column.
func TestRenderBorderedPane_NoScrollbarWhenFits(t *testing.T) {
	// When/Then: content that fits exactly (5 lines in 5 rows) renders no thumb
	result := renderBorderedPane("a\nb\nc\nd\ne", 40, 5, false, "T", nil, 0, "", scrollInfo{offset: 0, total: 5})
	if strings.Contains(result, "▐") {
		t.Fatal("should not show scrollbar when content fits")
	}
}

// TestRenderBorderedPane_VeryNarrow: a width-5 pane, just above the minimum, still renders.
// Given width 5, when the pane renders, then the output is non-empty.
// Why it matters: the narrowest sidebar slice must render a frame rather than vanish.
func TestRenderBorderedPane_VeryNarrow(t *testing.T) {
	// When/Then: width 5 (minimum is 4) still produces a frame
	result := renderBorderedPane("a", 5, 3, false, "", nil, 0, "")
	if result == "" {
		t.Fatal("expected non-empty result for width=5")
	}
}

// --- renderTooSmallView tests ---

// TestRenderTooSmallView_ContainsDimensions: the too-small screen names the current terminal size.
// Given a 40x8 terminal, when the too-small view renders, then it shows "40x8" and the "Terminal too
// small" headline.
// Why it matters: without the actual numbers users cannot tell how far off their window is from usable.
func TestRenderTooSmallView_ContainsDimensions(t *testing.T) {
	// When: the too-small view renders for a 40x8 terminal
	result := renderTooSmallView(40, 8)

	// Then: the message names the headline and the current size
	if !strings.Contains(result, "40x8") {
		t.Fatalf("expected current dimensions in message, got %q", result)
	}
	if !strings.Contains(result, "Terminal too small") {
		t.Fatalf("expected 'Terminal too small' in message, got %q", result)
	}
}

// TestRenderTooSmallView_ContainsMinDimensions: the too-small screen states the minimum required size.
// Given a 10x5 terminal, when the too-small view renders, then it shows the "Minimum required" line.
// Why it matters: the message must tell users what size to resize to, not just that the current one failed.
func TestRenderTooSmallView_ContainsMinDimensions(t *testing.T) {
	// When/Then: the rendered message includes the minimum-required line
	result := renderTooSmallView(10, 5)
	if !strings.Contains(result, "Minimum required") {
		t.Fatalf("expected 'Minimum required' in message, got %q", result)
	}
}

// --- chrome shape tests ---

// TestFrames_DrawSquareCorners: every frame in the multi-panel view draws square corners.
// Given the stub snapshot model at 120x40, when the full multi-panel view renders, then no rounded
// corner appears anywhere in the frame.
// Why it matters: the corner shape is one lever for the whole surface, so a rounded corner that
// survives anywhere means a call site escaped that lever.
func TestFrames_DrawSquareCorners(t *testing.T) {
	// Given: the stub model at 120x40 with the projects panel focused
	m := newSnapshotModel(PanelProjects, 120, 40)

	// When: the full multi-panel view renders
	output := renderMultiPanelView(m, m.width, m.height)

	// Then: no rounded corner survives anywhere in the frame
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(output, corner) {
			t.Errorf("found rounded corner %q in the rendered frame, corners must be square", corner)
		}
	}

	// And: square corners are actually drawn, so a border that draws none cannot pass
	for _, corner := range []string{"┌", "┐", "└", "┘"} {
		if !strings.Contains(output, corner) {
			t.Errorf("missing square corner %q in the rendered frame", corner)
		}
	}
}

// TestPaneTitle_KeepsASpaceBeforeTheFillRule: a pane title never touches the rule around it.
// Given a pane with a title alone and a pane with a title and tabs, when the top border is built at
// every width from 20 to 80, then a space separates the label from the rule, the rule itself carries
// no gap, and the border spans exactly the width asked for.
// Why it matters: the rule and the label are drawn cell by cell, and a fit check that misjudges either
// by one renders the border wider than its pane, which tears every frame on screen.
func TestPaneTitle_KeepsASpaceBeforeTheFillRule(t *testing.T) {
	// Given: plain styles and two panes, one with tabs and one without
	plain := lipgloss.NewStyle()
	cases := []struct {
		name   string
		title  string
		tabs   []string
		joined string
	}{
		{name: "title alone", title: "pipelines", tabs: nil, joined: "pipelines─"},
		{name: "title and tabs", title: "projects", tabs: []string{"favorites", "all"}, joined: "all─"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the top border is built at every width the label still fits in
			for width := 20; width <= 80; width++ {
				got := buildTopBorder(width, tc.title, tc.tabs, 0, plain, plain)
				if !strings.Contains(got, tc.title) {
					continue
				}

				// Then: the rule does not run straight into the label
				if strings.Contains(got, tc.joined) {
					t.Errorf("width %d: rule touches the label: found %q in %q", width, tc.joined, got)
				}

				// And: the rule itself is never broken by a gap
				if gap := frameBorder.Top + " " + frameBorder.Top; strings.Contains(got, gap) {
					t.Errorf("width %d: gap inside the rule: %q", width, got)
				}

				// And: spending a cell on the gap never changes the border width
				if w := ansi.StringWidth(got); w != width {
					t.Errorf("width = %d, want %d, in %q", w, width, got)
				}
			}
		})
	}
}
