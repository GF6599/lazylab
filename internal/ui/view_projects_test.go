package ui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/GF6599/lazylab/internal/gitlab"
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

// TestRenderDetailPane_TruncatesACommitTitleByRunes: a multibyte commit title truncates on a rune
// boundary, never mid-byte.
// Given a recent commit whose title is all two-byte runes, when the detail pane renders at a width
// that forces truncation, then the output is valid UTF-8 and the title carries the ellipsis.
// Why it matters: a byte-indexed cut through a multibyte rune leaves invalid UTF-8 that the
// terminal renders as replacement glyphs in the commit list.
func TestRenderDetailPane_TruncatesACommitTitleByRunes(t *testing.T) {
	// Given: one project whose latest commit title is all two-byte runes
	m := NewModel(context.Background(), &mockService{}, Options{})
	m.projectTab = projectTabAll
	m.allProjects = pagedProjects(1, 1)
	m.pagesReady = map[int]bool{1: true}
	m.totalProjects = 1
	m.selected = 0
	m.commitCache.Set(1, []gitlab.CommitSummary{{
		ShortID:   "abc1234",
		Title:     strings.Repeat("é", 60),
		Author:    "Dev",
		CreatedAt: time.Now(),
	}})

	// When: the pane renders at a width that forces the title to truncate
	view := renderDetailPane(&m, 40)

	// Then: no rune was cut in half and the truncation shows
	if !utf8.ValidString(view) {
		t.Fatalf("detail pane contains invalid UTF-8: %q", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("expected a truncated title with an ellipsis:\n%s", view)
	}
}
