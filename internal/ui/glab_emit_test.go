package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
	"github.com/GF6599/lazylab/internal/glabcmd"
)

func focusedPipelineModel() Model {
	return Model{
		focus: FocusState{Active: PanelPipelines},
		pipelineView: pipelineViewState{
			project:   gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
			pipelines: []gitlab.PipelineSummary{{ID: 4242, Ref: "main"}},
			selected:  0,
		},
	}
}

// glabSelection projects the focused panel and its selected item into a
// glabcmd.Selection (or reports that nothing emittable is focused).
// Why it matters: this is the one impure bridge between UI state and the pure
// command builder, so the project path it pulls from the owning panel is what
// makes every emitted command target the right repository.
func TestGlabSelection(t *testing.T) {
	// Given: a focused panel with a selected item, for each panel and the refusal cases
	tests := []struct {
		name   string
		model  func() Model
		want   glabcmd.Selection
		wantOK bool
	}{
		{
			name: "focused project",
			model: func() Model {
				return Model{
					focus:       FocusState{Active: PanelProjects},
					allProjects: []gitlab.ProjectNode{{PathWithNamespace: "acme/widgets"}},
					opts:        Options{ProjectsPerPage: 10},
					pagesReady:  map[int]bool{1: true},
					page:        1,
					projectTab:  projectTabAll,
					selected:    0,
				}
			},
			want:   glabcmd.Selection{Kind: glabcmd.KindProject, ProjectPath: "acme/widgets"},
			wantOK: true,
		},
		{
			name:   "focused pipeline carries ref and id",
			model:  focusedPipelineModel,
			want:   glabcmd.Selection{Kind: glabcmd.KindPipeline, ProjectPath: "acme/widgets", Ref: "main", PipelineID: 4242},
			wantOK: true,
		},
		{
			name: "configured instance host tags the selection",
			model: func() Model {
				m := focusedPipelineModel()
				m.opts.Host = "https://gitlab.mycompany.com"
				return m
			},
			want: glabcmd.Selection{
				Kind: glabcmd.KindPipeline, Host: "https://gitlab.mycompany.com",
				ProjectPath: "acme/widgets", Ref: "main", PipelineID: 4242,
			},
			wantOK: true,
		},
		{
			name: "focused job carries job id",
			model: func() Model {
				return Model{
					focus: FocusState{Active: PanelStages},
					pipelineView: pipelineViewState{
						project:       gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						jobRows:       []gitlab.PipelineJob{{ID: 99821, Name: "lint", Stage: "test"}},
						stageSelected: 0,
					},
				}
			},
			want:   glabcmd.Selection{Kind: glabcmd.KindJob, ProjectPath: "acme/widgets", JobID: 99821},
			wantOK: true,
		},
		{
			name: "matrix child row emits its real job",
			model: func() Model {
				job := gitlab.PipelineJob{ID: 7101, Name: "lint: [go, v1.24]", Stage: "test"}
				return Model{
					focus: FocusState{Active: PanelStages},
					pipelineView: pipelineViewState{
						project:       gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						jobRows:       []gitlab.PipelineJob{job},
						stageJobRows:  []stageJobRow{{Kind: rowKindMatrixChild, Job: &job}},
						stageSelected: 0,
					},
				}
			},
			want:   glabcmd.Selection{Kind: glabcmd.KindJob, ProjectPath: "acme/widgets", JobID: 7101},
			wantOK: true,
		},
		{
			name: "focused merge request carries iid",
			model: func() Model {
				return Model{
					focus: FocusState{Active: PanelMRs},
					mrView: mrViewState{
						project:  gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						mrs:      []gitlab.MergeRequestSummary{{IID: 42, SourceBranch: "feat"}},
						selected: 0,
					},
				}
			},
			want:   glabcmd.Selection{Kind: glabcmd.KindMergeRequest, ProjectPath: "acme/widgets", MRIID: 42},
			wantOK: true,
		},
		{
			name: "detail pane mirrors the panel that opened it",
			model: func() Model {
				return Model{
					focus: FocusState{Active: PanelDetail, PrevActive: PanelMRs},
					mrView: mrViewState{
						project:  gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						mrs:      []gitlab.MergeRequestSummary{{IID: 42}},
						selected: 0,
					},
				}
			},
			want:   glabcmd.Selection{Kind: glabcmd.KindMergeRequest, ProjectPath: "acme/widgets", MRIID: 42},
			wantOK: true,
		},
		{
			name: "bridge child job is refused (no project path for -R)",
			model: func() Model {
				job := gitlab.PipelineJob{ID: 7, Name: "downstream"}
				return Model{
					focus: FocusState{Active: PanelStages},
					pipelineView: pipelineViewState{
						project:       gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						jobRows:       []gitlab.PipelineJob{job},
						stageJobRows:  []stageJobRow{{Kind: rowKindBridgeChild, Job: &job, ChildProjectID: 555}},
						stageSelected: 0,
					},
				}
			},
			want:   glabcmd.Selection{},
			wantOK: false,
		},
		{
			name: "bridge header row is refused (bridge ID is not a job ID)",
			model: func() Model {
				bridge := gitlab.PipelineBridge{ID: 314, Name: "trigger-downstream", Stage: "deploy"}
				return Model{
					focus: FocusState{Active: PanelStages},
					pipelineView: pipelineViewState{
						project:       gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						jobRows:       []gitlab.PipelineJob{{ID: 314, Name: "trigger-downstream"}},
						stageJobRows:  []stageJobRow{{Kind: rowKindBridge, Bridge: &bridge}},
						stageSelected: 0,
					},
				}
			},
			want:   glabcmd.Selection{},
			wantOK: false,
		},
		{
			name: "matrix group header is refused (aggregates several jobs)",
			model: func() Model {
				jobs := []gitlab.PipelineJob{{ID: 1, Name: "lint: [go, v1.24]"}, {ID: 2, Name: "lint: [go, v1.25]"}}
				return Model{
					focus: FocusState{Active: PanelStages},
					pipelineView: pipelineViewState{
						project:       gitlab.ProjectNode{PathWithNamespace: "acme/widgets"},
						jobRows:       []gitlab.PipelineJob{jobs[0]},
						stageJobRows:  []stageJobRow{{Kind: rowKindMatrixGroup, Jobs: jobs}},
						stageSelected: 0,
					},
				}
			},
			want:   glabcmd.Selection{},
			wantOK: false,
		},
		{
			name: "empty pipeline list yields nothing",
			model: func() Model {
				return Model{focus: FocusState{Active: PanelPipelines}}
			},
			want:   glabcmd.Selection{},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: we derive a glab selection from the focused state
			got, ok := tt.model().glabSelection()

			// Then: both the selection and its presence flag match expectations
			if ok != tt.wantOK {
				t.Fatalf("glabSelection() ok = %v, want %v (selection %+v)", ok, tt.wantOK, got)
			}
			if got != tt.want {
				t.Errorf("glabSelection() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// glabCommands is the shared seam both hotkeys read: it resolves the focused
// selection into its ordered glab commands plus the owning project.
func TestGlabCommands(t *testing.T) {
	// Given: a focused pipeline
	m := focusedPipelineModel()

	// When: we resolve its glab commands
	cmds, project, ok := m.glabCommands()

	// Then: the full ordered list and the owning project come back
	if !ok {
		t.Fatal("glabCommands() ok = false, want true for a focused pipeline")
	}
	if project != "acme/widgets" {
		t.Errorf("project = %q, want acme/widgets", project)
	}
	if len(cmds) != 4 {
		t.Fatalf("got %d commands, want 4", len(cmds))
	}
	if cmds[0].Cmd != "glab ci get -p 4242 -R acme/widgets" {
		t.Errorf("cmds[0].Cmd = %q, want the ID-precise get as the yank default", cmds[0].Cmd)
	}

	// And: nothing selected resolves to not-ok
	empty := Model{focus: FocusState{Active: PanelPipelines}}
	if _, _, ok := empty.glabCommands(); ok {
		t.Error("glabCommands() ok = true, want false when nothing is selected")
	}
}

// yankGlabCommand copies the default (first) command and toasts; with nothing
// emittable it sets a status instead of returning a clipboard command.
func TestYankGlabCommand(t *testing.T) {
	// Given/When: a focused pipeline is yanked
	m := focusedPipelineModel()
	if cmd := m.yankGlabCommand(); cmd == nil {
		t.Error("yankGlabCommand() = nil, want a clipboard command for a focused pipeline")
	}

	// And: yanking with nothing focused toasts and returns no command
	empty := Model{focus: FocusState{Active: PanelPipelines}}
	if cmd := empty.yankGlabCommand(); cmd != nil {
		t.Error("yankGlabCommand() returned a command when nothing is selected")
	}
	if empty.status == "" {
		t.Error("expected a status toast when nothing is selected")
	}
}

// openGlabPreview populates the overlay with the focused item's commands.
func TestOpenGlabPreview(t *testing.T) {
	// Given/When: the overlay is opened on a focused pipeline
	m := focusedPipelineModel()
	if cmd := m.openGlabPreview(); cmd != nil {
		t.Error("openGlabPreview() should not return a command")
	}

	// Then: it is active, seeded with all commands, cursor at top, project recorded
	if !m.glabPreview.active {
		t.Fatal("expected overlay active")
	}
	if len(m.glabPreview.commands) != 4 {
		t.Fatalf("got %d commands, want 4", len(m.glabPreview.commands))
	}
	if m.glabPreview.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.glabPreview.cursor)
	}
	if m.glabPreview.project != "acme/widgets" {
		t.Errorf("project = %q, want acme/widgets", m.glabPreview.project)
	}

	// And: opening with nothing focused leaves it closed and toasts
	empty := Model{focus: FocusState{Active: PanelPipelines}}
	empty.openGlabPreview()
	if empty.glabPreview.active {
		t.Error("expected overlay to stay closed when nothing is selected")
	}
	if empty.status == "" {
		t.Error("expected a status toast when nothing is selected")
	}
}

// glabPreviewMove keeps the cursor within bounds.
func TestGlabPreviewMoveClamps(t *testing.T) {
	m := Model{}
	m.glabPreview = glabPreviewState{active: true, commands: make([]glabcmd.Command, 3)}

	m.glabPreviewMove(-1)
	if m.glabPreview.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped at top)", m.glabPreview.cursor)
	}
	m.glabPreviewMove(1)
	m.glabPreviewMove(1)
	if m.glabPreview.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.glabPreview.cursor)
	}
	m.glabPreviewMove(1)
	if m.glabPreview.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (clamped at bottom)", m.glabPreview.cursor)
	}
}

// handleGlabPreviewKey routes navigation, copy, and dismissal within the overlay.
func TestHandleGlabPreviewKey(t *testing.T) {
	build := func() Model {
		m := Model{}
		m.glabPreview = glabPreviewState{
			active:  true,
			project: "acme/widgets",
			commands: []glabcmd.Command{
				{Label: "Get pipeline by ID", Cmd: "glab ci get -p 4242 -R acme/widgets"},
				{Label: "View latest pipeline on ref", Cmd: "glab ci view main -R acme/widgets"},
			},
		}
		return m
	}

	// esc closes the overlay
	if got, _ := build().handleGlabPreviewKey(tea.KeyMsg{Type: tea.KeyEsc}); got.(Model).glabPreview.active {
		t.Error("esc should close the overlay")
	}

	// j moves the cursor down
	if got, _ := build().handleGlabPreviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}); got.(Model).glabPreview.cursor != 1 {
		t.Errorf("j -> cursor %d, want 1", got.(Model).glabPreview.cursor)
	}

	// enter copies the highlighted command and closes
	got, cmd := build().handleGlabPreviewKey(tea.KeyMsg{Type: tea.KeyEnter})
	if got.(Model).glabPreview.active {
		t.Error("enter should close the overlay")
	}
	if cmd == nil {
		t.Error("enter should return a clipboard command")
	}
}
