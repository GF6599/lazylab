package ui

import (
	"context"
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
// it, then the start time is requested alongside the stages and the jobs.
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

	// When: a jump lands on the running pipeline and the queued work runs
	updated, cmd := m.handlePipelinesPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if after := updated.(Model); after.selectedPipeline().Status != "running" {
		t.Fatalf("the jump did not land on the running pipeline: %+v", after.selectedPipeline())
	}
	runBatch(cmd)

	// Then: the start time was requested
	if !asked {
		t.Error("the jump did not ask for the start time of the run it landed on")
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
