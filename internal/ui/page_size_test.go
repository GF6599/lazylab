package ui

import (
	"context"
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

// recordingPaneModel puts a pipelines pane and an MR pane on screen over a client that records the
// size of every page each one is asked for.
func recordingPaneModel(pipelines, mrs *[]int) Model {
	m := newMultiPanelModel(PanelPipelines)
	m.ctx = context.Background()
	m.mrView.project = m.pipelineView.project
	m.client = &mockService{
		ListPipelinesFn: func(_ context.Context, _ int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			*pipelines = append(*pipelines, opts.PerPage)
			return gitlab.PipelinePage{}, nil
		},
		ListMergeRequestsFn: func(_ context.Context, _ int, opts gitlab.MRListOptions) (gitlab.MRPage, error) {
			*mrs = append(*mrs, opts.PerPage)
			return gitlab.MRPage{}, nil
		},
	}
	return m
}

// dragHeights are terminal heights that each give both paned panels a size of their own, so every
// step of a drag names a size the step before it did not. A plateau is a genuine settle and the
// pane is right to fetch on one, which would make "asked once" the wrong thing to assert.
var dragHeights = []int{40, 48, 56, 64, 72, 80, 88, 96}

// dragThrough resizes the terminal through every height in turn, firing the timer each step armed
// one step later, which is when it lands while the drag is still moving.
func dragThrough(t *testing.T, m Model, heights []int) Model {
	t.Helper()
	var armed *pageSizeTickMsg
	seen := map[pageSizeTickMsg]bool{}
	for _, height := range heights {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: height})
		m = fireResize(updated.(Model), armed)
		tick := settledResize(m)
		if seen[tick] {
			t.Fatalf("height %d repeats the pane sizes %+v, so the drag stands still there", height, tick)
		}
		seen[tick] = true
		armed = &tick
	}
	// The drag stops, so the last timer lands with no step after it.
	return fireResize(m, armed)
}

// settledResize is the message a resize timer delivers once the dragging has stopped.
func settledResize(m Model) pageSizeTickMsg {
	return pageSizeTickMsg{pipelines: m.pipelinePageSize(), mrs: m.mrPageSize()}
}

// applyBatch runs a command and any command it batched, then feeds each message back the way the
// runtime does. Unlike runBatch it keeps the answers, so a test can assert on what a fetch changed
// once its rows arrive rather than only on what it asked for.
func applyBatch(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		updated, _ := m.Update(msg)
		return updated.(Model)
	}
	for _, child := range batch {
		m = applyBatch(m, child)
	}
	return m
}

// A nil tick is the first step of a drag, which has no earlier step to have armed one.
func fireResize(m Model, tick *pageSizeTickMsg) Model {
	if tick == nil {
		return m
	}
	updated, cmd := m.Update(*tick)
	runBatch(cmd)
	return updated.(Model)
}

// TestPipelinesPane_FetchesAPageThatFillsThePane: a page holds what the pane draws.
// Given a pipelines pane over a client that records each page size, when the terminal grows and the
// resize settles, then GitLab is asked for a page as tall as the pane.
// Why it matters: a pipeline page comes over the network, so unlike the projects list the pane
// cannot fill itself from memory. A page fixed at twenty five leaves every row past the twenty
// fifth blank on a tall terminal.
func TestPipelinesPane_FetchesAPageThatFillsThePane(t *testing.T) {
	// Given: a pipelines pane on screen
	var asked, mrs []int
	m := recordingPaneModel(&asked, &mrs)

	// When: the terminal grows, and the resize settles
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 90})
	after := sized.(Model)
	_, cmd := after.Update(settledResize(after))
	runBatch(cmd)

	// Then: the page GitLab was asked for holds what the pane draws. The rows are counted off the
	// layout rather than off the page size, which would compare the code against itself.
	room := after.panePageSize(PanelPipelines)
	if len(asked) != 1 || asked[0] != room {
		t.Errorf("the pane holds %d rows and asked GitLab for pages of %v", room, asked)
	}
}

// TestMRsPane_FetchesAPageThatFillsThePane: the merge request pane follows its own height.
// Given an MR pane on screen, when the terminal grows and the resize settles, then GitLab is asked
// for a page as tall as that pane.
// Why it matters: the two paned panels get different shares of the sidebar, so a single page size
// covering both leaves one of them either short of rows or asking for rows it cannot draw.
func TestMRsPane_FetchesAPageThatFillsThePane(t *testing.T) {
	// Given: an MR pane on screen
	var pipelines, asked []int
	m := recordingPaneModel(&pipelines, &asked)

	// When: the terminal grows, and the resize settles
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 90})
	after := sized.(Model)
	_, cmd := after.Update(settledResize(after))
	runBatch(cmd)

	// Then: the page GitLab was asked for holds what the pane draws. The rows are counted off the
	// layout rather than off the page size, which would compare the code against itself.
	room := after.panePageSize(PanelMRs)
	if len(asked) != 1 || asked[0] != room {
		t.Errorf("the pane holds %d rows and asked GitLab for pages of %v", room, asked)
	}
}

// TestPanes_AskOnceForTheSizeADragEndsOn: a drag costs one fetch, not one per step.
// Given a pipelines pane and an MR pane on screen, when the user drags the terminal through eight
// heights and every timer the drag armed then fires, then each pane asks GitLab once, for the
// height the drag ended on.
// Why it matters: dragging a terminal corner delivers a resize per frame, and a fetch per frame is
// a burst of requests GitLab answers with a refusal that the panes show as a failure to load.
func TestPanes_AskOnceForTheSizeADragEndsOn(t *testing.T) {
	// Given: a pipelines pane and an MR pane on screen
	var pipelines, mrs []int
	m := recordingPaneModel(&pipelines, &mrs)

	// When: the user drags the corner through eight heights and then stops
	settled := dragThrough(t, m, dragHeights)

	// Then: each pane asked GitLab once, for a page that fills the height the drag ended on
	if want := settled.pipelinePageSize(); len(pipelines) != 1 || pipelines[0] != want {
		t.Errorf("the drag left a pipelines pane of %d rows and asked for pages of %v", want, pipelines)
	}
	if want := settled.mrPageSize(); len(mrs) != 1 || mrs[0] != want {
		t.Errorf("the drag left an MR pane of %d rows and asked for pages of %v", want, mrs)
	}
}

// TestPipelinesPane_AsksForNoMorePagesThanGitLabServes: a pane taller than the ceiling asks for the
// ceiling.
// Given a terminal tall enough for a pane of more than a hundred rows, when the resize settles,
// then the page asked for is a hundred.
// Why it matters: GitLab serves a hundred items whatever a larger request says, so a page recorded
// as larger than that counts every position past the first page against rows that never arrived.
func TestPipelinesPane_AsksForNoMorePagesThanGitLabServes(t *testing.T) {
	// Given: a terminal tall enough for a pane of more than a hundred rows
	var asked, mrs []int
	m := recordingPaneModel(&asked, &mrs)

	// When: the terminal grows past the ceiling, and the resize settles
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 300})
	after := sized.(Model)
	if room := after.panePageSize(PanelPipelines); room <= maxAPIPerPage {
		t.Fatalf("the pane holds %d rows, which does not reach the ceiling of %d", room, maxAPIPerPage)
	}
	settled, cmd := after.Update(settledResize(after))
	loaded := applyBatch(settled.(Model), cmd)

	// Then: the page asked for stops at the ceiling, and once those rows arrive the panel counts
	// positions against that same size
	if len(asked) != 1 || asked[0] != maxAPIPerPage {
		t.Errorf("asked GitLab for pages of %v, want one of %d", asked, maxAPIPerPage)
	}
	if got := loaded.pipelineView.perPage; got != maxAPIPerPage {
		t.Errorf("the panel counts against pages of %d, want %d", got, maxAPIPerPage)
	}
}

// TestPipelinesPane_KeepsTheRunItWasOnWhenThePageSizeChanges: a resize follows the row, not the
// page number.
// Given a panel deep into a long collection, when the pane settles at a size of its own, then the
// page asked for is the one that still holds the run the cursor was on.
// Why it matters: focusing a panel resizes it, so a page number kept across the resize would move
// the user somewhere else in the list every time they tab into the panel.
func TestPipelinesPane_KeepsTheRunItWasOnWhenThePageSizeChanges(t *testing.T) {
	// Given: a panel showing the twentieth page of pages of ten, with the cursor on the fifth row,
	// which is the hundred and ninety fifth run. It sits past the largest page GitLab serves, so no
	// page size leaves it on the first page.
	const position = 195
	var asked []int
	m := newMultiPanelModel(PanelPipelines)
	m.ctx = context.Background()
	m.client = &mockService{
		ListPipelinesFn: func(_ context.Context, _ int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			asked = append(asked, opts.Page)
			return gitlab.PipelinePage{}, nil
		},
	}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 90})
	after := sized.(Model)
	after.pipelineView.page, after.pipelineView.perPage, after.pipelineView.selected = 20, 10, 4

	// When: the resize settles at a size the pane picked for itself
	perPage := after.pipelinePageSize()
	_, cmd := after.Update(settledResize(after))
	runBatch(cmd)

	// Then: the page asked for is the one that holds the run the cursor was on
	if len(asked) != 1 {
		t.Fatalf("asked GitLab for pages %v, want exactly one", asked)
	}
	first, last := (asked[0]-1)*perPage+1, asked[0]*perPage
	if position < first || position > last {
		t.Errorf("run %d moved off screen: page %d of %d holds runs %d to %d",
			position, asked[0], perPage, first, last)
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
