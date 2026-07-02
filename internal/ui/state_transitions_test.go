package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// newTestModel builds a minimal Model suitable for state transition testing.
// It uses the existing mockService and sets up enough state for transitions
// to work without panicking.
func newTestModel() Model {
	projects := []gitlab.ProjectNode{
		{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha", DefaultBranch: "main"},
		{ID: 2, Name: "beta", PathWithNamespace: "team/beta", DefaultBranch: "develop"},
		{ID: 3, Name: "gamma", PathWithNamespace: "team/gamma"},
	}
	pipelineStatus := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	favorites := make(map[int]bool)
	delegate := projectDelegate{pipelineStatus: &pipelineStatus, favorites: favorites}
	items := make([]list.Item, len(projects))
	for i, p := range projects {
		items[i] = projectItem{project: p}
	}
	pl := newBareList(items, delegate, 40, 20)

	ti := textinput.New()
	ti.Placeholder = "Search projects"
	ti.CharLimit = 128
	ti.Prompt = "/ "
	ti.Blur()

	return Model{
		ctx:            context.Background(),
		client:         &mockService{},
		mode:           modeMultiPanel,
		width:          120,
		height:         40,
		allProjects:    projects,
		opts:           Options{ProjectsPerPage: 10},
		pagesReady:     map[int]bool{1: true},
		page:           1,
		projectTab:     projectTabAll,
		favorites:      favorites,
		pipelineStatus: &pipelineStatus,
		projectList:    pl,
		focus:          FocusState{Active: PanelProjects},
		search:         searchState{input: ti},
		pipelineView: pipelineViewState{
			stages:  NewAsyncCache[int, []gitlab.PipelineStage](),
			jobs:    NewAsyncCache[int, []gitlab.PipelineJob](),
			logs:    NewAsyncCache[int, string](),
			bridges: NewAsyncCache[int, []gitlab.PipelineBridge](),
		},
		commitCache: NewAsyncCache[int, []gitlab.CommitSummary](),
	}
}

// --- openExplorer tests ---

// TestOpenExplorer_InitializesRootAndPreview: opening the explorer starts a root fetch with a renderable preview.
// Given a sized multi-panel model, when the explorer opens for a project, then a tree fetch is queued,
// the stack holds a single loading root directory, the preview viewport has usable dimensions, and the
// status line names the project.
// Why it matters: a missing fetch or a zero-sized preview viewport leaves the file browser permanently blank.
func TestOpenExplorer_InitializesRootAndPreview(t *testing.T) {
	// Given: a multi-panel model sized for rendering
	m := newTestModel()
	project := m.allProjects[0]

	// When: the explorer opens for the project
	result, cmd := m.openExplorer(project)
	got := result.(Model)

	// Then: a root tree fetch is queued
	if cmd == nil {
		t.Fatal("expected a fetch tree command")
	}

	// And: the navigation stack starts at a single loading root directory
	if len(got.explorer.stack) != 1 {
		t.Fatalf("expected stack length 1 (root), got %d", len(got.explorer.stack))
	}
	root := got.explorer.stack[0]
	if root.path != "" || !root.loading {
		t.Fatalf("expected loading root dir with empty path, got path=%q loading=%v", root.path, root.loading)
	}

	// And: the preview viewport is sized so previews can render
	vp := got.explorer.preview.viewport
	if vp.Width == 0 || vp.Height == 0 {
		t.Fatalf("expected non-zero preview viewport, got %dx%d", vp.Width, vp.Height)
	}

	// And: the status line names the project being browsed
	if !strings.Contains(got.status, project.PathWithNamespace) {
		t.Fatalf("expected status to name %s, got %q", project.PathWithNamespace, got.status)
	}
}

// --- openPipelineView tests ---

// TestOpenPipelineView_InitializesFreshView: opening the pipeline view yields a loading pane wired to fetch.
// Given a multi-panel model, when the pipeline view opens for a project, then a pipelines fetch is
// queued, the pipelines pane renders the loading hint, focus starts on the pipelines list, and paging
// and log auto-follow start at their defaults.
// Why it matters: stale paging or a missing fetch would show another project's pipelines or a blank pane.
func TestOpenPipelineView_InitializesFreshView(t *testing.T) {
	// Given: a multi-panel model with a project to inspect
	m := newTestModel()
	project := m.allProjects[0]

	// When: the pipeline view opens for the project
	result, cmd := m.openPipelineView(project)
	got := result.(Model)

	// Then: a pipelines fetch is queued and the pane renders the loading hint
	if cmd == nil {
		t.Fatal("expected a fetch pipelines command")
	}
	pane := renderPipelinesPanelContent(got, 50, 20)
	if !strings.Contains(pane, "Loading pipelines") {
		t.Fatalf("expected loading hint in pipelines pane, got %q", pane)
	}

	// And: focus starts on the pipelines list
	if got.pipelineView.focus != pipelineFocusPipelines {
		t.Fatalf("expected focus=pipelineFocusPipelines, got %d", got.pipelineView.focus)
	}

	// And: paging starts at its defaults (no render equivalent)
	pv := got.pipelineView
	if pv.page != 1 || pv.totalPages != 1 || pv.perPage != pipelinePerPage {
		t.Fatalf("expected page=1 totalPages=1 perPage=%d, got page=%d totalPages=%d perPage=%d",
			pipelinePerPage, pv.page, pv.totalPages, pv.perPage)
	}

	// And: log auto-follow is enabled for the fresh view (no render equivalent)
	if !pv.logAutoFollow {
		t.Fatal("expected logAutoFollow=true")
	}

	// And: the status line names the project
	if !strings.Contains(got.status, project.PathWithNamespace) {
		t.Fatalf("expected status to name %s, got %q", project.PathWithNamespace, got.status)
	}
}

// --- closePipelineView tests ---

// TestClosePipelineView_ReturnsToProjects: closing the pipeline view lands back in the project list mode.
// Given an open pipeline view, when it closes, then the mode is modeProjects.
// Why it matters: landing in the wrong mode routes the next keypress through a handler for a view that is
// no longer on screen.
func TestClosePipelineView_ReturnsToProjects(t *testing.T) {
	// Given: an open pipeline view
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	m = result.(Model)

	// When: the view closes
	m.closePipelineView()

	// Then: the mode is back on the project list
	if m.mode != modeProjects {
		t.Fatalf("expected mode=modeProjects after close, got %d", m.mode)
	}
}

// TestClosePipelineView_ClearsState: closing the pipeline view resets its project and loading state.
// Given an open pipeline view, when it closes, then the view's project is cleared and loading is off.
// Why it matters: a leftover project ID would flash the previous project's pipelines on the next open
// before the fresh fetch lands.
func TestClosePipelineView_ClearsState(t *testing.T) {
	// Given: an open pipeline view
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	m = result.(Model)

	// When: the view closes
	m.closePipelineView()

	// Then: the project and loading state are cleared
	if m.pipelineView.project.ID != 0 {
		t.Fatal("expected pipelineView project to be cleared")
	}
	if m.pipelineView.loading {
		t.Fatal("expected loading=false after close")
	}
}

// --- closeExplorer tests ---

// TestCloseExplorer_ReturnsToProjects: closing the standalone explorer returns to the project list mode.
// Given the explorer open in legacy modeExplorer, when it closes, then the mode is modeProjects.
// Why it matters: staying in modeExplorer would leave the explorer key handler and message guards active
// for a view that is no longer rendered.
func TestCloseExplorer_ReturnsToProjects(t *testing.T) {
	// Given: the explorer open in legacy modeExplorer
	m := newTestModel()
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	// When: the explorer closes
	m = m.closeExplorer("Back")

	// Then: the mode is back on the project list
	if m.mode != modeProjects {
		t.Fatalf("expected mode=modeProjects after close, got %d", m.mode)
	}
}

// TestCloseExplorer_ClearsState: closing the explorer clears its project and navigation stack.
// Given an open explorer with a root stack frame, when it closes, then the project and stack are emptied.
// Why it matters: a surviving stack would reopen the next project's explorer inside the previous project's
// directory tree.
func TestCloseExplorer_ClearsState(t *testing.T) {
	// Given: an open explorer with a root stack frame
	m := newTestModel()
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	// When: the explorer closes
	m = m.closeExplorer("Back to projects")

	// Then: the project and navigation stack are cleared
	if m.explorer.project.ID != 0 {
		t.Fatal("expected explorer project to be cleared")
	}
	if len(m.explorer.stack) != 0 {
		t.Fatal("expected explorer stack to be cleared")
	}
}

// TestCloseExplorer_SetsStatus: closing the explorer with a message puts it on the status line.
// Given an open explorer, when it closes with "Back to projects", then the status shows that text.
// Why it matters: the status line is the only confirmation of where a dismissal left the user.
func TestCloseExplorer_SetsStatus(t *testing.T) {
	// Given: an open explorer
	m := newTestModel()
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	// When: the explorer closes with a status message
	m = m.closeExplorer("Back to projects")

	// Then: the message lands on the status line
	if m.status != "Back to projects" {
		t.Fatalf("expected status 'Back to projects', got %q", m.status)
	}
}

// TestCloseExplorer_EmptyStatusPreservesExisting: closing with an empty message keeps the current status.
// Given a status already on screen, when the explorer closes with "", then the previous status survives.
// Why it matters: blanking the status on every close would erase toasts, such as copy confirmations, the
// user has not read yet.
func TestCloseExplorer_EmptyStatusPreservesExisting(t *testing.T) {
	// Given: an open explorer with a status already showing
	m := newTestModel()
	m.mode = modeExplorer
	m.status = "Previous status"
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	// When: the explorer closes with an empty message
	m = m.closeExplorer("")

	// Then: the existing status is preserved
	if m.status != "Previous status" {
		t.Fatalf("expected preserved status, got %q", m.status)
	}
}

// TestCloseExplorer_FromMultiPanel_StaysInMultiPanel: closing the explorer overlay keeps multi-panel mode.
// Given the explorer opened as an overlay over modeMultiPanel, when it closes, then the mode is still
// modeMultiPanel.
// Why it matters: dropping into the legacy standalone mode would swap the whole four-pane layout out from
// under the user just for having peeked at a file.
func TestCloseExplorer_FromMultiPanel_StaysInMultiPanel(t *testing.T) {
	// Given: the explorer overlay open over the multi-panel mode
	m := newTestModel()
	m.mode = modeMultiPanel
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	// When: the explorer closes
	m = m.closeExplorer("Done")

	// Then: the multi-panel mode survives
	if m.mode != modeMultiPanel {
		t.Fatalf("expected mode=modeMultiPanel when closing from multi-panel, got %d", m.mode)
	}
}

// --- ensureSelectionBounds tests ---

// TestEnsureSelectionBounds_ClampsToVisibleRange: out-of-range selections clamp to the visible project range.
// Given a model with a known project count and a selection index, when ensureSelectionBounds runs, then
// the selection lands inside [0, count-1] and already-valid selections stay untouched.
// Why it matters: an out-of-bounds index panics or highlights the wrong project after paging, search, or reload.
func TestEnsureSelectionBounds_ClampsToVisibleRange(t *testing.T) {
	// Given: selection indexes against a project list trimmed to projectCount (newTestModel has 3)
	tests := []struct {
		name         string
		selected     int
		projectCount int
		want         int
	}{
		{"far above clamps to last", 100, 3, 2},
		{"one past the end clamps to last", 3, 3, 2},
		{"last index unchanged", 2, 3, 2},
		{"middle index unchanged", 1, 3, 1},
		{"zero unchanged", 0, 3, 0},
		{"negative clamps to zero", -5, 3, 0},
		{"empty project set resets to zero", 5, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.allProjects = m.allProjects[:tc.projectCount]
			m.invalidateVisibleCache()
			m.selected = tc.selected

			// When: bounds are enforced
			m.ensureSelectionBounds()

			// Then: the selection is clamped into the visible range
			if m.selected != tc.want {
				t.Fatalf("selected=%d with %d projects: got %d, want %d",
					tc.selected, tc.projectCount, m.selected, tc.want)
			}
		})
	}
}

// --- Round-trip state transition tests ---

// TestRoundTrip_MultiPanel_Pipelines: opening a pipeline view initializes it and closing clears it.
// Given the default multi-panel mode, when the pipeline view opens for a project and then closes, then the
// open switches to modePipelines with a fetch command and the project recorded, and the close wipes the
// view state.
// Why it matters: leftover state on either side of the round trip bleeds one project's pipelines into the
// next project the user opens.
func TestRoundTrip_MultiPanel_Pipelines(t *testing.T) {
	// Given: the default multi-panel mode
	m := newTestModel()
	if m.mode != modeMultiPanel {
		t.Fatalf("initial mode should be modeMultiPanel, got %d", m.mode)
	}

	// When: the pipeline view opens for a project
	project := m.allProjects[0]
	result, cmd := m.openPipelineView(project)
	m = result.(Model)

	// Then: the mode switches, a fetch is queued, and the view tracks the project
	if m.mode != modePipelines {
		t.Fatalf("expected modePipelines, got %d", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected fetch command from openPipelineView")
	}
	if m.pipelineView.project.ID != project.ID {
		t.Fatalf("pipeline view should be for project %d, got %d",
			project.ID, m.pipelineView.project.ID)
	}

	// When: the pipeline view closes
	m.closePipelineView()

	// Then: the view state is cleared
	if m.pipelineView.project.ID != 0 {
		t.Fatal("pipeline view state should be cleared after close")
	}
}

// TestRoundTrip_MultiPanel_Explorer: the explorer overlay opens with project state and closes back clean.
// Given the multi-panel mode, when the explorer opens for a project and then closes, then the mode stays
// modeMultiPanel throughout, the explorer tracks the project and its branch, and closing clears the state.
// Why it matters: the overlay must not leak mode changes or state, or the panels underneath desync from
// what the user last saw.
func TestRoundTrip_MultiPanel_Explorer(t *testing.T) {
	m := newTestModel()

	// When: the explorer opens as an overlay for a project
	project := m.allProjects[0]
	result, cmd := m.openExplorer(project)
	m = result.(Model)

	// Then: the mode stays multi-panel and a tree fetch is queued
	if m.mode != modeMultiPanel {
		t.Fatalf("expected mode to stay modeMultiPanel, got %d", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected fetch command from openExplorer")
	}

	// And: the explorer tracks the project and its branch
	if m.explorer.project.ID != project.ID {
		t.Fatalf("explorer should be for project %d, got %d",
			project.ID, m.explorer.project.ID)
	}
	if m.explorer.ref != "main" {
		t.Fatalf("explorer ref should be 'main', got %q", m.explorer.ref)
	}

	// When: the explorer closes
	m = m.closeExplorer("Back to projects")

	// Then: the mode is still multi-panel and the state is cleared
	if m.mode != modeMultiPanel {
		t.Fatalf("expected modeMultiPanel after close, got %d", m.mode)
	}
	if m.explorer.project.ID != 0 {
		t.Fatal("explorer state should be cleared after close")
	}
}

// TestRoundTrip_OpenDifferentProjects: reopening the pipeline view for another project retargets it.
// Given the pipeline view opened for project 1, when it closes and opens for project 2, then the view's
// project ID follows.
// Why it matters: a sticky project ID would fetch and render pipelines for the previously viewed project.
func TestRoundTrip_OpenDifferentProjects(t *testing.T) {
	m := newTestModel()

	// Given: the pipeline view opened for project 1
	result, _ := m.openPipelineView(m.allProjects[0])
	m = result.(Model)
	if m.pipelineView.project.ID != 1 {
		t.Fatalf("expected project ID 1, got %d", m.pipelineView.project.ID)
	}

	// When: it closes and reopens for project 2
	m.closePipelineView()
	result, _ = m.openPipelineView(m.allProjects[1])
	m = result.(Model)

	// Then: the view targets project 2
	if m.pipelineView.project.ID != 2 {
		t.Fatalf("expected project ID 2, got %d", m.pipelineView.project.ID)
	}
}

// TestRoundTrip_ExplorerUsesCorrectBranch: the explorer browses each project's default branch, falling
// back to main.
// Given project 2 with default branch develop and project 3 with none, when the explorer opens each, then
// the ref is develop and then main.
// Why it matters: browsing the wrong ref silently shows file trees from a branch the user did not pick.
func TestRoundTrip_ExplorerUsesCorrectBranch(t *testing.T) {
	m := newTestModel()

	// When: the explorer opens project 2, whose DefaultBranch is "develop"
	result, _ := m.openExplorer(m.allProjects[1])
	m = result.(Model)

	// Then: the explorer browses that branch
	if m.explorer.ref != "develop" {
		t.Fatalf("expected ref='develop', got %q", m.explorer.ref)
	}

	// When: it closes and opens project 3, which has no default branch
	m = m.closeExplorer("")
	result, _ = m.openExplorer(m.allProjects[2])
	m = result.(Model)

	// Then: the ref falls back to main
	if m.explorer.ref != "main" {
		t.Fatalf("expected fallback ref='main', got %q", m.explorer.ref)
	}
}

// TestRoundTrip_PipelineViewReinitialization: reopening the pipeline view resets selection, paging, and
// log auto-follow.
// Given a pipeline view with a moved selection, page 3, and auto-follow off, when it closes and reopens
// for the same project, then selection and page reset to their defaults and auto-follow is back on.
// Why it matters: stale paging would reopen the view deep in the pipeline history and make it look empty
// or out of date.
func TestRoundTrip_PipelineViewReinitialization(t *testing.T) {
	// Given: an open pipeline view with moved selection, paging, and follow state
	m := newTestModel()
	result, _ := m.openPipelineView(m.allProjects[0])
	m = result.(Model)

	m.pipelineView.selected = 5
	m.pipelineView.page = 3
	m.pipelineView.logAutoFollow = false

	// When: the view closes and reopens for the same project
	m.closePipelineView()
	result, _ = m.openPipelineView(m.allProjects[0])
	m = result.(Model)

	// Then: the view state is fresh
	if m.pipelineView.selected != 0 {
		t.Fatalf("expected selected=0 after reopen, got %d", m.pipelineView.selected)
	}
	if m.pipelineView.page != 1 {
		t.Fatalf("expected page=1 after reopen, got %d", m.pipelineView.page)
	}
	if !m.pipelineView.logAutoFollow {
		t.Fatal("expected logAutoFollow=true after reopen")
	}
}

// --- NewModel initialization tests ---

// TestNewModel_DefaultOptions: a model built without options starts in multi-panel with sane defaults.
// Given empty Options, when NewModel runs, then the mode is modeMultiPanel, ProjectsPerPage defaults to
// 30, focus starts on the projects panel, and loading is true.
// Why it matters: these are the first-frame guarantees, and a wrong default mode or focus renders a
// startup screen with nothing selected and no loading hint.
func TestNewModel_DefaultOptions(t *testing.T) {
	// When: a model is built with empty options
	client := &mockService{}
	m := NewModel(context.Background(), client, Options{})

	// Then: mode, page size, focus, and loading all take their defaults
	if m.mode != modeMultiPanel {
		t.Fatalf("expected initial mode=modeMultiPanel, got %d", m.mode)
	}
	if m.opts.ProjectsPerPage != 30 {
		t.Fatalf("expected default ProjectsPerPage=30, got %d", m.opts.ProjectsPerPage)
	}
	if m.focus.Active != PanelProjects {
		t.Fatalf("expected initial focus on PanelProjects, got %d", m.focus.Active)
	}
	if !m.loading {
		t.Fatal("expected loading=true on init")
	}
}

// TestNewModel_CustomOptions: an explicit ProjectsPerPage survives model construction.
// Given Options with ProjectsPerPage 50, when NewModel runs, then the model keeps 50.
// Why it matters: silently reverting to the default page size would desync pagination from what the user
// configured with --projects-per-page.
func TestNewModel_CustomOptions(t *testing.T) {
	// When: a model is built with an explicit page size
	client := &mockService{}
	m := NewModel(context.Background(), client, Options{ProjectsPerPage: 50})

	// Then: the configured value survives
	if m.opts.ProjectsPerPage != 50 {
		t.Fatalf("expected ProjectsPerPage=50, got %d", m.opts.ProjectsPerPage)
	}
}

// TestNewModel_FirstWindowSizeNoPanic: the very first window size message completes layout without panicking.
// Given a freshly constructed model before any project is selected, when the initial WindowSizeMsg
// arrives, then Update finishes and the mode is still modeMultiPanel.
// Why it matters: if NewModel left pipelineView zero-valued, the layout pass would drive list.SetSize into
// a nil-delegate dereference (bubbles/list updatePagination calls m.delegate.Height()) and crash the app
// at startup, so a panic here fails the test.
//
// The fixture window must clear computeLayout's minimums (width >= 63) so the layout pass actually runs
// and sizes the sidebar panels instead of short-circuiting on a too-small terminal.
func TestNewModel_FirstWindowSizeNoPanic(t *testing.T) {
	// Given: a freshly constructed model with no project selected
	client := &mockService{}
	m := NewModel(context.Background(), client, Options{})

	// When/Then: the first resize completes the layout pass without panicking
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	if updated.(Model).mode != modeMultiPanel {
		t.Fatalf("expected modeMultiPanel after resize, got %d", updated.(Model).mode)
	}
}

// TestLoadProjectPipelines_SizesPipelineList: selecting a project leaves the rebuilt pipeline list sized
// and renderable.
// Given a model that has completed a layout pass, when loadProjectPipelines rebuilds the pipeline list and
// the fetched pipelines arrive, then the list has non-zero dimensions and the pane renders entry #11.
// Why it matters: the multi-panel pane sizes the list from state rather than in View, so a rebuild that
// skips re-pushing the layout renders loaded pipelines into a 0x0 list, a blank pane until the next
// terminal resize.
func TestLoadProjectPipelines_SizesPipelineList(t *testing.T) {
	// Given: a model whose first resize has established the multi-panel layout
	project := gitlab.ProjectNode{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha", DefaultBranch: "main"}
	svc := &mockService{
		ListPipelinesFn: func(_ context.Context, _ int, _ gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			return gitlab.PipelinePage{
				Pipelines:  []gitlab.PipelineSummary{{ID: 11, Status: "success", Ref: "main"}},
				Page:       1,
				TotalPages: 1,
			}, nil
		},
	}
	m := NewModel(context.Background(), svc, Options{ProjectsPerPage: 10})
	m.allProjects = []gitlab.ProjectNode{project}

	res, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	m = res.(Model)

	// When: selecting a project rebuilds the pipeline sub-components from scratch
	_ = (&m).loadProjectPipelines(project)

	// Then: the rebuilt list carries non-zero dimensions
	if w, h := m.pipelineView.pipelineList.Width(), m.pipelineView.pipelineList.Height(); w == 0 || h == 0 {
		t.Fatalf("pipeline list not sized after load: %dx%d", w, h)
	}

	// And: once the fetched pipelines arrive, the pane renders them
	res, _ = m.Update(pipelinesLoadedMsg{
		projectID:  project.ID,
		pipelines:  []gitlab.PipelineSummary{{ID: 11, Status: "success", Ref: "main"}},
		page:       1,
		totalPages: 1,
	})
	m = res.(Model)

	pane := strings.TrimSpace(renderPipelinesPanelContent(m, 50, 20))
	if pane == "" {
		t.Fatal("pipeline pane rendered empty despite loaded pipelines")
	}
	if !strings.Contains(pane, "#11") {
		t.Fatalf("pipeline pane missing pipeline entry #11, got:\n%s", pane)
	}
}

// TestProjectsPanel_ShowsSharedPipelineStatus: status stored on the model's cache renders in the projects panel.
// Given a model built by NewModel with one visible project, when a pipeline status is stored through
// m.pipelineStatus, then the projects panel renders that project's status icon.
// Why it matters: if the list delegate held a copy of the cache instead of sharing it, fetched pipeline
// statuses would never appear next to projects.
func TestProjectsPanel_ShowsSharedPipelineStatus(t *testing.T) {
	// Given: a NewModel-built model with one visible project
	m := NewModel(context.Background(), &mockService{}, Options{})
	m.loading = false
	m.projectTab = projectTabAll
	m.allProjects = []gitlab.ProjectNode{{ID: 42, Name: "alpha", PathWithNamespace: "team/alpha"}}
	m.pagesReady = map[int]bool{1: true}
	m.invalidateVisibleCache()
	m.projectList.SetSize(60, 10)
	m.updateProjectList()

	// When: a pipeline status is stored through the model's shared cache
	m.pipelineStatus.Set(42, pipelineState{
		hasInfo: true,
		info:    gitlab.PipelineSummary{ID: 100, Ref: "main", Status: "success"},
	})

	// Then: the projects panel shows the project with its status icon
	pane := renderProjectsPanelContent(m, 60, 10)
	if !strings.Contains(pane, "alpha") {
		t.Fatalf("expected project in panel, got %q", pane)
	}
	if !strings.Contains(pane, iconSuccess) {
		t.Fatalf("expected success icon %q in panel, got %q", iconSuccess, pane)
	}
}

// --- Multiple actions on same model ---

// TestMultipleOpens_DoNotCorrupt: opening the pipeline view over an explorer overlay keeps both states
// coherent.
// Given the explorer overlay open for project 1, when the pipeline view opens for project 2, then the
// explorer still tracks project 1 and the pipeline view enters modePipelines on project 2.
// Why it matters: the two surfaces share one model, and cross-contamination would show one project's files
// alongside another project's pipelines.
func TestMultipleOpens_DoNotCorrupt(t *testing.T) {
	m := newTestModel()

	// Given: the explorer overlay open for project 1 (mode stays modeMultiPanel)
	result, _ := m.openExplorer(m.allProjects[0])
	m = result.(Model)
	if m.explorer.project.ID != 1 {
		t.Fatalf("expected explorer for project 1, got %d", m.explorer.project.ID)
	}

	// When: the pipeline view opens on a different project
	result, _ = m.openPipelineView(m.allProjects[1])
	m = result.(Model)

	// Then: the pipeline view takes over with its own project
	if m.mode != modePipelines {
		t.Fatalf("expected modePipelines, got %d", m.mode)
	}
	if m.pipelineView.project.ID != 2 {
		t.Fatalf("expected pipeline view for project 2, got %d", m.pipelineView.project.ID)
	}
}

// --- pipeline ticker lifecycle tests ---

// TestPipelineTick_LifecycleByMode: the tick chain continues, dies, starts, or stays single per mode.
// Given a model in a given mode with a given chain state, when the continue or ensure helper runs, then
// the returned command and the alive flag match the mode's refresh policy, and a live chain never starts twice.
// Why it matters: a dead chain freezes pipeline auto-refresh, and a doubled chain hammers the API with
// overlapping refreshes.
func TestPipelineTick_LifecycleByMode(t *testing.T) {
	// Given: continue/ensure invocations across modes and chain states
	tests := []struct {
		name        string
		op          func(*Model) tea.Cmd
		mode        Mode
		aliveBefore bool
		wantCmd     bool
		wantAlive   bool
	}{
		{"continue re-enqueues in modeProjects", continuePipelineTickCmd, modeProjects, true, true, true},
		{"continue re-enqueues in modePipelines", continuePipelineTickCmd, modePipelines, true, true, true},
		{"continue re-enqueues in modeMultiPanel", continuePipelineTickCmd, modeMultiPanel, true, true, true},
		{"continue kills the chain in modeExplorer", continuePipelineTickCmd, modeExplorer, true, false, false},
		{"ensure starts a dead chain in modeProjects", ensurePipelineTickCmd, modeProjects, false, true, true},
		{"ensure never double-starts a live chain", ensurePipelineTickCmd, modeProjects, true, false, true},
		{"ensure stays idle in modeExplorer", ensurePipelineTickCmd, modeExplorer, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.mode = tc.mode
			m.pipelineTickAlive = tc.aliveBefore

			// When: the lifecycle helper runs
			cmd := tc.op(&m)

			// Then: the command and alive flag follow the mode's refresh policy
			if gotCmd := cmd != nil; gotCmd != tc.wantCmd {
				t.Fatalf("cmd != nil = %v, want %v", gotCmd, tc.wantCmd)
			}
			if m.pipelineTickAlive != tc.wantAlive {
				t.Fatalf("pipelineTickAlive = %v, want %v", m.pipelineTickAlive, tc.wantAlive)
			}
		})
	}
}
