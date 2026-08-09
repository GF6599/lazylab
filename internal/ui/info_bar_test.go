package ui

import (
	"testing"

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
