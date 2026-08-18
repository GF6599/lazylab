package ui

import (
	"context"
	"strings"
	"testing"
)

// TestRenderDetailPane_SelectionPastTheEndFallsBack: a selection index beyond the visible rows
// renders the placeholder instead of panicking.
// Given three visible projects and a selection index of five, when the detail pane renders through
// both the direct and the cached path, then each returns the "select a project" placeholder.
// Why it matters: renderDetailPane runs inside View on every frame, so one clamp missed on any
// mutation path crashes the whole TUI instead of drawing one stale pane.
func TestRenderDetailPane_SelectionPastTheEndFallsBack(t *testing.T) {
	// Given: three visible projects and a selection index past the end
	m := NewModel(context.Background(), &mockService{}, Options{})
	m.projectTab = projectTabAll
	m.allProjects = pagedProjects(1, 3)
	m.pagesReady = map[int]bool{1: true}
	m.totalProjects = 3
	m.selected = 5

	// When/Then: both render paths fall back to the placeholder
	if view := renderDetailPane(&m, 60); !strings.Contains(view, "Select a project") {
		t.Fatalf("renderDetailPane did not fall back to the placeholder:\n%s", view)
	}
	if view := m.renderDetailCached(60, 20); !strings.Contains(view, "Select a project") {
		t.Fatalf("renderDetailCached did not fall back to the placeholder:\n%s", view)
	}
}
