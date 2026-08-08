package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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

// animationFrames stops early when the tick chain does, so fewer than n frames back is
// what a frozen animation looks like from the outside.
func animationFrames(m Model, n int, render func(Model) string) []string {
	var frames []string
	msg := tickFrom(m.spinner.Tick)
	for range n {
		if msg == nil {
			return frames
		}
		updated, cmd := m.Update(msg)
		m = updated.(Model)
		frames = append(frames, render(m))
		msg = tickFrom(cmd)
	}
	return frames
}

func spinnerFrames(m Model, n int) []string {
	return animationFrames(m, n, func(m Model) string { return m.spinner.View() })
}

func projectRowFrames(m Model, n int) []string {
	return animationFrames(m, n, func(m Model) string { return m.projectList.View() })
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

// loadingStatusModel leaves the pending status fetch as the only thing on screen that
// could animate, so a test over it cannot pass on some other state's animation.
func loadingStatusModel(t *testing.T) Model {
	t.Helper()
	m := newMultiPanelModel(PanelProjects)
	m.spinner = newAppSpinner()
	m.ctx = context.Background()
	m.client = &mockService{}
	m.pipelineView.pipelines = nil
	if m.needsAnimation() {
		t.Fatal("this scenario needs the pending status fetch to be the only reason to animate")
	}
	m.pipelineStatus.Set(1, pipelineState{loading: true})
	return m
}

// TestProjectRow_AnimatesWhileItsPipelineStatusLoads: a project row waiting on its pipeline
// status shows a moving indicator.
// Given a projects panel whose only pending work is one project's status fetch, when four
// animation frames are driven through Update, then the panel renders four frames and more than
// one of them differs.
// Why it matters: that row is the only place the fetch is visible, so a frozen glyph there reads
// as a request that died.
func TestProjectRow_AnimatesWhileItsPipelineStatusLoads(t *testing.T) {
	// Given: a project row waiting on its pipeline status
	m := loadingStatusModel(t)

	// When: four animation frames are driven through Update
	rows := projectRowFrames(m, 4)

	// Then: the animation ran for every frame and the row actually moved
	if len(rows) < 4 {
		t.Fatalf("the animation stopped after %d frames, so the row freezes while the fetch runs", len(rows))
	}
	if distinctFrames(rows) < 2 {
		t.Errorf("the project row drew the same glyph on every frame:\n%s", rows[0])
	}
}

// TestProjectRow_KeepsTheRowStylingWhileItAnimates: the animated row is styled like every other row.
// Given a colour-capable terminal, when the panel is drawn with one row waiting on its status and
// again with that row's status known, then both renders carry the same number of escape sequences.
// Why it matters: a styled inner component closes its own span with a reset that clears every
// attribute, so the project name after the glyph would draw with no colour and no bold while the
// rest of the list keeps both.
func TestProjectRow_KeepsTheRowStylingWhileItAnimates(t *testing.T) {
	// Given: a colour-capable terminal, so row styling is observable at all
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	// When: the panel is drawn with the row waiting, and again with its status known
	m := loadingStatusModel(t)
	waiting := projectRowsAfterOneTick(m)
	m.pipelineStatus.Set(1, pipelineState{hasInfo: true, info: gitlab.PipelineSummary{Status: "success"}})
	known := projectRowsAfterOneTick(m)

	// Then: the waiting row is styled no differently from the row beside it
	if strings.Count(known, "\x1b[") == 0 {
		t.Fatal("the panel rendered with no styling at all, so this test cannot observe the defect")
	}
	if got, want := strings.Count(waiting, "\x1b["), strings.Count(known, "\x1b["); got != want {
		t.Errorf("the waiting row carries %d escape sequences against %d once its status is known, "+
			"so the animated glyph brings styling of its own and the reset closing it strips the "+
			"row colour from the project name\nwaiting: %q\nknown:   %q", got, want, waiting, known)
	}
}

func projectRowsAfterOneTick(m Model) string {
	updated, _ := m.Update(tickFrom(m.spinner.Tick))
	return updated.(Model).projectList.View()
}
