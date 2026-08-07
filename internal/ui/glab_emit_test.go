package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/demo"
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

// TestGlabSelection: each focused panel projects its selected item into a glab selection, and ambiguous
// rows are refused.
// Given a model focused on each panel kind (project, pipeline, job, matrix child, MR, detail mirror) plus
// the refusal cases (bridge child, bridge header, matrix group header, empty list), when glabSelection
// runs, then the selection carries the owning project path, host, and identifiers exactly, and the refusal
// cases report ok=false.
// Why it matters: this is the one impure bridge between UI state and the pure command builder, so a wrong
// project path or ID here makes every emitted glab command target the wrong repository or run.
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

// TestGlabCommands: the focused selection resolves to its ordered glab commands and owning project.
// Given a focused pipeline, when glabCommands runs, then it returns ok with the owning project, all four
// commands, and the ID-precise get first, while an empty model resolves to not-ok.
// Why it matters: both the y yank and the Y overlay read this shared seam, so a reordered list silently
// changes which command y copies.
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

// TestYankGlabCommand: yanking copies the selection's default command and only toasts when nothing is
// emittable.
// Given a focused pipeline and an empty model, when each is yanked, then the pipeline yields a clipboard
// command while the empty model yields none and sets a status toast.
// Why it matters: returning a clipboard command with nothing selected would overwrite the user's clipboard
// with an empty or stale command.
func TestYankGlabCommand(t *testing.T) {
	// Given: a focused pipeline
	m := focusedPipelineModel()

	// When/Then: yanking it returns a clipboard command
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

// TestOpenGlabPreview: opening the overlay seeds it with the focused item's commands, or toasts when
// nothing is focused.
// Given a focused pipeline and an empty model, when the overlay opens on each, then the pipeline model
// activates it with all four commands, the cursor at the top, and the project recorded, while the empty
// model stays closed with a status toast.
// Why it matters: an overlay seeded from a stale selection would offer the user commands for a different
// item than the one on screen.
func TestOpenGlabPreview(t *testing.T) {
	// Given: a focused pipeline
	m := focusedPipelineModel()

	// When: the overlay opens (which must not return a command)
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

// TestGlabPreviewMoveClamps: overlay cursor movement clamps at both ends of the command list.
// Given an open overlay with three commands, when the cursor moves above the top and past the bottom,
// then it pins to 0 and to the last index.
// Why it matters: an unclamped cursor would index outside the command slice and panic the overlay render
// on the next frame.
func TestGlabPreviewMoveClamps(t *testing.T) {
	// Given: an open overlay with three commands
	m := Model{}
	m.glabPreview = glabPreviewState{active: true, commands: make([]glabcmd.Command, 3)}

	// When/Then: moving above the top clamps to 0
	m.glabPreviewMove(-1)
	if m.glabPreview.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped at top)", m.glabPreview.cursor)
	}

	// And: moving down twice lands on the last command
	m.glabPreviewMove(1)
	m.glabPreviewMove(1)
	if m.glabPreview.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.glabPreview.cursor)
	}

	// And: moving past the bottom stays clamped there
	m.glabPreviewMove(1)
	if m.glabPreview.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (clamped at bottom)", m.glabPreview.cursor)
	}
}

// TestHandleGlabPreviewKey: overlay keys navigate, copy, and dismiss.
// Given an open overlay with two commands, when esc, j, and enter are each pressed, then esc closes the
// overlay, j moves the cursor down, and enter closes it while returning a clipboard command.
// Why it matters: a dead esc traps the user in the overlay, and an enter that copies without closing makes
// every copy require a second dismissal.
func TestHandleGlabPreviewKey(t *testing.T) {
	// Given: a builder for an open overlay with two commands
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

	// When/Then: esc closes the overlay
	if got, _ := build().handleGlabPreviewKey(tea.KeyMsg{Type: tea.KeyEsc}); got.(Model).glabPreview.active {
		t.Error("esc should close the overlay")
	}

	// When/Then: j moves the cursor down
	if got, _ := build().handleGlabPreviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}); got.(Model).glabPreview.cursor != 1 {
		t.Errorf("j -> cursor %d, want 1", got.(Model).glabPreview.cursor)
	}

	// When: enter is pressed on the highlighted command
	got, cmd := build().handleGlabPreviewKey(tea.KeyMsg{Type: tea.KeyEnter})

	// Then: the overlay closes and a clipboard command comes back
	if got.(Model).glabPreview.active {
		t.Error("enter should close the overlay")
	}
	if cmd == nil {
		t.Error("enter should return a clipboard command")
	}
}

// Expected glab invocations for the demo selection: project acme-corp/api-gateway
// (ID 1001) has its newest pipeline as the running one, ID 1001003 on
// feature/add-metrics, so that is the pipeline the hotkeys read. No Host is
// configured, so -R stays the bare project path.
const (
	demoGlabGetCmd  = "glab ci get -p 1001003 -R acme-corp/api-gateway"
	demoGlabViewCmd = "glab ci view feature/add-metrics -R acme-corp/api-gateway"
)

// demoPipelinesPanelModel builds a NewModel over the demo service, sizes it,
// and drives the real key/message loop until the pipelines panel is focused
// and populated for the first demo project: seed the project list, enter the
// project, execute the fetch command, and apply the resulting load message.
func demoPipelinesPanelModel(t *testing.T) Model {
	t.Helper()
	svc := &demo.DemoService{}
	m := NewModel(context.Background(), svc, Options{})

	page, err := svc.ListProjects(context.Background(), gitlab.ProjectListOptions{Page: 1, PerPage: m.opts.ProjectsPerPage})
	if err != nil {
		t.Fatalf("demo ListProjects: %v", err)
	}
	m.loading = false
	m.projectTab = projectTabAll
	m.allProjects = page.Projects
	m.pagesReady = map[int]bool{1: true}
	m.invalidateVisibleCache()
	m.updateProjectList()

	res, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	m = res.(Model)

	res, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if m.focus.Active != PanelPipelines {
		t.Fatalf("focus after enter = %d, want PanelPipelines", m.focus.Active)
	}
	if cmd == nil {
		t.Fatal("expected a pipelines fetch command after enter")
	}
	loaded, ok := findMsg[pipelinesLoadedMsg](cmd)
	if !ok {
		t.Fatal("expected a pipelinesLoadedMsg from the fetch command")
	}
	res, _ = m.Update(loaded)
	m = res.(Model)
	if len(m.pipelineView.pipelines) == 0 {
		t.Fatal("no pipelines loaded from the demo service")
	}
	return m
}

// TestGlabPreviewOverlay_CopiesSelectedCommand: Y renders the command overlay for
// the selected pipeline and j+enter copies the highlighted command.
// Given a demo-backed model with the pipelines panel populated, when Y opens the
// overlay and j+enter picks the second command, then the rendered view lists the
// ID-precise get and ref view commands, the clipboard receives the ref view command
// verbatim, the status toasts it, and the overlay closes.
// Why it matters: a stale selection or cursor drift makes the user paste a glab
// command that acts on a different pipeline than the one on screen.
func TestGlabPreviewOverlay_CopiesSelectedCommand(t *testing.T) {
	// Given: the pipelines panel focused and populated from the demo service
	m := demoPipelinesPanelModel(t)

	// When: Y opens the command overlay
	res, _ := m.Update(keyMsg("Y"))
	m = res.(Model)

	// Then: the rendered view shows the overlay with the selection's commands
	view := m.View()
	for _, want := range []string{"glab commands · acme-corp/api-gateway", demoGlabGetCmd, demoGlabViewCmd} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay view missing %q in:\n%s", want, view)
		}
	}

	// When: j moves to the second command and enter copies it
	res, _ = m.Update(keyMsg("j"))
	m = res.(Model)
	capture := captureClipboard(t)
	res, copyCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if copyCmd == nil {
		t.Fatal("expected a clipboard command from enter")
	}
	res, _ = m.Update(copyCmd())
	m = res.(Model)

	// Then: the clipboard holds the second command verbatim and the status toasts it
	if capture.text != demoGlabViewCmd {
		t.Fatalf("copied text = %q, want %q", capture.text, demoGlabViewCmd)
	}
	if want := "Copied: " + demoGlabViewCmd; m.status != want {
		t.Fatalf("status = %q, want %q", m.status, want)
	}

	// And: the overlay is closed and gone from the rendered view
	if m.glabPreview.active {
		t.Fatal("expected the overlay to close after enter")
	}
	if after := m.View(); strings.Contains(after, "enter or y copy") {
		t.Fatal("expected the overlay hint to disappear from the view after enter")
	}
}

// TestYankGlabKey_CopiesDefaultCommand: y copies the selection's default glab
// command without opening the overlay.
// Given a demo-backed model with the pipelines panel populated, when y is pressed,
// then the clipboard receives the ID-precise get command verbatim and the status
// toasts it.
// Why it matters: the yank default is the only form guaranteed to target the
// selected run, so copying anything else silently points users at the wrong pipeline.
func TestYankGlabKey_CopiesDefaultCommand(t *testing.T) {
	// Given: the pipelines panel focused and populated from the demo service
	m := demoPipelinesPanelModel(t)
	capture := captureClipboard(t)

	// When: y yanks the default command and its result message is applied
	res, cmd := m.Update(keyMsg("y"))
	m = res.(Model)
	if cmd == nil {
		t.Fatal("expected a clipboard command from y")
	}
	res, _ = m.Update(cmd())
	m = res.(Model)

	// Then: the clipboard holds the ID-precise default verbatim and the status toasts it
	if capture.text != demoGlabGetCmd {
		t.Fatalf("copied text = %q, want %q", capture.text, demoGlabGetCmd)
	}
	if want := "Copied: " + demoGlabGetCmd; m.status != want {
		t.Fatalf("status = %q, want %q", m.status, want)
	}
}
