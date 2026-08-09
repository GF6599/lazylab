package ui

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// pipelineNavModel puts a full page of pipelines in the panel, all finished except the last, and
// leaves the cursor on the first. A big step then has somewhere to go.
func pipelineNavModel(count int) Model {
	m := newMultiPanelModel(PanelPipelines)
	m.ctx = context.Background()
	m.client = &mockService{}
	m.height = 40
	m.pipelineView.project = gitlab.ProjectNode{ID: 1}
	pipelines := make([]gitlab.PipelineSummary, count)
	items := make([]list.Item, count)
	for i := range pipelines {
		status := "success"
		if i == count-1 {
			status = "running"
		}
		pipelines[i] = gitlab.PipelineSummary{ID: 100 + i, Ref: "main", Status: status}
		items[i] = pipelineItem{summary: pipelines[i]}
	}
	m.pipelineView.pipelines = pipelines
	m.pipelineView.selected = 0
	m.pipelineView.pipelineList = newBareList(items, pipelineDelegate{}, 60, 10)
	return m
}

// TestPipelinePanel_MovesTheHighlightOnEveryJump: the row drawn as current is the row acted on.
// Given a panel of pipelines with the cursor on the first, when a jump key moves the selection,
// then the list draws its cursor on the same row the panel acts on.
// Why it matters: the panel draws the list's own cursor and every action reads a separate index, so
// a jump that moves one and not the other highlights one pipeline while retrying another.
func TestPipelinePanel_MovesTheHighlightOnEveryJump(t *testing.T) {
	for _, key := range []string{"G", ">", "ctrl+d", "g", "<", "ctrl+u"} {
		// Given: a panel of pipelines with the cursor part way down
		m := pipelineNavModel(12)
		m.pipelineView.selected = 5
		m.pipelineView.pipelineList.Select(5)

		// When: a jump key moves the selection
		updated, _ := m.handlePipelinesPanelKey(keyMsgFor(key))
		after := updated.(Model)

		// Then: the list draws its cursor on the row the panel acts on
		if after.pipelineView.pipelineList.Index() != after.pipelineView.selected {
			t.Errorf("%q: the highlight is on row %d but the panel acts on row %d",
				key, after.pipelineView.pipelineList.Index(), after.pipelineView.selected)
		}
	}
}

// keyMsgFor builds the key event for a key name, because a control key does not arrive as a rune.
func keyMsgFor(key string) tea.KeyMsg {
	switch key {
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

// TestPipelinePanel_AsksForTheStartTimeOfTheRunItLandsOn: a jump loads everything the pane draws.
// Given a panel whose last pipeline is still running and no start time known, when a jump lands on
// it and the user pauses, then the start time is requested alongside the stages and the jobs.
// Why it matters: the detail pane draws the run's elapsed time from that fetch, and a jump that
// queues the stages and the jobs but not the start leaves the pane half filled until the next
// refresh answers seconds later.
func TestPipelinePanel_AsksForTheStartTimeOfTheRunItLandsOn(t *testing.T) {
	// Given: a panel whose last pipeline is still running, with no start time known
	m := pipelineNavModel(12)
	var asked bool
	m.client = &mockService{GetPipelineFn: func(context.Context, int, int) (gitlab.PipelineSummary, error) {
		asked = true
		return gitlab.PipelineSummary{}, nil
	}}

	// When: a jump lands on the running pipeline
	updated, _ := m.handlePipelinesPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	after := updated.(Model)
	if after.selectedPipeline().Status != "running" {
		t.Fatalf("the jump did not land on the running pipeline: %+v", after.selectedPipeline())
	}

	// And: the user pauses, so the timer the jump armed fires and the queued work runs
	_, cmd := after.Update(armedTick(t, after))
	runBatch(cmd)

	// Then: the start time was requested
	if !asked {
		t.Error("the jump did not ask for the start time of the run it landed on")
	}
}

// A nil tick is the first press, which has no earlier keystroke to have armed one.
func fireTimer(m Model, tick *pipelineSelectionTickMsg) Model {
	if tick == nil {
		return m
	}
	updated, cmd := m.Update(*tick)
	runBatch(cmd)
	return updated.(Model)
}

// Firing this is what a pause in navigation looks like to the model.
func armedTick(t *testing.T, m Model) pipelineSelectionTickMsg {
	t.Helper()
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		t.Fatal("no row is selected, so no timer names a row to fetch")
	}
	return pipelineSelectionTickMsg{pipelineID: pipeline.ID}
}

// TestPipelinePanel_AsksAboutTheRunTheUserStopsOnNotEveryRunPassed: scrolling costs one round of
// requests, not one per row.
// Given a panel of runs with the cursor on the first, when the user holds the key down across eight
// rows and every timer the burst armed then fires, then GitLab is asked about the eighth run alone.
// Why it matters: each row costs three requests, so a held key sends hundreds within seconds, and
// GitLab answers the rest with a refusal that the panel shows the user as a failure to load.
func TestPipelinePanel_AsksAboutTheRunTheUserStopsOnNotEveryRunPassed(t *testing.T) {
	// Given: a panel of runs, and a client that records which run each request names
	m := pipelineNavModel(12)
	var asked []string
	m.client = &mockService{
		PipelineStagesFn: func(_ context.Context, _, pipelineID int) ([]gitlab.PipelineStage, error) {
			asked = append(asked, fmt.Sprintf("stages:%d", pipelineID))
			return nil, nil
		},
		ListPipelineJobsFn: func(_ context.Context, _, pipelineID int) ([]gitlab.PipelineJob, error) {
			asked = append(asked, fmt.Sprintf("jobs:%d", pipelineID))
			return nil, nil
		},
	}

	// When: the user holds the key down across eight rows, each press arming a timer that lands
	// while the presses after it are still arriving
	model := Model(m)
	var armed *pipelineSelectionTickMsg
	for i := 0; i < 8; i++ {
		updated, _ := model.handlePipelinesPanelKey(keyMsgFor("j"))
		model = fireTimer(updated.(Model), armed)
		tick := armedTick(t, model)
		armed = &tick
	}

	// And: the user stops moving, so the last timer lands with no press after it
	fireTimer(model, armed)

	// Then: GitLab was asked about the run the burst ended on, and about no other
	want := []string{"jobs:108", "stages:108"}
	slices.Sort(asked)
	if !slices.Equal(asked, want) {
		t.Errorf("asked GitLab for %v, want %v", asked, want)
	}
}

// runBatch runs a command and any command it batched, so the fetches a key handler queues all reach
// the client. tea.Batch hides its children behind one BatchMsg, which nothing unpacks in a test.
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			runBatch(child)
		}
	}
}
