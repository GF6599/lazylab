package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// tickFrom runs cmd and finds the spinner tick inside it, unpacking a batch the way the
// Bubble Tea runtime does. It returns nil when the command carries no tick, which is what
// a stopped animation looks like from the outside.
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
