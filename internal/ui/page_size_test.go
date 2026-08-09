package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// manyProjectsModel caches far more projects than any pane can hold, and reaches its resting size
// the way a launch does.
func manyProjectsModel(height int) Model {
	m := newMultiPanelModel(PanelProjects)
	projects := make([]gitlab.ProjectNode, 400)
	for i := range projects {
		projects[i] = gitlab.ProjectNode{
			ID:                i + 1,
			Name:              fmt.Sprintf("proj-%03d", i),
			PathWithNamespace: fmt.Sprintf("team/proj-%03d", i),
		}
	}
	m.allProjects = projects
	m.opts.ProjectsPerPage = 30
	m.page = 1
	m.pagesReady = map[int]bool{}
	for p := 1; p <= 20; p++ {
		m.pagesReady[p] = true
	}
	m.invalidateVisibleCache()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: height})
	return sized.(Model)
}

// TestProjectsPane_ShowsAsManyProjectsAsItHasRoomFor: a page holds what the pane can draw.
// Given more projects cached than any pane can hold, when the pane is drawn at two heights, then
// at each height it draws its own height in projects.
// Why it matters: every project is already cached, so a page fixed at 30 leaves a taller pane
// blank below the thirtieth row and asks the user to change page for rows already in memory.
func TestProjectsPane_ShowsAsManyProjectsAsItHasRoomFor(t *testing.T) {
	for _, height := range []int{50, 90} {
		// Given: a pane sized by the terminal, with far more projects cached than it can hold
		m := manyProjectsModel(height)
		layout := computeLayout(m.width, m.height, m.focus)
		room := layout.PanelHeights[PanelProjects]

		// When: the pane is drawn
		drawn := 0
		for _, line := range strings.Split(renderProjectsPanelContent(m, layout.SidebarWidth, room), "\n") {
			if strings.TrimSpace(line) != "" {
				drawn++
			}
		}

		// Then: it draws exactly as many projects as it has room for
		if drawn != room {
			t.Errorf("terminal %d: the pane holds %d rows and drew %d projects", height, room, drawn)
		}
	}
}

// TestProjectsPane_CountsItsPagesFromWhatThePaneHolds: the page count follows the pane.
// Given a fixed number of cached projects, when the pane grows, then the number of pages falls.
// Why it matters: the page count is what the user reads to judge how far they have to travel, so
// a count fixed at the old page size describes a list they are no longer looking at.
func TestProjectsPane_CountsItsPagesFromWhatThePaneHolds(t *testing.T) {
	// Given: a short pane and a tall one over the same cached projects
	short := manyProjectsModel(50)
	tall := manyProjectsModel(90)

	// When: each reports how many pages it needs
	shortPages := short.displayTotalPages()
	tallPages := tall.displayTotalPages()

	// Then: the taller pane needs fewer of them, and neither reports none
	if shortPages <= tallPages {
		t.Errorf("the short pane needs %d pages and the tall one %d, so the count ignores the pane",
			shortPages, tallPages)
	}
	if tallPages < 1 {
		t.Errorf("the tall pane reports %d pages", tallPages)
	}
}

func testProjects(n int) []gitlab.ProjectNode {
	projects := make([]gitlab.ProjectNode, n)
	for i := range projects {
		projects[i] = gitlab.ProjectNode{
			ID:                i + 1,
			Name:              fmt.Sprintf("proj-%03d", i),
			PathWithNamespace: fmt.Sprintf("team/proj-%03d", i),
		}
	}
	return projects
}
