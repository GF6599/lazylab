package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestPanelFooter_CountsTheWholeCollectionNotThePage: the position counts every item there is.
// Given a pane showing the second page of a larger collection, when the footer is drawn, then it
// reports the row's place among every item rather than its place on the page.
// Why it matters: the pane also shows which page of many it is on, so a footer counting only the
// page contradicts it, and a reader has no way to tell how much there is.
func TestPanelFooter_CountsTheWholeCollectionNotThePage(t *testing.T) {
	// Given: a projects pane on page 2 of a collection of 137, with the first row selected
	m := newMultiPanelModel(PanelProjects)
	perPage := m.displayPerPage()
	m.opts.ProjectsPerPage = perPage
	m.allProjects = testProjects(perPage * 2)
	m.totalProjects = 137
	m.pagesReady = map[int]bool{1: true, 2: true}
	m.page = 2
	m.selected = 0
	m.invalidateVisibleCache()

	// When: the footer is drawn
	got := panelFooter(PanelProjects, &m)

	// Then: it counts from the start of the collection, not the start of the page
	want := formatPosition(perPage+1, 137)
	if got != want {
		t.Errorf("the footer reads %q, want %q", got, want)
	}
}

// TestPanelFooter_CountsWhatIsLoadedWhenTheTotalIsUnknown: an unknown total is never invented.
// Given a pipelines pane whose collection total GitLab withheld, when the footer is drawn, then it
// counts the runs in hand.
// Why it matters: GitLab stops reporting a total past ten thousand items, and a footer that
// printed zero there would tell the user the pane is empty while it is drawing rows.
func TestPanelFooter_CountsWhatIsLoadedWhenTheTotalIsUnknown(t *testing.T) {
	// Given: a pipelines pane holding three runs and no collection total
	m := newMultiPanelModel(PanelPipelines)
	m.pipelineView.pipelines = []gitlab.PipelineSummary{{ID: 1}, {ID: 2}, {ID: 3}}
	m.pipelineView.totalItems = 0
	m.pipelineView.page = 1
	m.pipelineView.perPage = 25
	m.pipelineView.selected = 1

	// When: the footer is drawn
	got := panelFooter(PanelPipelines, &m)

	// Then: it counts the runs it has rather than reporting none
	want := formatPosition(2, 3)
	if got != want {
		t.Errorf("the footer reads %q, want %q", got, want)
	}
}

// TestPanelFooter_DescribesTheRowsOnScreenWhileAResizeIsInFlight: the footer never runs ahead of
// the rows.
// Given a pipelines pane deep into a collection, when the pane settles at a new size and GitLab has
// not answered yet, then the footer still reports the position of the row on screen.
// Why it matters: the page number and the page size decide what the footer counts, so moving them
// when the request goes out makes the footer describe a page nobody is looking at, which is the
// disagreement between the two counts that this panel had in the first place.
func TestPanelFooter_DescribesTheRowsOnScreenWhileAResizeIsInFlight(t *testing.T) {
	// Given: a pipelines pane on page 20 of pages of 10, with the cursor on the fifth row
	m := newMultiPanelModel(PanelPipelines)
	m.ctx = context.Background()
	m.client = &mockService{}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 90})
	after := sized.(Model)
	after.pipelineView.page, after.pipelineView.perPage, after.pipelineView.selected = 20, 10, 4
	after.pipelineView.totalItems = 2075
	after.pipelineView.pipelines = make([]gitlab.PipelineSummary, 10)
	before := panelFooter(PanelPipelines, &after)

	// When: the resize settles and the request goes out, with no answer yet
	settled, _ := after.Update(settledResize(after))

	// Then: the footer still describes the rows that are drawn
	next := settled.(Model)
	got := panelFooter(PanelPipelines, &next)
	if got != before {
		t.Errorf("the footer moved to %q before the rows did, want it still reading %q", got, before)
	}
}

// TestPanelFooter_KeepsDescribingTheRowsWhenAResizeFetchFails: a failed resize leaves the footer
// describing what is drawn.
// Given an MR pane whose resize request fails, when the failure comes back, then the footer still
// reports the position of the row on screen.
// Why it matters: merge requests have no auto-refresh to correct a wrong count later, so a footer
// left describing a page that never arrived stays wrong until the user changes page or tab.
func TestPanelFooter_KeepsDescribingTheRowsWhenAResizeFetchFails(t *testing.T) {
	// Given: an MR pane on page 4 of pages of 10, with the cursor on the third row
	m := newMultiPanelModel(PanelMRs)
	m.ctx = context.Background()
	m.client = &mockService{}
	m.mrView.project = m.pipelineView.project
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 90})
	after := sized.(Model)
	after.mrView.page, after.mrView.perPage, after.mrView.selected = 4, 10, 2
	after.mrView.totalItems = 96
	after.mrView.mrs = make([]gitlab.MergeRequestSummary, 10)
	before := panelFooter(PanelMRs, &after)

	// When: the resize settles and GitLab refuses the request
	settled, _ := after.Update(settledResize(after))
	failed, _ := settled.(Model).Update(mrsLoadedMsg{
		projectID: after.mrView.project.ID,
		err:       context.DeadlineExceeded,
	})

	// Then: the footer still describes the rows that are drawn
	next := failed.(Model)
	got := panelFooter(PanelMRs, &next)
	if got != before {
		t.Errorf("a failed resize left the footer reading %q, want %q", got, before)
	}
}
