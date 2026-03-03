package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestAccordionLayout_DetailFocusPreservesSidebar(t *testing.T) {
	// When the detail pane is focused, the sidebar accordion should use
	// PrevActive so that the previously-focused sidebar panel stays expanded.
	width, height := 120, 40

	// Focus on PanelPipelines — this is the "before" state.
	sidebarFocus := FocusState{
		Active:     PanelPipelines,
		PrevActive: PanelProjects,
		ScreenMode: ScreenNormal,
	}
	sidebarLayout := computeLayout(width, height, sidebarFocus)
	if !sidebarLayout.OK {
		t.Fatal("expected layout OK for sidebar focus")
	}

	// Now move focus to PanelDetail, with PrevActive = PanelPipelines.
	detailFocus := FocusState{
		Active:     PanelDetail,
		PrevActive: PanelPipelines,
		ScreenMode: ScreenNormal,
	}
	detailLayout := computeLayout(width, height, detailFocus)
	if !detailLayout.OK {
		t.Fatal("expected layout OK for detail focus")
	}

	// The sidebar panel heights should be identical: moving to the detail
	// pane must not collapse the previously-focused sidebar panel.
	for _, p := range SidebarPanels {
		if sidebarLayout.PanelHeights[p] != detailLayout.PanelHeights[p] {
			t.Errorf("panel %d height changed when moving to detail: sidebar=%d detail=%d",
				p, sidebarLayout.PanelHeights[p], detailLayout.PanelHeights[p])
		}
	}

	// The expanded panel must be larger than the minimum.
	if detailLayout.PanelHeights[PanelPipelines] <= minPanelHeight {
		t.Errorf("PanelPipelines should be expanded (got %d, min %d)",
			detailLayout.PanelHeights[PanelPipelines], minPanelHeight)
	}
}

func TestAccordionLayout_DetailFocusPreservesSidebar_AllModes(t *testing.T) {
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
			width, height := 120, 40

			sidebarFocus := FocusState{
				Active:     PanelStages,
				PrevActive: PanelProjects,
				ScreenMode: tc.mode,
			}
			sidebarLayout := computeLayout(width, height, sidebarFocus)
			if !sidebarLayout.OK {
				t.Fatal("expected layout OK for sidebar focus")
			}

			detailFocus := FocusState{
				Active:     PanelDetail,
				PrevActive: PanelStages,
				ScreenMode: tc.mode,
			}
			detailLayout := computeLayout(width, height, detailFocus)
			if !detailLayout.OK {
				t.Fatal("expected layout OK for detail focus")
			}

			for _, p := range SidebarPanels {
				if sidebarLayout.PanelHeights[p] != detailLayout.PanelHeights[p] {
					t.Errorf("panel %d height changed: sidebar=%d detail=%d",
						p, sidebarLayout.PanelHeights[p], detailLayout.PanelHeights[p])
				}
			}
		})
	}
}

// --- Border rendering tests ---

func TestBuildTopBorder_TitleOnly(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle().Bold(true)
	result := buildTopBorder(40, "Projects", nil, 0, borderStyle, titleStyle, false)
	if !strings.Contains(result, "Projects") {
		t.Fatalf("expected title in border, got %q", result)
	}
	if ansi.StringWidth(result) != 40 {
		t.Fatalf("expected width 40, got %d", ansi.StringWidth(result))
	}
}

func TestBuildTopBorder_TitleAndTabs(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle().Bold(true)
	tabs := []string{"Log", "Tests", "Artifacts"}
	result := buildTopBorder(60, "Detail", tabs, 0, borderStyle, titleStyle, false)
	if !strings.Contains(result, "Detail") {
		t.Fatalf("expected title in border, got %q", result)
	}
	// At least one tab label should appear if there's room
	if !strings.Contains(result, "Log") {
		t.Fatalf("expected tab in border, got %q", result)
	}
}

func TestBuildTopBorder_NarrowWidth(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	titleStyle := lipgloss.NewStyle()
	result := buildTopBorder(3, "X", nil, 0, borderStyle, titleStyle, false)
	if result == "" {
		t.Fatal("expected non-empty result for width=3")
	}
	// Width < 4 should produce a simple line
	if !strings.Contains(result, "─") {
		t.Fatalf("expected horizontal line chars, got %q", result)
	}
}

func TestBuildBottomBorder_WithFooter(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(40, "3 of 47", borderStyle)
	if !strings.Contains(result, "3 of 47") {
		t.Fatalf("expected footer in border, got %q", result)
	}
	if !strings.HasPrefix(result, "╰") {
		t.Fatalf("expected ╰ prefix, got %q", result)
	}
	if !strings.HasSuffix(result, "╯") {
		t.Fatalf("expected ╯ suffix, got %q", result)
	}
}

func TestBuildBottomBorder_NoFooter(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(30, "", borderStyle)
	if !strings.HasPrefix(result, "╰") || !strings.HasSuffix(result, "╯") {
		t.Fatalf("expected plain border ╰───╯, got %q", result)
	}
	// Should be all horizontal lines between corners
	inner := result[len("╰") : len(result)-len("╯")]
	for _, r := range inner {
		if r != '─' {
			t.Fatalf("expected only ─ in inner border, got %q", string(r))
		}
	}
}

func TestBuildBottomBorder_FooterTooWide(t *testing.T) {
	borderStyle := lipgloss.NewStyle()
	result := buildBottomBorder(10, "this is a very long footer", borderStyle)
	// Footer should be omitted when there's not enough space
	if strings.Contains(result, "this is a very long footer") {
		t.Fatalf("footer should be omitted when too wide, got %q", result)
	}
}

func TestBuildTabString_Empty(t *testing.T) {
	result := buildTabString(nil, 0, false)
	if result != "" {
		t.Fatalf("expected empty string for nil tabs, got %q", result)
	}
	result = buildTabString([]string{}, 0, false)
	if result != "" {
		t.Fatalf("expected empty string for empty tabs, got %q", result)
	}
}

func TestBuildTabString_ActiveHighlighted(t *testing.T) {
	tabs := []string{"Log", "Tests", "Artifacts"}
	result := buildTabString(tabs, 1, true)
	// The active tab (Tests) should appear in the output
	if !strings.Contains(result, "Tests") {
		t.Fatalf("expected active tab 'Tests' in output, got %q", result)
	}
	// All tab labels should be present
	for _, tab := range tabs {
		if !strings.Contains(result, tab) {
			t.Fatalf("expected tab %q in output, got %q", tab, result)
		}
	}
}

// --- Accordion edge case tests ---

func TestAccordionLayout_EmptyPanels(t *testing.T) {
	heights := accordionLayout(nil, PanelProjects, ScreenNormal, 50)
	if len(heights) != 0 {
		t.Fatalf("expected empty map for nil panels, got %v", heights)
	}
	heights = accordionLayout([]PanelID{}, PanelProjects, ScreenNormal, 50)
	if len(heights) != 0 {
		t.Fatalf("expected empty map for empty panels, got %v", heights)
	}
}

func TestAccordionLayout_TinyTerminal(t *testing.T) {
	// 4 panels * (minPanelHeight + borderCharsV) = 4*(4+2) = 24
	// Give less than that
	heights := accordionLayout(SidebarPanels, PanelProjects, ScreenNormal, 20)
	for _, p := range SidebarPanels {
		if heights[p] != minPanelHeight {
			t.Errorf("panel %d: expected minPanelHeight %d, got %d", p, minPanelHeight, heights[p])
		}
	}
}
