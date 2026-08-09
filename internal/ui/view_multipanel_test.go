package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// sidebarResizeModel holds more runs than any panel height can hold, and reaches its resting size
// the way a launch does, by being told the terminal size rather than by being set up at it.
func sidebarResizeModel() Model {
	m := newMultiPanelModel(PanelProjects)
	m.ctx = context.Background()
	m.client = &mockService{}
	pipelines := make([]gitlab.PipelineSummary, 60)
	items := make([]list.Item, 60)
	for i := range pipelines {
		pipelines[i] = gitlab.PipelineSummary{ID: 100 + i, Ref: "main", Status: "success"}
		items[i] = pipelineItem{summary: pipelines[i]}
	}
	m.pipelineView.pipelines = pipelines
	m.pipelineView.pipelineList = newBareList(items, pipelineDelegate{}, 40, 20)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	return sized.(Model)
}

func pipelinePanelRows(t *testing.T, m Model) (frame, drawn int) {
	t.Helper()
	layout := computeLayout(m.width, m.height, m.focus)
	if !layout.OK {
		t.Fatal("the terminal is too small for the layout, so the test proves nothing")
	}
	frame = layout.PanelHeights[PanelPipelines]
	for _, line := range strings.Split(renderPipelinesPanelContent(m, layout.SidebarWidth, frame), "\n") {
		if strings.TrimSpace(line) != "" {
			drawn++
		}
	}
	return frame, drawn
}

// TestSidebar_FillsAPanelThatGrowsWhenItTakesFocus: a pane that grows fills with rows.
// Given a sidebar drawn at the right size with the pipelines panel unfocused, when the user jumps
// the focus onto it, then the list draws as many rows as the enlarged pane holds.
// Why it matters: the focused pane takes most of the sidebar height, so a list that keeps its old
// height leaves the bottom of the pane blank and hides runs there is now room to show.
func TestSidebar_FillsAPanelThatGrowsWhenItTakesFocus(t *testing.T) {
	// Given: a sidebar drawn at the right size, focused on another panel
	m := sidebarResizeModel()

	// When: the user jumps the focus onto the pipelines panel
	updated, _ := m.Update(keyMsgFor("2"))
	after := updated.(Model)
	if after.focus.Active != PanelPipelines {
		t.Fatalf("the jump key left the focus on %v", after.focus.Active)
	}

	// Then: the list fills the pane it was given
	frame, drawn := pipelinePanelRows(t, after)
	if drawn != frame {
		t.Errorf("the focused pane holds %d rows and the list drew %d", frame, drawn)
	}
}

// TestSidebar_FillsAPanelThatGrowsWhenTheTerminalGrows: a taller terminal fills with more rows.
// Given a sidebar drawn at its resting size, when the terminal reports a taller window, then the
// list draws as many rows as the taller pane holds.
// Why it matters: this is the one path that already worked, so nothing else would report it if a
// change to how the panels learn their size dropped it.
func TestSidebar_FillsAPanelThatGrowsWhenTheTerminalGrows(t *testing.T) {
	// Given: a sidebar drawn at its resting size
	m := sidebarResizeModel()

	// When: the terminal reports a taller window
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 70})
	after := updated.(Model)

	// Then: the list fills the taller pane
	frame, drawn := pipelinePanelRows(t, after)
	if drawn != frame {
		t.Errorf("the taller pane holds %d rows and the list drew %d", frame, drawn)
	}
}

// TestSidebar_FillsAPanelThatGrowsWhenTheScreenModeChanges: the key that resizes a pane fills it.
// Given a sidebar drawn at the right size with the pipelines panel focused, when the user changes
// the screen mode, then the list draws as many rows as the enlarged pane holds.
// Why it matters: this key exists only to give the focused pane more room, so a pane that grows
// without its contents growing makes the key look broken.
func TestSidebar_FillsAPanelThatGrowsWhenTheScreenModeChanges(t *testing.T) {
	// Given: a sidebar drawn at the right size with the pipelines panel focused
	m := sidebarResizeModel()
	focused, _ := m.Update(keyMsgFor("2"))
	m = focused.(Model)

	// When: the user changes the screen mode, which is the only thing left to change
	updated, _ := m.Update(keyMsgFor("="))
	after := updated.(Model)
	if after.focus.ScreenMode == m.focus.ScreenMode {
		t.Fatal("the key did not change the screen mode, so the pane never resized")
	}

	// Then: the list fills the pane it was given
	frame, drawn := pipelinePanelRows(t, after)
	if drawn != frame {
		t.Errorf("the resized pane holds %d rows and the list drew %d", frame, drawn)
	}
}
