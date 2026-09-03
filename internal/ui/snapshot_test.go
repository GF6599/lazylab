package ui

import (
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/golden"
)

// newSnapshotModel builds a Model with richer stub data for snapshot testing.
// It extends newMultiPanelModel with configurable dimensions and additional state.
func newSnapshotModel(active PanelID, width, height int) Model {
	m := newMultiPanelModel(active)
	m.width = width
	m.height = height
	m.status = "Ready"

	// Add pipeline status for first project so info bar and detail pane render content
	m.pipelineStatus.Set(1, pipelineState{
		hasInfo: true,
		info:    gitlab.PipelineSummary{ID: 100, Ref: "main", Status: "success", WebURL: "https://gitlab.com/team/alpha/-/pipelines/100"},
	})

	// Initialize pipeline list with proper delegate (required for SetSize)
	pipelineItems := []list.Item{
		pipelineItem{summary: gitlab.PipelineSummary{ID: 10, Ref: "main", Status: "success"}},
	}
	m.pipelineView.pipelineList = newBareList(pipelineItems, pipelineDelegate{}, 40, 10)

	// Populate MR view with stub data
	m.mrView = mrViewState{
		project: m.allProjects[0],
		mrs: []gitlab.MergeRequestSummary{
			{IID: 42, Title: "Add dark mode support", State: "opened", Author: "alice", SourceBranch: "feature/dark-mode", TargetBranch: "main"},
			{IID: 41, Title: "Fix login redirect", State: "merged", Author: "bob", SourceBranch: "fix/login", TargetBranch: "main"},
		},
		selected:   0,
		mrViewport: viewport.New(60, 20),
	}

	return m
}

// TestSnapshot_MultiPanel_Default: the full multi-panel frame at 120x40 matches its golden file.
// Given the stub snapshot model with the projects panel focused, when the multi-panel view renders at
// 120x40, then the output equals the stored golden snapshot.
// Why it matters: the frame is assembled from width math across four panes, and a one-cell drift tears
// the borders of every panel on screen.
func TestSnapshot_MultiPanel_Default(t *testing.T) {
	// Given: the stub model at 120x40 with projects focused
	m := newSnapshotModel(PanelProjects, 120, 40)

	// When/Then: the rendered frame matches the golden file
	output := renderMultiPanelView(m, m.width, m.height)
	golden.RequireEqual(t, output)
}

// TestSnapshot_MultiPanel_Small: the multi-panel frame at a classic 80x24 terminal matches its golden file.
// Given the stub snapshot model with the projects panel focused, when the multi-panel view renders at
// 80x24, then the output equals the stored golden snapshot.
// Why it matters: 80x24 is the tightest common terminal, where rounding errors in the pane split surface
// first.
func TestSnapshot_MultiPanel_Small(t *testing.T) {
	// Given: the stub model at 80x24 with projects focused
	m := newSnapshotModel(PanelProjects, 80, 24)

	// When/Then: the rendered frame matches the golden file
	output := renderMultiPanelView(m, m.width, m.height)
	golden.RequireEqual(t, output)
}

// TestSnapshot_MultiPanel_PipelinesFocused: focusing the pipelines panel renders the expected frame.
// Given the stub snapshot model with the pipelines panel focused, when the multi-panel view renders at
// 120x40, then the output equals the stored golden snapshot.
// Why it matters: focus drives the accordion heights and border styling, so a regression here misdraws
// the frame every time the user moves between panels.
func TestSnapshot_MultiPanel_PipelinesFocused(t *testing.T) {
	// Given: the stub model with the pipelines panel focused
	m := newSnapshotModel(PanelPipelines, 120, 40)

	// When/Then: the rendered frame matches the golden file
	output := renderMultiPanelView(m, m.width, m.height)
	golden.RequireEqual(t, output)
}

// TestSnapshot_MultiPanel_DetailFocused: focusing the detail pane renders the expected frame.
// Given the stub snapshot model focused on the detail pane with projects as the previous panel, when the
// multi-panel view renders at 120x40, then the output equals the stored golden snapshot.
// Why it matters: detail focus renders from PrevActive, and a regression would show detail content for a
// panel the user never came from.
func TestSnapshot_MultiPanel_DetailFocused(t *testing.T) {
	// Given: the stub model focused on the detail pane, arrived at from projects
	m := newSnapshotModel(PanelDetail, 120, 40)
	m.focus.PrevActive = PanelProjects

	// When/Then: the rendered frame matches the golden file
	output := renderMultiPanelView(m, m.width, m.height)
	golden.RequireEqual(t, output)
}

// TestSnapshot_MultiPanel_TooSmall: an undersized terminal renders the too-small notice frame.
// Given the stub snapshot model at 30x10, when the multi-panel view renders, then the output equals the
// stored golden snapshot of the too-small message.
// Why it matters: below the layout minimums the app must degrade to a readable notice instead of
// emitting a mangled half-frame.
func TestSnapshot_MultiPanel_TooSmall(t *testing.T) {
	// Given: the stub model far below the minimum terminal size
	m := newSnapshotModel(PanelProjects, 30, 10)

	// When/Then: the rendered frame matches the golden file
	output := renderMultiPanelView(m, m.width, m.height)
	golden.RequireEqual(t, output)
}

// TestSnapshot_BorderedPane_Focused: a focused pane with title, tabs, and footer renders its golden frame.
// Given three content lines with a title, two tabs, and a footer, when a focused bordered pane renders at
// 40x5, then the output equals the stored golden snapshot.
// Why it matters: every panel on screen goes through this renderer, so a drift in how the border embeds
// title, tabs, or footer misdraws all of them at once.
func TestSnapshot_BorderedPane_Focused(t *testing.T) {
	// Given: pane content with a title, tabs, and a footer
	content := "Line 1\nLine 2\nLine 3"
	tabs := []string{"Tab A", "Tab B"}

	// When/Then: the focused pane matches the golden file
	output := renderBorderedPane(content, 40, 5, true, "My Panel", tabs, 0, "1 of 3")
	golden.RequireEqual(t, output)
}

// TestSnapshot_BorderedPane_Unfocused: an unfocused pane renders its golden frame.
// Given plain content with a title and no tabs or footer, when an unfocused bordered pane renders at
// 40x5, then the output equals the stored golden snapshot.
// Why it matters: unfocused panes are most of every frame, and a styling drift there dominates what the
// user sees.
func TestSnapshot_BorderedPane_Unfocused(t *testing.T) {
	// Given: plain pane content with only a title
	content := "Some content here"

	// When/Then: the unfocused pane matches the golden file
	output := renderBorderedPane(content, 40, 5, false, "Unfocused Panel", nil, 0, "")
	golden.RequireEqual(t, output)
}

// TestSnapshot_ProjectsPanel_Empty: a project list with no projects renders its empty-state golden frame.
// Given the stub model stripped of all projects, when the projects panel content renders at 40x10, then
// the output equals the stored golden snapshot.
// Why it matters: the empty state is the first thing users see before the initial fetch lands, and it
// must read as intentional rather than broken.
func TestSnapshot_ProjectsPanel_Empty(t *testing.T) {
	// Given: the stub model with the project list emptied
	m := newSnapshotModel(PanelProjects, 120, 40)
	m.allProjects = nil
	m.projectList.SetItems(nil)
	m.invalidateVisibleCache()

	// When/Then: the empty projects panel matches the golden file
	output := renderProjectsPanelContent(m, 40, 10)
	golden.RequireEqual(t, output)
}

// TestSnapshot_ProjectsPanel_FavoritesEmpty: the favorites tab with no favorites renders its hint frame.
// Given the stub model on the favorites tab with an empty favorites set, when the projects panel content
// renders at 40x10, then the output equals the stored golden snapshot.
// Why it matters: an empty favorites tab must explain itself, or users assume their favorites were lost.
func TestSnapshot_ProjectsPanel_FavoritesEmpty(t *testing.T) {
	// Given: the stub model on the favorites tab with nothing favorited
	m := newSnapshotModel(PanelProjects, 120, 40)
	m.projectTab = projectTabFavorites
	m.favorites = make(map[int]bool) // no favorites set
	m.invalidateVisibleCache()

	// When/Then: the empty favorites panel matches the golden file
	output := renderProjectsPanelContent(m, 40, 10)
	golden.RequireEqual(t, output)
}

// TestSnapshot_PipelinesPanel_NoProject: the pipelines panel without a project renders its hint frame.
// Given the stub model with the pipeline view's project cleared, when the pipelines panel content renders
// at 40x10, then the output equals the stored golden snapshot.
// Why it matters: before any project is entered the panel must prompt for a selection instead of
// rendering blank.
func TestSnapshot_PipelinesPanel_NoProject(t *testing.T) {
	// Given: the stub model with no project selected for pipelines
	m := newSnapshotModel(PanelPipelines, 120, 40)
	m.pipelineView.project = gitlab.ProjectNode{}

	// When/Then: the no-project pipelines panel matches the golden file
	output := renderPipelinesPanelContent(m, 40, 10)
	golden.RequireEqual(t, output)
}

// TestSnapshot_PipelinesPanel_Loading: the pipelines panel mid-fetch renders its loading frame.
// Given the stub model with pipeline loading set and no pipelines, when the pipelines panel content
// renders at 40x10, then the output equals the stored golden snapshot.
// Why it matters: without the loading hint a slow fetch is indistinguishable from a project that has no
// pipelines.
func TestSnapshot_PipelinesPanel_Loading(t *testing.T) {
	// Given: the stub model with a pipelines fetch in flight
	m := newSnapshotModel(PanelPipelines, 120, 40)
	m.pipelineView.loading = true
	m.pipelineView.pipelines = nil

	// When/Then: the loading pipelines panel matches the golden file
	output := renderPipelinesPanelContent(m, 40, 10)
	golden.RequireEqual(t, output)
}

// TestSnapshot_StagesPanel_NoSelection: the stages panel without a selected pipeline renders its hint frame.
// Given the stub model with the pipeline list emptied, when the stages panel content renders at 40x10,
// then the output equals the stored golden snapshot.
// Why it matters: the stages panel depends on a pipeline selection, and without the hint an empty table
// looks like a rendering bug.
func TestSnapshot_StagesPanel_NoSelection(t *testing.T) {
	// Given: the stub model with no pipelines to select from
	m := newSnapshotModel(PanelStages, 120, 40)
	m.pipelineView.pipelines = nil

	// When/Then: the no-selection stages panel matches the golden file
	output := renderStagesPanelContent(m, 40, 10)
	golden.RequireEqual(t, output)
}

// TestSnapshot_MRsPanel_Empty: the merge requests panel with no MRs renders its empty-state frame.
// Given the stub model with the MR list emptied, when the MRs panel renders at 40x10, then the output
// equals the stored golden snapshot.
// Why it matters: projects without open MRs are common, and the panel must say so rather than render
// nothing.
func TestSnapshot_MRsPanel_Empty(t *testing.T) {
	// Given: the stub model with no merge requests
	m := newSnapshotModel(PanelMRs, 120, 40)
	m.mrView.mrs = nil

	// When/Then: the empty MRs panel matches the golden file
	output := renderMRsPanel(&m, 40, 10)
	golden.RequireEqual(t, output)
}

// TestSnapshot_InfoBar: the bottom info bar renders its golden frame.
// Given the stub model with a cached pipeline status and a "Ready" status message, when the info bar
// renders at width 120, then the output equals the stored golden snapshot.
// Why it matters: the info bar carries the status toasts and selection summary, and a drift here garbles
// the one line users read constantly.
func TestSnapshot_InfoBar(t *testing.T) {
	// Given: the stub model with status and pipeline info populated
	m := newSnapshotModel(PanelProjects, 120, 40)

	// When/Then: the info bar matches the golden file
	output := renderInfoBar(&m, 120)
	golden.RequireEqual(t, output)
}

// TestSnapshot_View_TooSmall: the top-level View falls back to the too-small notice on tiny terminals.
// Given the stub model at 40x8, when View renders, then the output equals the stored golden snapshot of
// the too-small message.
// Why it matters: View is the entry point Bubble Tea actually calls, so the fallback must trigger there
// and not only in the panel-level renderers.
func TestSnapshot_View_TooSmall(t *testing.T) {
	// Given: the stub model below the minimum terminal size
	m := newSnapshotModel(PanelProjects, 40, 8)

	// When/Then: the full View output matches the golden file
	output := m.View()
	golden.RequireEqual(t, output)
}

// TestUpdate_TooSmall_BlocksKeys: below the minimum terminal size only the quit keys still work.
// Given a model at 40x8, when normal keys and then q and ctrl+c are pressed, then the normal keys produce
// no command and no mode change while q and ctrl+c still return a quit command.
// Why it matters: keys acting on panes that are not rendered would mutate invisible state, but blocking
// quit as well would trap the user inside an unusable window.
func TestUpdate_TooSmall_BlocksKeys(t *testing.T) {
	// Given: a model below the minimum terminal size
	m := newSnapshotModel(PanelProjects, 40, 8)

	// When/Then: normal keys are swallowed without commands or mode changes
	for _, k := range []string{"j", "k", "enter", "/", "r"} {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		got := updated.(Model)
		if cmd != nil {
			t.Fatalf("key %q produced a command when terminal too small", k)
		}
		if got.mode != m.mode {
			t.Fatalf("key %q changed mode when terminal too small", k)
		}
	}

	// And: q still quits
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q did not produce a quit command")
	}

	// And: ctrl+c still quits
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c did not produce a quit command")
	}
}

// snapshotTriggerModel builds a snapshot model whose pipeline view carries the
// project and selection both trigger modals read when they open.
func snapshotTriggerModel(width, height int) Model {
	m := newSnapshotModel(PanelPipelines, width, height)
	m.pipelineView.project = m.allProjects[0]
	m.pipelineView.pipelines = []gitlab.PipelineSummary{
		{ID: 10, Ref: "main", Status: "manual"},
	}
	return m
}

// TestSnapshot_PlayJobModal: the play-job form with two variables matches its golden file.
// Given the trigger snapshot model and a play-job modal holding a filled and an empty row, when the
// modal renders at 120 columns, then the output equals the stored golden snapshot.
// Why it matters: the key and value inputs are sized from a width split, so a drift overlaps the two
// columns and the user edits a field they cannot fully read.
func TestSnapshot_PlayJobModal(t *testing.T) {
	// Given: the play-job modal over a manual job, one variable filled
	m := snapshotTriggerModel(120, 40)
	m.pipelineView.playJob = playJobState{
		active:  true,
		jobID:   901,
		jobName: "deploy:production",
		vars:    variablesFormWith([2]string{"DEPLOY_ENV", "production"}, [2]string{"", ""}),
	}

	// When/Then: the rendered modal matches the golden file
	golden.RequireEqual(t, []byte(renderPlayJobModal(m, m.width)))
}

// TestSnapshot_RunPipelineModal: the run-pipeline form matches its golden file.
// Given the trigger snapshot model and a run-pipeline modal holding a ref and one variable, when the
// modal renders at 120 columns, then the output equals the stored golden snapshot.
// Why it matters: the ref field and the variables table share the modal width, so a regression in
// either pushes the other past the border.
func TestSnapshot_RunPipelineModal(t *testing.T) {
	// Given: the run-pipeline modal with a ref and one variable
	m := snapshotTriggerModel(120, 40)
	ref := newModalTextinput("Branch or tag (required)")
	ref.SetValue("release/2.0")
	m.pipelineView.runPipeline = runPipelineState{
		active: true,
		ref:    ref,
		vars:   variablesFormWith([2]string{"DRY_RUN", "true"}, [2]string{"", ""}),
	}

	// When/Then: the rendered modal matches the golden file
	golden.RequireEqual(t, []byte(renderRunPipelineModal(m, m.width)))
}

// TestSnapshot_RunPipelineModal_Small: the run-pipeline form at a classic 80x24 terminal matches its golden file.
// Given the trigger snapshot model at 80 columns and a run-pipeline modal holding a ref and one
// variable, when the modal renders, then the output equals the stored golden snapshot.
// Why it matters: 80 columns is the tightest common terminal, and it is where the split between the
// key and value inputs runs out of room first, wrapping a row and tearing the modal border.
func TestSnapshot_RunPipelineModal_Small(t *testing.T) {
	// Given: the run-pipeline modal at 80 columns
	m := snapshotTriggerModel(80, 24)
	ref := newModalTextinput("Branch or tag (required)")
	ref.SetValue("release/2.0")
	m.pipelineView.runPipeline = runPipelineState{
		active: true,
		ref:    ref,
		vars:   variablesFormWith([2]string{"DRY_RUN", "true"}, [2]string{"", ""}),
	}

	// When/Then: the rendered modal matches the golden file
	golden.RequireEqual(t, []byte(renderRunPipelineModal(m, m.width)))
}
