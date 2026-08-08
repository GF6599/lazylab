package ui

import (
	"context"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// tickFrom runs cmd and finds the spinner tick inside it, unpacking a batch the way the
// Bubble Tea runtime does. It returns nil when the command carries no tick, which is what
// a stopped animation looks like from the outside.
//
// It runs every command it unpacks, including fetches batched alongside the tick, so a
// model handed to it needs a context and a client that make those harmless.
func tickFrom(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case spinner.TickMsg:
		return msg
	case tea.BatchMsg:
		for _, batched := range msg {
			if found := tickFrom(batched); found != nil {
				return found
			}
		}
	}
	return nil
}

func spinnerFrames(m Model, n int) []string {
	var frames []string
	msg := tickFrom(m.spinner.Tick)
	for range n {
		if msg == nil {
			return frames
		}
		updated, cmd := m.Update(msg)
		m = updated.(Model)
		frames = append(frames, m.spinner.View())
		msg = tickFrom(cmd)
	}
	return frames
}

func distinctFrames(frames []string) int {
	seen := map[string]bool{}
	for _, f := range frames {
		seen[f] = true
	}
	return len(seen)
}

// pipelineStatusModel sets a context and a client because tickFrom runs whatever is
// batched alongside the tick.
func pipelineStatusModel(status string) Model {
	m := newMultiPanelModel(PanelPipelines)
	m.spinner = newAppSpinner()
	m.ctx = context.Background()
	m.client = &mockService{}
	m.pipelineView.project = gitlab.ProjectNode{ID: 1}
	m.pipelineView.pipelines = []gitlab.PipelineSummary{{ID: 10, Ref: "main", Status: status}}
	m.pipelineView.selected = 0
	return m
}

func runningPipelineModel() Model {
	return pipelineStatusModel("running")
}

// TestSpinner_KeepsAnimatingWhileProjectsLoad: the loading spinner moves on its own.
// Given a model loading its project list, when four animation frames are driven through Update,
// then the spinner renders four frames and more than one of them differs.
// Why it matters: the spinner is the only sign the app is still working during a load, so one
// that stops after a single frame reports a hang that is not happening.
func TestSpinner_KeepsAnimatingWhileProjectsLoad(t *testing.T) {
	// Given: a model whose project list is loading
	m := newMultiPanelModel(PanelProjects)
	m.spinner = newAppSpinner()
	m.loading = true

	// When: four animation frames are driven through Update
	frames := spinnerFrames(m, 4)

	// Then: the animation ran for every frame and actually moved
	if len(frames) < 4 {
		t.Fatalf("the animation stopped after %d frames: %q", len(frames), frames)
	}
	if distinctFrames(frames) < 2 {
		t.Errorf("the spinner rendered the same frame every time: %q", frames)
	}
}

// TestSpinner_KeepsAnimatingWhileAPipelineRuns: a running pipeline animates with no load in flight.
// Given a pipelines panel showing a running pipeline and nothing loading, when four animation
// frames are driven through Update, then the spinner renders four frames and more than one differs.
// Why it matters: a running pipeline is the state the user watches for minutes at a time, and it
// is exactly the state in which nothing is loading, so a gate that only animates during a fetch
// leaves the screen still while the thing being watched changes.
func TestSpinner_KeepsAnimatingWhileAPipelineRuns(t *testing.T) {
	// Given: a running pipeline on screen with nothing loading
	m := runningPipelineModel()
	if m.isLoading() {
		t.Fatal("this scenario needs nothing loading, so the running pipeline is the only reason to animate")
	}

	// When: four animation frames are driven through Update
	frames := spinnerFrames(m, 4)

	// Then: the animation ran for every frame and actually moved
	if len(frames) < 4 {
		t.Fatalf("the animation stopped after %d frames: %q", len(frames), frames)
	}
	if distinctFrames(frames) < 2 {
		t.Errorf("the spinner rendered the same frame every time: %q", frames)
	}
}

// TestSpinner_AnimationReturnsAfterAnIdlePeriod: the animation comes back once it is needed again.
// Given an idle model whose tick chain has already stopped, when a pipeline starts running, then
// the next message hands back a tick and the spinner moves again.
// Why it matters: the tick chain is self-terminating, so nothing restarts it on its own. Without
// a restart the first idle moment freezes the spinner for the rest of the session, and every
// state that animates afterwards is dead on arrival.
func TestSpinner_AnimationReturnsAfterAnIdlePeriod(t *testing.T) {
	// Given: an idle model, with the tick it started with already consumed
	m := pipelineStatusModel("success")
	if m.needsAnimation() {
		t.Fatal("this scenario needs an idle model, with nothing to animate")
	}
	updated, cmd := m.Update(tickFrom(m.spinner.Tick))
	m = updated.(Model)
	if tickFrom(cmd) != nil {
		t.Fatal("the chain should stop while nothing needs animating")
	}

	// When: a pipeline starts running and any next message arrives
	m.pipelineView.pipelines[0].Status = "running"
	updated, cmd = m.Update(cacheSavedMsg{})
	m = updated.(Model)

	// Then: a tick comes back, and the spinner moves on it
	if tickFrom(cmd) == nil {
		t.Fatal("no tick came back, so the spinner stays frozen for the rest of the session")
	}
	if frames := spinnerFrames(m, 4); distinctFrames(frames) < 2 {
		t.Errorf("the spinner did not move after the restart: %q", frames)
	}
}

// TestSpinner_AnimatesEveryStatusThatStillMoves: animation follows whether a pipeline can still
// change on its own.
// Given a pipelines panel over a pipeline in one status, when a tick is driven through Update,
// then a follow-up tick comes back only for a status GitLab still advances by itself.
// Why it matters: manual and blocked wait for a person and never move on their own, so animating
// them spins forever against nothing, while created and preparing are the first seconds after a
// retry, which is exactly when the user is watching for a sign that it worked.
func TestSpinner_AnimatesEveryStatusThatStillMoves(t *testing.T) {
	for _, tc := range []struct {
		status string
		moves  bool
	}{
		{"created", true},
		{"waiting_for_resource", true},
		{"preparing", true},
		{"pending", true},
		{"running", true},
		{"scheduled", true},
		{"success", false},
		{"failed", false},
		{"canceled", false},
		{"skipped", false},
		{"manual", false},
		{"blocked", false},
	} {
		// Given: a pipelines panel over a pipeline in this status
		m := pipelineStatusModel(tc.status)

		// When: a tick is driven through Update
		_, cmd := m.Update(tickFrom(m.spinner.Tick))

		// Then: the animation continues only for a status that still moves
		if got := tickFrom(cmd) != nil; got != tc.moves {
			t.Errorf("status %q animates = %v, want %v", tc.status, got, tc.moves)
		}
	}
}

// TestSpinner_AnimatesARunningPipelineInTheProjectList: the projects panel animates too.
// Given a projects panel whose cached status for a project is running, and no pipeline loaded
// into the pipelines panel, when a tick is driven through Update, then a follow-up tick comes back.
// Why it matters: the projects panel carries its own status icon per row from a separate cache, so
// a check that reads only the pipelines panel leaves that row frozen while its pipeline runs.
func TestSpinner_AnimatesARunningPipelineInTheProjectList(t *testing.T) {
	// Given: a projects panel with a running pipeline cached for one project
	m := newMultiPanelModel(PanelProjects)
	m.spinner = newAppSpinner()
	m.ctx = context.Background()
	m.client = &mockService{}
	m.pipelineView.pipelines = nil
	if m.needsAnimation() {
		t.Fatal("this scenario needs the cache to be the only reason to animate")
	}
	m.pipelineStatus.Set(1, pipelineState{hasInfo: true, info: gitlab.PipelineSummary{ID: 10, Status: "running"}})

	// When: a tick is driven through Update
	_, cmd := m.Update(tickFrom(m.spinner.Tick))

	// Then: the animation continues
	if tickFrom(cmd) == nil {
		t.Error("the project row's pipeline is running, but the spinner stopped")
	}
}
