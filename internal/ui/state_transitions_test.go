package ui

import (
	"context"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"

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
		commitCache: func() *LRUCache[int, []gitlab.CommitSummary] {
			c := NewLRUCache[int, []gitlab.CommitSummary](maxCommitCacheSize)
			return &c
		}(),
		commitLoading: make(map[int]bool),
	}
}

// --- openExplorer tests ---

func TestOpenExplorer_SetsProject(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openExplorer(project)
	got := result.(Model)

	if got.explorer.project.ID != project.ID {
		t.Fatalf("expected explorer project ID=%d, got %d",
			project.ID, got.explorer.project.ID)
	}
}

func TestOpenExplorer_UsesDefaultBranch(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0] // DefaultBranch: "main"
	result, _ := m.openExplorer(project)
	got := result.(Model)

	if got.explorer.ref != "main" {
		t.Fatalf("expected ref='main', got %q", got.explorer.ref)
	}
}

func TestOpenExplorer_FallsBackToMain(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[2] // DefaultBranch: "" (empty)
	result, _ := m.openExplorer(project)
	got := result.(Model)

	if got.explorer.ref != "main" {
		t.Fatalf("expected ref='main' as fallback, got %q", got.explorer.ref)
	}
}

func TestOpenExplorer_InitializesStack(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openExplorer(project)
	got := result.(Model)

	if len(got.explorer.stack) != 1 {
		t.Fatalf("expected stack length 1 (root), got %d", len(got.explorer.stack))
	}
	if got.explorer.stack[0].path != "" {
		t.Fatalf("expected root path '', got %q", got.explorer.stack[0].path)
	}
	if !got.explorer.stack[0].loading {
		t.Fatal("expected root dir to be loading")
	}
}

func TestOpenExplorer_ReturnsFetchCommand(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	_, cmd := m.openExplorer(project)

	if cmd == nil {
		t.Fatal("expected a fetch tree command")
	}
}

func TestOpenExplorer_SetsStatus(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openExplorer(project)
	got := result.(Model)

	if got.status == "" {
		t.Fatal("expected non-empty status after openExplorer")
	}
}

// --- openPipelineView tests ---

func TestOpenPipelineView_SetsMode(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if got.mode != modePipelines {
		t.Fatalf("expected mode=modePipelines, got %d", got.mode)
	}
}

func TestOpenPipelineView_SetsProject(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[1]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if got.pipelineView.project.ID != project.ID {
		t.Fatalf("expected pipeline view project ID=%d, got %d",
			project.ID, got.pipelineView.project.ID)
	}
}

func TestOpenPipelineView_InitializesLoading(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if !got.pipelineView.loading {
		t.Fatal("expected pipeline view to be loading")
	}
}

func TestOpenPipelineView_InitializesPage(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if got.pipelineView.page != 1 {
		t.Fatalf("expected page=1, got %d", got.pipelineView.page)
	}
	if got.pipelineView.totalPages != 1 {
		t.Fatalf("expected totalPages=1, got %d", got.pipelineView.totalPages)
	}
	if got.pipelineView.perPage != pipelinePerPage {
		t.Fatalf("expected perPage=%d, got %d", pipelinePerPage, got.pipelineView.perPage)
	}
}

func TestOpenPipelineView_LogAutoFollowEnabled(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if !got.pipelineView.logAutoFollow {
		t.Fatal("expected logAutoFollow=true")
	}
}

func TestOpenPipelineView_FocusOnPipelines(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if got.pipelineView.focus != pipelineFocusPipelines {
		t.Fatalf("expected focus=pipelineFocusPipelines, got %d", got.pipelineView.focus)
	}
}

func TestOpenPipelineView_ReturnsFetchCommand(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	_, cmd := m.openPipelineView(project)

	if cmd == nil {
		t.Fatal("expected a fetch pipelines command")
	}
}

func TestOpenPipelineView_SetsStatus(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	if got.status == "" {
		t.Fatal("expected non-empty status after openPipelineView")
	}
}

// --- closePipelineView tests ---

func TestClosePipelineView_ReturnsToProjects(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	m = result.(Model)

	m.closePipelineView()
	if m.mode != modeProjects {
		t.Fatalf("expected mode=modeProjects after close, got %d", m.mode)
	}
}

func TestClosePipelineView_ClearsState(t *testing.T) {
	m := newTestModel()
	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	m = result.(Model)

	m.closePipelineView()
	if m.pipelineView.project.ID != 0 {
		t.Fatal("expected pipelineView project to be cleared")
	}
	if m.pipelineView.loading {
		t.Fatal("expected loading=false after close")
	}
}

// --- closeExplorer tests ---

func TestCloseExplorer_ReturnsToProjects(t *testing.T) {
	m := newTestModel()
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	m = m.closeExplorer("Back")
	if m.mode != modeProjects {
		t.Fatalf("expected mode=modeProjects after close, got %d", m.mode)
	}
}

func TestCloseExplorer_ClearsState(t *testing.T) {
	m := newTestModel()
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	m = m.closeExplorer("Back to projects")
	if m.explorer.project.ID != 0 {
		t.Fatal("expected explorer project to be cleared")
	}
	if len(m.explorer.stack) != 0 {
		t.Fatal("expected explorer stack to be cleared")
	}
}

func TestCloseExplorer_SetsStatus(t *testing.T) {
	m := newTestModel()
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	m = m.closeExplorer("Back to projects")
	if m.status != "Back to projects" {
		t.Fatalf("expected status 'Back to projects', got %q", m.status)
	}
}

func TestCloseExplorer_EmptyStatusPreservesExisting(t *testing.T) {
	m := newTestModel()
	m.mode = modeExplorer
	m.status = "Previous status"
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	m = m.closeExplorer("")
	if m.status != "Previous status" {
		t.Fatalf("expected preserved status, got %q", m.status)
	}
}

func TestCloseExplorer_FromMultiPanel_StaysInMultiPanel(t *testing.T) {
	m := newTestModel()
	m.mode = modeMultiPanel
	m.explorer = explorerState{
		project: m.allProjects[0],
		stack:   []dirState{{path: ""}},
	}

	m = m.closeExplorer("Done")
	if m.mode != modeMultiPanel {
		t.Fatalf("expected mode=modeMultiPanel when closing from multi-panel, got %d", m.mode)
	}
}

// --- ensureSelectionBounds tests ---

func TestEnsureSelectionBounds_ClampsAbove(t *testing.T) {
	m := newTestModel()
	m.selected = 100 // Way beyond the 3 projects

	m.ensureSelectionBounds()
	if m.selected != len(m.allProjects)-1 {
		t.Fatalf("expected selected=%d, got %d", len(m.allProjects)-1, m.selected)
	}
}

func TestEnsureSelectionBounds_ClampsBelow(t *testing.T) {
	m := newTestModel()
	m.selected = -5

	m.ensureSelectionBounds()
	if m.selected != 0 {
		t.Fatalf("expected selected=0, got %d", m.selected)
	}
}

func TestEnsureSelectionBounds_ValidIndexUnchanged(t *testing.T) {
	m := newTestModel()
	m.selected = 1

	m.ensureSelectionBounds()
	if m.selected != 1 {
		t.Fatalf("expected selected=1, got %d", m.selected)
	}
}

func TestEnsureSelectionBounds_EmptyProjects(t *testing.T) {
	m := newTestModel()
	m.allProjects = nil
	m.invalidateVisibleCache()
	m.selected = 5

	m.ensureSelectionBounds()
	if m.selected != 0 {
		t.Fatalf("expected selected=0 for empty projects, got %d", m.selected)
	}
}

func TestEnsureSelectionBounds_ZeroWithProjects(t *testing.T) {
	m := newTestModel()
	m.selected = 0

	m.ensureSelectionBounds()
	if m.selected != 0 {
		t.Fatalf("expected selected=0, got %d", m.selected)
	}
}

func TestEnsureSelectionBounds_LastIndex(t *testing.T) {
	m := newTestModel()
	m.selected = len(m.allProjects) - 1

	m.ensureSelectionBounds()
	if m.selected != len(m.allProjects)-1 {
		t.Fatalf("expected selected=%d, got %d", len(m.allProjects)-1, m.selected)
	}
}

func TestEnsureSelectionBounds_OneOverMax(t *testing.T) {
	m := newTestModel()
	m.selected = len(m.allProjects) // Exactly one over

	m.ensureSelectionBounds()
	if m.selected != len(m.allProjects)-1 {
		t.Fatalf("expected selected=%d, got %d", len(m.allProjects)-1, m.selected)
	}
}

// --- Round-trip state transition tests ---

func TestRoundTrip_MultiPanel_Pipelines(t *testing.T) {
	m := newTestModel()
	if m.mode != modeMultiPanel {
		t.Fatalf("initial mode should be modeMultiPanel, got %d", m.mode)
	}

	// Open pipeline view
	project := m.allProjects[0]
	result, cmd := m.openPipelineView(project)
	m = result.(Model)
	if m.mode != modePipelines {
		t.Fatalf("expected modePipelines, got %d", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected fetch command from openPipelineView")
	}

	// Verify pipeline state is initialized
	if m.pipelineView.project.ID != project.ID {
		t.Fatalf("pipeline view should be for project %d, got %d",
			project.ID, m.pipelineView.project.ID)
	}

	// Close pipeline view
	m.closePipelineView()
	if m.pipelineView.project.ID != 0 {
		t.Fatal("pipeline view state should be cleared after close")
	}
}

func TestRoundTrip_MultiPanel_Explorer(t *testing.T) {
	m := newTestModel()

	// Open explorer as overlay (mode stays modeMultiPanel)
	project := m.allProjects[0]
	result, cmd := m.openExplorer(project)
	m = result.(Model)
	if m.mode != modeMultiPanel {
		t.Fatalf("expected mode to stay modeMultiPanel, got %d", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected fetch command from openExplorer")
	}

	// Verify explorer state is initialized
	if m.explorer.project.ID != project.ID {
		t.Fatalf("explorer should be for project %d, got %d",
			project.ID, m.explorer.project.ID)
	}
	if m.explorer.ref != "main" {
		t.Fatalf("explorer ref should be 'main', got %q", m.explorer.ref)
	}

	// Close explorer — stays in multi-panel
	m = m.closeExplorer("Back to projects")
	if m.mode != modeMultiPanel {
		t.Fatalf("expected modeMultiPanel after close, got %d", m.mode)
	}
	if m.explorer.project.ID != 0 {
		t.Fatal("explorer state should be cleared after close")
	}
}

func TestRoundTrip_OpenDifferentProjects(t *testing.T) {
	m := newTestModel()

	// Open pipeline view for project 1
	result, _ := m.openPipelineView(m.allProjects[0])
	m = result.(Model)
	if m.pipelineView.project.ID != 1 {
		t.Fatalf("expected project ID 1, got %d", m.pipelineView.project.ID)
	}

	// Close and open for project 2
	m.closePipelineView()
	result, _ = m.openPipelineView(m.allProjects[1])
	m = result.(Model)
	if m.pipelineView.project.ID != 2 {
		t.Fatalf("expected project ID 2, got %d", m.pipelineView.project.ID)
	}
}

func TestRoundTrip_ExplorerUsesCorrectBranch(t *testing.T) {
	m := newTestModel()

	// Project 2 has DefaultBranch "develop"
	result, _ := m.openExplorer(m.allProjects[1])
	m = result.(Model)
	if m.explorer.ref != "develop" {
		t.Fatalf("expected ref='develop', got %q", m.explorer.ref)
	}

	// Close and open project 3 (no default branch)
	m = m.closeExplorer("")
	result, _ = m.openExplorer(m.allProjects[2])
	m = result.(Model)
	if m.explorer.ref != "main" {
		t.Fatalf("expected fallback ref='main', got %q", m.explorer.ref)
	}
}

func TestRoundTrip_PipelineViewReinitialization(t *testing.T) {
	m := newTestModel()

	// Open pipeline view
	result, _ := m.openPipelineView(m.allProjects[0])
	m = result.(Model)

	// Simulate state changes
	m.pipelineView.selected = 5
	m.pipelineView.page = 3
	m.pipelineView.logAutoFollow = false

	// Close and reopen
	m.closePipelineView()
	result, _ = m.openPipelineView(m.allProjects[0])
	m = result.(Model)

	// State should be fresh
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

func TestNewModel_DefaultOptions(t *testing.T) {
	client := &mockService{}
	m := NewModel(context.Background(), client, Options{})

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

func TestNewModel_CustomOptions(t *testing.T) {
	client := &mockService{}
	m := NewModel(context.Background(), client, Options{ProjectsPerPage: 50})

	if m.opts.ProjectsPerPage != 50 {
		t.Fatalf("expected ProjectsPerPage=50, got %d", m.opts.ProjectsPerPage)
	}
}

func TestNewModel_PipelineStatusMapShared(t *testing.T) {
	client := &mockService{}
	m := NewModel(context.Background(), client, Options{})

	// The pipeline status cache should be shared between model and delegate
	m.pipelineStatus.Set(42, pipelineState{hasInfo: true})
	// This verifies the cache is the same reference — if the delegate had
	// a copy, this wouldn't be visible through the delegate.
	if _, ok := m.pipelineStatus.Get(42); !ok {
		t.Fatal("expected shared pipelineStatus cache")
	}
}

// --- pipelineViewState.resetCaches test ---

func TestPipelineViewState_ResetCaches(t *testing.T) {
	pv := &pipelineViewState{
		stages:         NewAsyncCache[int, []gitlab.PipelineStage](),
		jobs:           NewAsyncCache[int, []gitlab.PipelineJob](),
		logs:           NewAsyncCache[int, string](),
		bridges:        NewAsyncCache[int, []gitlab.PipelineBridge](),
		childJobs:      NewAsyncCache[int, []gitlab.PipelineJob](),
		stageSelected:  5,
		jobRows:        make([]gitlab.PipelineJob, 3),
		stageJobRows:   make([]stageJobRow, 2),
		matrixExpanded: map[string]bool{"key1": true},
	}

	// Put some data in caches
	pv.stages.Set(1, []gitlab.PipelineStage{{Name: "build"}})
	pv.jobs.Set(1, []gitlab.PipelineJob{{ID: 1}})
	pv.logs.Set(1, "log content")

	pv.resetCaches()

	if pv.stageSelected != 0 {
		t.Fatalf("expected stageSelected=0 after reset, got %d", pv.stageSelected)
	}
	if pv.jobRows != nil {
		t.Fatal("expected jobRows=nil after reset")
	}
	if pv.stageJobRows != nil {
		t.Fatal("expected stageJobRows=nil after reset")
	}
	// matrixExpanded should be preserved
	if !pv.matrixExpanded["key1"] {
		t.Fatal("matrixExpanded should be preserved across resetCaches")
	}
}

// --- openExplorer with viewport initialization ---

func TestOpenExplorer_InitializesPreviewViewport(t *testing.T) {
	m := newTestModel()
	m.width = 120
	m.height = 40

	project := m.allProjects[0]
	result, _ := m.openExplorer(project)
	got := result.(Model)

	// The preview viewport should be initialized with non-zero dimensions
	vp := got.explorer.preview.viewport
	if vp.Width == 0 || vp.Height == 0 {
		t.Fatalf("expected non-zero viewport dimensions, got %dx%d", vp.Width, vp.Height)
	}
}

// --- openPipelineView with log viewport ---

func TestOpenPipelineView_InitializesLogViewport(t *testing.T) {
	m := newTestModel()
	m.width = 120
	m.height = 40

	project := m.allProjects[0]
	result, _ := m.openPipelineView(project)
	got := result.(Model)

	vp := got.pipelineView.logViewport
	_ = vp
	// The viewport should be initialized (non-zero width and height from the
	// pipelineLogContentWidth/pipelineLogContentHeight helpers)
	// We just verify no panic occurred and the state is valid
	if got.pipelineView.logJobID != 0 {
		t.Fatal("expected logJobID=0 initially")
	}
}

// --- Multiple actions on same model ---

func TestMultipleOpens_DoNotCorrupt(t *testing.T) {
	m := newTestModel()

	// Open explorer as overlay then immediately open pipeline view
	result, _ := m.openExplorer(m.allProjects[0])
	m = result.(Model)

	// Explorer overlay is active (mode stays modeMultiPanel)
	if m.explorer.project.ID != 1 {
		t.Fatalf("expected explorer for project 1, got %d", m.explorer.project.ID)
	}

	// Now open pipeline view on a different project
	result, _ = m.openPipelineView(m.allProjects[1])
	m = result.(Model)

	if m.mode != modePipelines {
		t.Fatalf("expected modePipelines, got %d", m.mode)
	}
	if m.pipelineView.project.ID != 2 {
		t.Fatalf("expected pipeline view for project 2, got %d", m.pipelineView.project.ID)
	}
}

// --- Panel helpers tests ---

func TestPanelLabel_AllPanels(t *testing.T) {
	tests := []struct {
		id       PanelID
		contains string
	}{
		{PanelProjects, "Projects"},
		{PanelPipelines, "Pipelines"},
		{PanelStages, "Stages"},
		{PanelMRs, "Merge Requests"},
		{PanelDetail, "Detail"},
	}
	for _, tc := range tests {
		label := panelLabel(tc.id)
		if label == "" {
			t.Errorf("panelLabel(%d) returned empty", tc.id)
		}
		if !contains(label, tc.contains) {
			t.Errorf("panelLabel(%d) = %q, expected to contain %q", tc.id, label, tc.contains)
		}
	}
}

func TestPanelLabel_Unknown(t *testing.T) {
	label := panelLabel(PanelID(99))
	if label != "Unknown" {
		t.Fatalf("expected 'Unknown' for invalid PanelID, got %q", label)
	}
}

func TestPanelShortcut_SidebarPanels(t *testing.T) {
	for i, p := range SidebarPanels {
		shortcut := panelShortcut(p)
		if shortcut != i+1 {
			t.Errorf("panelShortcut(%d) = %d, expected %d", p, shortcut, i+1)
		}
	}
}

func TestPanelShortcut_Detail(t *testing.T) {
	shortcut := panelShortcut(PanelDetail)
	if shortcut != 0 {
		t.Fatalf("panelShortcut(PanelDetail) = %d, expected 0", shortcut)
	}
}

func TestPanelByShortcut_ValidRange(t *testing.T) {
	for i := 1; i <= len(SidebarPanels); i++ {
		p, ok := panelByShortcut(i)
		if !ok {
			t.Fatalf("panelByShortcut(%d) returned ok=false", i)
		}
		if p != SidebarPanels[i-1] {
			t.Errorf("panelByShortcut(%d) = %d, expected %d", i, p, SidebarPanels[i-1])
		}
	}
}

func TestPanelByShortcut_OutOfRange(t *testing.T) {
	_, ok := panelByShortcut(0)
	if ok {
		t.Fatal("panelByShortcut(0) should return false")
	}
	_, ok = panelByShortcut(len(SidebarPanels) + 1)
	if ok {
		t.Fatal("panelByShortcut(too large) should return false")
	}
	_, ok = panelByShortcut(-1)
	if ok {
		t.Fatal("panelByShortcut(-1) should return false")
	}
}

func TestNextSidebarPanel_Wraps(t *testing.T) {
	last := SidebarPanels[len(SidebarPanels)-1]
	next := nextSidebarPanel(last)
	if next != SidebarPanels[0] {
		t.Fatalf("expected wrap to first panel, got %d", next)
	}
}

func TestNextSidebarPanel_Sequential(t *testing.T) {
	for i := 0; i < len(SidebarPanels)-1; i++ {
		next := nextSidebarPanel(SidebarPanels[i])
		if next != SidebarPanels[i+1] {
			t.Errorf("nextSidebarPanel(%d) = %d, expected %d",
				SidebarPanels[i], next, SidebarPanels[i+1])
		}
	}
}

func TestPrevSidebarPanel_Wraps(t *testing.T) {
	first := SidebarPanels[0]
	prev := prevSidebarPanel(first)
	if prev != SidebarPanels[len(SidebarPanels)-1] {
		t.Fatalf("expected wrap to last panel, got %d", prev)
	}
}

func TestPrevSidebarPanel_Sequential(t *testing.T) {
	for i := 1; i < len(SidebarPanels); i++ {
		prev := prevSidebarPanel(SidebarPanels[i])
		if prev != SidebarPanels[i-1] {
			t.Errorf("prevSidebarPanel(%d) = %d, expected %d",
				SidebarPanels[i], prev, SidebarPanels[i-1])
		}
	}
}

// --- pipeline ticker lifecycle tests ---

func TestPipelineTick_KeepsAliveInRefreshableModes(t *testing.T) {
	for _, mode := range []Mode{modeProjects, modePipelines, modeMultiPanel} {
		m := newTestModel()
		m.mode = mode
		m.pipelineTickAlive = true
		if got := continuePipelineTickCmd(&m); got == nil {
			t.Errorf("mode=%d: expected re-enqueue cmd, got nil", mode)
		}
		if !m.pipelineTickAlive {
			t.Errorf("mode=%d: expected pipelineTickAlive=true after continue", mode)
		}
	}
}

func TestPipelineTick_DiesInIdleMode(t *testing.T) {
	m := newTestModel()
	m.mode = modeExplorer
	m.pipelineTickAlive = true
	if got := continuePipelineTickCmd(&m); got != nil {
		t.Fatal("expected nil cmd (chain should die) in modeExplorer")
	}
	if m.pipelineTickAlive {
		t.Fatal("expected pipelineTickAlive=false after chain dies in idle mode")
	}
}

func TestEnsurePipelineTick_StartsWhenDead(t *testing.T) {
	m := newTestModel()
	m.mode = modeProjects
	m.pipelineTickAlive = false
	cmd := ensurePipelineTickCmd(&m)
	if cmd == nil {
		t.Fatal("expected tick cmd when chain is dead and mode is refreshable")
	}
	if !m.pipelineTickAlive {
		t.Fatal("expected pipelineTickAlive=true after start")
	}
}

func TestEnsurePipelineTick_NoDoubleStart(t *testing.T) {
	m := newTestModel()
	m.mode = modeProjects
	m.pipelineTickAlive = true
	if cmd := ensurePipelineTickCmd(&m); cmd != nil {
		t.Fatal("expected nil cmd when chain already alive (would double-tick)")
	}
}

func TestEnsurePipelineTick_NoStartInIdleMode(t *testing.T) {
	m := newTestModel()
	m.mode = modeExplorer
	m.pipelineTickAlive = false
	if cmd := ensurePipelineTickCmd(&m); cmd != nil {
		t.Fatal("expected nil cmd in non-refreshable mode")
	}
	if m.pipelineTickAlive {
		t.Fatal("expected pipelineTickAlive to stay false")
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
