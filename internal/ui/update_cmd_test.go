package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestUpdate_PipelineTick_ReturnsRefreshCmd: a pipeline tick in modePipelines schedules follow-up work.
// Given a model in modePipelines, when a pipelineTickMsg reaches Update, then a non-nil command comes back
// and the mode is unchanged.
// Why it matters: the tick chain is self-perpetuating, so a single nil command here permanently freezes
// the pipeline view's 5-second auto-refresh.
func TestUpdate_PipelineTick_ReturnsRefreshCmd(t *testing.T) {
	// Given: a model in modePipelines
	m := newTestModel()
	project := m.allProjects[0]
	res, _ := m.openPipelineView(project)
	m = res.(Model)

	if m.mode != modePipelines {
		t.Fatalf("precondition: expected modePipelines, got %d", m.mode)
	}

	// When: a pipeline tick reaches Update
	got, cmd := m.Update(pipelineTickMsg{})

	// Then: a follow-up command is scheduled and the mode is unchanged
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd from pipelineTickMsg in modePipelines")
	}
	if got.(Model).mode != modePipelines {
		t.Fatalf("expected to stay in modePipelines after tick, got %d", got.(Model).mode)
	}
}

// TestUpdate_OpenPipelineView_ReturnsLoadCmd: opening the pipeline view returns a command that fetches and
// delivers the pipelines.
// Given a model whose service records ListPipelines calls, when openPipelineView runs and its command
// executes, then the service is called and the command yields a pipelinesLoadedMsg carrying the fetched
// pipelines with no error.
// Why it matters: openPipelineView is what the project-action modal's "View pipelines" selects, and a nil
// or wrong-typed command would open a pipeline view that never populates.
func TestUpdate_OpenPipelineView_ReturnsLoadCmd(t *testing.T) {
	// Given: a model whose service records ListPipelines calls
	m := newTestModel()
	called := false
	m.client = &mockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			called = true
			return gitlab.PipelinePage{Pipelines: []gitlab.PipelineSummary{{ID: 7, Status: "success"}}, Page: 1, TotalPages: 1}, nil
		},
	}

	// When: the pipeline view opens and the returned fetch command runs
	_, cmd := m.openPipelineView(m.allProjects[0])
	if cmd == nil {
		t.Fatal("expected openPipelineView to return a fetch tea.Cmd")
	}
	msg := cmd()

	// Then: the service was called and the message carries the fetched pipelines
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

// TestUpdate_QuitKey_ReturnsTeaQuit: ctrl+c while searching still quits the program.
// Given a model with search active, when ctrl+c reaches the search key handler, then the returned command
// produces tea.QuitMsg.
// Why it matters: if search swallowed ctrl+c along with the other keys it captures, a user mid-search
// could not exit the app.
//
// tea.Cmd function values cannot be compared to tea.Quit directly, so the test executes the returned
// command and type-checks for the tea.QuitMsg it is documented to produce.
func TestUpdate_QuitKey_ReturnsTeaQuit(t *testing.T) {
	// Given: a model with search active
	m := newTestModel()
	m.search.active = true

	// When: ctrl+c reaches the search key handler
	_, cmd := m.handleProjectSearchKey(tea.KeyMsg{Type: tea.KeyCtrlC})

	// Then: the returned command produces tea.QuitMsg
	if cmd == nil {
		t.Fatal("expected ctrl+c in search state to return tea.Quit, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg from returned cmd, got %T", cmd())
	}
}
