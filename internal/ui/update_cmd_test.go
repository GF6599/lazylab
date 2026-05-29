package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// The Round-1 refactor split Update into typed handlers but the existing
// tests only assert state changes. These tests pin the tea.Cmd values
// returned in the critical paths so the auto-refresh chain, async loads,
// and tea.Quit can't silently be regressed into nil.

// TestUpdate_PipelineTick_ReturnsRefreshCmd guards the auto-refresh
// cascade: a pipelineTickMsg arriving while modePipelines is active must
// schedule the next tick (and likely a fetch). A nil cmd here would
// freeze the live-refresh that drives the pipeline view.
func TestUpdate_PipelineTick_ReturnsRefreshCmd(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	res, _ := m.openPipelineView(project)
	m = res.(Model)

	if m.mode != modePipelines {
		t.Fatalf("precondition: expected modePipelines, got %d", m.mode)
	}

	got, cmd := m.Update(pipelineTickMsg{})
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd from pipelineTickMsg in modePipelines")
	}
	if got.(Model).mode != modePipelines {
		t.Fatalf("expected to stay in modePipelines after tick, got %d", got.(Model).mode)
	}
}

// TestUpdate_OpenPipelineView_ReturnsLoadCmd verifies that the
// "View pipelines" transition (modeled by openPipelineView, which is
// what the project-action modal selects) returns a tea.Cmd whose
// produced message is the pipelinesLoadedMsg the Update loop expects.
// Without this guarantee the pipeline view would open but never populate.
func TestUpdate_OpenPipelineView_ReturnsLoadCmd(t *testing.T) {
	m := newTestModel()
	called := false
	m.client = &mockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			called = true
			return gitlab.PipelinePage{Pipelines: []gitlab.PipelineSummary{{ID: 7, Status: "success"}}, Page: 1, TotalPages: 1}, nil
		},
	}

	_, cmd := m.openPipelineView(m.allProjects[0])
	if cmd == nil {
		t.Fatal("expected openPipelineView to return a fetch tea.Cmd")
	}

	msg := cmd()
	if !called {
		t.Fatal("expected fetch cmd to call ListPipelines")
	}
	loaded, ok := msg.(pipelinesLoadedMsg)
	if !ok {
		t.Fatalf("expected pipelinesLoadedMsg, got %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("unexpected err: %v", loaded.err)
	}
	if len(loaded.pipelines) != 1 || loaded.pipelines[0].ID != 7 {
		t.Fatalf("unexpected pipelines payload: %+v", loaded.pipelines)
	}
}

// TestUpdate_QuitKey_ReturnsTeaQuit verifies that ctrl+c in the search
// state hits tea.Quit. tea.Quit is documented to produce tea.QuitMsg{}
// when executed, so we execute the returned cmd and type-check the msg.
func TestUpdate_QuitKey_ReturnsTeaQuit(t *testing.T) {
	m := newTestModel()
	m.search.active = true

	_, cmd := m.handleProjectSearchKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected ctrl+c in search state to return tea.Quit, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg from returned cmd, got %T", cmd())
	}
}
