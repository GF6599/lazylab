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

// TestParseMatrixName_Variants: job names split into base and matrix variables only for real matrix syntax.
// Given job names with and without the "name: [vars]" shape, when each is parsed, then matrix names yield
// their base name and variable list while everything else passes through unchanged with isMatrix false.
// Why it matters: a false positive groups unrelated jobs under a bogus header, and a false negative floods
// the stages panel with one row per matrix variant.
func TestParseMatrixName_Variants(t *testing.T) {
	// Given: matrix-shaped and non-matrix job names
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
			// When: the job name is parsed
			baseName, vars, isMatrix := parseMatrixName(tt.input)

			// Then: base name, variables, and the matrix flag all match
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

// TestBuildStageJobRows_NoMatrix: plain jobs produce one plain row each, in stage order.
// Given two stages with three non-matrix jobs, when rows are built, then there are exactly three
// rowKindJob rows, each carrying its job and stage.
// Why it matters: the common non-matrix pipeline must not grow headers or lose rows, or every ordinary
// project's stages panel misrenders.
func TestBuildStageJobRows_NoMatrix(t *testing.T) {
	// Given: two stages with plain jobs only
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "test", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "unit-test", Stage: "test", Status: "success"},
		{ID: 3, Name: "lint", Stage: "test", Status: "success"},
	}

	// When: the rows are built
	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	// Then: each job yields one plain row with its job and stage attached
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

// TestBuildStageJobRows_MatrixAlwaysExpanded: matrix jobs render as a header plus all children with no expand state needed.
// Given three "test: [...]" variants and a nil expanded map, when rows are built, then a rowKindMatrixGroup
// header with the aggregated failed status precedes three child rows whose vars are parsed and where only
// the final child is IsLast.
// Why it matters: children hidden behind a collapse state would bury the one failed variant, and a wrong
// IsLast breaks the tree glyphs that show where the group ends.
func TestBuildStageJobRows_MatrixAlwaysExpanded(t *testing.T) {
	// Given: one stage whose jobs are three matrix variants
	stages := []gitlab.PipelineStage{
		{Name: "test", Status: "failed"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "test: [aws, monitoring]", Stage: "test", Status: "success"},
		{ID: 2, Name: "test: [aws, backup]", Stage: "test", Status: "failed"},
		{ID: 3, Name: "test: [gcp, monitoring]", Stage: "test", Status: "success"},
	}

	// When: rows are built with no expanded state at all
	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	// Then: the children are emitted anyway, 1 header + 3 children = 4
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

	// And: only the final child is marked IsLast
	if rows[1].IsLast {
		t.Error("row 1 should not be IsLast")
	}
	if rows[2].IsLast {
		t.Error("row 2 should not be IsLast")
	}
	if !rows[3].IsLast {
		t.Error("row 3 should be IsLast")
	}

	// And: the child rows carry their parsed matrix variables
	if rows[1].Vars != "aws, monitoring" {
		t.Errorf("row 1 vars = %q, want 'aws, monitoring'", rows[1].Vars)
	}
}

// TestBuildStageJobRows_Mixed: matrix groups interleave with plain jobs in stage and job order.
// Given plain jobs around a two-variant matrix, when rows are built, then the order is compile, the matrix
// header, its two children, lint, deploy.
// Why it matters: rows out of order break the row-index-to-job mapping that retry and the log preview rely
// on, so actions would land on a neighboring job.
func TestBuildStageJobRows_Mixed(t *testing.T) {
	// Given: three stages mixing plain jobs and a matrix group
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

	// When: the rows are built
	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	// Then: compile(1) + test group(1) + 2 children + lint(1) + deploy(1) = 6, in order
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

// TestAggregateMatrixStatus: a matrix group reports the most severe of its children's statuses.
// Given child status combinations, when they are aggregated, then failed beats running, running, canceled,
// and manual each beat success, and no children yields unknown.
// Why it matters: a group that shows success while one variant failed hides exactly the failure the user
// opened the pipeline to find.
func TestAggregateMatrixStatus(t *testing.T) {
	// Given: child status combinations with their expected aggregate
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

			// When/Then: aggregation picks the most severe status
			got := aggregateMatrixStatus(jobs)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildStageJobRows_Empty: no stages and no jobs produce no rows.
// Given all-nil inputs, when rows are built, then the result is nil.
// Why it matters: a non-nil placeholder would render ghost rows in an otherwise empty stages panel.
func TestBuildStageJobRows_Empty(t *testing.T) {
	// When/Then: building from nothing yields nil
	rows := buildStageJobRows(nil, nil, nil, nil, nil)
	if rows != nil {
		t.Errorf("expected nil, got %d rows", len(rows))
	}
}

// TestBuildStageJobRows_StageOrder: rows follow the given stage order, not the job list order.
// Given stages listed deploy-then-build with one job each, when rows are built, then the deploy job's row
// comes first.
// Why it matters: the panel must present stages in the order the stage list defines, or it misrepresents
// the pipeline flow users step through.
func TestBuildStageJobRows_StageOrder(t *testing.T) {
	// Given: stages listed deploy-first while the job list starts with build
	stages := []gitlab.PipelineStage{
		{Name: "deploy", Status: "pending"},
		{Name: "build", Status: "success"},
	}
	jobs := []gitlab.PipelineJob{
		{ID: 1, Name: "compile", Stage: "build", Status: "success"},
		{ID: 2, Name: "push", Stage: "deploy", Status: "pending"},
	}

	// When: the rows are built
	rows := buildStageJobRows(stages, jobs, nil, nil, nil)

	// Then: the deploy row precedes the build row, following stage order
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
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

	return withPanelLists(Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		keys:   newKeyMap(),
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
	})
}

// TestUpdateStageTable_MatrixAlwaysExpanded: the stage table renders matrix groups expanded, with tree
// glyphs and a faithful row-to-job mapping.
// Given the matrix pipeline model, when the stage table updates, then seven rows exist, the header shows
// the expanded icon, the [3] count, and the aggregated FAILED status, children carry tree prefixes with an
// empty stage column, and jobRows maps each child index to its real job ID.
// Why it matters: jobRows is the mapping retry and the log preview index into, so a drifted row would show
// one job's log while retrying another.
func TestUpdateStageTable_MatrixAlwaysExpanded(t *testing.T) {
	// Given: the matrix pipeline model
	m := newMatrixPipelineModel()

	// When: the stage table updates
	m.updateStageTable()

	// Then: compile(1) + test group(1) + 3 children + lint(1) + deploy(1) = 7, in both row slices
	if len(m.pipelineView.stageJobRows) != 7 {
		t.Fatalf("expected 7 stageJobRows, got %d", len(m.pipelineView.stageJobRows))
	}
	if len(m.pipelineView.jobRows) != 7 {
		t.Fatalf("expected 7 jobRows, got %d", len(m.pipelineView.jobRows))
	}

	// And: the plain job and the matrix header keep their kinds and base name
	if m.pipelineView.stageJobRows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", m.pipelineView.stageJobRows[0].Kind)
	}
	if m.pipelineView.stageJobRows[1].Kind != rowKindMatrixGroup {
		t.Errorf("row 1: expected rowKindMatrixGroup, got %d", m.pipelineView.stageJobRows[1].Kind)
	}
	if m.pipelineView.stageJobRows[1].BaseName != "test" {
		t.Errorf("row 1: baseName = %q, want test", m.pipelineView.stageJobRows[1].BaseName)
	}

	// And: the header cell renders the expanded icon, child count, and aggregated status
	tableRows := m.pipelineView.stageTable.Rows()
	if !strings.Contains(tableRows[1][0], iconTreeExpanded) {
		t.Errorf("group header should contain expanded icon %q, got %q", iconTreeExpanded, tableRows[1][0])
	}
	if !strings.Contains(tableRows[1][0], "[3]") {
		t.Errorf("group header should show [3] count, got %q", tableRows[1][0])
	}
	if !strings.Contains(tableRows[1][2], "FAILED") {
		t.Errorf("group header status should contain FAILED, got %q", tableRows[1][2])
	}

	// And: children render tree prefixes with the closing glyph on the last child
	if !strings.Contains(tableRows[2][0], "├─") {
		t.Errorf("first child should have ├─ prefix, got %q", tableRows[2][0])
	}
	if !strings.Contains(tableRows[3][0], "├─") {
		t.Errorf("middle child should have ├─ prefix, got %q", tableRows[3][0])
	}
	if !strings.Contains(tableRows[4][0], "└─") {
		t.Errorf("last child should have └─ prefix, got %q", tableRows[4][0])
	}

	// And: children leave the stage column empty
	for i := 2; i <= 4; i++ {
		if tableRows[i][1] != "" {
			t.Errorf("child row %d stage column should be empty, got %q", i, tableRows[i][1])
		}
	}

	// And: each child's jobRows entry maps to its actual job
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

// TestUpdateStageTable_JobRowsMapCorrectlyForLogPreview: a matrix header maps to its first child job.
// Given the matrix pipeline model, when the stage table updates, then the header row's jobRows entry is
// the first variant's job.
// Why it matters: the log pane previews jobRows for the cursor row, so an unmapped header would blank the
// log pane whenever the cursor rests on a group.
func TestUpdateStageTable_JobRowsMapCorrectlyForLogPreview(t *testing.T) {
	// Given: the matrix pipeline model
	m := newMatrixPipelineModel()

	// When: the stage table updates
	m.updateStageTable()

	// Then: the group header's jobRows entry is the first sub-job, so the log preview shows something useful
	if m.pipelineView.jobRows[1].ID != 2 {
		t.Errorf("group header jobRows should map to first sub-job (ID 2), got ID %d",
			m.pipelineView.jobRows[1].ID)
	}
}

// TestStagesPanel_EnterOnRegularJobIsNoOp: enter on a plain job row toggles nothing.
// Given the cursor on a regular job, when enter is pressed, then no matrixExpanded entries appear.
// Why it matters: phantom expand state on plain rows would leak into the shared toggle map and change how
// later bridge rows render.
func TestStagesPanel_EnterOnRegularJobIsNoOp(t *testing.T) {
	// Given: the cursor on a regular job (row 0 = compile)
	m := newMatrixPipelineModel()
	m.updateStageTable()
	m.pipelineView.stageSelected = 0
	m.pipelineView.stageTable.SetCursor(0)

	// When: enter is pressed
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleStagesPanelKey(enterMsg)
	got := updated.(Model)

	// Then: no expand entries are created
	if len(got.pipelineView.matrixExpanded) != 0 {
		t.Fatalf("Enter on regular job should not create expand entries, got %v", got.pipelineView.matrixExpanded)
	}
}

// TestStagesPanel_EnterOnMatrixGroupIsNoOp: enter cannot collapse a matrix group header.
// Given the cursor on the matrix group header, when enter is pressed, then no expand entries appear and
// the row count is unchanged.
// Why it matters: matrix groups are always expanded by design, and a toggle here would hide failing
// variants behind a collapse state users cannot see.
func TestStagesPanel_EnterOnMatrixGroupIsNoOp(t *testing.T) {
	// Given: the cursor on the matrix group header (row 1)
	m := newMatrixPipelineModel()
	m.updateStageTable()
	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	// When: enter is pressed
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleStagesPanelKey(enterMsg)
	got := updated.(Model)

	// Then: nothing toggles and the rows are unchanged
	if len(got.pipelineView.matrixExpanded) != 0 {
		t.Fatalf("Enter on matrix group should not create expand entries, got %v", got.pipelineView.matrixExpanded)
	}
	if len(got.pipelineView.stageJobRows) != 7 {
		t.Fatalf("expected 7 rows unchanged, got %d", len(got.pipelineView.stageJobRows))
	}
}

// TestStagesPanel_RetryBlockedOnMatrixGroup: R on a matrix header is refused with a hint.
// Given the cursor on the matrix group header, when R is pressed, then no retry modal opens and the status
// tells the user to select an individual job.
// Why it matters: a header aggregates several jobs, so a retry dispatched from it would pick one variant
// arbitrarily while the user believes they retried the whole group.
func TestStagesPanel_RetryBlockedOnMatrixGroup(t *testing.T) {
	// Given: the cursor on the matrix group header (row 1)
	m := newMatrixPipelineModel()
	m.updateStageTable()
	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	// When: R is pressed
	retryMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}
	updated, _ := m.handleStagesPanelKey(retryMsg)
	got := updated.(Model)

	// Then: no modal opens and the hint names the required action
	if got.pipelineView.retryConfirm.active {
		t.Fatal("R on matrix group header should not open retry modal")
	}
	if !strings.Contains(got.status, "Select an individual job") {
		t.Errorf("expected hint message, got status=%q", got.status)
	}
}

// TestStagesPanel_CancelBlockedOnMatrixGroup: C on a matrix header is refused with a hint.
// Given the cursor on the matrix group header, when C is pressed, then the status tells the user to select
// an individual job.
// Why it matters: canceling an arbitrary variant instead of the intended one kills a healthy job and
// leaves the stuck one running.
func TestStagesPanel_CancelBlockedOnMatrixGroup(t *testing.T) {
	// Given: the cursor on the matrix group header
	m := newMatrixPipelineModel()
	m.updateStageTable()
	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	// When: C is pressed
	cancelMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")}
	updated, _ := m.handleStagesPanelKey(cancelMsg)
	got := updated.(Model)

	// Then: the hint names the required action
	if !strings.Contains(got.status, "Select an individual job") {
		t.Errorf("C on matrix group should show hint, got status=%q", got.status)
	}
}

// TestStagesPanel_PlayBlockedOnMatrixGroup: P on a matrix header is refused with a hint.
// Given the cursor on the matrix group header, when P is pressed, then the status tells the user to select
// an individual job.
// Why it matters: playing an arbitrary variant would start a manual job the user never chose to run.
func TestStagesPanel_PlayBlockedOnMatrixGroup(t *testing.T) {
	// Given: the cursor on the matrix group header
	m := newMatrixPipelineModel()
	m.updateStageTable()
	m.pipelineView.stageSelected = 1
	m.pipelineView.stageTable.SetCursor(1)

	// When: P is pressed
	playMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")}
	updated, _ := m.handleStagesPanelKey(playMsg)
	got := updated.(Model)

	// Then: the hint names the required action
	if !strings.Contains(got.status, "Select an individual job") {
		t.Errorf("P on matrix group should show hint, got status=%q", got.status)
	}
}

// TestStagesPanel_RetryAllowedOnMatrixChild: R on a matrix child opens the retry modal for that exact job.
// Given the cursor on the failed "test: [aws, backup]" child row, when R is pressed, then the retry modal
// opens targeting job ID 3.
// Why it matters: retrying one variant is the whole point of expanding a group, and a wrong job ID would
// re-run a passing variant while the failed one stays red.
func TestStagesPanel_RetryAllowedOnMatrixChild(t *testing.T) {
	// Given: the cursor on a child row (row 3 = "test: [aws, backup]" with failed status)
	m := newMatrixPipelineModel()
	m.updateStageTable()
	m.pipelineView.stageSelected = 3
	m.pipelineView.stageTable.SetCursor(3)

	// When: R is pressed
	retryMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}
	updated, _ := m.handleStagesPanelKey(retryMsg)
	got := updated.(Model)

	// Then: the retry modal opens for exactly that child job
	if !got.pipelineView.retryConfirm.active {
		t.Fatal("R on matrix child should open retry modal")
	}
	if got.pipelineView.retryConfirm.jobID != 3 {
		t.Errorf("expected retry for job ID 3, got %d", got.pipelineView.retryConfirm.jobID)
	}
}

// TestResetCaches_PreservesMatrixExpanded: cache resets keep bridge expand state but drop derived rows.
// Given expanded bridge entries and built rows, when resetCaches runs, then matrixExpanded survives while
// jobRows and stageJobRows are cleared.
// Why it matters: auto-refresh resets caches every few seconds, so losing expand state would snap open
// downstream pipelines shut mid-read, while stale rows would keep mapping actions to jobs that no longer exist.
func TestResetCaches_PreservesMatrixExpanded(t *testing.T) {
	// Given: two expanded bridges and a built stage table
	m := newMatrixPipelineModel()
	m.pipelineView.matrixExpanded = map[string]bool{
		"bridge:100": true,
		"bridge:200": true,
	}
	m.updateStageTable()

	// When: the caches are reset, as happens during auto-refresh
	m.pipelineView.resetCaches()

	// Then: the bridge expand state survives
	if len(m.pipelineView.matrixExpanded) != 2 {
		t.Fatalf("expected matrixExpanded to survive resetCaches, got %d entries",
			len(m.pipelineView.matrixExpanded))
	}

	// And: the derived row slices are cleared
	if m.pipelineView.jobRows != nil {
		t.Error("jobRows should be cleared after resetCaches")
	}
	if m.pipelineView.stageJobRows != nil {
		t.Error("stageJobRows should be cleared after resetCaches")
	}
}

// TestSelectedStageJobRow_OutOfBounds: row lookup returns nil outside the valid range.
// Given a built stage table, when the selection index is 999, -1, and 0, then the out-of-range lookups
// return nil and the valid one returns a row.
// Why it matters: auto-refresh can shrink the row list under the cursor, and an unguarded index would
// panic the key handler on the next action.
func TestSelectedStageJobRow_OutOfBounds(t *testing.T) {
	// Given: a built stage table
	m := newMatrixPipelineModel()
	m.updateStageTable()

	// When/Then: an index past the end resolves to nil
	m.pipelineView.stageSelected = 999
	if row := m.selectedStageJobRow(); row != nil {
		t.Fatal("expected nil for out-of-bounds index")
	}

	// And: a negative index resolves to nil
	m.pipelineView.stageSelected = -1
	if row := m.selectedStageJobRow(); row != nil {
		t.Fatal("expected nil for negative index")
	}

	// And: a valid index resolves to a row
	m.pipelineView.stageSelected = 0
	if row := m.selectedStageJobRow(); row == nil {
		t.Fatal("expected non-nil for valid index")
	}
}

// TestUpdateStageTable_NoMatrixJobs_BackwardsCompatible: pipelines without matrix jobs render flat rows
// with per-stage labels and no tree glyphs.
// Given a plain two-stage pipeline, when the stage table updates, then every row is rowKindJob mapping 1:1
// to the jobs, no tree icons appear, and the stage column labels only the first job of each stage.
// Why it matters: matrix grouping must leave ordinary pipelines rendering unchanged, or every non-matrix
// project pays a visual cost for a feature it does not use.
func TestUpdateStageTable_NoMatrixJobs_BackwardsCompatible(t *testing.T) {
	// Given: a plain two-stage pipeline with no matrix jobs
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
		keys: newKeyMap(),
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

	// When: the stage table updates
	m.updateStageTable()

	// Then: every row is a regular job
	for i, row := range m.pipelineView.stageJobRows {
		if row.Kind != rowKindJob {
			t.Errorf("row %d: expected rowKindJob for non-matrix pipeline, got %d", i, row.Kind)
		}
	}

	// And: jobRows maps 1:1 to the jobs
	if len(m.pipelineView.jobRows) != 3 {
		t.Fatalf("expected 3 jobRows, got %d", len(m.pipelineView.jobRows))
	}
	if m.pipelineView.jobRows[0].Name != "compile" {
		t.Errorf("jobRows[0] = %q, want compile", m.pipelineView.jobRows[0].Name)
	}

	// And: the table shows job names directly, with no tree icons
	tableRows := m.pipelineView.stageTable.Rows()
	if strings.Contains(tableRows[0][0], iconTreeCollapsed) || strings.Contains(tableRows[0][0], iconTreeExpanded) {
		t.Errorf("non-matrix job should not have tree icons: %q", tableRows[0][0])
	}

	// And: the stage column labels only the first job of each stage
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

// TestBuildStageJobRows_BridgeCollapsed: an unexpanded bridge renders as a single header row.
// Given one job and one bridge with a downstream pipeline but no expand state, when rows are built, then
// the bridge contributes exactly one rowKindBridge row carrying its name, group key, and stage.
// Why it matters: the "bridge:<id>" group key is the toggle identity, so a wrong key would make enter
// expand a different trigger than the one selected.
func TestBuildStageJobRows_BridgeCollapsed(t *testing.T) {
	// Given: one plain job plus a bridge with a downstream pipeline
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

	// When: rows are built with no expand state
	rows := buildStageJobRows(stages, jobs, bridges, nil, nil)

	// Then: compile(1) + bridge(1) = 2, with the bridge row carrying its identity
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

// TestBuildStageJobRows_BridgeExpanded: an expanded bridge grows a downstream child row.
// Given a bridge with a downstream pipeline and its key in the expanded map, when rows are built, then a
// child rowKindBridge row with IsLast=true and the downstream status follows the header.
// Why it matters: that child row is the only place the downstream pipeline's status is visible without
// leaving the parent pipeline.
func TestBuildStageJobRows_BridgeExpanded(t *testing.T) {
	// Given: one plain job plus a bridge with a downstream pipeline
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

	// When: rows are built with the bridge marked expanded
	expanded := map[string]bool{"bridge:100": true}
	rows := buildStageJobRows(stages, jobs, bridges, expanded, nil)

	// Then: compile(1) + bridge header(1) + bridge child(1) = 3
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

// TestBuildStageJobRows_BridgeNoDownstream: expanding a bridge without a downstream pipeline adds nothing.
// Given a bridge whose DownstreamPipeline is nil but whose key is expanded, when rows are built, then only
// the header row appears.
// Why it matters: a trigger that has not created its downstream yet must not render a phantom child row
// for the cursor to land on.
func TestBuildStageJobRows_BridgeNoDownstream(t *testing.T) {
	// Given: a bridge with no downstream pipeline
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

	// When: rows are built with the bridge marked expanded anyway
	expanded := map[string]bool{"bridge:200": true}
	rows := buildStageJobRows(stages, jobs, bridges, expanded, nil)

	// Then: compile(1) + bridge header(1) = 2, no child because there is no downstream
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1].Kind != rowKindBridge {
		t.Errorf("row 1: expected rowKindBridge, got %d", rows[1].Kind)
	}
}

// TestBuildStageJobRows_BridgeInCorrectStage: bridges render inside their own stage, after its jobs.
// Given jobs in build and deploy plus a deploy-stage bridge, when rows are built, then the bridge appears
// as the last deploy row rather than attaching to another stage.
// Why it matters: a bridge shown under the wrong stage misleads the user about where in the pipeline flow
// the downstream trigger sits.
func TestBuildStageJobRows_BridgeInCorrectStage(t *testing.T) {
	// Given: jobs in two stages and a bridge belonging to deploy
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

	// When: the rows are built
	rows := buildStageJobRows(stages, jobs, bridges, nil, nil)

	// Then: compile(1) + push(1) + bridge(1) = 3, with the bridge in deploy after "push"
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
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

// TestStagesPanel_EnterTogglesBridge: enter expands a bridge row and enter again collapses it.
// Given the cursor on a bridge row in the stages panel, when enter is pressed twice, then the expanded map
// gains and then loses the bridge key while the row count grows by one and shrinks back.
// Why it matters: this toggle is the only way to peek into downstream pipelines from the panel, and a
// stuck state would either hide them or permanently clutter the table.
func TestStagesPanel_EnterTogglesBridge(t *testing.T) {
	// Given: the cursor on the bridge row
	m := newBridgePipelineModel()
	m.updateStageTable()

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

	m.pipelineView.stageSelected = bridgeIdx
	m.pipelineView.stageTable.SetCursor(bridgeIdx)

	// When: enter is pressed on the bridge
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleStagesPanelKey(enterMsg)
	got := updated.(Model)

	// Then: the bridge expands and its child row appears
	groupKey := fmt.Sprintf("bridge:%d", 100)
	if !got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("Enter should expand the bridge")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount+1 {
		t.Fatalf("after expand: expected %d rows, got %d", initialRowCount+1, len(got.pipelineView.stageJobRows))
	}

	// When: enter is pressed again
	got.pipelineView.stageSelected = bridgeIdx
	got.pipelineView.stageTable.SetCursor(bridgeIdx)
	updated, _ = got.handleStagesPanelKey(enterMsg)
	got = updated.(Model)

	// Then: the bridge collapses back to its original row count
	if got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("second Enter should collapse the bridge")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount {
		t.Fatalf("after collapse: expected %d rows, got %d", initialRowCount, len(got.pipelineView.stageJobRows))
	}
}

// TestBuildStageJobRows_BridgeOnlyStage: a stage whose only member is a bridge still renders it.
// Given a trigger stage with no regular jobs, when rows are built, then the bridge row appears under that
// stage after the other stage's job.
// Why it matters: trigger-only stages are common in multi-project setups, and dropping them would hide the
// deploy trigger from its pipeline entirely.
func TestBuildStageJobRows_BridgeOnlyStage(t *testing.T) {
	// Given: a trigger stage whose only member is a bridge (build has the regular job)
	stages := []gitlab.PipelineStage{
		{Name: "build", Status: "success"},
		{Name: "trigger", Status: "success"},
	}
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

	// When: the rows are built
	rows := buildStageJobRows(stages, jobs, bridges, nil, nil)

	// Then: compile(1) + bridge(1) = 2, with the bridge under its own stage
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

// TestUpdateStageTable_BridgeOnlyStageInjected: a stage that exists only in the bridges cache is injected
// into the table.
// Given a stages cache without "build" and a bridges cache holding a build-stage trigger, when the stage
// table updates, then a third row renders the bridge with "build" in its stage column.
// Why it matters: stage summaries are derived from regular jobs, so without injection a stage containing
// only a trigger silently disappears from the panel.
func TestUpdateStageTable_BridgeOnlyStageInjected(t *testing.T) {
	// Given: a stages cache missing "build" because no regular jobs belong to it,
	// while the bridges cache holds a build-stage trigger
	stagesCache := NewAsyncCache[int, []gitlab.PipelineStage]()
	stagesCache.Set(10, []gitlab.PipelineStage{
		{Name: "prebuild", Status: "success"},
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
		keys:   newKeyMap(),
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

	// When: the stage table updates
	m.updateStageTable()

	// Then: prepare:matrix(1) + terraform:validate(1) + bridge(1) = 3
	if len(m.pipelineView.stageJobRows) != 3 {
		t.Fatalf("expected 3 stageJobRows, got %d", len(m.pipelineView.stageJobRows))
	}

	// And: the first two rows are the regular prebuild jobs
	if m.pipelineView.stageJobRows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", m.pipelineView.stageJobRows[0].Kind)
	}
	if m.pipelineView.stageJobRows[1].Kind != rowKindJob {
		t.Errorf("row 1: expected rowKindJob, got %d", m.pipelineView.stageJobRows[1].Kind)
	}

	// And: the last row is the bridge in the injected "build" stage
	if m.pipelineView.stageJobRows[2].Kind != rowKindBridge {
		t.Errorf("row 2: expected rowKindBridge, got %d", m.pipelineView.stageJobRows[2].Kind)
	}
	if m.pipelineView.stageJobRows[2].Stage != "build" {
		t.Errorf("row 2: stage = %q, want build", m.pipelineView.stageJobRows[2].Stage)
	}
	if m.pipelineView.stageJobRows[2].Bridge.Name != "trigger:matrix-plan" {
		t.Errorf("row 2: bridge name = %q, want trigger:matrix-plan", m.pipelineView.stageJobRows[2].Bridge.Name)
	}

	// And: the rendered table row shows the injected stage name
	tableRows := m.pipelineView.stageTable.Rows()
	if tableRows[2][1] != "build" {
		t.Errorf("bridge row stage column = %q, want build", tableRows[2][1])
	}
}

// TestPipelineView_EnterTogglesBridge: the legacy pipeline view's enter toggle matches the stages panel.
// Given a bridge row selected in modePipelines with stages focus, when enter is pressed twice, then the
// bridge expands and collapses exactly as in the multi-panel handler.
// Why it matters: the standalone pipeline view routes keys through a different handler, and a missed
// branch there would make the same key work in one surface and dead-end in the other.
func TestPipelineView_EnterTogglesBridge(t *testing.T) {
	// Given: the cursor on the bridge row in the legacy pipeline view
	m := newBridgePipelineModel()
	m.mode = modePipelines
	m.pipelineView.focus = pipelineFocusStages
	m.updateStageTable()

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

	m.pipelineView.stageSelected = bridgeIdx
	m.pipelineView.stageTable.SetCursor(bridgeIdx)

	// When: enter is pressed through the legacy handler
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handlePipelineViewKey(enterMsg)
	got := updated.(Model)

	// Then: the bridge expands and its child row appears
	groupKey := fmt.Sprintf("bridge:%d", 100)
	if !got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("Enter should expand the bridge in old pipeline mode")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount+1 {
		t.Fatalf("after expand: expected %d rows, got %d", initialRowCount+1, len(got.pipelineView.stageJobRows))
	}

	// When: enter is pressed again
	got.pipelineView.stageSelected = bridgeIdx
	got.pipelineView.stageTable.SetCursor(bridgeIdx)
	updated, _ = got.handlePipelineViewKey(enterMsg)
	got = updated.(Model)

	// Then: the bridge collapses back to its original row count
	if got.pipelineView.matrixExpanded[groupKey] {
		t.Fatal("second Enter should collapse the bridge")
	}
	if len(got.pipelineView.stageJobRows) != initialRowCount {
		t.Fatalf("after collapse: expected %d rows, got %d", initialRowCount, len(got.pipelineView.stageJobRows))
	}
}

// TestBuildStageJobRows_BridgeExpandedWithChildJobs: loaded downstream jobs render as child rows under
// the bridge.
// Given an expanded bridge whose downstream pipeline has three fetched jobs, when rows are built, then the
// header is followed by three rowKindBridgeChild rows carrying each job, its status, the downstream
// project ID, and IsLast only on the final row.
// Why it matters: ChildProjectID is what routes actions on child jobs to the downstream project, so a
// missing ID would aim them at the parent project instead.
func TestBuildStageJobRows_BridgeExpandedWithChildJobs(t *testing.T) {
	// Given: an expanded bridge whose downstream jobs have been fetched
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

	// When: the rows are built
	rows := buildStageJobRows(stages, jobs, bridges, expanded, childJobs)

	// Then: compile(1) + bridge header(1) + 3 child jobs = 5
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	if rows[0].Kind != rowKindJob {
		t.Errorf("row 0: expected rowKindJob, got %d", rows[0].Kind)
	}
	if rows[1].Kind != rowKindBridge {
		t.Errorf("row 1: expected rowKindBridge, got %d", rows[1].Kind)
	}

	// And: each child row carries its job and the downstream project ID
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

	// And: the child jobs appear in order
	if rows[2].Job.Name != "child-build" {
		t.Errorf("row 2: job name = %q, want child-build", rows[2].Job.Name)
	}
	if rows[3].Job.Name != "child-test" {
		t.Errorf("row 3: job name = %q, want child-test", rows[3].Job.Name)
	}
	if rows[4].Job.Name != "child-deploy" {
		t.Errorf("row 4: job name = %q, want child-deploy", rows[4].Job.Name)
	}

	// And: only the final child is marked IsLast
	if rows[2].IsLast {
		t.Error("row 2 should not be IsLast")
	}
	if rows[3].IsLast {
		t.Error("row 3 should not be IsLast")
	}
	if !rows[4].IsLast {
		t.Error("row 4 should be IsLast")
	}

	// And: each child row reflects its own job status
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

// TestBuildStageJobRows_BridgeExpandedNoChildJobsYet: an expanded bridge shows a placeholder until its
// child jobs load.
// Given an expanded bridge whose downstream jobs have not been fetched, when rows are built, then a single
// rowKindBridge placeholder with IsLast=true follows the header.
// Why it matters: expansion triggers an async fetch, and without the placeholder the group would render as
// mysteriously empty during the load.
func TestBuildStageJobRows_BridgeExpandedNoChildJobsYet(t *testing.T) {
	// Given: an expanded bridge with a running downstream pipeline
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

	// When: rows are built with no child jobs loaded yet
	expanded := map[string]bool{"bridge:100": true}
	rows := buildStageJobRows(stages, jobs, bridges, expanded, nil)

	// Then: compile(1) + bridge header(1) + placeholder(1) = 3
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// And: the placeholder is a rowKindBridge marked IsLast
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

	return withPanelLists(Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		keys:   newKeyMap(),
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
	})
}
