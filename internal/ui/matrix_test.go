package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

func TestParseMatrixName_Variants(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		baseName string
		vars     string
		isMatrix bool
	}{
		{"standard matrix", "test: [aws, monitoring]", "test", "aws, monitoring", true},
		{"key-value matrix", "test: [K=v, X=y]", "test", "K=v, X=y", true},
		{"single var matrix", "deploy: [prod]", "deploy", "prod", true},
		{"non-matrix job", "build", "build", "", false},
		{"colon but no brackets", "test: something", "test: something", "", false},
		{"brackets but no colon", "test [a, b]", "test [a, b]", "", false},
		{"spaced colon", "lint:check: [a, b]", "lint:check", "a, b", true},
		{"empty brackets", "test: []", "test: []", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseName, vars, isMatrix := parseMatrixName(tt.input)
			if baseName != tt.baseName {
				t.Errorf("baseName = %q, want %q", baseName, tt.baseName)
			}
			if vars != tt.vars {
				t.Errorf("vars = %q, want %q", vars, tt.vars)
			}
			if isMatrix != tt.isMatrix {
				t.Errorf("isMatrix = %v, want %v", isMatrix, tt.isMatrix)
			}
		})
	}
}

func TestBuildStageJobRows_NoMatrix(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "test", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "unit-test", Stage: "test", Status: "success"},
		{ID: 3, Name: "lint", Stage: "test", Status: "success"},
	}

	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.Kind != rowKindJob {
			t.Errorf("expected rowKindJob, got %d", row.Kind)
		}
		if row.Job == nil {
			t.Error("expected Job to be set")
		}
	}
	if rows[0].Job.Name != "compile" {
		t.Errorf("first row = %q, want compile", rows[0].Job.Name)
	}
	if rows[0].Stage != "build" {
		t.Errorf("first row stage = %q, want build", rows[0].Stage)
	}
}

func TestBuildStageJobRows_MatrixAlwaysExpanded(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "test", Status: "failed"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "test: [aws, monitoring]", Stage: "test", Status: "success"},
		{ID: 2, Name: "test: [aws, backup]", Stage: "test", Status: "failed"},
		{ID: 3, Name: "test: [gcp, monitoring]", Stage: "test", Status: "success"},
	}

	// Even without any expanded state, children are always emitted
	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	// 1 header + 3 children = 4
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (header + 3 children), got %d", len(rows))
	}
	if rows[0].Kind != rowKindMatrixGroup {
		t.Errorf("row 0: expected rowKindMatrixGroup, got %d", rows[0].Kind)
	}
	if rows[0].BaseName != "test" {
		t.Errorf("baseName = %q, want test", rows[0].BaseName)
	}
	if len(rows[0].Jobs) != 3 {
		t.Errorf("group has %d jobs, want 3", len(rows[0].Jobs))
	}
	if rows[0].Status != "failed" {
		t.Errorf("aggregated status = %q, want failed", rows[0].Status)
	}
	for i := 1; i <= 3; i++ {
		if rows[i].Kind != rowKindMatrixChild {
			t.Errorf("row %d: expected rowKindMatrixChild, got %d", i, rows[i].Kind)
		}
		if rows[i].Job == nil {
			t.Errorf("row %d: expected Job to be set", i)
		}
	}
	// Check IsLast
	if rows[1].IsLast {
		t.Error("row 1 should not be IsLast")
	}
	if rows[2].IsLast {
		t.Error("row 2 should not be IsLast")
	}
	if !rows[3].IsLast {
		t.Error("row 3 should be IsLast")
	}
	// Check vars parsing
	if rows[1].Vars != "aws, monitoring" {
		t.Errorf("row 1 vars = %q, want 'aws, monitoring'", rows[1].Vars)
	}
}

func TestBuildStageJobRows_Mixed(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "test", Status: "failed"},
		{Name: "deploy", Status: "pending"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "test: [aws, monitoring]", Stage: "test", Status: "success"},
		{ID: 3, Name: "test: [gcp, monitoring]", Stage: "test", Status: "failed"},
		{ID: 4, Name: "lint", Stage: "test", Status: "success"},
		{ID: 5, Name: "deploy", Stage: "deploy", Status: "pending"},
	}

	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	// compile(1) + test group(1) + 2 children + lint(1) + deploy(1) = 6
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(rows))
	}
	if rows[0].Kind != rowKindJob || rows[0].Job.Name != "compile" {
		t.Errorf("row 0: expected compile job, got %v", rows[0])
	}
	if rows[1].Kind != rowKindMatrixGroup || rows[1].BaseName != "test" {
		t.Errorf("row 1: expected test matrix group, got kind=%d base=%q", rows[1].Kind, rows[1].BaseName)
	}
	if rows[2].Kind != rowKindMatrixChild {
		t.Errorf("row 2: expected rowKindMatrixChild, got %d", rows[2].Kind)
	}
	if rows[3].Kind != rowKindMatrixChild {
		t.Errorf("row 3: expected rowKindMatrixChild, got %d", rows[3].Kind)
	}
	if rows[4].Kind != rowKindJob || rows[4].Job.Name != "lint" {
		t.Errorf("row 4: expected lint job, got %v", rows[4])
	}
	if rows[5].Kind != rowKindJob || rows[5].Job.Name != "deploy" {
		t.Errorf("row 5: expected deploy job, got %v", rows[5])
	}
}

func TestAggregateMatrixStatus(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"all success", []string{"success", "success"}, "success"},
		{"one failed", []string{"success", "failed", "success"}, "failed"},
		{"running wins over success", []string{"success", "running"}, "running"},
		{"failed wins over running", []string{"running", "failed"}, "failed"},
		{"canceled over success", []string{"success", "canceled"}, "canceled"},
		{"manual over success", []string{"success", "manual"}, "manual"},
		{"empty", []string{}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var jobs []gitlab.PipelineJob
			for i, s := range tt.statuses {
				jobs = append(jobs, gitlab.PipelineJob{ID: i + 1, Status: s})
			}
			got := aggregateMatrixStatus(jobs)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildStageJobRows_Empty(t *testing.T) {
	rows := buildStageJobRows(nil, nil, nil, nil, nil)
	if rows != nil {
		t.Errorf("expected nil, got %d rows", len(rows))
	}
}

func TestBuildStageJobRows_StageOrder(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "deploy", Status: "pending"},
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "push", Stage: "deploy", Status: "pending"},
	}

	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// deploy stage comes first in stage order
	if rows[0].Stage != "deploy" {
		t.Errorf("first row stage = %q, want deploy", rows[0].Stage)
	}
	if rows[1].Stage != "build" {
		t.Errorf("second row stage = %q, want build", rows[1].Stage)
	}
}

// --- Integration tests: updateStageTable, key handling, action guards ---

// newMatrixPipelineModel creates a Model with pipeline view state populated
// with matrix jobs, suitable for testing updateStageTable and key handlers.
func newMatrixPipelineModel() Model {
	stagesCache := NewAsyncCache[int, []gitlab.PipelineStage]()
	stagesCache.Set(10, []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "test", Status: "failed"},
		{Name: "deploy", Status: "pending"},
	})

	jobsCache := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobsCache.Set(10, []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "test: [aws, monitoring]", Stage: "test", Status: "success"},
		{ID: 3, Name: "test: [aws, backup]", Stage: "test", Status: "failed"},
		{ID: 4, Name: "test: [gcp, monitoring]", Stage: "test", Status: "success"},
		{ID: 5, Name: "lint", Stage: "test", Status: "success"},
		{ID: 6, Name: "deploy", Stage: "deploy", Status: "pending"},
	})

	tbl := table.New(
		table.WithColumns(stageTableColumns(56)),
		table.WithFocused(false),
		table.WithHeight(10),
	)

	return Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		pipelineView: pipelineViewState{
			project:     gitlab.ProjectNode{ID: 1},
			pipelines:   []gitlab.PipelineSummary{{ID: 10, Ref: "main", Status: "failed"}},
			selected:    0,
			stages:      stagesCache,
			jobs:        jobsCache,
			stageTable:  tbl,
			logs:        NewAsyncCache[int, string](),
			bridges:     NewAsyncCache[int, []gitlab.PipelineBridge](),
			logViewport: viewport.New(60, 20),
		},
	}
}

func TestUpdateStageTable_MatrixAlwaysExpanded(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// Expected: compile(1) + test group(1) + 3 children + lint(1) + deploy(1) = 7
	if len(m.pipelineView.stageJobRows) != 7 {
		t.Fatalf("expected 7 stageJobRows, got %d", len(m.pipelineView.stageJobRows))
	}
	if len(m.pipelineView.jobRows) != 7 {
		t.Fatalf("expected 7 jobRows, got %d", len(m.pipelineView.jobRows))
	}

	// Row 0: regular job
	if m.pipelineView.stageJobRows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", m.pipelineView.stageJobRows[0].Kind)
	}
	// Row 1: matrix group header
	if m.pipelineView.stageJobRows[1].Kind != rowKindMatrixGroup {
		t.Errorf("row 1: expected rowKindMatrixGroup, got %d", m.pipelineView.stageJobRows[1].Kind)
	}
	if m.pipelineView.stageJobRows[1].BaseName != "test" {
		t.Errorf("row 1: baseName = %q, want test", m.pipelineView.stageJobRows[1].BaseName)
	}

	// Group header should always have expanded icon
	tableRows := m.pipelineView.stageTable.Rows()
	if !strings.Contains(tableRows[1][0], iconTreeExpanded) {
		t.Errorf("group header should contain expanded icon %q, got %q", iconTreeExpanded, tableRows[1][0])
	}
	// Group header should show count
	if !strings.Contains(tableRows[1][0], "[3]") {
		t.Errorf("group header should show [3] count, got %q", tableRows[1][0])
	}
	// Group header should show aggregated failed status
	if !strings.Contains(tableRows[1][2], "FAILED") {
		t.Errorf("group header status should contain FAILED, got %q", tableRows[1][2])
	}

	// Children rows should have tree prefixes
	if !strings.Contains(tableRows[2][0], "├─") {
		t.Errorf("first child should have ├─ prefix, got %q", tableRows[2][0])
	}
	if !strings.Contains(tableRows[3][0], "├─") {
		t.Errorf("middle child should have ├─ prefix, got %q", tableRows[3][0])
	}
	if !strings.Contains(tableRows[4][0], "└─") {
		t.Errorf("last child should have └─ prefix, got %q", tableRows[4][0])
	}

	// Children should have empty stage column
	for i := 2; i <= 4; i++ {
		if tableRows[i][1] != "" {
			t.Errorf("child row %d stage column should be empty, got %q", i, tableRows[i][1])
		}
	}

	// Children's jobRows should map to actual jobs
	if m.pipelineView.jobRows[2].ID != 2 {
		t.Errorf("jobRows[2] ID = %d, want 2", m.pipelineView.jobRows[2].ID)
	}
	if m.pipelineView.jobRows[3].ID != 3 {
		t.Errorf("jobRows[3] ID = %d, want 3", m.pipelineView.jobRows[3].ID)
	}
	if m.pipelineView.jobRows[4].ID != 4 {
		t.Errorf("jobRows[4] ID = %d, want 4", m.pipelineView.jobRows[4].ID)
	}
}

func TestUpdateStageTable_JobRowsMapCorrectlyForLogPreview(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// The group header's jobRows entry should be the first sub-job
	// (so log preview shows something useful)
	if m.pipelineView.jobRows[1].ID != 2 {
		t.Errorf("group header jobRows should map to first sub-job (ID 2), got ID %d",
			m.pipelineView.jobRows[1].ID)
	}
}

func TestStagesPanel_EnterOnRegularJobIsNoOp(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// Cursor on regular job (row 0 = compile)
	m.pipelineView.stageSelected = 0
	m.pipelineView.stageTable.SetCursor(0)

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleStagesPanelKey(enterMsg)
	got := updated.(Model)

	// Should not have changed anything — no matrixExpanded entries
	if len(got.pipelineView.matrixExpanded) != 0 {
		t.Fatalf("Enter on regular job should not create expand entries, got %v", got.pipelineView.matrixExpanded)
	}
}

func TestStagesPanel_EnterOnMatrixGroupIsNoOp(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// Cursor on matrix group header (row 1)
	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleStagesPanelKey(enterMsg)
	got := updated.(Model)

	// Matrix groups are always expanded; Enter should not toggle them
	if len(got.pipelineView.matrixExpanded) != 0 {
		t.Fatalf("Enter on matrix group should not create expand entries, got %v", got.pipelineView.matrixExpanded)
	}
	// Row count should remain unchanged
	if len(got.pipelineView.stageJobRows) != 7 {
		t.Fatalf("expected 7 rows unchanged, got %d", len(got.pipelineView.stageJobRows))
	}
}

func TestStagesPanel_RetryBlockedOnMatrixGroup(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// Cursor on matrix group header (row 1)
	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	retryMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}
	updated, _ := m.handleStagesPanelKey(retryMsg)
	got := updated.(Model)

	if got.pipelineView.retryConfirm.active {
		t.Fatal("R on matrix group header should not open retry modal")
	}
	if !strings.Contains(got.status, "Select an individual job") {
		t.Errorf("expected hint message, got status=%q", got.status)
	}
}

func TestStagesPanel_CancelBlockedOnMatrixGroup(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	cancelMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")}
	updated, _ := m.handleStagesPanelKey(cancelMsg)
	got := updated.(Model)

	if !strings.Contains(got.status, "Select an individual job") {
		t.Errorf("C on matrix group should show hint, got status=%q", got.status)
	}
}

func TestStagesPanel_PlayBlockedOnMatrixGroup(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	playMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")}
	updated, _ := m.handleStagesPanelKey(playMsg)
	got := updated.(Model)

	if !strings.Contains(got.status, "Select an individual job") {
		t.Errorf("P on matrix group should show hint, got status=%q", got.status)
	}
}

func TestStagesPanel_RetryAllowedOnMatrixChild(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// Move to a child row (row 3 = "test: [aws, backup]" with failed status)
	m.pipelineView.stageSelected = 3
	m.pipelineView.stageTable.SetCursor(3)

	retryMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}
	updated, _ := m.handleStagesPanelKey(retryMsg)
	got := updated.(Model)

	// Retry modal should open for the child job
	if !got.pipelineView.retryConfirm.active {
		t.Fatal("R on matrix child should open retry modal")
	}
	if got.pipelineView.retryConfirm.jobID != 3 {
		t.Errorf("expected retry for job ID 3, got %d", got.pipelineView.retryConfirm.jobID)
	}
}

func TestResetCaches_PreservesMatrixExpanded(t *testing.T) {
	m := newMatrixPipelineModel()
	m.pipelineView.matrixExpanded = map[string]bool{
		"bridge:100": true,
		"bridge:200": true,
	}
	m.updateStageTable()

	// Reset caches (as happens during auto-refresh)
	m.pipelineView.resetCaches()

	// matrixExpanded should survive (used for bridges)
	if len(m.pipelineView.matrixExpanded) != 2 {
		t.Fatalf("expected matrixExpanded to survive resetCaches, got %d entries",
			len(m.pipelineView.matrixExpanded))
	}

	// But jobRows and stageJobRows should be cleared
	if m.pipelineView.jobRows != nil {
		t.Error("jobRows should be cleared after resetCaches")
	}
	if m.pipelineView.stageJobRows != nil {
		t.Error("stageJobRows should be cleared after resetCaches")
	}
}

func TestSelectedStageJobRow_OutOfBounds(t *testing.T) {
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// Out of bounds
	m.pipelineView.stageSelected = 999
	if row := m.selectedStageJobRow(); row != nil {
		t.Fatal("expected nil for out-of-bounds index")
	}

	// Negative
	m.pipelineView.stageSelected = -1
	if row := m.selectedStageJobRow(); row != nil {
		t.Fatal("expected nil for negative index")
	}

	// Valid
	m.pipelineView.stageSelected = 0
	if row := m.selectedStageJobRow(); row == nil {
		t.Fatal("expected non-nil for valid index")
	}
}

func TestUpdateStageTable_NoMatrixJobs_BackwardsCompatible(t *testing.T) {
	// Ensure non-matrix pipelines still work exactly as before
	stagesCache := NewAsyncCache[int, []gitlab.PipelineStage]()
	stagesCache.Set(10, []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "test", Status: "success"},
	})
	jobsCache := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobsCache.Set(10, []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "unit-test", Stage: "test", Status: "success"},
		{ID: 3, Name: "lint", Stage: "test", Status: "success"},
	})

	tbl := table.New(table.WithColumns(stageTableColumns(56)), table.WithHeight(10))

	m := Model{
		pipelineView: pipelineViewState{
			project:     gitlab.ProjectNode{ID: 1},
			pipelines:   []gitlab.PipelineSummary{{ID: 10}},
			selected:    0,
			stages:      stagesCache,
			jobs:        jobsCache,
			stageTable:  tbl,
			logs:        NewAsyncCache[int, string](),
			bridges:     NewAsyncCache[int, []gitlab.PipelineBridge](),
			logViewport: viewport.New(60, 20),
		},
	}
	m.updateStageTable()

	// All rows should be regular jobs
	for i, row := range m.pipelineView.stageJobRows {
		if row.Kind != rowKindJob {
			t.Errorf("row %d: expected rowKindJob for non-matrix pipeline, got %d", i, row.Kind)
		}
	}

	// jobRows should map 1:1 to jobs
	if len(m.pipelineView.jobRows) != 3 {
		t.Fatalf("expected 3 jobRows, got %d", len(m.pipelineView.jobRows))
	}
	if m.pipelineView.jobRows[0].Name != "compile" {
		t.Errorf("jobRows[0] = %q, want compile", m.pipelineView.jobRows[0].Name)
	}

	// Table rows should show job names directly (no tree icons)
	tableRows := m.pipelineView.stageTable.Rows()
	if strings.Contains(tableRows[0][0], iconTreeCollapsed) || strings.Contains(tableRows[0][0], iconTreeExpanded) {
		t.Errorf("non-matrix job should not have tree icons: %q", tableRows[0][0])
	}

	// Stage column should appear on first job of each stage, empty on rest
	if tableRows[0][1] != "build" {
		t.Errorf("first build job stage col = %q, want 'build'", tableRows[0][1])
	}
	if tableRows[1][1] != "test" {
		t.Errorf("first test job stage col = %q, want 'test'", tableRows[1][1])
	}
	if tableRows[2][1] != "" {
		t.Errorf("second test job stage col should be empty, got %q", tableRows[2][1])
	}
}

func TestBuildStageJobRows_BridgeCollapsed(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:     100,
			Name:   "trigger:child",
			Stage:  "build",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:     2000,
				Status: "success",
			},
		},
	}

	rows := buildStageJobRows(stages, jobs, bridges, nil, nil)

	// compile(1) + bridge(1) = 2
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", rows[0].Kind)
	}
	if rows[1].Kind != rowKindBridge {
		t.Errorf("row 1: expected rowKindBridge, got %d", rows[1].Kind)
	}
	if rows[1].Bridge == nil {
		t.Fatal("row 1: expected Bridge to be set")
	}
	if rows[1].Bridge.Name != "trigger:child" {
		t.Errorf("row 1: bridge name = %q, want trigger:child", rows[1].Bridge.Name)
	}
	if rows[1].GroupKey != "bridge:100" {
		t.Errorf("row 1: groupKey = %q, want bridge:100", rows[1].GroupKey)
	}
	if rows[1].Stage != "build" {
		t.Errorf("row 1: stage = %q, want build", rows[1].Stage)
	}
}

func TestBuildStageJobRows_BridgeExpanded(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:     100,
			Name:   "trigger:child",
			Stage:  "build",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:     2000,
				Status: "success",
			},
		},
	}

	expanded := map[string]bool{"bridge:100": true}
	rows := buildStageJobRows(stages, jobs, bridges, expanded, nil)

	// compile(1) + bridge header(1) + bridge child(1) = 3
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[1].Kind != rowKindBridge || rows[1].IsLast {
		t.Errorf("row 1: expected bridge header (IsLast=false), got kind=%d IsLast=%v", rows[1].Kind, rows[1].IsLast)
	}
	if rows[2].Kind != rowKindBridge || !rows[2].IsLast {
		t.Errorf("row 2: expected bridge child (IsLast=true), got kind=%d IsLast=%v", rows[2].Kind, rows[2].IsLast)
	}
	if rows[2].Status != "success" {
		t.Errorf("row 2: child status = %q, want success", rows[2].Status)
	}
}

func TestBuildStageJobRows_BridgeNoDownstream(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:     200,
			Name:   "trigger:nochild",
			Stage:  "build",
			Status: "created",
		},
	}

	// Even when expanded, no child row since no downstream pipeline
	expanded := map[string]bool{"bridge:200": true}
	rows := buildStageJobRows(stages, jobs, bridges, expanded, nil)

	// compile(1) + bridge header(1) = 2 (no child because no downstream)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1].Kind != rowKindBridge {
		t.Errorf("row 1: expected rowKindBridge, got %d", rows[1].Kind)
	}
}

func TestBuildStageJobRows_BridgeInCorrectStage(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "deploy", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "push", Stage: "deploy", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:                 100,
			Name:               "trigger:deploy",
			Stage:              "deploy",
			Status:             "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{ID: 3000, Status: "success"},
		},
	}

	rows := buildStageJobRows(stages, jobs, bridges, nil, nil)

	// compile(1) + push(1) + bridge(1) = 3
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Bridge should be in deploy stage, after "push"
	if rows[0].Stage != "build" {
		t.Errorf("row 0: stage = %q, want build", rows[0].Stage)
	}
	if rows[1].Stage != "deploy" {
		t.Errorf("row 1: stage = %q, want deploy", rows[1].Stage)
	}
	if rows[2].Kind != rowKindBridge || rows[2].Stage != "deploy" {
		t.Errorf("row 2: expected bridge in deploy stage, got kind=%d stage=%q", rows[2].Kind, rows[2].Stage)
	}
}

func TestStagesPanel_EnterTogglesBridge(t *testing.T) {
	m := newBridgePipelineModel()
	m.updateStageTable()

	// Find the bridge row
	bridgeIdx := -1
	for i, row := range m.pipelineView.stageJobRows {
		if row.Kind == rowKindBridge {
			bridgeIdx = i
			break
		}
	}
	if bridgeIdx == -1 {
		t.Fatal("no bridge row found")
	}

	initialRowCount := len(m.pipelineView.stageJobRows)

	// Move cursor to bridge row and press Enter to expand
	m.pipelineView.stageSelected = bridgeIdx
	m.pipelineView.stageTable.SetCursor(bridgeIdx)

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleStagesPanelKey(enterMsg)
	got := updated.(Model)

	groupKey := fmt.Sprintf("bridge:%d", 100)
	if !got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("Enter should expand the bridge")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount+1 {
		t.Fatalf("after expand: expected %d rows, got %d", initialRowCount+1, len(got.pipelineView.stageJobRows))
	}

	// Press Enter again to collapse
	got.pipelineView.stageSelected = bridgeIdx
	got.pipelineView.stageTable.SetCursor(bridgeIdx)
	updated, _ = got.handleStagesPanelKey(enterMsg)
	got = updated.(Model)

	if got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("second Enter should collapse the bridge")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount {
		t.Fatalf("after collapse: expected %d rows, got %d", initialRowCount, len(got.pipelineView.stageJobRows))
	}
}

func TestBuildStageJobRows_BridgeOnlyStage(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "trigger", Status: "success"},
	}
	// Only the build stage has regular jobs; trigger has none
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:     300,
			Name:   "downstream",
			Stage:  "trigger",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:     5000,
				Status: "success",
			},
		},
	}

	rows := buildStageJobRows(stages, jobs, bridges, nil, nil)

	// compile(1) + bridge(1) = 2
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Kind != rowKindJob || rows[0].Job.Name != "compile" {
		t.Errorf("row 0: expected compile job, got kind=%d", rows[0].Kind)
	}
	if rows[1].Kind != rowKindBridge || rows[1].Bridge.Name != "downstream" {
		t.Errorf("row 1: expected bridge 'downstream', got kind=%d", rows[1].Kind)
	}
	if rows[1].Stage != "trigger" {
		t.Errorf("row 1: stage = %q, want trigger", rows[1].Stage)
	}
}

func TestUpdateStageTable_BridgeOnlyStageInjected(t *testing.T) {
	// Simulates a real pipeline where "build" stage has only a bridge trigger
	// and no regular jobs. PipelineStages (built from jobs) won't include "build",
	// but updateStageTable should inject it from the bridges cache.
	stagesCache := NewAsyncCache[int, []gitlab.PipelineStage]()
	stagesCache.Set(10, []gitlab.PipelineStage{
		{Name: "prebuild", Status: "success"},
		// "build" stage is missing — no regular jobs belong to it
	})

	jobsCache := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobsCache.Set(10, []gitlab.PipelineJob{
		{ID: 1, Name: "prepare:matrix", Stage: "prebuild", Status: "success"},
		{ID: 2, Name: "terraform:validate", Stage: "prebuild", Status: "success"},
	})

	bridgesCache := NewAsyncCache[int, []gitlab.PipelineBridge]()
	bridgesCache.Set(10, []gitlab.PipelineBridge{
		{
			ID:     100,
			Name:   "trigger:matrix-plan",
			Stage:  "build",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:     2000,
				Status: "success",
			},
		},
	})

	tbl := table.New(table.WithColumns(stageTableColumns(56)), table.WithHeight(10))

	m := Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		pipelineView: pipelineViewState{
			project:     gitlab.ProjectNode{ID: 1},
			pipelines:   []gitlab.PipelineSummary{{ID: 10, Ref: "main", Status: "success"}},
			selected:    0,
			stages:      stagesCache,
			jobs:        jobsCache,
			stageTable:  tbl,
			logs:        NewAsyncCache[int, string](),
			bridges:     bridgesCache,
			logViewport: viewport.New(60, 20),
		},
	}
	m.updateStageTable()

	// Should have: prepare:matrix(1) + terraform:validate(1) + bridge(1) = 3
	if len(m.pipelineView.stageJobRows) != 3 {
		t.Fatalf("expected 3 stageJobRows, got %d", len(m.pipelineView.stageJobRows))
	}

	// First two are regular jobs in prebuild
	if m.pipelineView.stageJobRows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", m.pipelineView.stageJobRows[0].Kind)
	}
	if m.pipelineView.stageJobRows[1].Kind != rowKindJob {
		t.Errorf("row 1: expected rowKindJob, got %d", m.pipelineView.stageJobRows[1].Kind)
	}

	// Last row is the bridge in the injected "build" stage
	if m.pipelineView.stageJobRows[2].Kind != rowKindBridge {
		t.Errorf("row 2: expected rowKindBridge, got %d", m.pipelineView.stageJobRows[2].Kind)
	}
	if m.pipelineView.stageJobRows[2].Stage != "build" {
		t.Errorf("row 2: stage = %q, want build", m.pipelineView.stageJobRows[2].Stage)
	}
	if m.pipelineView.stageJobRows[2].Bridge.Name != "trigger:matrix-plan" {
		t.Errorf("row 2: bridge name = %q, want trigger:matrix-plan", m.pipelineView.stageJobRows[2].Bridge.Name)
	}

	// Check the table row shows the bridge stage
	tableRows := m.pipelineView.stageTable.Rows()
	if tableRows[2][1] != "build" {
		t.Errorf("bridge row stage column = %q, want build", tableRows[2][1])
	}
}

func TestPipelineView_EnterTogglesBridge(t *testing.T) {
	m := newBridgePipelineModel()
	m.mode = modePipelines
	m.pipelineView.focus = pipelineFocusStages
	m.updateStageTable()

	// Find the bridge row
	bridgeIdx := -1
	for i, row := range m.pipelineView.stageJobRows {
		if row.Kind == rowKindBridge {
			bridgeIdx = i
			break
		}
	}
	if bridgeIdx == -1 {
		t.Fatal("no bridge row found")
	}

	initialRowCount := len(m.pipelineView.stageJobRows)

	// Move cursor to bridge row and press Enter to expand
	m.pipelineView.stageSelected = bridgeIdx
	m.pipelineView.stageTable.SetCursor(bridgeIdx)

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handlePipelineViewKey(enterMsg)
	got := updated.(Model)

	groupKey := fmt.Sprintf("bridge:%d", 100)
	if !got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("Enter should expand the bridge in old pipeline mode")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount+1 {
		t.Fatalf("after expand: expected %d rows, got %d", initialRowCount+1, len(got.pipelineView.stageJobRows))
	}

	// Press Enter again to collapse
	got.pipelineView.stageSelected = bridgeIdx
	got.pipelineView.stageTable.SetCursor(bridgeIdx)
	updated, _ = got.handlePipelineViewKey(enterMsg)
	got = updated.(Model)

	if got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("second Enter should collapse the bridge")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount {
		t.Fatalf("after collapse: expected %d rows, got %d", initialRowCount, len(got.pipelineView.stageJobRows))
	}
}

func TestBuildStageJobRows_BridgeExpandedWithChildJobs(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:     100,
			Name:   "trigger:child",
			Stage:  "build",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:        2000,
				ProjectID: 42,
				Status:    "success",
			},
		},
	}

	expanded := map[string]bool{"bridge:100": true}
	childJobs := map[int][]gitlab.PipelineJob{
		2000: {
			{ID: 50, Name: "child-build", Stage: "build", Status: "success"},
			{ID: 51, Name: "child-test", Stage: "test", Status: "failed"},
			{ID: 52, Name: "child-deploy", Stage: "deploy", Status: "pending"},
		},
	}

	rows := buildStageJobRows(stages, jobs, bridges, expanded, childJobs)

	// compile(1) + bridge header(1) + 3 child jobs = 5
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}

	// Row 0: regular job
	if rows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", rows[0].Kind)
	}

	// Row 1: bridge header
	if rows[1].Kind != rowKindBridge {
		t.Errorf("row 1: expected rowKindBridge, got %d", rows[1].Kind)
	}

	// Rows 2-4: bridge child jobs
	for i := 2; i <= 4; i++ {
		if rows[i].Kind != rowKindBridgeChild {
			t.Errorf("row %d: expected rowKindBridgeChild, got %d", i, rows[i].Kind)
		}
		if rows[i].Job == nil {
			t.Errorf("row %d: expected Job to be set", i)
		}
		if rows[i].ChildProjectID != 42 {
			t.Errorf("row %d: ChildProjectID = %d, want 42", i, rows[i].ChildProjectID)
		}
	}

	// Check specific child jobs
	if rows[2].Job.Name != "child-build" {
		t.Errorf("row 2: job name = %q, want child-build", rows[2].Job.Name)
	}
	if rows[3].Job.Name != "child-test" {
		t.Errorf("row 3: job name = %q, want child-test", rows[3].Job.Name)
	}
	if rows[4].Job.Name != "child-deploy" {
		t.Errorf("row 4: job name = %q, want child-deploy", rows[4].Job.Name)
	}

	// Check IsLast
	if rows[2].IsLast {
		t.Error("row 2 should not be IsLast")
	}
	if rows[3].IsLast {
		t.Error("row 3 should not be IsLast")
	}
	if !rows[4].IsLast {
		t.Error("row 4 should be IsLast")
	}

	// Check statuses
	if rows[2].Status != "success" {
		t.Errorf("row 2: status = %q, want success", rows[2].Status)
	}
	if rows[3].Status != "failed" {
		t.Errorf("row 3: status = %q, want failed", rows[3].Status)
	}
	if rows[4].Status != "pending" {
		t.Errorf("row 4: status = %q, want pending", rows[4].Status)
	}
}

func TestBuildStageJobRows_BridgeExpandedNoChildJobsYet(t *testing.T) {
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
	}
	bridges := []gitlab.PipelineBridge{
		{
			ID:     100,
			Name:   "trigger:child",
			Stage:  "build",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:        2000,
				ProjectID: 42,
				Status:    "running",
			},
		},
	}

	expanded := map[string]bool{"bridge:100": true}
	// No child jobs loaded yet — empty map
	rows := buildStageJobRows(stages, jobs, bridges, expanded, nil)

	// compile(1) + bridge header(1) + placeholder(1) = 3 (same as old behavior)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Placeholder should be rowKindBridge with IsLast=true
	if rows[2].Kind != rowKindBridge || !rows[2].IsLast {
		t.Errorf("row 2: expected placeholder (rowKindBridge, IsLast=true), got kind=%d IsLast=%v", rows[2].Kind, rows[2].IsLast)
	}
}

// newBridgePipelineModel creates a Model with bridge jobs for testing.
func newBridgePipelineModel() Model {
	stagesCache := NewAsyncCache[int, []gitlab.PipelineStage]()
	stagesCache.Set(10, []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "test", Status: "success"},
	})

	jobsCache := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobsCache.Set(10, []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "unit-test", Stage: "test", Status: "success"},
	})

	bridgesCache := NewAsyncCache[int, []gitlab.PipelineBridge]()
	bridgesCache.Set(10, []gitlab.PipelineBridge{
		{
			ID:     100,
			Name:   "trigger:child",
			Stage:  "build",
			Status: "success",
			DownstreamPipeline: &gitlab.PipelineBridgeDownstream{
				ID:     2000,
				Status: "success",
			},
		},
	})

	tbl := table.New(
		table.WithColumns(stageTableColumns(56)),
		table.WithFocused(false),
		table.WithHeight(10),
	)

	return Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		pipelineView: pipelineViewState{
			project:     gitlab.ProjectNode{ID: 1},
			pipelines:   []gitlab.PipelineSummary{{ID: 10, Ref: "main", Status: "success"}},
			selected:    0,
			stages:      stagesCache,
			jobs:        jobsCache,
			stageTable:  tbl,
			logs:        NewAsyncCache[int, string](),
			bridges:     bridgesCache,
			childJobs:   NewAsyncCache[int, []gitlab.PipelineJob](),
			logViewport: viewport.New(60, 20),
		},
	}
}
