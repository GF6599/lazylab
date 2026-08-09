package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestVisibleProjectsSearch: a search query filters the project list and clearing it restores the page.
// Given three projects, when the query is "api" and then empty, then only api-server is visible first and
// the full page comes back after.
// Why it matters: a filter that failed to reset would lock users into a one-project list after closing
// search.
func TestVisibleProjectsSearch(t *testing.T) {
	// Given: three projects on a ready page
	m := Model{
		allProjects: []gitlab.ProjectNode{
			{Name: "api-server", PathWithNamespace: "team/api-server"},
			{Name: "frontend", PathWithNamespace: "team/frontend"},
			{Name: "infra-tools", PathWithNamespace: "team/infra-tools"},
		},
		opts:       Options{ProjectsPerPage: 10},
		pagesReady: map[int]bool{1: true},
		page:       1,
		projectTab: projectTabAll,
	}

	// When/Then: the query "api" narrows the list to the one match
	m.search.query = "api"
	filtered := m.visibleProjects()
	if len(filtered) != 1 || filtered[0].Name != "api-server" {
		t.Fatalf("search failed, got %#v", filtered)
	}

	// And: clearing the query restores the full page
	m.search.query = ""
	page := m.visibleProjects()
	if len(page) != len(m.allProjects) {
		t.Fatalf("expected %d projects, got %d", len(m.allProjects), len(page))
	}
}

// TestVisibleProjectsSearch_Nested: fuzzy search matches any segment of deeply nested group paths.
// Given projects under paths like org/platform/backend/auth-service, when queries target leaf names,
// middle groups, partial paths, and cross-segment subsequences, then exactly the expected projects match,
// the empty query returns everything, and non-matches return nothing.
// Why it matters: self-hosted GitLab trees nest groups deeply, and a search that only saw the leaf name
// would hide most of an organization's projects.
func TestVisibleProjectsSearch_Nested(t *testing.T) {
	// Given: projects spread across deeply nested group paths
	m := Model{
		allProjects: []gitlab.ProjectNode{
			{Name: "api-server", PathWithNamespace: "team/api-server"},
			{Name: "auth-service", PathWithNamespace: "org/platform/backend/auth-service"},
			{Name: "user-service", PathWithNamespace: "org/platform/backend/user-service"},
			{Name: "web-app", PathWithNamespace: "org/platform/frontend/web-app"},
			{Name: "deploy-tool", PathWithNamespace: "company/org/team/infra/deploy-tool"},
		},
		opts:       Options{ProjectsPerPage: 10},
		pagesReady: map[int]bool{1: true},
		page:       1,
		projectTab: projectTabAll,
	}

	tests := []struct {
		name      string
		query     string
		wantNames []string
	}{
		{
			name:      "leaf project name",
			query:     "auth",
			wantNames: []string{"auth-service"},
		},
		{
			name:      "middle group",
			query:     "platform",
			wantNames: []string{"auth-service", "user-service", "web-app"},
		},
		{
			name:      "partial nested path",
			query:     "org/plat",
			wantNames: []string{"auth-service", "user-service", "web-app"},
		},
		{
			name:      "deep path segment",
			query:     "backend/auth",
			wantNames: []string{"auth-service"},
		},
		{
			name:      "fuzzy cross-segment",
			query:     "opbau",
			wantNames: []string{"auth-service", "user-service"},
		},
		{
			name:      "empty query returns all",
			query:     "",
			wantNames: []string{"api-server", "auth-service", "user-service", "web-app", "deploy-tool"},
		},
		{
			name:      "no match",
			query:     "zzz",
			wantNames: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the query is applied and the visible projects recomputed
			m.search.query = tt.query
			m.invalidateVisibleCache()
			got := m.visibleProjects()

			// Then: exactly the expected projects match
			if len(got) != len(tt.wantNames) {
				names := make([]string, len(got))
				for i, p := range got {
					names[i] = p.Name
				}
				t.Fatalf("query %q: expected %d results %v, got %d %v",
					tt.query, len(tt.wantNames), tt.wantNames, len(got), names)
			}

			gotNames := make(map[string]bool)
			for _, p := range got {
				gotNames[p.Name] = true
			}
			for _, want := range tt.wantNames {
				if !gotNames[want] {
					t.Errorf("query %q: expected %q in results", tt.query, want)
				}
			}
		})
	}
}

// TestHandlePipelineLogLoadedIgnoresStale: a log arriving for a previously selected job caches without
// touching the active preview.
// Given the preview following job 20, when job 10's log response lands, then the preview content stays,
// the stale log is cached for job 10, and its loading flag clears.
// Why it matters: out-of-order responses would otherwise yank the visible log and scroll position over to
// a job the user already navigated away from.
func TestHandlePipelineLogLoadedIgnoresStale(t *testing.T) {
	// Given: the preview following job 20 while jobs 10 and 20 are both loading
	logs := NewAsyncCache[int, string]()
	logs.SetLoading(10)
	logs.SetLoading(20)
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:    gitlab.ProjectNode{ID: 1},
			logJobID:   20,
			logs:       logs,
			logPreview: previewState{content: "current"},
		},
	}

	// When: job 10's log response lands
	msg := pipelineLogLoadedMsg{projectID: 1, jobID: 10, content: "stale"}
	updated, _ := m.handlePipelineLogLoaded(msg)
	got := updated.(Model).pipelineView

	// Then: the preview stays on the current job while the stale log caches and stops loading
	if got.logPreview.content != "current" {
		t.Fatalf("expected preview to stay on current job, got %q", got.logPreview.content)
	}
	if v, ok := got.logs.Get(10); !ok || v != "stale" {
		t.Fatalf("expected stale log to be cached")
	}
	if got.logs.IsLoading(10) {
		t.Fatalf("expected stale log loading to clear")
	}
}

// TestHandlePipelineLogLoadedTruncatesActivePreview: an oversized log is truncated before display and
// caching.
// Given a log body past maxLogSizeBytes for the active job, when its response lands, then the preview
// content, raw text, and cache all hold the truncated form and the preview path survives.
// Why it matters: rendering a multi-megabyte trace as-is stalls every frame, and caching the untruncated
// body would repeat the cost on each revisit.
func TestHandlePipelineLogLoadedTruncatesActivePreview(t *testing.T) {
	// Given: an oversized log body destined for the active job's preview
	content := strings.Repeat("x", maxLogSizeBytes+128)
	want := truncateLogContent(content)
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:       gitlab.ProjectNode{ID: 1},
			logJobID:      10,
			logs:          NewAsyncCache[int, string](),
			logAutoFollow: true,
			logViewport:   viewport.New(80, 20),
			logPreview:    previewState{path: "build"},
		},
	}

	// When: the log response lands
	updated, _ := m.handlePipelineLogLoaded(pipelineLogLoadedMsg{
		projectID: 1,
		jobID:     10,
		content:   content,
	})
	got := updated.(Model).pipelineView

	// Then: preview content, raw text, path, and the cache all hold the truncated form
	if got.logPreview.content != want {
		t.Fatalf("expected truncated preview content, got len=%d want len=%d", len(got.logPreview.content), len(want))
	}
	if got.logPreview.raw != want {
		t.Fatalf("expected truncated preview raw content")
	}
	if got.logPreview.path != "build" {
		t.Fatalf("expected preview path to be preserved, got %q", got.logPreview.path)
	}
	if cached, ok := got.logs.Get(10); !ok || cached != want {
		t.Fatalf("expected truncated log to be cached")
	}
}

// TestQueuePipelineLogPreviewPreservesOffset: re-queuing a cached log with auto-follow off refreshes the
// content without moving the scroll position.
// Given logAutoFollow false, the selected job's log cached, and the log viewport scrolled mid-log, when
// the preview is re-queued for the same job, then the content updates from the cache and the viewport
// offset stays where the user left it.
// Why it matters: refresh ticks re-queue the preview every few seconds, and a reset offset would yank
// users back to the top of a long log the moment they scrolled up to read an earlier failure.
func TestQueuePipelineLogPreviewPreservesOffset(t *testing.T) {
	// Given: a cached log for the selected job, auto-follow off, and the viewport scrolled to line 7
	content := strings.Repeat("line\n", 40)
	stages := NewAsyncCache[int, []gitlab.PipelineStage]()
	stages.Set(10, []gitlab.PipelineStage{{Name: "build"}})
	jobs := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobs.Set(10, []gitlab.PipelineJob{{ID: 100, Name: "build-job", Stage: "build"}})
	logs := NewAsyncCache[int, string]()
	logs.Set(100, content)
	vp := viewport.New(80, 10)
	vp.SetContent(strings.Repeat("old line\n", 40))
	vp.SetYOffset(7)
	m := Model{
		width:  80,
		height: 20,
		pipelineView: pipelineViewState{
			project:       gitlab.ProjectNode{ID: 1},
			pipelines:     []gitlab.PipelineSummary{{ID: 10}},
			selected:      0,
			stages:        stages,
			jobs:          jobs,
			jobRows:       []gitlab.PipelineJob{{ID: 100, Name: "build-job", Stage: "build"}},
			logs:          logs,
			logPreview:    previewState{content: "old", raw: "old"},
			logJobID:      100,
			logAutoFollow: false,
			logViewport:   vp,
		},
	}

	// When: the preview is re-queued
	m.queuePipelineLogPreview()

	// Then: the preview content refreshes from the cache
	if m.pipelineView.logPreview.content != content {
		t.Fatalf("expected log content to update from cache")
	}

	// And: the scroll offset survives the refresh
	if got := m.pipelineView.logViewport.YOffset; got != 7 {
		t.Fatalf("expected viewport offset 7 to survive the refresh, got %d", got)
	}
}

// TestHandleBatchPipelineStatusClearsStaleFlags: batch status results overwrite every stale flag
// combination.
// Given cached states carrying leftover error, empty, and info flags, when a batch result maps project 1
// to empty, 2 to an error, and 3 to a pipeline, then each state carries exactly its new flag with the
// others cleared, and empty results record the all-refs marker.
// Why it matters: a state with two flags set at once renders contradictory icons, showing a project as
// simultaneously failed and pipeline-less.
func TestHandleBatchPipelineStatusClearsStaleFlags(t *testing.T) {
	// Given: cached states with contradictory leftover flags
	statuses := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	statuses.Set(1, pipelineState{err: fmt.Errorf("boom"), ref: "main"})
	statuses.Set(2, pipelineState{empty: true})
	statuses.Set(3, pipelineState{
		err:   fmt.Errorf("old"),
		empty: true,
		info:  gitlab.PipelineSummary{ID: 99},
	})
	m := Model{
		mode:           modeMultiPanel,
		allProjects:    []gitlab.ProjectNode{{ID: 1}, {ID: 2}, {ID: 3}},
		opts:           Options{ProjectsPerPage: 10},
		pagesReady:     map[int]bool{1: true},
		page:           1,
		projectTab:     projectTabAll,
		pipelineStatus: &statuses,
	}

	// When: a batch result delivers a distinct outcome per project
	updated, _ := m.handleBatchPipelineStatus(batchPipelineStatusMsg{
		results: map[int]pipelineStatusResult{
			1: {empty: true},
			2: {err: fmt.Errorf("fetch failed")},
			3: {pipeline: gitlab.PipelineSummary{ID: 42, Status: "success"}},
		},
	})
	got := updated.(Model)

	// Then: each state carries exactly its new flag with the stale ones cleared
	state1, _ := got.pipelineStatus.Get(1)
	if !state1.empty || state1.err != nil || state1.hasInfo || state1.info.ID != 0 {
		t.Fatalf("expected empty state to clear stale error/info, got %#v", state1)
	}
	if state1.ref != pipelineAllRefsRef {
		t.Fatalf("expected batch refresh ref=%q, got %q", pipelineAllRefsRef, state1.ref)
	}

	state2, _ := got.pipelineStatus.Get(2)
	if state2.err == nil || state2.empty || state2.hasInfo || state2.info.ID != 0 {
		t.Fatalf("expected error state to clear stale empty/info flags, got %#v", state2)
	}

	state3, _ := got.pipelineStatus.Get(3)
	if !state3.hasInfo || state3.err != nil || state3.empty || state3.info.ID != 42 {
		t.Fatalf("expected info state to clear stale error/empty flags, got %#v", state3)
	}
}

// TestBatchPipelineStatus_ReportsOnEveryProjectWhenItRunsOutOfTime: a batch that gives up still
// accounts for the projects it never reached.
// Given three projects whose status fetches never answer, when the batch runs out of time, then the
// reply carries an entry for each of the three and each names an error.
// Why it matters: a project left out of the reply keeps the loading flag that queued it, every
// later refresh skips it as already in flight, and its row animates forever while never showing
// a status again.
func TestBatchPipelineStatus_ReportsOnEveryProjectWhenItRunsOutOfTime(t *testing.T) {
	// Given: three projects whose status fetches never answer
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	client := &mockService{
		LatestPipelineFn: func(ctx context.Context, _ int, _ string) (gitlab.PipelineSummary, error) {
			<-release
			return gitlab.PipelineSummary{}, ctx.Err()
		},
	}
	projects := []gitlab.ProjectNode{{ID: 1}, {ID: 2}, {ID: 3}}

	// When: the batch runs out of time
	raw := batchFetchPipelineStatusCmd(context.Background(), client, 20*time.Millisecond, projects, &atomic.Bool{})()
	msg, ok := raw.(batchPipelineStatusMsg)
	if !ok {
		t.Fatalf("expected a batch status message, got %T", raw)
	}

	// Then: every project is accounted for, and as a failure rather than a result
	for _, project := range projects {
		result, reported := msg.results[project.ID]
		if !reported {
			t.Errorf("project %d is missing from the reply, so it stays marked loading and no "+
				"later refresh retries it", project.ID)
			continue
		}
		if result.err == nil {
			t.Errorf("project %d is reported as fetched, but its status never arrived", project.ID)
		}
	}
}

// TestHandlePipelinesLoadedNoPipelinesClearsSubstate: an ErrNoPipelines reload wipes every piece of
// pipeline substate.
// Given a fully populated pipeline view (selection, caches, log preview, test report), when a load result
// arrives with ErrNoPipelines, then pipelines, selection, every cache, the log preview, the test report
// state, and paging all reset to empty.
// Why it matters: switching to a project without pipelines must not keep presenting the previous project's
// stages, logs, and test failures as if they belonged to it.
func TestHandlePipelinesLoadedNoPipelinesClearsSubstate(t *testing.T) {
	// Given: a pipeline view populated across every substate
	logs := NewAsyncCache[int, string]()
	logs.Set(100, "old log")
	stages := NewAsyncCache[int, []gitlab.PipelineStage]()
	stages.Set(10, []gitlab.PipelineStage{{Name: "build"}})
	jobs := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobs.Set(10, []gitlab.PipelineJob{{ID: 100, Name: "build", Stage: "build"}})
	bridges := NewAsyncCache[int, []gitlab.PipelineBridge]()
	bridges.Set(10, []gitlab.PipelineBridge{{ID: 7, Name: "child", Stage: "deploy"}})
	childJobs := NewAsyncCache[int, []gitlab.PipelineJob]()
	childJobs.Set(77, []gitlab.PipelineJob{{ID: 200, Name: "child-job"}})

	pipelineList := newBareList([]list.Item{pipelineItem{summary: gitlab.PipelineSummary{ID: 10}}}, pipelineDelegate{}, 40, 10)
	stageTable := table.New()

	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:              gitlab.ProjectNode{ID: 1},
			pipelines:            []gitlab.PipelineSummary{{ID: 10}},
			pipelineList:         pipelineList,
			selected:             0,
			pendingSelectID:      10,
			stages:               stages,
			stageTable:           stageTable,
			jobs:                 jobs,
			logs:                 logs,
			logPreview:           previewState{content: "old log", raw: "old log"},
			logJobID:             100,
			bridges:              bridges,
			childJobs:            childJobs,
			stageSelected:        1,
			jobRows:              []gitlab.PipelineJob{{ID: 100, Name: "build"}},
			stageJobRows:         []stageJobRow{{Kind: rowKindJob, Job: &gitlab.PipelineJob{ID: 100, Name: "build"}}},
			testReport:           &gitlab.TestReport{},
			testReportLoading:    true,
			testReportErr:        fmt.Errorf("old"),
			testReportPipelineID: 10,
		},
	}

	// When: the load result reports no pipelines
	updated, _ := m.handlePipelinesLoaded(pipelinesLoadedMsg{
		projectID: 1,
		err:       gitlab.ErrNoPipelines,
	})
	got := updated.(Model).pipelineView

	// Then: selection, caches, preview, test report, and paging all reset
	if got.pipelines != nil || got.selected != 0 || got.pendingSelectID != 0 {
		t.Fatalf("expected pipeline selection to reset, got %#v", got.pipelines)
	}
	if got.logs.Len() != 0 || got.jobs.Len() != 0 || got.stages.Len() != 0 {
		t.Fatalf("expected pipeline caches to clear")
	}
	if got.bridges.Len() != 0 || got.childJobs.Len() != 0 {
		t.Fatalf("expected child pipeline caches to clear")
	}
	if got.logPreview.content != "" || got.logJobID != 0 {
		t.Fatalf("expected stale log preview to clear, got %#v", got.logPreview)
	}
	if got.testReport != nil || got.testReportLoading || got.testReportErr != nil || got.testReportPipelineID != 0 {
		t.Fatalf("expected stale test report state to clear")
	}
	if got.totalPages != 0 {
		t.Fatalf("expected totalPages=0, got %d", got.totalPages)
	}
}

// TestTruncateLogContent: logs at or under the cap pass through and larger ones truncate with a notice.
// Given logs below, at, and above maxLogSizeBytes, when each is truncated, then sizes at or under the cap
// are unchanged and oversized logs shrink to the cap plus a "log truncated" warning.
// Why it matters: without the visible warning a truncated trace looks complete and users hunt for output
// that was silently cut.
func TestTruncateLogContent(t *testing.T) {
	// Given: logs below, at, and above the size cap
	tests := []struct {
		name    string
		content string
		want    int // expected length
	}{
		{
			name:    "small log unchanged",
			content: "hello world",
			want:    len("hello world"),
		},
		{
			name:    "exactly at limit",
			content: strings.Repeat("a", maxLogSizeBytes),
			want:    maxLogSizeBytes,
		},
		{
			name:    "oversized log truncated",
			content: strings.Repeat("b", maxLogSizeBytes+1000),
			want:    maxLogSizeBytes + len("\n\n... (log truncated at 1MB, full log available in GitLab web UI)"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the log is truncated
			got := truncateLogContent(tt.content)

			// Then: the length matches and oversized logs carry the warning
			if len(got) != tt.want {
				t.Errorf("truncateLogContent() length = %d, want %d", len(got), tt.want)
			}
			if len(tt.content) > maxLogSizeBytes && !strings.Contains(got, "log truncated") {
				t.Error("Expected truncated log to contain warning message")
			}
		})
	}
}

// TestEvictOldLogs: log eviction trims to the cap, keeps the visible log, and drops the oldest job IDs.
// Given five logs over the cache cap while viewing job 15, when eviction runs, then at most
// maxLogCacheEntries remain, job 15 and the newest IDs survive, and the lowest IDs are gone.
// Why it matters: evicting the log on screen would blank the pane mid-read, and skipping eviction lets a
// long session accumulate unbounded log memory.
func TestEvictOldLogs(t *testing.T) {
	// Given: five logs over the cap while job 15 is on screen
	logs := NewAsyncCache[int, string]()
	m := Model{
		pipelineView: pipelineViewState{
			logs:     logs,
			logJobID: 15, // Currently viewing job 15
		},
	}

	for i := 1; i <= maxLogCacheEntries+5; i++ {
		m.pipelineView.logs.Set(i, fmt.Sprintf("log content for job %d", i))
	}

	initialCount := m.pipelineView.logs.Len()
	if initialCount != maxLogCacheEntries+5 {
		t.Fatalf("Setup failed: expected %d logs, got %d", maxLogCacheEntries+5, initialCount)
	}

	// When: eviction runs
	m.evictOldLogs()

	// Then: the cache is back within its cap
	if m.pipelineView.logs.Len() > maxLogCacheEntries {
		t.Errorf("evictOldLogs() left %d logs, want at most %d", m.pipelineView.logs.Len(), maxLogCacheEntries)
	}

	// And: the currently displayed log survives
	if _, exists := m.pipelineView.logs.Get(15); !exists {
		t.Error("evictOldLogs() evicted the currently displayed log")
	}

	// And: the oldest job IDs are the ones evicted while the newest survive
	if _, exists := m.pipelineView.logs.Get(1); exists {
		t.Error("evictOldLogs() should have evicted job 1 (oldest)")
	}
	if _, exists := m.pipelineView.logs.Get(maxLogCacheEntries + 5); !exists {
		t.Error("evictOldLogs() should have kept the newest log")
	}
}

// TestPipelineView_RetryModalOpens: R in pipeline focus opens the confirm modal for the selected pipeline.
// Given the pipeline view with pipeline #42 on main selected, when R is pressed, then the retry modal is
// active carrying that ID and ref.
// Why it matters: the modal's captured ID and ref are what the retry ultimately dispatches, so seeding
// them from the wrong row would re-run a pipeline the user never confirmed.
func TestPipelineView_RetryModalOpens(t *testing.T) {
	// Given: the pipeline view with pipeline #42 on main selected
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:   gitlab.ProjectNode{ID: 1},
			pipelines: []gitlab.PipelineSummary{{ID: 42, Ref: "main"}},
			selected:  0,
		},
	}

	// When: R is pressed
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}
	updated, _ := m.handlePipelineViewKey(msg)
	got := updated.(Model).pipelineView

	// Then: the modal opens carrying the selection's ID and ref
	if !got.retryConfirm.active {
		t.Fatalf("expected retry modal to open")
	}
	if got.retryConfirm.id != 42 || got.retryConfirm.ref != "main" {
		t.Fatalf("expected confirm data to match selection, got id=%d ref=%q", got.retryConfirm.id, got.retryConfirm.ref)
	}
}

// TestPipelineView_RetryConfirmStartsRetry: enter in the retry modal starts the retry and closes it.
// Given an active retry confirmation, when enter is pressed, then retrying flips true, the modal closes,
// and a dispatch command is returned.
// Why it matters: a confirm that closed without dispatching would silently drop the retry the user just
// approved.
func TestPipelineView_RetryConfirmStartsRetry(t *testing.T) {
	// Given: an active retry confirmation
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:      gitlab.ProjectNode{ID: 1},
			retryConfirm: retryConfirmState{active: true, id: 55},
		},
	}

	// When: enter confirms
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, cmd := m.handlePipelineRetryConfirmKey(msg)
	got := updated.(Model).pipelineView

	// Then: the retry is in flight, the modal is closed, and a dispatch command returned
	if got.retrying != true {
		t.Fatalf("expected retrying to be true")
	}
	if got.retryConfirm.active {
		t.Fatalf("expected retry modal to close")
	}
	if cmd == nil {
		t.Fatalf("expected retry command")
	}
}

// retryScenarioModel builds a renderable multi-panel model wired to svc for
// key-driven retry scenarios. The bare test model leaves the pipeline list
// without a delegate, which panics inside View(), so a real list is attached.
func retryScenarioModel(svc gitlab.Service, active PanelID) Model {
	m := newMultiPanelModel(active)
	m.ctx = context.Background()
	m.client = svc
	items := []list.Item{pipelineItem{summary: m.pipelineView.pipelines[0]}}
	m.pipelineView.pipelineList = newBareList(items, pipelineDelegate{}, 40, 10)
	// Settle the layout the way a launch does, so a key press answers with its own command alone
	// rather than with the panel sizing a first resize always brings.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return sized.(Model)
}

// TestRetryConfirmFlow_RetriesSelectedPipeline: R renders the confirm modal and
// enter retries exactly the pipeline shown in it.
// Given a pipelines panel with a selected pipeline, when R is pressed and the modal
// confirmed with enter, then the view shows the modal with the pipeline's identity,
// the service receives the exact project, pipeline, and ref, and the result message
// toasts the retry, closes the modal, and reloads the list.
// Why it matters: a retry dispatched with a different ID or ref than the modal shows
// re-runs a pipeline the user never confirmed.
func TestRetryConfirmFlow_RetriesSelectedPipeline(t *testing.T) {
	// Given: a pipelines panel over a service spying on RetryPipeline
	var gotProjectID, gotPipelineID int
	var gotRef string
	svc := &mockService{
		RetryPipelineFn: func(_ context.Context, projectID, pipelineID int, ref string) (gitlab.PipelineSummary, error) {
			gotProjectID, gotPipelineID, gotRef = projectID, pipelineID, ref
			return gitlab.PipelineSummary{ID: pipelineID, Status: "pending", Ref: ref}, nil
		},
	}
	m := retryScenarioModel(svc, PanelPipelines)

	// When: R is pressed through the real update loop
	res, _ := m.Update(keyMsg("R"))
	m = res.(Model)

	// Then: the view renders the confirm modal with the selection's identity and keys
	view := m.View()
	for _, want := range []string{"Retry Pipeline · team/alpha", "Pipeline: #10", "Ref: main", "Enter to retry pipeline · Esc to cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("retry confirm modal missing %q in view:\n%s", want, view)
		}
	}

	// When: enter confirms and the returned command runs
	res, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if cmd == nil {
		t.Fatal("expected a retry command from confirm")
	}
	msg := cmd()

	// Then: the service got the exact project, pipeline, and ref
	if gotProjectID != 1 || gotPipelineID != 10 || gotRef != "main" {
		t.Fatalf("RetryPipeline called with (%d, %d, %q), want (1, 10, %q)", gotProjectID, gotPipelineID, gotRef, "main")
	}

	// And: applying the result toasts the retry, closes the modal, and reloads
	retried, ok := msg.(pipelineRetriedMsg)
	if !ok {
		t.Fatalf("expected pipelineRetriedMsg, got %T", msg)
	}
	res, reload := m.Update(retried)
	m = res.(Model)
	if !strings.Contains(m.status, "Retried pipeline #10") {
		t.Fatalf("status = %q, want it to name the retried pipeline", m.status)
	}
	if m.pipelineView.retryConfirm.active {
		t.Fatal("expected the confirm modal to be closed after the retry result")
	}
	if reload == nil {
		t.Fatal("expected a pipeline reload command after the retry result")
	}
}

// TestRetryConfirmFlow_RetriesSelectedJob: in the stages panel, R renders the
// job confirm modal and enter retries exactly the job shown in it.
// Given a stages panel with a job row selected, when R is pressed and the modal
// confirmed with enter, then the view shows the job-scoped modal, the service
// receives the exact project and job, and the result message toasts the retried job.
// Why it matters: retrying the wrong job wastes CI minutes and leaves the failed
// job the user confirmed untouched.
func TestRetryConfirmFlow_RetriesSelectedJob(t *testing.T) {
	// Given: a stages panel with a failed job selected, spying on RetryJob
	var gotProjectID, gotJobID int
	svc := &mockService{
		RetryJobFn: func(_ context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
			gotProjectID, gotJobID = projectID, jobID
			return gitlab.PipelineJob{ID: jobID, Name: "lint", Status: "pending"}, nil
		},
	}
	m := retryScenarioModel(svc, PanelStages)
	m.pipelineView.jobRows = []gitlab.PipelineJob{{ID: 77, Name: "lint", Stage: "test", Status: "failed"}}
	m.pipelineView.stageSelected = 0

	// When: R is pressed through the real update loop
	res, _ := m.Update(keyMsg("R"))
	m = res.(Model)

	// Then: the view renders the job-scoped confirm modal
	view := m.View()
	for _, want := range []string{"Retry Job · team/alpha", "Job: lint (#77)", "Stage: test", "Enter to retry job"} {
		if !strings.Contains(view, want) {
			t.Fatalf("job retry confirm modal missing %q in view:\n%s", want, view)
		}
	}

	// When: enter confirms and the returned command runs
	res, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = res.(Model)
	if cmd == nil {
		t.Fatal("expected a retry command from confirm")
	}
	msg := cmd()

	// Then: the service got the exact project and job
	if gotProjectID != 1 || gotJobID != 77 {
		t.Fatalf("RetryJob called with (%d, %d), want (1, 77)", gotProjectID, gotJobID)
	}

	// And: applying the result toasts the retried job and closes the modal
	res, _ = m.Update(msg)
	m = res.(Model)
	if !strings.Contains(m.status, "Retried job lint (#77)") {
		t.Fatalf("status = %q, want it to name the retried job", m.status)
	}
	if m.pipelineView.retryConfirm.active {
		t.Fatal("expected the confirm modal to be closed after the retry result")
	}
}

// TestStagesPanel_BridgeChildActions: cancel, play and retry on a downstream (bridge-child)
// job address that job's own project, and every reply reaches the view that asked.
// Given a bridge-child job selected in the stages panel whose ChildProjectID differs from the
// parent project, when the user cancels a running one, plays a manual one and retries a failed
// one, then each request carries the downstream project ID and each reply reports its outcome.
// Why it matters: a child pipeline's jobs live in a different GitLab project, so a request sent
// to the parent reaches no job, and a reply the view discards strands the action on screen with
// no outcome and, for retry, no way to try again.
func TestStagesPanel_BridgeChildActions(t *testing.T) {
	// Given: a stages panel over a bridge-child job whose downstream project differs from the
	// parent, spying on the project ID each action addresses
	const childProjectID = 555
	var cancelProjectID, playProjectID, retryProjectID int
	svc := &mockService{
		CancelJobFn: func(_ context.Context, projectID, _ int) error {
			cancelProjectID = projectID
			return nil
		},
		PlayJobFn: func(_ context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
			playProjectID = projectID
			return gitlab.PipelineJob{ID: jobID, Name: "release", Status: "running"}, nil
		},
		RetryJobFn: func(_ context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
			retryProjectID = projectID
			return gitlab.PipelineJob{ID: jobID, Name: "deploy", Status: "running"}, nil
		},
	}
	stagesOver := func(job gitlab.PipelineJob) Model {
		m := retryScenarioModel(svc, PanelStages)
		m.pipelineView.stageSelected = 0
		m.pipelineView.jobRows = []gitlab.PipelineJob{job}
		m.pipelineView.stageJobRows = []stageJobRow{{
			Kind: rowKindBridgeChild, Job: &m.pipelineView.jobRows[0], ChildProjectID: childProjectID,
		}}
		return m
	}

	for _, action := range []struct {
		name       string
		key        string
		job        gitlab.PipelineJob
		addressed  *int
		wantStatus string
	}{
		{"cancel", "C", gitlab.PipelineJob{ID: 900, Name: "deploy", Stage: "deploy", Status: "running"}, &cancelProjectID, "Canceled job"},
		{"play", "P", gitlab.PipelineJob{ID: 901, Name: "release", Stage: "deploy", Status: "manual"}, &playProjectID, "Triggered job"},
	} {
		// When: the user acts on a bridge-child job and the reply comes back
		sent, cmd := stagesOver(action.job).Update(keyMsg(action.key))
		if cmd == nil {
			t.Fatalf("%s: expected a command from %s", action.name, action.key)
		}
		replied, refresh := sent.(Model).Update(cmd())
		got := replied.(Model)

		// Then: the request went to the downstream project and the reply reported the outcome
		if *action.addressed != childProjectID {
			t.Errorf("%s addressed project %d, want downstream %d", action.name, *action.addressed, childProjectID)
		}
		if !strings.Contains(got.status, action.wantStatus) {
			t.Errorf("%s: status is %q, want it to contain %q", action.name, got.status, action.wantStatus)
		}
		if refresh == nil {
			t.Errorf("%s: expected a refresh command after the reply", action.name)
		}
	}

	// When: the user retries a failed bridge-child job through the confirm modal
	opened, _ := stagesOver(gitlab.PipelineJob{ID: 902, Name: "deploy", Stage: "deploy", Status: "failed"}).Update(keyMsg("R"))
	confirmed, cmd := opened.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("retry: expected a command from enter")
	}
	replied, _ := confirmed.(Model).Update(cmd())
	m := replied.(Model)

	// Then: the retry went to the downstream project and reported the outcome
	if retryProjectID != childProjectID {
		t.Errorf("retry addressed project %d, want downstream %d", retryProjectID, childProjectID)
	}
	if !strings.Contains(m.status, "Retried job") {
		t.Errorf("retry: status is %q, want it to contain %q", m.status, "Retried job")
	}

	// And: retry is usable again, so pressing R reopens the modal
	again, _ := m.Update(keyMsg("R"))
	if view := again.(Model).View(); !strings.Contains(view, "Enter to retry job") {
		t.Errorf("retry: R did not reopen the confirm modal, so retry stays blocked:\n%s", view)
	}
}

// TestNormalizeColumnBounds: column normalization clamps content to the exact width and height.
// Given one overlong line normalized to 5x2, when the lines come back, then there are exactly two rows,
// each measuring width 5.
// Why it matters: horizontally joined panes must be perfectly rectangular, or lipgloss produces ragged
// borders across the whole layout.
func TestNormalizeColumnBounds(t *testing.T) {
	// When: an overlong line is normalized to a 5x2 cell block
	lines := normalizeColumn("one very very long line", 5, 2)

	// Then: exactly two rows come back, each clamped to width 5
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(lines))
	}
	for _, l := range lines {
		if lipgloss.Width(l) != 5 {
			t.Fatalf("line %q not clamped to width 5", l)
		}
	}
}

// TestHandleTreeLoadedPreview: a tree response for the previewed directory fills the preview pane.
// Given a preview loading for "dir", when its tree entries arrive, then loading clears and the preview
// lists the file and the subdirectory with a trailing slash.
// Why it matters: a preview stuck on loading leaves the explorer's right pane spinning after the data has
// already arrived.
func TestHandleTreeLoadedPreview(t *testing.T) {
	// Given: an explorer whose preview is loading the "dir" directory
	delegate := treeEntryDelegate{}
	parentList := list.New([]list.Item{}, delegate, 0, 0)
	currentList := list.New([]list.Item{}, delegate, 0, 0)

	m := Model{
		mode: modeExplorer,
		explorer: explorerState{
			project:     gitlab.ProjectNode{ID: 1},
			ref:         "main",
			stack:       []dirState{{path: ""}},
			preview:     previewState{path: "dir", loading: true},
			parentList:  parentList,
			currentList: currentList,
		},
	}

	// When: the directory's tree entries arrive
	msg := treeLoadedMsg{
		projectID: 1,
		path:      "dir",
		entries: []gitlab.TreeNode{
			{Name: "file.txt", Type: "blob"},
			{Name: "nested", Type: "tree"},
		},
	}
	updated, _ := m.handleTreeLoaded(msg)
	got := updated.(Model).explorer.preview

	// Then: loading clears and the preview lists both entries, marking the directory
	if got.loading || !strings.Contains(got.content, "file.txt") || !strings.Contains(got.content, "nested/") {
		t.Fatalf("preview not populated: %#v", got)
	}
}

// TestHandleTreeLoadedDirectory: a tree response for the current directory populates the navigation stack.
// Given the root dirState marked loading, when its entries arrive, then loading clears and the entries are
// stored on the stack frame.
// Why it matters: the stack frame drives the file list itself, so a dropped response would leave the
// explorer's main column empty.
func TestHandleTreeLoadedDirectory(t *testing.T) {
	// Given: an explorer whose root directory is still loading
	delegate := treeEntryDelegate{}
	parentList := list.New([]list.Item{}, delegate, 0, 0)
	currentList := list.New([]list.Item{}, delegate, 0, 0)

	m := Model{
		mode: modeExplorer,
		explorer: explorerState{
			project:     gitlab.ProjectNode{ID: 1},
			ref:         "main",
			stack:       []dirState{{path: "", loading: true}},
			parentList:  parentList,
			currentList: currentList,
		},
	}

	// When: the root's tree entries arrive
	msg := treeLoadedMsg{
		projectID: 1,
		path:      "",
		entries: []gitlab.TreeNode{
			{Name: "dir", Path: "dir", Type: "tree"},
		},
	}
	updated, _ := m.handleTreeLoaded(msg)
	dir := updated.(Model).explorer.stack[0]

	// Then: loading clears and the entries land on the stack frame
	if dir.loading || len(dir.entries) != 1 {
		t.Fatalf("directory not loaded: %#v", dir)
	}
}

// TestHandleFileLoaded: a file response for the previewed path stores its content.
// Given a preview loading README.md, when the file content arrives, then loading clears and the preview
// holds the text.
// Why it matters: file preview is the explorer's core read path, and a mismatch here shows the spinner
// forever while the content sits discarded.
func TestHandleFileLoaded(t *testing.T) {
	// Given: an explorer whose preview is loading README.md
	delegate := treeEntryDelegate{}
	parentList := list.New([]list.Item{}, delegate, 0, 0)
	currentList := list.New([]list.Item{}, delegate, 0, 0)

	m := Model{
		mode: modeExplorer,
		explorer: explorerState{
			project:     gitlab.ProjectNode{ID: 1},
			ref:         "main",
			stack:       []dirState{{path: ""}},
			preview:     previewState{path: "README.md", loading: true},
			parentList:  parentList,
			currentList: currentList,
		},
	}

	// When: the file content arrives
	msg := fileLoadedMsg{projectID: 1, path: "README.md", content: "hello world"}
	updated, _ := m.handleFileLoaded(msg)
	p := updated.(Model).explorer.preview

	// Then: loading clears and the preview holds the text
	if p.loading || !strings.Contains(p.content, "hello") {
		t.Fatalf("file preview not stored: %#v", p)
	}
}

// TestQueueExplorerPreviewDir: selecting a directory queues its tree fetch and marks the preview loading.
// Given the cursor on a tree entry, when the preview is queued, then a command is returned and the preview
// targets the directory path in a loading state.
// Why it matters: without the fetch command the preview pane would keep showing the previous selection
// while claiming to load.
func TestQueueExplorerPreviewDir(t *testing.T) {
	// Given: the explorer cursor on a directory entry
	delegate := treeEntryDelegate{}
	parentList := list.New([]list.Item{}, delegate, 0, 0)
	currentList := list.New([]list.Item{}, delegate, 0, 0)

	m := Model{
		explorer: explorerState{
			project: gitlab.ProjectNode{ID: 1},
			ref:     "main",
			stack: []dirState{{
				path: "",
				entries: []gitlab.TreeNode{
					{Path: "src", Name: "src", Type: "tree"},
				},
			}},
			parentList:  parentList,
			currentList: currentList,
		},
	}

	// When/Then: queuing the preview returns a fetch and marks the directory loading
	cmd := m.queueExplorerPreview()
	if cmd == nil || !m.explorer.preview.loading || m.explorer.preview.path != "src" {
		t.Fatalf("directory preview not queued: %#v", m.explorer.preview)
	}
}

// TestQueueExplorerPreviewFile: selecting a file queues its content fetch and marks the preview loading.
// Given the cursor on a blob entry, when the preview is queued, then a command is returned and the preview
// targets the file path in a loading state.
// Why it matters: a file selection that queued no fetch would permanently show the neighboring entry's
// preview.
func TestQueueExplorerPreviewFile(t *testing.T) {
	// Given: the explorer cursor on a file entry
	delegate := treeEntryDelegate{}
	parentList := list.New([]list.Item{}, delegate, 0, 0)
	currentList := list.New([]list.Item{}, delegate, 0, 0)

	m := Model{
		explorer: explorerState{
			project: gitlab.ProjectNode{ID: 1},
			ref:     "main",
			stack: []dirState{{
				path: "",
				entries: []gitlab.TreeNode{
					{Path: "README.md", Name: "README.md", Type: "blob"},
				},
			}},
			parentList:  parentList,
			currentList: currentList,
		},
	}

	// When/Then: queuing the preview returns a fetch and marks the file loading
	cmd := m.queueExplorerPreview()
	if cmd == nil || m.explorer.preview.path != "README.md" || !m.explorer.preview.loading {
		t.Fatalf("file preview not queued: %#v", m.explorer.preview)
	}
}

// TestRenderExplorerPreviewWrapsLongLines: rendered preview content never exceeds the pane width.
// Given preview content six times wider than the pane, when the preview renders at width 10, then every
// line after the header measures within the width.
// Why it matters: an overflowing preview line breaks the explorer's three-column alignment for every row
// below it.
func TestRenderExplorerPreviewWrapsLongLines(t *testing.T) {
	// Given: preview content far wider than the pane
	m := Model{
		explorer: explorerState{
			preview: previewState{
				content: strings.Repeat("abc", 20),
			},
		},
	}

	// When: the preview renders at width 10
	const width = 10
	out := renderExplorerPreview(m, width, false)
	lines := strings.Split(out, "\n")

	// Then: every line after the header fits the width
	if len(lines) <= 1 {
		t.Fatalf("expected preview output lines, got %q", out)
	}
	for i, line := range lines {
		if i == 0 {
			continue // header
		}
		if lipgloss.Width(line) > width {
			t.Fatalf("line %q exceeds preview width %d", line, width)
		}
	}
}

// TestSetLogViewportContent_WrapsLongLines: log lines wrap to the viewport width before display.
// Given a 200-character line in a 40-wide viewport, when the content is set and rendered, then no rendered
// line's visual width exceeds 40.
// Why it matters: CI logs routinely emit very long lines, and unwrapped ones force the terminal to clip or
// smear the log pane.
func TestSetLogViewportContent_WrapsLongLines(t *testing.T) {
	// Given: a 40-wide log viewport
	vp := viewport.New(40, 20)
	m := Model{
		pipelineView: pipelineViewState{
			logViewport: vp,
		},
	}

	// When: a 200-character line is set as content
	longLine := strings.Repeat("X", 200)
	m.setLogViewportContent(longLine)

	// Then: every rendered line fits the viewport width
	output := m.pipelineView.logViewport.View()
	for line := range strings.SplitSeq(output, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("line visual width %d exceeds viewport width 40: %q", w, line)
		}
	}
}

// TestRenderBorderedPane_OutputFitsWidth: pane output never renders wider than the requested width.
// Given 200 characters of content in a 50-wide pane, when it renders, then every line measures at most 50
// visual cells.
// Why it matters: one overwide pane line pushes the neighboring panel's border out of column for the whole
// frame.
func TestRenderBorderedPane_OutputFitsWidth(t *testing.T) {
	// When: a pane renders content four times wider than itself
	content := strings.Repeat("Z", 200)
	output := renderBorderedPane(content, 50, 10, false, "Test", nil, 0, "")

	// Then: every rendered line fits the pane width
	for i, line := range strings.Split(output, "\n") {
		if w := lipgloss.Width(line); w > 50 {
			t.Fatalf("line %d visual width %d exceeds pane width 50: %q", i, w, line)
		}
	}
}

// TestRenderBorderedPane_OutputFitsHeight: pane output is exactly the content height plus borders.
// Given 50 lines of content in a height-5 pane, when it renders, then the output has exactly 5 content
// rows plus the two border rows.
// Why it matters: a pane taller than requested pushes the info bar off-screen and scrolls the terminal.
func TestRenderBorderedPane_OutputFitsHeight(t *testing.T) {
	// When: a pane renders ten times more lines than its height
	content := strings.Repeat("line\n", 50)
	output := renderBorderedPane(content, 40, 5, false, "Test", nil, 0, "")

	// Then: the output is exactly the content height plus borders
	lines := strings.Split(output, "\n")
	// Remove trailing empty line from final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	wantHeight := 5 + borderCharsV // content + top/bottom borders
	if len(lines) != wantHeight {
		t.Fatalf("output has %d lines, want %d (content %d + borders %d)", len(lines), wantHeight, 5, borderCharsV)
	}
}

// --- Arrow key navigation tests ---

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func newMultiPanelModel(active PanelID) Model {
	projects := []gitlab.ProjectNode{
		{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha"},
		{ID: 2, Name: "beta", PathWithNamespace: "team/beta"},
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

	// A hand written copy of this state drifts silently, and reports the drift as a nil map panic
	// inside whichever test next touches the field it missed.
	pipelineView := newPipelineViewState()
	pipelineView.project = projects[0]
	pipelineView.pipelines = []gitlab.PipelineSummary{{ID: 10, Ref: "main"}}
	pipelineView.logViewport = viewport.New(60, 20)

	return Model{
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
		focus:          FocusState{Active: active},
		search:         searchState{input: ti},
		keys:           newKeyMap(),
		pipelineView:   pipelineView,
		mrView:         mrViewState{project: projects[0]},
		commitCache:    NewAsyncCache[int, []gitlab.CommitSummary](),
	}
}

// TestRightArrow_FocusesDetailFromAnySidebar: right arrow jumps to the detail pane and remembers the origin.
// Given each sidebar panel focused, when right is pressed, then focus lands on PanelDetail with PrevActive
// recording the origin panel.
// Why it matters: PrevActive decides both what the detail pane shows and where left returns to, so losing
// it strands the user in a detail pane about the wrong panel.
func TestRightArrow_FocusesDetailFromAnySidebar(t *testing.T) {
	for _, panel := range SidebarPanels {
		t.Run(panelLabel(panel), func(t *testing.T) {
			// Given: the sidebar panel focused
			m := newMultiPanelModel(panel)

			// When: right is pressed
			updated, _ := m.handleMultiPanelKey(keyMsg("right"))
			got := updated.(Model)

			// Then: the detail pane is focused with the origin recorded
			if got.focus.Active != PanelDetail {
				t.Fatalf("expected Active=PanelDetail, got %d", got.focus.Active)
			}
			if got.focus.PrevActive != panel {
				t.Fatalf("expected PrevActive=%d, got %d", panel, got.focus.PrevActive)
			}
		})
	}
}

// TestLeftArrow_ReturnsFromDetail: left arrow returns from the detail pane to the panel it came from.
// Given the detail pane focused with each possible PrevActive, when left is pressed, then focus returns to
// that panel.
// Why it matters: an asymmetric return would dump users into a default panel and lose their place after
// every detail peek.
func TestLeftArrow_ReturnsFromDetail(t *testing.T) {
	for _, panel := range SidebarPanels {
		t.Run(panelLabel(panel), func(t *testing.T) {
			// Given: the detail pane focused, arrived at from the panel
			m := newMultiPanelModel(PanelDetail)
			m.focus.PrevActive = panel

			// When/Then: left returns focus to that panel
			updated, _ := m.handleMultiPanelKey(keyMsg("left"))
			got := updated.(Model)
			if got.focus.Active != panel {
				t.Fatalf("expected Active=%d, got %d", panel, got.focus.Active)
			}
		})
	}
}

// TestLeftArrow_NavigatesBackInSidebar: left arrow steps up the sidebar hierarchy like h.
// Given Pipelines, Stages, and MRs focused, when left is pressed, then focus moves to Projects, Pipelines,
// and Stages respectively.
// Why it matters: arrow keys and vim keys must agree, or muscle memory from one scheme dead-ends in the
// other.
func TestLeftArrow_NavigatesBackInSidebar(t *testing.T) {
	// Given: each drilled-in panel with its hierarchical parent
	tests := []struct {
		from PanelID
		to   PanelID
	}{
		{PanelPipelines, PanelProjects},
		{PanelStages, PanelPipelines},
		{PanelMRs, PanelStages},
	}
	for _, tt := range tests {
		t.Run(panelLabel(tt.from), func(t *testing.T) {
			// When/Then: left steps back to the parent panel
			m := newMultiPanelModel(tt.from)
			updated, _ := m.handleMultiPanelKey(keyMsg("left"))
			got := updated.(Model)
			if got.focus.Active != tt.to {
				t.Fatalf("left arrow: expected panel %d, got %d", tt.to, got.focus.Active)
			}
		})
	}
}

// TestH_StillNavigatesBackHierarchically: h steps up the sidebar hierarchy.
// Given Pipelines, Stages, and MRs focused, when h is pressed, then focus moves to Projects, Pipelines,
// and Stages respectively.
// Why it matters: h is the vim-native back key, and breaking it would strand keyboard-only users deep in
// the drill-down.
func TestH_StillNavigatesBackHierarchically(t *testing.T) {
	// Given: each drilled-in panel with its hierarchical parent
	tests := []struct {
		from PanelID
		to   PanelID
	}{
		{PanelPipelines, PanelProjects},
		{PanelStages, PanelPipelines},
		{PanelMRs, PanelStages},
	}
	for _, tt := range tests {
		t.Run(panelLabel(tt.from)+"_to_"+panelLabel(tt.to), func(t *testing.T) {
			// When/Then: h steps back to the parent panel
			m := newMultiPanelModel(tt.from)
			updated, _ := m.handleMultiPanelKey(keyMsg("h"))
			got := updated.(Model)
			if got.focus.Active != tt.to {
				t.Fatalf("expected h to navigate to %d, got %d", tt.to, got.focus.Active)
			}
		})
	}
}

// TestEnterL_DrillsIn_NotRight: enter drills down the sidebar while l peels off to the detail pane.
// Given Projects and Pipelines focused, when enter and l are each pressed, then enter advances to the next
// sidebar panel and l focuses the detail pane.
// Why it matters: if the two keys collapsed into one behavior, either drilling into stages or opening the
// detail pane would become unreachable.
func TestEnterL_DrillsIn_NotRight(t *testing.T) {
	t.Run("Projects_enter", func(t *testing.T) {
		// Given: the projects panel focused
		m := newMultiPanelModel(PanelProjects)

		// When/Then: enter drills into Pipelines
		updated, _ := m.handleMultiPanelKey(keyMsg("enter"))
		got := updated.(Model)
		if got.focus.Active != PanelPipelines {
			t.Fatalf("expected enter to drill into Pipelines, got %d", got.focus.Active)
		}
	})
	t.Run("Projects_l", func(t *testing.T) {
		// Given: the projects panel focused
		m := newMultiPanelModel(PanelProjects)

		// When/Then: l focuses the detail pane
		updated, _ := m.handleMultiPanelKey(keyMsg("l"))
		got := updated.(Model)
		if got.focus.Active != PanelDetail {
			t.Fatalf("expected l to focus Detail, got %d", got.focus.Active)
		}
	})
	t.Run("Pipelines_enter", func(t *testing.T) {
		// Given: the pipelines panel focused
		m := newMultiPanelModel(PanelPipelines)

		// When/Then: enter drills into Stages
		updated, _ := m.handleMultiPanelKey(keyMsg("enter"))
		got := updated.(Model)
		if got.focus.Active != PanelStages {
			t.Fatalf("expected enter to drill into Stages, got %d", got.focus.Active)
		}
	})
	t.Run("Pipelines_l", func(t *testing.T) {
		// Given: the pipelines panel focused
		m := newMultiPanelModel(PanelPipelines)

		// When/Then: l focuses the detail pane
		updated, _ := m.handleMultiPanelKey(keyMsg("l"))
		got := updated.(Model)
		if got.focus.Active != PanelDetail {
			t.Fatalf("expected l to focus Detail, got %d", got.focus.Active)
		}
	})
}

// TestDetailPanel_ScrollKeys: detail pane scroll keys move the log viewport and keep focus in place.
// Given the detail pane focused over a long pipeline log, when j, J, k, and K are pressed in turn, then
// the viewport offset moves one line down, half a page down, one line up, and half a page back to the top
// while focus stays on the detail pane, and t still cycles the detail tab to Tests.
// Why it matters: if scroll keys leaked out of the detail pane as navigation or never moved the viewport,
// nothing past the first screen of a pipeline log would be readable.
func TestDetailPanel_ScrollKeys(t *testing.T) {
	// Given: the detail pane focused over a long pipeline log in a 20-line viewport
	m := newMultiPanelModel(PanelDetail)
	m.focus.PrevActive = PanelPipelines
	content := strings.Repeat("line\n", 100)
	m.pipelineView.logViewport.SetContent(content)

	// When: each scroll key is pressed in sequence
	steps := []struct {
		key        string
		wantOffset int
	}{
		{"j", 1},  // one line down
		{"J", 11}, // half of the 20-line viewport down
		{"k", 10}, // one line back up
		{"K", 0},  // half a page back to the top
	}
	got := m
	for _, step := range steps {
		updated, _ := got.handleMultiPanelKey(keyMsg(step.key))
		got = updated.(Model)

		// Then: the viewport offset lands on the expected line with focus unmoved
		if off := got.pipelineView.logViewport.YOffset; off != step.wantOffset {
			t.Fatalf("%s: expected viewport offset %d, got %d", step.key, step.wantOffset, off)
		}
		if got.focus.Active != PanelDetail {
			t.Fatalf("%s should not change focus", step.key)
		}
	}

	// And: t cycles the detail tab
	updated, _ := got.handleMultiPanelKey(keyMsg("t"))
	got = updated.(Model)
	if got.pipelineView.detailTab != detailTabTests {
		t.Fatal("t should cycle detail tab")
	}
}

// TestProjectNav_TriggersAutoLoad: moving the project selection schedules the selected project's data load.
// Given the projects panel on the first project, when down moves to the second, then the selection updates
// and a non-nil auto-load command is returned.
// Why it matters: the pipelines, MRs, and detail panes only follow the cursor because of this hook, and a
// nil command would freeze them on the previous project.
func TestProjectNav_TriggersAutoLoad(t *testing.T) {
	// Given: the projects panel on project index 0 (ID=1)
	m := newMultiPanelModel(PanelProjects)
	m.selected = 0
	m.projectList.Select(0)

	// When: down moves to project index 1 (ID=2)
	updated, cmd := m.handleMultiPanelKey(keyMsg("down"))
	got := updated.(Model)

	// Then: the selection updates and the auto-load command fires
	if got.selected != 1 {
		t.Fatalf("expected selected=1 after down, got %d", got.selected)
	}
	if cmd == nil {
		t.Fatal("expected auto-load command after project selection change, got nil")
	}
}

// TestProjectPageChangeSchedulesAutoLoadForNewSelection: paging forward re-targets auto-load at the new
// page's selection.
// Given one project per page with two pages ready, when ] moves to page 2, then project 2 becomes the
// selection, a pending auto-load and debounce timer exist for it, and commands are returned.
// Why it matters: without re-arming on page change the sidebar keeps describing page 1's project while the
// cursor sits on page 2.
func TestProjectPageChangeSchedulesAutoLoadForNewSelection(t *testing.T) {
	// Given: a full screen of projects plus one on the page after it, both pages ready. A screen
	// holds as many projects as the pane does, so the fetch size is set to match and the two kinds
	// of page line up one to one.
	m := newMultiPanelModel(PanelProjects)
	perPage := m.displayPerPage()
	m.opts.ProjectsPerPage = perPage
	m.allProjects = testProjects(perPage + 1)
	m.totalProjects = perPage + 1
	m.pagesReady = map[int]bool{1: true, 2: true}
	m.page = 1
	m.selected = 0
	m.invalidateVisibleCache()
	m.projectList.Select(0)
	m.updateProjectList()
	wantID := m.allProjects[perPage].ID

	// When: ] moves to page 2
	updated, cmd := m.handleMultiPanelKey(keyMsg("]"))
	got := updated.(Model)

	// Then: the first project of page 2 is selected, with a pending debounced auto-load
	project, ok := got.selectedProject()
	if !ok || project.ID != wantID {
		t.Fatalf("expected page 2 to select project %d, got %#v ok=%v", wantID, project, ok)
	}
	if got.selectionPending == nil || got.selectionPending.ID != wantID {
		t.Fatalf("expected selectionPending for project %d, got %#v", wantID, got.selectionPending)
	}
	if got.selectionDebounce == nil {
		t.Fatal("expected debounce timer for auto-load")
	}
	if cmd == nil {
		t.Fatal("expected batched commands after page change")
	}
}

// TestHandleProjectsLoadedBackgroundSchedulesAutoLoadForVisiblePage: a background page load that fills the
// visible page arms auto-load.
// Given the user already viewing not-yet-loaded page 2, when that page arrives from the background fetch,
// then its project becomes the selection with a pending auto-load and follow-up commands.
// Why it matters: users who page ahead of the background loader would otherwise stare at a page that
// renders but never loads its pipeline and MR data.
func TestHandleProjectsLoadedBackgroundSchedulesAutoLoadForVisiblePage(t *testing.T) {
	// Given: the user viewing page 2 before it has loaded. The fetch size is set to the screen size
	// so that one fetched page fills exactly one screen.
	m := newMultiPanelModel(PanelProjects)
	perPage := m.displayPerPage()
	m.opts.ProjectsPerPage = perPage
	m.page = 2
	m.pagesReady = map[int]bool{1: true}
	m.allProjects = testProjects(perPage)
	m.totalProjects = perPage + 1
	m.invalidateVisibleCache()
	m.updateProjectList()

	// When: page 2 arrives from the background fetch
	updated, cmd := m.handleProjectsLoaded(projectsLoadedMsg{
		background: true,
		page: gitlab.ProjectPage{
			Page:       2,
			TotalPages: 2,
			TotalItems: perPage + 1,
			Projects: []gitlab.ProjectNode{
				{ID: 9001, Name: "beta", PathWithNamespace: "team/beta"},
			},
		},
	})
	got := updated.(Model)

	// Then: the newly visible project is selected with a pending auto-load and commands
	project, ok := got.selectedProject()
	if !ok || project.ID != 9001 {
		t.Fatalf("expected loaded background page to become visible selection, got %#v ok=%v", project, ok)
	}
	if got.selectionPending == nil || got.selectionPending.ID != 9001 {
		t.Fatalf("expected selectionPending for project 9001, got %#v", got.selectionPending)
	}
	if cmd == nil {
		t.Fatal("expected follow-up commands after background page load")
	}
}

// TestSearchDebounceProjectChangeSchedulesAutoLoad: a settled search query arms auto-load for the newly
// filtered selection.
// Given a pending query "beta" and its debounce timestamp, when the debounce tick fires, then project 2 is
// selected with a pending auto-load, a fresh debounce timer, and follow-up commands.
// Why it matters: search moves the selection without any navigation key, so skipping this hook would leave
// the sidebar describing the pre-search project.
func TestSearchDebounceProjectChangeSchedulesAutoLoad(t *testing.T) {
	// Given: a pending query with its debounce timestamp
	m := newMultiPanelModel(PanelProjects)
	ts := time.Now()
	m.search.pendingQuery = "beta"
	m.search.debounceTimer = &ts

	// When: the search debounce tick fires
	updated, cmd := m.handleSearchDebounceTickMsg(searchDebounceTickMsg{
		query:     "beta",
		timestamp: ts,
	})
	got := updated.(Model)

	// Then: the filtered selection is armed for auto-load with follow-up commands
	project, ok := got.selectedProject()
	if !ok || project.ID != 2 {
		t.Fatalf("expected filtered selection to move to project 2, got %#v ok=%v", project, ok)
	}
	if got.selectionPending == nil || got.selectionPending.ID != 2 {
		t.Fatalf("expected selectionPending for project 2, got %#v", got.selectionPending)
	}
	if got.selectionDebounce == nil {
		t.Fatal("expected selection debounce timer after search filter change")
	}
	if cmd == nil {
		t.Fatal("expected follow-up commands after search debounce")
	}
}

// TestSearchDebounceClearsPendingAutoLoadWhenSelectionDisappears: filtering to zero results cancels the
// queued auto-load.
// Given an armed auto-load for project 2, when a search settles on a query with no matches and the stale
// debounce tick later fires, then the pending state is cleared, the tick is ignored, and the pipeline view
// stays on its original project.
// Why it matters: a stale debounce surviving the filter would load pipelines for a project that is no
// longer even visible, overwriting what the user is looking at.
func TestSearchDebounceClearsPendingAutoLoadWhenSelectionDisappears(t *testing.T) {
	// Given: an armed, debounced auto-load for the selected project
	m := newMultiPanelModel(PanelProjects)
	m.selected = 1
	m.projectList.Select(1)

	if cmd := (&m).autoLoadSelectedProjectData(); cmd == nil {
		t.Fatal("expected pending auto-load for selected project")
	}
	if m.selectionPending == nil {
		t.Fatal("expected pending project to be queued")
	}
	if m.selectionDebounce == nil {
		t.Fatal("expected debounce timer to be queued")
	}
	pendingProjectID := m.selectionPending.ID
	pendingTS := *m.selectionDebounce

	// When: a search settles on a query with zero matches
	searchTS := time.Now()
	m.search.pendingQuery = "zzz"
	m.search.debounceTimer = &searchTS

	updated, _ := m.handleSearchDebounceTickMsg(searchDebounceTickMsg{
		query:     "zzz",
		timestamp: searchTS,
	})
	got := updated.(Model)

	// Then: the selection and the pending auto-load are gone
	if _, ok := got.selectedProject(); ok {
		t.Fatal("expected selection to disappear after filtering to zero results")
	}
	if got.selectionPending != nil {
		t.Fatalf("expected pending auto-load to be cleared, got %#v", got.selectionPending)
	}
	if got.selectionDebounce != nil {
		t.Fatal("expected selection debounce to be cleared")
	}

	// And: the stale debounce tick is ignored and the pipeline view stays put
	updated, cmd := got.handleSelectionDebounce(selectionDebounceTickMsg{
		projectID: pendingProjectID,
		timestamp: pendingTS,
	})
	after := updated.(Model)
	if cmd != nil {
		t.Fatal("expected stale debounce tick to be ignored")
	}
	if after.pipelineView.project.ID != 1 {
		t.Fatalf("expected pipeline view to stay on project 1, got %d", after.pipelineView.project.ID)
	}
}

// TestFocusState_ToggleLayoutMode: toggling flips between the default and wide layouts.
// Given a fresh FocusState, when ToggleLayoutMode runs twice, then the mode goes LayoutDefault ->
// LayoutWide -> LayoutDefault.
// Why it matters: a one-way toggle would lock users into the wide split with no key to restore the default.
func TestFocusState_ToggleLayoutMode(t *testing.T) {
	// Given: a fresh focus state in the default layout
	f := FocusState{}
	if f.LayoutMode != LayoutDefault {
		t.Fatal("expected initial LayoutMode to be LayoutDefault")
	}

	// When/Then: each toggle flips the layout and back
	f.ToggleLayoutMode()
	if f.LayoutMode != LayoutWide {
		t.Fatal("expected LayoutMode to be LayoutWide after first toggle")
	}
	f.ToggleLayoutMode()
	if f.LayoutMode != LayoutDefault {
		t.Fatal("expected LayoutMode to be LayoutDefault after second toggle")
	}
}

// TestRenderPipelineLogPaneWrapsLongLines: the log pane wraps ANSI content to width and strips control
// characters.
// Given a log line with ANSI color, 60 characters of text, and a tab, when the pane renders at width 12,
// then every line after the header fits the width and contains no tabs or carriage returns.
// Why it matters: raw tabs and CRs from CI logs desync the terminal's column tracking and smear the frame.
func TestRenderPipelineLogPaneWrapsLongLines(t *testing.T) {
	// Given: ANSI-colored log content with an embedded tab
	m := Model{
		pipelineView: pipelineViewState{
			logPreview: previewState{
				content: "\x1b[31m" + strings.Repeat("abc", 20) + "\x1b[0m\tend",
			},
		},
	}

	// When: the log pane renders at width 12
	const width = 12
	out := renderPipelineLogPane(m, width, false)
	lines := strings.Split(out, "\n")

	// Then: every line after the header fits the width, with no tabs or carriage returns
	if len(lines) <= 1 {
		t.Fatalf("expected log output lines, got %q", out)
	}
	for i, line := range lines {
		if i == 0 {
			continue // header
		}
		if lipgloss.Width(line) > width {
			t.Fatalf("line %q exceeds log width %d", line, width)
		}
		if strings.Contains(line, "\t") || strings.Contains(line, "\r") {
			t.Fatalf("line %q should not contain tabs or carriage returns", line)
		}
	}
}

// TestProjectDelegate_RowKeepsItsStatusColumnWithNoFrames: a row drawn before any animation frame
// reaches it is as wide as one drawn after.
// Given a delegate holding no frames, when a row renders with its status fetch in flight and again
// with the status known, then both draw the same number of cells before the project name.
// Why it matters: the pane is drawn to an exact cell count, so a row that leaves the status column
// empty pulls its name a cell left and the column stops lining up down the list.
func TestProjectDelegate_RowKeepsItsStatusColumnWithNoFrames(t *testing.T) {
	// Given: a delegate holding no animation frames
	proj := projectItem{project: gitlab.ProjectNode{ID: 1, PathWithNamespace: "team/app"}}
	psCache := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	d := projectDelegate{pipelineStatus: &psCache, favorites: map[int]bool{}}
	lm := list.New([]list.Item{proj}, d, 60, 10)

	render := func(state pipelineState) string {
		psCache.Set(1, state)
		var buf strings.Builder
		d.Render(&buf, lm, 0, proj)
		before, _, _ := strings.Cut(ansi.Strip(buf.String()), "team/app")
		return before
	}

	// When: the row renders with the fetch in flight, and again with the status known
	waiting := render(pipelineState{loading: true})
	known := render(pipelineState{hasInfo: true, info: gitlab.PipelineSummary{Status: "success"}})

	// Then: both draw the same number of cells before the name
	if got, want := lipgloss.Width(waiting), lipgloss.Width(known); got != want {
		t.Errorf("the waiting row draws %d cells before the name against %d once the status is "+
			"known (%q against %q), so the status column collapses on that row", got, want, waiting, known)
	}
}

// TestProjectDelegate_PipelineStatusIcons: the project row renders the icon for each pipeline state and
// none without state.
// Given a list delegate over a shared status cache, when each state (success, failed, empty, loading,
// error) is stored and the row rendered, then the matching icon appears, and with no cached state no
// status icon renders at all.
// Why it matters: these glyphs are the at-a-glance CI health view, and a wrong mapping would mark passing
// projects as failed across the whole list.
func TestProjectDelegate_PipelineStatusIcons(t *testing.T) {
	// Given: a project row rendered through a delegate sharing a status cache
	proj := projectItem{project: gitlab.ProjectNode{ID: 1, PathWithNamespace: "team/app"}}
	items := []list.Item{proj}
	psCache := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	frames := (&Model{spinner: newAppSpinner()}).statusFrames()
	if frames.spin == "" {
		t.Fatal("the animation frame is empty, and every row contains the empty string, so the " +
			"loading case below would assert nothing")
	}
	delegate := projectDelegate{
		pipelineStatus: &psCache,
		favorites:      map[int]bool{},
		frames:         frames,
	}
	m := list.New(items, delegate, 60, 10)

	render := func(d projectDelegate) string {
		var buf strings.Builder
		d.Render(&buf, m, 0, proj)
		return buf.String()
	}

	tests := []struct {
		name     string
		state    pipelineState
		wantIcon string
	}{
		{"success", pipelineState{hasInfo: true, info: gitlab.PipelineSummary{Status: "success"}}, iconSuccess},
		{"failed", pipelineState{hasInfo: true, info: gitlab.PipelineSummary{Status: "failed"}}, iconFailed},
		{"empty", pipelineState{empty: true}, iconNoPipeline},
		{"loading", pipelineState{loading: true}, frames.spin},
		{"error", pipelineState{err: fmt.Errorf("oops")}, iconUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the state is cached and the row rendered
			delegate.pipelineStatus.Set(1, tt.state)
			out := render(delegate)

			// Then: the matching icon appears
			if !strings.Contains(out, tt.wantIcon) {
				t.Fatalf("expected icon %q in output %q", tt.wantIcon, out)
			}
		})
	}

	t.Run("no state", func(t *testing.T) {
		// When: the row renders with no cached state at all
		emptyCache := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
		d := projectDelegate{
			pipelineStatus: &emptyCache,
			favorites:      map[int]bool{},
			frames:         frames,
		}
		out := render(d)

		// Then: no status icon renders
		for _, icon := range []string{iconSuccess, iconFailed, iconNoPipeline, frames.spin, iconUnknown} {
			if strings.Contains(out, icon) {
				t.Fatalf("expected no icon, but found %q in output %q", icon, out)
			}
		}
	})
}

// --- State management tests ---

// TestVisibleProjects_FavoritesTab: the favorites tab lists only favorited projects in favorite order.
// Given favorites 1 and 3 with favOrder [1, 3], when visible projects are computed on the favorites tab,
// then exactly those two projects appear in that order.
// Why it matters: leaking non-favorites into the tab or ignoring favOrder defeats the point of a curated
// shortlist.
func TestVisibleProjects_FavoritesTab(t *testing.T) {
	// Given: three projects with two favorited in a recorded order
	m := Model{
		allProjects: []gitlab.ProjectNode{
			{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha"},
			{ID: 2, Name: "beta", PathWithNamespace: "team/beta"},
			{ID: 3, Name: "gamma", PathWithNamespace: "team/gamma"},
		},
		opts:       Options{ProjectsPerPage: 10},
		pagesReady: map[int]bool{1: true},
		page:       1,
		projectTab: projectTabFavorites,
		favorites:  map[int]bool{1: true, 3: true},
		favOrder:   []int{1, 3},
	}

	// When: the visible projects are computed on the favorites tab
	filtered := m.visibleProjects()

	// Then: exactly the favorites appear, in favorite order
	if len(filtered) != 2 {
		t.Fatalf("expected 2 favorites, got %d", len(filtered))
	}
	if filtered[0].ID != 1 || filtered[1].ID != 3 {
		t.Fatalf("unexpected favorites: %v", filtered)
	}
}

// TestVisibleProjects_CacheHit: a repeat visibleProjects call is served from the memo, not recomputed.
// Given a search query whose filtered result a first call memoized, when visibleProjects runs again with
// no state change, then it returns the very slice the first call built (same backing array) instead of
// running a fresh filter pass.
// Why it matters: visibleProjects runs on every render frame and keystroke, and a silently bypassed memo
// would re-filter the whole project list each time, turning search typing into visible lag on large
// instances.
func TestVisibleProjects_CacheHit(t *testing.T) {
	// The search path allocates a fresh slice on every compute (the no-search
	// page path returns a subslice of allProjects either way), so backing-array
	// identity is the observable that separates a memo hit from a recompute.

	// Given: a search query with its filtered result memoized by a first call
	m := Model{
		allProjects: []gitlab.ProjectNode{
			{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha"},
			{ID: 2, Name: "beta", PathWithNamespace: "team/beta"},
		},
		opts:       Options{ProjectsPerPage: 10},
		pagesReady: map[int]bool{1: true},
		page:       1,
		projectTab: projectTabAll,
	}
	m.search.query = "alpha"
	result1 := m.visibleProjects()
	if len(result1) != 1 || result1[0].ID != 1 {
		t.Fatalf("expected the first call to filter to alpha, got %#v", result1)
	}

	// When: visibleProjects runs again with no state change
	result2 := m.visibleProjects()

	// Then: the memoized slice itself comes back, not a recomputed copy
	if len(result2) != len(result1) || &result2[0] != &result1[0] {
		t.Fatalf("expected the second call to return the memoized slice, got a recomputed one (%p vs %p)",
			&result2[0], &result1[0])
	}
}

// TestVisibleProjects_CacheInvalidation: invalidating after a query change makes filtering take effect.
// Given a memoized unfiltered result, when the query changes to "alpha" and the memo is invalidated, then
// the next call returns only alpha.
// Why it matters: a memo surviving invalidation would keep serving the old filter, making search appear
// dead.
func TestVisibleProjects_CacheInvalidation(t *testing.T) {
	// Given: two projects with the unfiltered result memoized
	m := Model{
		allProjects: []gitlab.ProjectNode{
			{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha"},
			{ID: 2, Name: "beta", PathWithNamespace: "team/beta"},
		},
		opts:       Options{ProjectsPerPage: 10},
		pagesReady: map[int]bool{1: true},
		page:       1,
		projectTab: projectTabAll,
	}
	_ = m.visibleProjects()

	// When: the query changes and the memo is invalidated
	m.search.query = "alpha"
	m.invalidateVisibleCache()
	filtered := m.visibleProjects()

	// Then: the fresh filter takes effect
	if len(filtered) != 1 || filtered[0].Name != "alpha" {
		t.Fatalf("cache not invalidated: got %v", filtered)
	}
}

// TestLRUPipelineStatusCache_BelowMax: the status cache holds entries freely below its capacity.
// Given two statuses in a cache sized at maxPipelineStatusCacheSize, when both are set, then both remain.
// Why it matters: premature eviction would flicker status icons off rows the user can still see.
func TestLRUPipelineStatusCache_BelowMax(t *testing.T) {
	// Given/When: two statuses stored in a cache far below capacity
	cache := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	cache.Set(1, pipelineState{hasInfo: true})
	cache.Set(2, pipelineState{hasInfo: true})

	// Then: nothing is evicted
	if cache.Len() != 2 {
		t.Fatalf("should not evict when below max, got %d", cache.Len())
	}
}

// TestLRUPipelineStatusCache_RemovesOldest: overflowing the status cache evicts the oldest project's entry.
// Given one more status than the capacity, when all are set, then the size holds at the cap and project
// 0's entry is the one gone.
// Why it matters: without the cap a long browsing session accumulates a status entry per project visited
// and never releases the memory.
func TestLRUPipelineStatusCache_RemovesOldest(t *testing.T) {
	// Given/When: one more status than the capacity is stored
	cache := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	for i := 0; i <= maxPipelineStatusCacheSize; i++ {
		cache.Set(i, pipelineState{
			hasInfo:      true,
			lastAccessed: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Then: the size holds at the cap and the oldest entry is the one evicted
	if cache.Len() != maxPipelineStatusCacheSize {
		t.Fatalf("expected %d entries after eviction, got %d", maxPipelineStatusCacheSize, cache.Len())
	}
	if _, exists := cache.Get(0); exists {
		t.Fatal("expected oldest entry (ID=0) to be evicted")
	}
}

// TestAppendPage_SortsIntoCorrectSlot: pages landing out of order still occupy their correct offsets.
// Given page 2 appended before page 1 with two projects per page, when both have landed, then all four
// projects sit in ID order with each page at its slot.
// Why it matters: background page fetches complete in arbitrary order, and misplaced pages would shuffle
// the project list and break page slicing.
func TestAppendPage_SortsIntoCorrectSlot(t *testing.T) {
	// Given: an empty model expecting two projects per page
	m := Model{
		opts:       Options{ProjectsPerPage: 2},
		pagesReady: make(map[int]bool),
	}

	// When: page 2 lands before page 1
	m.appendPage(gitlab.ProjectPage{
		Page:       2,
		TotalPages: 2,
		Projects: []gitlab.ProjectNode{
			{ID: 3, Name: "gamma"},
			{ID: 4, Name: "delta"},
		},
	})
	m.appendPage(gitlab.ProjectPage{
		Page:       1,
		TotalPages: 2,
		Projects: []gitlab.ProjectNode{
			{ID: 1, Name: "alpha"},
			{ID: 2, Name: "beta"},
		},
	})

	// Then: every project sits at its page's offset
	if len(m.allProjects) != 4 {
		t.Fatalf("expected 4 projects, got %d", len(m.allProjects))
	}
	if m.allProjects[0].Name != "alpha" || m.allProjects[2].Name != "gamma" {
		t.Fatalf("projects not in correct slot: %v", m.allProjects)
	}
}

// --- Key handler smoke tests ---

// TestMultiPanelKey_Tab_CyclesPanels: tab walks the sidebar panels in order and wraps.
// Given the projects panel focused, when tab is pressed four times, then focus visits Pipelines, Stages,
// MRs, and wraps back to Projects.
// Why it matters: a broken wrap dead-ends tab cycling on the last panel.
func TestMultiPanelKey_Tab_CyclesPanels(t *testing.T) {
	// Given: the projects panel focused
	m := newMultiPanelModel(PanelProjects)

	// When/Then: each tab press advances one panel, wrapping on the fourth
	updated, _ := m.handleMultiPanelKey(keyMsg("tab"))
	got := updated.(Model)
	if got.focus.Active != PanelPipelines {
		t.Fatalf("Tab from Projects should go to Pipelines, got %d", got.focus.Active)
	}

	updated, _ = got.handleMultiPanelKey(keyMsg("tab"))
	got = updated.(Model)
	if got.focus.Active != PanelStages {
		t.Fatalf("Tab from Pipelines should go to Stages, got %d", got.focus.Active)
	}

	updated, _ = got.handleMultiPanelKey(keyMsg("tab"))
	got = updated.(Model)
	if got.focus.Active != PanelMRs {
		t.Fatalf("Tab from Stages should go to MRs, got %d", got.focus.Active)
	}

	updated, _ = got.handleMultiPanelKey(keyMsg("tab"))
	got = updated.(Model)
	if got.focus.Active != PanelProjects {
		t.Fatalf("Tab from MRs should wrap to Projects, got %d", got.focus.Active)
	}
}

// TestMultiPanelKey_ShiftTab_CyclesReverse: shift+tab cycles the sidebar backwards.
// Given the projects panel focused, when shift+tab is pressed, then focus wraps back to MRs.
// Why it matters: without the reverse direction, overshooting a panel with tab costs a full loop to
// correct.
func TestMultiPanelKey_ShiftTab_CyclesReverse(t *testing.T) {
	// Given: the projects panel focused
	m := newMultiPanelModel(PanelProjects)

	// When/Then: shift+tab wraps backwards to MRs
	msg := tea.KeyMsg{Type: tea.KeyShiftTab}
	updated, _ := m.handleMultiPanelKey(msg)
	got := updated.(Model)
	if got.focus.Active != PanelMRs {
		t.Fatalf("Shift+Tab from Projects should go to MRs, got %d", got.focus.Active)
	}
}

// TestMultiPanelKey_NumberKeys_SwitchPanels: number keys 1-4 jump straight to their panel and 5 does
// nothing.
// Given the projects panel focused, when 1 through 4 are pressed, then focus lands on the matching sidebar
// panel, and 5 leaves focus unchanged.
// Why it matters: the panel titles advertise these numbers, so a drifted mapping sends users to a panel
// other than the one labeled.
func TestMultiPanelKey_NumberKeys_SwitchPanels(t *testing.T) {
	// Given: the projects panel focused and each number key's target panel
	m := newMultiPanelModel(PanelProjects)

	tests := []struct {
		key  string
		want PanelID
	}{
		{"1", PanelProjects},
		{"2", PanelPipelines},
		{"3", PanelStages},
		{"4", PanelMRs},
	}
	for _, tt := range tests {
		// When/Then: the number key focuses its advertised panel
		updated, _ := m.handleMultiPanelKey(keyMsg(tt.key))
		got := updated.(Model)
		if got.focus.Active != tt.want {
			t.Errorf("key %s: expected panel %d, got %d", tt.key, tt.want, got.focus.Active)
		}
	}

	// And: key 5 is a no-op that falls through to the panel handler
	updated, _ := m.handleMultiPanelKey(keyMsg("5"))
	got := updated.(Model)
	if got.focus.Active != PanelProjects {
		t.Errorf("key 5 should not change panel, got %d", got.focus.Active)
	}
}

// TestMultiPanelKey_Plus_TogglesLayout: + flips the sidebar/detail split and flips back.
// Given the default layout, when + is pressed twice, then the layout goes wide and returns to default.
// Why it matters: a stuck toggle traps users in whichever split fits their terminal worse.
func TestMultiPanelKey_Plus_TogglesLayout(t *testing.T) {
	// Given: the default layout
	m := newMultiPanelModel(PanelProjects)
	if m.focus.LayoutMode != LayoutDefault {
		t.Fatal("expected initial LayoutDefault")
	}

	// When/Then: each + press flips the layout and back
	updated, _ := m.handleMultiPanelKey(keyMsg("+"))
	got := updated.(Model)
	if got.focus.LayoutMode != LayoutWide {
		t.Fatalf("expected LayoutWide after +, got %d", got.focus.LayoutMode)
	}
	updated, _ = got.handleMultiPanelKey(keyMsg("+"))
	got = updated.(Model)
	if got.focus.LayoutMode != LayoutDefault {
		t.Fatalf("expected LayoutDefault after second +, got %d", got.focus.LayoutMode)
	}
}

// TestMultiPanelKey_Equals_CyclesScreenMode: = cycles the focused panel through half, full, and back to
// normal.
// Given the normal screen mode, when = is pressed three times, then the mode visits ScreenHalf and
// ScreenFull and wraps to ScreenNormal.
// Why it matters: a broken wrap leaves the focused panel stuck full-screen with the other panels
// unreachable behind it.
func TestMultiPanelKey_Equals_CyclesScreenMode(t *testing.T) {
	// Given: the normal screen mode
	m := newMultiPanelModel(PanelProjects)

	// When/Then: each = press advances one mode, wrapping on the third
	updated, _ := m.handleMultiPanelKey(keyMsg("="))
	got := updated.(Model)
	if got.focus.ScreenMode != ScreenHalf {
		t.Fatalf("expected ScreenHalf after =, got %d", got.focus.ScreenMode)
	}
	updated, _ = got.handleMultiPanelKey(keyMsg("="))
	got = updated.(Model)
	if got.focus.ScreenMode != ScreenFull {
		t.Fatalf("expected ScreenFull, got %d", got.focus.ScreenMode)
	}
	updated, _ = got.handleMultiPanelKey(keyMsg("="))
	got = updated.(Model)
	if got.focus.ScreenMode != ScreenNormal {
		t.Fatalf("expected ScreenNormal (wrap), got %d", got.focus.ScreenMode)
	}
}

// TestProjectsPanelKey_Slash_ActivatesSearch: / opens project search.
// Given the projects panel focused, when / is pressed, then the search state activates.
// Why it matters: search is the primary way to reach a project on large instances, and a dead / key leaves
// only page-by-page scrolling.
func TestProjectsPanelKey_Slash_ActivatesSearch(t *testing.T) {
	// Given: the projects panel focused
	m := newMultiPanelModel(PanelProjects)

	// When/Then: / activates the search state
	updated, _ := m.handleMultiPanelKey(keyMsg("/"))
	got := updated.(Model)
	if !got.search.active {
		t.Fatal("/ should activate search")
	}
}

// TestProjectsPanelKey_F_TogglesFavorite: f favorites the selected project and unfavorites it on repeat.
// Given the first project selected, when f is pressed twice, then the favorite flag for its ID turns on
// and back off.
// Why it matters: a one-way toggle would let favorites accumulate with no way to prune the tab.
func TestProjectsPanelKey_F_TogglesFavorite(t *testing.T) {
	// Given: the first project selected
	m := newMultiPanelModel(PanelProjects)
	m.selected = 0
	m.projectList.Select(0)

	// When/Then: the first f press favorites project ID 1
	updated, _ := m.handleMultiPanelKey(keyMsg("f"))
	got := updated.(Model)
	if !got.favorites[1] { // project ID 1
		t.Fatal("f should add favorite")
	}

	// And: the second press removes it
	updated, _ = got.handleMultiPanelKey(keyMsg("f"))
	got = updated.(Model)
	if got.favorites[1] {
		t.Fatal("f should remove favorite on second press")
	}
}

// TestDetailPanelKey_T_CyclesDetailTab: t advances the pipeline detail tab from Log to Tests.
// Given the detail pane opened from pipelines on the Log tab, when t is pressed, then the tab moves to
// Tests.
// Why it matters: the Tests and Artifacts views are only reachable through this key, so a dead t hides the
// test report entirely.
func TestDetailPanelKey_T_CyclesDetailTab(t *testing.T) {
	// Given: the detail pane opened from pipelines, starting on the Log tab
	m := newMultiPanelModel(PanelDetail)
	m.focus.PrevActive = PanelPipelines
	if m.pipelineView.detailTab != detailTabLog {
		t.Fatal("expected initial detailTabLog")
	}

	// When/Then: t advances to the Tests tab
	updated, _ := m.handleMultiPanelKey(keyMsg("t"))
	got := updated.(Model)
	if got.pipelineView.detailTab != detailTabTests {
		t.Fatalf("expected detailTabTests after t, got %d", got.pipelineView.detailTab)
	}
}

// clipboardCapture records what a copy command wrote through the clipboardWrite
// seam. Setting err before the command runs makes the fake writer fail, which
// exercises the copy-failure path without touching a real clipboard.
type clipboardCapture struct {
	text string
	err  error
}

// captureClipboard swaps clipboardWrite for an in-memory fake for the duration
// of the test (restored via t.Cleanup), so copy tests stay hermetic on machines
// without an OS clipboard and can assert the exact copied payload.
func captureClipboard(t *testing.T) *clipboardCapture {
	t.Helper()
	c := &clipboardCapture{}
	prev := clipboardWrite
	clipboardWrite = func(text string) error {
		if c.err != nil {
			return c.err
		}
		c.text = text
		return nil
	}
	t.Cleanup(func() { clipboardWrite = prev })
	return c
}

// runClipboardCmd executes the tea.Cmd returned by a copy method off-loop and,
// if it produced a clipboardWroteMsg, applies it to the model so tests can
// assert on the post-write m.status. Returns the resulting status string; with
// a nil cmd (a guard short-circuited before the write) the status is whatever
// the guard already set.
func runClipboardCmd(t *testing.T, m Model, cmd tea.Cmd) (Model, string) {
	t.Helper()
	if cmd == nil {
		return m, m.status
	}
	msg := cmd()
	clip, ok := msg.(clipboardWroteMsg)
	if !ok {
		t.Fatalf("expected clipboardWroteMsg, got %T", msg)
	}
	updated, _ := m.handleClipboardWrote(clip)
	return updated.(Model), updated.(Model).status
}

// TestCopyMRComment_WithPosition: copying a positioned discussion writes the file
// reference and every note to the clipboard.
// Given a discussion whose first note carries a file and line, when the comment is
// copied, then the clipboard holds the file:line header plus both notes separated by
// a divider and the status toasts success.
// Why it matters: a reviewer pasting the thread into a commit or reply loses the code
// location if the position header is dropped.
func TestCopyMRComment_WithPosition(t *testing.T) {
	// Given: a selected MR with a positioned two-note discussion
	discussions := NewAsyncCache[int, []gitlab.MRDiscussion]()
	discussions.Set(42, []gitlab.MRDiscussion{
		{
			ID: "disc-1",
			Notes: []gitlab.MRNote{
				{ID: 1, Author: "Alice", Body: "Fix this", FilePath: "main.go", Line: 10},
				{ID: 2, Author: "Bob", Body: "Done"},
			},
		},
	})
	m := Model{
		mrView: mrViewState{
			mrs:                []gitlab.MergeRequestSummary{{IID: 42}},
			selected:           0,
			discussions:        discussions,
			selectedDiscussion: 0,
		},
	}
	capture := captureClipboard(t)

	// When: the comment is copied and the result applied
	cmd := m.copyMRComment()
	_, status := runClipboardCmd(t, m, cmd)

	// Then: the clipboard holds the positioned thread verbatim
	want := "main.go:10\nAlice: Fix this\n\n---\nBob: Done\n"
	if capture.text != want {
		t.Fatalf("copied text = %q, want %q", capture.text, want)
	}

	// And: the status toasts success
	if !strings.Contains(status, "Copied comment") {
		t.Fatalf("expected success status, got %q", status)
	}
}

// TestCopyMRComment_WithoutPosition: copying an unpositioned comment writes just the
// author-prefixed body.
// Given a discussion whose only note has no file position, when the comment is copied,
// then the clipboard holds "Author: body" with no location header and the status
// toasts success.
// Why it matters: a stray or empty position header in general comments corrupts what
// users paste into replies.
func TestCopyMRComment_WithoutPosition(t *testing.T) {
	// Given: a selected MR with a single unpositioned note
	discussions := NewAsyncCache[int, []gitlab.MRDiscussion]()
	discussions.Set(42, []gitlab.MRDiscussion{
		{
			ID: "disc-1",
			Notes: []gitlab.MRNote{
				{ID: 1, Author: "Alice", Body: "Looks good"},
			},
		},
	})
	m := Model{
		mrView: mrViewState{
			mrs:                []gitlab.MergeRequestSummary{{IID: 42}},
			selected:           0,
			discussions:        discussions,
			selectedDiscussion: 0,
		},
	}
	capture := captureClipboard(t)

	// When: the comment is copied and the result applied
	cmd := m.copyMRComment()
	_, status := runClipboardCmd(t, m, cmd)

	// Then: the clipboard holds only the author-prefixed body
	if want := "Alice: Looks good\n"; capture.text != want {
		t.Fatalf("copied text = %q, want %q", capture.text, want)
	}

	// And: the status toasts success
	if !strings.Contains(status, "Copied comment") {
		t.Fatalf("expected success status, got %q", status)
	}
}

// TestWriteClipboardCmd_FailureSetsStatus: a failing clipboard write surfaces the
// copy failure instead of the success toast.
// Given a clipboard writer that errors, when a copy command runs and its message is
// applied, then the status reads "Failed to copy" and nothing is recorded as written.
// Why it matters: without the failure toast a user on a broken clipboard (headless
// SSH, no display) believes the copy landed and pastes stale content.
func TestWriteClipboardCmd_FailureSetsStatus(t *testing.T) {
	// Given: a clipboard writer that fails
	capture := captureClipboard(t)
	capture.err = errors.New("no clipboard available")

	// When: a copy command runs and its message is applied
	_, status := runClipboardCmd(t, Model{}, writeClipboardCmd("payload", "Copied payload"))

	// Then: the status shows the failure wording, not the success toast
	if status != "Failed to copy" {
		t.Fatalf("status = %q, want %q", status, "Failed to copy")
	}

	// And: nothing was recorded as written
	if capture.text != "" {
		t.Fatalf("expected no captured write, got %q", capture.text)
	}
}

// TestCopyMRComment_NoDiscussions: copying with no discussions loaded toasts instead of writing.
// Given a selected MR without cached discussions, when the comment copy runs, then no clipboard command is
// returned and the status says there are no discussions.
// Why it matters: silently copying nothing would let the user paste stale clipboard content believing it
// is the comment.
func TestCopyMRComment_NoDiscussions(t *testing.T) {
	// Given: a selected MR with no cached discussions
	m := Model{
		mrView: mrViewState{
			mrs:      []gitlab.MergeRequestSummary{{IID: 42}},
			selected: 0,
		},
	}

	// When: the comment copy runs
	cmd := m.copyMRComment()

	// Then: the guard returns no command and toasts the reason
	if cmd != nil {
		t.Fatalf("expected nil cmd on guard path, got %T", cmd)
	}
	if !strings.Contains(m.status, "No discussions") {
		t.Fatalf("expected no discussions status, got %q", m.status)
	}
}

// TestCopyExplorerURL_File: copying a selected file writes its blob URL.
// Given an explorer with a file selected on ref main, when the URL is copied, then
// the clipboard holds the project's /-/blob/main/<path> URL and the status names
// the file.
// Why it matters: a wrong kind segment or ref yields a 404 for whoever the URL is
// shared with.
func TestCopyExplorerURL_File(t *testing.T) {
	// Given: an explorer with a file entry selected
	m := Model{
		explorer: explorerState{
			project: gitlab.ProjectNode{WebURL: "https://gitlab.com/team/app"},
			ref:     "main",
			stack: []dirState{{
				path:     "",
				selected: 0,
				entries: []gitlab.TreeNode{
					{Path: "src/main.go", Name: "main.go", Type: "blob"},
				},
			}},
		},
	}
	capture := captureClipboard(t)

	// When: the URL is copied and the result applied
	m, cmd := m.copyExplorerURL()
	_, status := runClipboardCmd(t, m, cmd)

	// Then: the clipboard holds the blob URL for the file at the explorer's ref
	if want := "https://gitlab.com/team/app/-/blob/main/src/main.go"; capture.text != want {
		t.Fatalf("copied text = %q, want %q", capture.text, want)
	}

	// And: the status names the copied file
	if !strings.Contains(status, "Copied main.go URL") {
		t.Fatalf("expected success status, got %q", status)
	}
}

// TestCopyExplorerURL_Dir: copying a selected directory writes its tree URL.
// Given an explorer with a directory selected on ref develop, when the URL is copied,
// then the clipboard holds the project's /-/tree/develop/<path> URL and the status
// names the directory.
// Why it matters: directories linked with the blob kind render as a broken page
// instead of a listing.
func TestCopyExplorerURL_Dir(t *testing.T) {
	// Given: an explorer with a directory entry selected
	m := Model{
		explorer: explorerState{
			project: gitlab.ProjectNode{WebURL: "https://gitlab.com/team/app"},
			ref:     "develop",
			stack: []dirState{{
				path:     "",
				selected: 0,
				entries: []gitlab.TreeNode{
					{Path: "src", Name: "src", Type: "tree"},
				},
			}},
		},
	}
	capture := captureClipboard(t)

	// When: the URL is copied and the result applied
	m, cmd := m.copyExplorerURL()
	_, status := runClipboardCmd(t, m, cmd)

	// Then: the clipboard holds the tree URL for the directory at the explorer's ref
	if want := "https://gitlab.com/team/app/-/tree/develop/src"; capture.text != want {
		t.Fatalf("copied text = %q, want %q", capture.text, want)
	}

	// And: the status names the copied directory
	if !strings.Contains(status, "Copied src URL") {
		t.Fatalf("expected success status, got %q", status)
	}
}

// TestCopyExplorerURL_NoEntry: copying with nothing selected toasts instead of writing.
// Given an explorer directory with no entries, when the URL copy runs, then no clipboard command is
// returned and the status reads "No file selected".
// Why it matters: writing an empty or partial URL would replace whatever the user had on the clipboard
// with garbage.
func TestCopyExplorerURL_NoEntry(t *testing.T) {
	// Given: an explorer directory with nothing to select
	m := Model{
		explorer: explorerState{
			project: gitlab.ProjectNode{WebURL: "https://gitlab.com/team/app"},
			ref:     "main",
			stack:   []dirState{{path: "", entries: nil}},
		},
	}

	// When: the URL copy runs
	m, cmd := m.copyExplorerURL()

	// Then: the guard returns no command and toasts the reason
	if cmd != nil {
		t.Fatalf("expected nil cmd on guard path, got %T", cmd)
	}
	if m.status != "No file selected" {
		t.Fatalf("expected 'No file selected', got %q", m.status)
	}
}
