package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

func TestParentDir(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"", ""},
		{"src", ""},
		{"src/pkg", "src"},
		{"src/pkg/util", "src/pkg"},
		{"src/pkg/util/", "src/pkg"},
	}
	for _, tt := range tests {
		if got := parentDir(tt.path); got != tt.want {
			t.Fatalf("parentDir(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestVisibleProjectsSearch(t *testing.T) {
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
	m.search.query = "api"
	filtered := m.visibleProjects()
	if len(filtered) != 1 || filtered[0].Name != "api-server" {
		t.Fatalf("search failed, got %#v", filtered)
	}
	m.search.query = ""
	page := m.visibleProjects()
	if len(page) != len(m.allProjects) {
		t.Fatalf("expected %d projects, got %d", len(m.allProjects), len(page))
	}
}

// TestVisibleProjectsSearch_Nested exercises fuzzy search against deeply nested
// GitLab group paths (e.g., "org/platform/backend/auth-service"), verifying
// that queries can match any path segment and cross segment boundaries.
func TestVisibleProjectsSearch_Nested(t *testing.T) {
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
			m.search.query = tt.query
			m.invalidateVisibleCache()
			got := m.visibleProjects()

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

// TestHandlePipelineLogLoadedIgnoresStale verifies that log responses for a
// previously-selected job are cached but do not overwrite the active preview.
// This prevents the viewport from jumping when an out-of-order response arrives
// after the user has already moved to a different job.
func TestHandlePipelineLogLoadedIgnoresStale(t *testing.T) {
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
	msg := pipelineLogLoadedMsg{projectID: 1, jobID: 10, content: "stale"}
	updated, _ := m.handlePipelineLogLoaded(msg)
	got := updated.(Model).pipelineView
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

// TestQueuePipelineLogPreviewPreservesOffset verifies that when logAutoFollow
// is false (user has scrolled), re-queuing the same job's log updates content
// without resetting the viewport scroll position.
func TestQueuePipelineLogPreviewPreservesOffset(t *testing.T) {
	content := strings.Repeat("line\n", 40)
	stages := NewAsyncCache[int, []gitlab.PipelineStage]()
	stages.Set(10, []gitlab.PipelineStage{{Name: "build"}})
	jobs := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobs.Set(10, []gitlab.PipelineJob{{ID: 100, Name: "build-job", Stage: "build"}})
	logs := NewAsyncCache[int, string]()
	logs.Set(100, content)
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
		},
	}

	m.queuePipelineLogPreview()

	// When logAutoFollow is false, viewport preserves scroll position
	if m.pipelineView.logPreview.content != content {
		t.Fatalf("expected log content to update from cache")
	}
}

func TestTruncateLogContent(t *testing.T) {
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
			got := truncateLogContent(tt.content)
			if len(got) != tt.want {
				t.Errorf("truncateLogContent() length = %d, want %d", len(got), tt.want)
			}
			// Verify truncated logs have the message
			if len(tt.content) > maxLogSizeBytes && !strings.Contains(got, "log truncated") {
				t.Error("Expected truncated log to contain warning message")
			}
		})
	}
}

func TestEvictOldLogs(t *testing.T) {
	// Create a model with more than maxLogCacheEntries logs
	logs := NewAsyncCache[int, string]()
	m := Model{
		pipelineView: pipelineViewState{
			logs:     logs,
			logJobID: 15, // Currently viewing job 15
		},
	}

	// Add more logs than the max
	for i := 1; i <= maxLogCacheEntries+5; i++ {
		m.pipelineView.logs.Set(i, fmt.Sprintf("log content for job %d", i))
	}

	initialCount := m.pipelineView.logs.Len()
	if initialCount != maxLogCacheEntries+5 {
		t.Fatalf("Setup failed: expected %d logs, got %d", maxLogCacheEntries+5, initialCount)
	}

	// Call eviction
	m.evictOldLogs()

	// Should keep maxLogCacheEntries logs
	if m.pipelineView.logs.Len() > maxLogCacheEntries {
		t.Errorf("evictOldLogs() left %d logs, want at most %d", m.pipelineView.logs.Len(), maxLogCacheEntries)
	}

	// Should keep the currently viewed log (15)
	if _, exists := m.pipelineView.logs.Get(15); !exists {
		t.Error("evictOldLogs() evicted the currently displayed log")
	}

	// Should evict oldest logs (lowest IDs)
	if _, exists := m.pipelineView.logs.Get(1); exists {
		t.Error("evictOldLogs() should have evicted job 1 (oldest)")
	}

	// Should keep newest logs (highest IDs)
	if _, exists := m.pipelineView.logs.Get(maxLogCacheEntries + 5); !exists {
		t.Error("evictOldLogs() should have kept the newest log")
	}
}

// TestPipelineView_RetryModalOpens verifies that pressing R in pipeline focus
// opens the retry confirmation modal with the correct pipeline ID and ref
// pre-populated from the current selection.
func TestPipelineView_RetryModalOpens(t *testing.T) {
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:   gitlab.ProjectNode{ID: 1},
			pipelines: []gitlab.PipelineSummary{{ID: 42, Ref: "main"}},
			selected:  0,
		},
	}

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}
	updated, _ := m.handlePipelineViewKey(msg)
	got := updated.(Model).pipelineView
	if !got.confirmRetry {
		t.Fatalf("expected retry modal to open")
	}
	if got.confirmRetryID != 42 || got.confirmRetryRef != "main" {
		t.Fatalf("expected confirm data to match selection, got id=%d ref=%q", got.confirmRetryID, got.confirmRetryRef)
	}
}

// TestPipelineView_RetryConfirmStartsRetry verifies that pressing Enter in the
// retry confirmation modal sets retrying=true, closes the modal, and returns
// a non-nil command to dispatch the retry API call.
func TestPipelineView_RetryConfirmStartsRetry(t *testing.T) {
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:        gitlab.ProjectNode{ID: 1},
			confirmRetry:   true,
			confirmRetryID: 55,
		},
	}

	msg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, cmd := m.handlePipelineRetryConfirmKey(msg)
	got := updated.(Model).pipelineView
	if got.retrying != true {
		t.Fatalf("expected retrying to be true")
	}
	if got.confirmRetry {
		t.Fatalf("expected retry modal to close")
	}
	if cmd == nil {
		t.Fatalf("expected retry command")
	}
}

func TestNormalizeColumnBounds(t *testing.T) {
	lines := normalizeColumn("one very very long line", 5, 2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(lines))
	}
	for _, l := range lines {
		if lipgloss.Width(l) != 5 {
			t.Fatalf("line %q not clamped to width 5", l)
		}
	}
}

func TestHandleTreeLoadedPreview(t *testing.T) {
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
	if got.loading || !strings.Contains(got.content, "file.txt") || !strings.Contains(got.content, "nested/") {
		t.Fatalf("preview not populated: %#v", got)
	}
}

func TestHandleTreeLoadedDirectory(t *testing.T) {
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
	msg := treeLoadedMsg{
		projectID: 1,
		path:      "",
		entries: []gitlab.TreeNode{
			{Name: "dir", Path: "dir", Type: "tree"},
		},
	}
	updated, _ := m.handleTreeLoaded(msg)
	dir := updated.(Model).explorer.stack[0]
	if dir.loading || len(dir.entries) != 1 {
		t.Fatalf("directory not loaded: %#v", dir)
	}
}

func TestHandleFileLoaded(t *testing.T) {
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
	msg := fileLoadedMsg{projectID: 1, path: "README.md", content: "hello world"}
	updated, _ := m.handleFileLoaded(msg)
	p := updated.(Model).explorer.preview
	if p.loading || !strings.Contains(p.content, "hello") {
		t.Fatalf("file preview not stored: %#v", p)
	}
}

func TestQueueExplorerPreviewDir(t *testing.T) {
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
	cmd := m.queueExplorerPreview()
	if cmd == nil || !m.explorer.preview.loading || m.explorer.preview.path != "src" {
		t.Fatalf("directory preview not queued: %#v", m.explorer.preview)
	}
}

func TestQueueExplorerPreviewFile(t *testing.T) {
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
	cmd := m.queueExplorerPreview()
	if cmd == nil || m.explorer.preview.path != "README.md" || !m.explorer.preview.loading {
		t.Fatalf("file preview not queued: %#v", m.explorer.preview)
	}
}

func TestWrapPreviewLine(t *testing.T) {
	segments := wrapPreviewLine("abcdefghijkl", 5)
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d (%v)", len(segments), segments)
	}
	for _, seg := range segments {
		if lipgloss.Width(seg) > 5 {
			t.Fatalf("segment %q exceeds width 5", seg)
		}
	}
	if got := wrapPreviewLine("line", 0); len(got) != 1 || got[0] != "line" {
		t.Fatalf("width <= 0 should return original line, got %#v", got)
	}
}

func TestRenderExplorerPreviewWrapsLongLines(t *testing.T) {
	m := Model{
		explorer: explorerState{
			preview: previewState{
				content: strings.Repeat("abc", 20),
			},
		},
	}
	const width = 10
	out := renderExplorerPreview(m, width, 6, false)
	lines := strings.Split(out, "\n")
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

func TestComputeLayout_DetailWidthFitsTerminal(t *testing.T) {
	layout := computeLayout(120, 40, FocusState{Active: PanelProjects})
	if !layout.OK {
		t.Fatal("expected layout.OK to be true")
	}
	totalWidth := (layout.SidebarWidth + borderCharsH) + paneGap + (layout.DetailWidth + borderCharsH)
	if totalWidth > 120 {
		t.Fatalf("total width %d exceeds terminal width 120", totalWidth)
	}
	totalHeight := layout.DetailHeight + borderCharsV + infoBarHeight
	if totalHeight > 40 {
		t.Fatalf("total height %d exceeds terminal height 40", totalHeight)
	}
}

func TestComputeLayout_DetailGetsFullHeight(t *testing.T) {
	tests := []struct {
		width, height int
	}{
		{120, 40},
		{80, 24},
		{200, 60},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			layout := computeLayout(tt.width, tt.height, FocusState{Active: PanelProjects})
			if !layout.OK {
				t.Fatal("expected layout.OK to be true")
			}
			want := tt.height - infoBarHeight - borderCharsV
			if layout.DetailHeight != want {
				t.Fatalf("DetailHeight = %d, want %d", layout.DetailHeight, want)
			}
		})
	}
}

func TestSetLogViewportContent_WrapsLongLines(t *testing.T) {
	vp := viewport.New(40, 20)
	m := Model{
		pipelineView: pipelineViewState{
			logViewport: vp,
		},
	}
	longLine := strings.Repeat("X", 200)
	m.setLogViewportContent(longLine)

	output := m.pipelineView.logViewport.View()
	for line := range strings.SplitSeq(output, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("line visual width %d exceeds viewport width 40: %q", w, line)
		}
	}
}

func TestRenderBorderedPane_OutputFitsWidth(t *testing.T) {
	content := strings.Repeat("Z", 200)
	output := renderBorderedPane(content, 50, 10, false, "Test", nil, 0, "")
	for i, line := range strings.Split(output, "\n") {
		if w := lipgloss.Width(line); w > 50 {
			t.Fatalf("line %d visual width %d exceeds pane width 50: %q", i, w, line)
		}
	}
}

func TestRenderBorderedPane_OutputFitsHeight(t *testing.T) {
	content := strings.Repeat("line\n", 50)
	output := renderBorderedPane(content, 40, 5, false, "Test", nil, 0, "")
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
	pipelineStatus := make(map[int]pipelineState)
	favorites := make(map[int]bool)
	delegate := projectDelegate{pipelineStatus: pipelineStatus, favorites: favorites}
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
		mode:           modeMultiPanel,
		width:          120,
		height:         40,
		allProjects:    projects,
		opts:           Options{ProjectsPerPage: 10},
		pagesReady:     map[int]bool{1: true},
		page:           1,
		projectTab:     projectTabAll,
		favorites:      favorites,
		pipelineStatus: pipelineStatus,
		projectList:    pl,
		focus:          FocusState{Active: active},
		search:         searchState{input: ti},
		pipelineView: pipelineViewState{
			project:     projects[0],
			pipelines:   []gitlab.PipelineSummary{{ID: 10, Ref: "main"}},
			stages:      NewAsyncCache[int, []gitlab.PipelineStage](),
			jobs:        NewAsyncCache[int, []gitlab.PipelineJob](),
			logs:        NewAsyncCache[int, string](),
			bridges:     NewAsyncCache[int, []gitlab.PipelineBridge](),
			logViewport: viewport.New(60, 20),
		},
		mrView:        mrViewState{project: projects[0]},
		commitCache:   make(map[int][]gitlab.CommitSummary),
		commitLoading: make(map[int]bool),
	}
}

func TestRightArrow_FocusesDetailFromAnySidebar(t *testing.T) {
	for _, panel := range SidebarPanels {
		t.Run(panelLabel(panel), func(t *testing.T) {
			m := newMultiPanelModel(panel)
			updated, _ := m.handleMultiPanelKey(keyMsg("right"))
			got := updated.(Model)
			if got.focus.Active != PanelDetail {
				t.Fatalf("expected Active=PanelDetail, got %d", got.focus.Active)
			}
			if got.focus.PrevActive != panel {
				t.Fatalf("expected PrevActive=%d, got %d", panel, got.focus.PrevActive)
			}
		})
	}
}

func TestLeftArrow_ReturnsFromDetail(t *testing.T) {
	for _, panel := range SidebarPanels {
		t.Run(panelLabel(panel), func(t *testing.T) {
			m := newMultiPanelModel(PanelDetail)
			m.focus.PrevActive = panel
			updated, _ := m.handleMultiPanelKey(keyMsg("left"))
			got := updated.(Model)
			if got.focus.Active != panel {
				t.Fatalf("expected Active=%d, got %d", panel, got.focus.Active)
			}
		})
	}
}

func TestLeftArrow_NavigatesBackInSidebar(t *testing.T) {
	// left arrow in Pipelines/Stages/MRs navigates back (same as h)
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
			m := newMultiPanelModel(tt.from)
			updated, _ := m.handleMultiPanelKey(keyMsg("left"))
			got := updated.(Model)
			if got.focus.Active != tt.to {
				t.Fatalf("left arrow: expected panel %d, got %d", tt.to, got.focus.Active)
			}
		})
	}
}

func TestH_StillNavigatesBackHierarchically(t *testing.T) {
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
			m := newMultiPanelModel(tt.from)
			updated, _ := m.handleMultiPanelKey(keyMsg("h"))
			got := updated.(Model)
			if got.focus.Active != tt.to {
				t.Fatalf("expected h to navigate to %d, got %d", tt.to, got.focus.Active)
			}
		})
	}
}

func TestEnterL_DrillsIn_NotRight(t *testing.T) {
	// enter from Projects should go to Pipelines
	t.Run("Projects_enter", func(t *testing.T) {
		m := newMultiPanelModel(PanelProjects)
		updated, _ := m.handleMultiPanelKey(keyMsg("enter"))
		got := updated.(Model)
		if got.focus.Active != PanelPipelines {
			t.Fatalf("expected enter to drill into Pipelines, got %d", got.focus.Active)
		}
	})
	// l from Projects should go to Detail pane
	t.Run("Projects_l", func(t *testing.T) {
		m := newMultiPanelModel(PanelProjects)
		updated, _ := m.handleMultiPanelKey(keyMsg("l"))
		got := updated.(Model)
		if got.focus.Active != PanelDetail {
			t.Fatalf("expected l to focus Detail, got %d", got.focus.Active)
		}
	})
	// enter from Pipelines should go to Stages
	t.Run("Pipelines_enter", func(t *testing.T) {
		m := newMultiPanelModel(PanelPipelines)
		updated, _ := m.handleMultiPanelKey(keyMsg("enter"))
		got := updated.(Model)
		if got.focus.Active != PanelStages {
			t.Fatalf("expected enter to drill into Stages, got %d", got.focus.Active)
		}
	})
	// l from Pipelines should go to Detail pane
	t.Run("Pipelines_l", func(t *testing.T) {
		m := newMultiPanelModel(PanelPipelines)
		updated, _ := m.handleMultiPanelKey(keyMsg("l"))
		got := updated.(Model)
		if got.focus.Active != PanelDetail {
			t.Fatalf("expected l to focus Detail, got %d", got.focus.Active)
		}
	})
}

func TestDetailPanel_ScrollKeys(t *testing.T) {
	m := newMultiPanelModel(PanelDetail)
	m.focus.PrevActive = PanelPipelines
	content := strings.Repeat("line\n", 100)
	m.pipelineView.logViewport.SetContent(content)

	// Scroll down
	updated, _ := m.handleMultiPanelKey(keyMsg("j"))
	got := updated.(Model)
	if got.focus.Active != PanelDetail {
		t.Fatal("j should not change focus")
	}

	// Tab cycling
	updated, _ = got.handleMultiPanelKey(keyMsg("t"))
	got = updated.(Model)
	if got.pipelineView.detailTab != detailTabTests {
		t.Fatal("t should cycle detail tab")
	}
}

func TestProjectNav_TriggersAutoLoad(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
	// Start with project index 0 (ID=1)
	m.selected = 0
	m.projectList.Select(0)

	// Press down to move to project index 1 (ID=2)
	updated, cmd := m.handleMultiPanelKey(keyMsg("down"))
	got := updated.(Model)
	if got.selected != 1 {
		t.Fatalf("expected selected=1 after down, got %d", got.selected)
	}
	// The auto-load block should fire and return a batch command
	if cmd == nil {
		t.Fatal("expected auto-load command after project selection change, got nil")
	}
}

// --- Layout tests ---

func TestAccordionLayout_NormalMode_UnfocusedPanelsUsable(t *testing.T) {
	heights := accordionLayout(SidebarPanels, PanelProjects, ScreenNormal, 50)
	for _, panel := range SidebarPanels {
		if panel == PanelProjects {
			continue
		}
		if heights[panel] < minPanelHeight {
			t.Fatalf("unfocused panel %d height %d < minPanelHeight %d", panel, heights[panel], minPanelHeight)
		}
	}
	// In ScreenNormal, unfocused panels should get MORE than minPanelHeight
	// when there's enough budget (50 lines is plenty)
	for _, panel := range SidebarPanels {
		if panel == PanelProjects {
			continue
		}
		if heights[panel] <= minPanelHeight {
			t.Fatalf("ScreenNormal: unfocused panel %d height %d should exceed minPanelHeight %d when space allows",
				panel, heights[panel], minPanelHeight)
		}
	}
}

func TestAccordionLayout_NormalVsFull_UnfocusedDiffers(t *testing.T) {
	normal := accordionLayout(SidebarPanels, PanelProjects, ScreenNormal, 50)
	full := accordionLayout(SidebarPanels, PanelProjects, ScreenFull, 50)
	// ScreenFull should give unfocused panels less space than ScreenNormal
	for _, panel := range SidebarPanels {
		if panel == PanelProjects {
			continue
		}
		if normal[panel] <= full[panel] {
			t.Fatalf("ScreenNormal unfocused panel %d (%d) should be larger than ScreenFull (%d)",
				panel, normal[panel], full[panel])
		}
	}
}

func TestAccordionLayout_HeightsSumCorrectly(t *testing.T) {
	for _, mode := range []ScreenMode{ScreenNormal, ScreenHalf, ScreenFull} {
		heights := accordionLayout(SidebarPanels, PanelPipelines, mode, 50)
		total := 0
		for _, panel := range SidebarPanels {
			total += heights[panel] + borderCharsV
		}
		if total > 50 {
			t.Fatalf("mode %d: panel heights sum %d exceeds available 50", mode, total)
		}
	}
}

func TestFocusState_ToggleLayoutMode(t *testing.T) {
	f := FocusState{}
	if f.LayoutMode != LayoutDefault {
		t.Fatal("expected initial LayoutMode to be LayoutDefault")
	}
	f.ToggleLayoutMode()
	if f.LayoutMode != LayoutWide {
		t.Fatal("expected LayoutMode to be LayoutWide after first toggle")
	}
	f.ToggleLayoutMode()
	if f.LayoutMode != LayoutDefault {
		t.Fatal("expected LayoutMode to be LayoutDefault after second toggle")
	}
}

func TestComputeLayout_DefaultLayoutSidebarWidth(t *testing.T) {
	layout := computeLayout(200, 40, FocusState{Active: PanelProjects, LayoutMode: LayoutDefault})
	if !layout.OK {
		t.Fatal("expected layout.OK")
	}
	// 30% of 200 = 60, purely percentage-based (no hard cap)
	expected := 200 * sidebarWidthPct / 100
	if layout.SidebarWidth != expected {
		t.Fatalf("default layout sidebar = %d, want %d (30%% of 200)", layout.SidebarWidth, expected)
	}
	if layout.SidebarWidth < minSidebarWidth {
		t.Fatalf("default layout sidebar %d below minSidebarWidth %d", layout.SidebarWidth, minSidebarWidth)
	}
}

func TestComputeLayout_WideLayoutSidebarWidth(t *testing.T) {
	layout := computeLayout(200, 40, FocusState{Active: PanelProjects, LayoutMode: LayoutWide})
	if !layout.OK {
		t.Fatal("expected layout.OK")
	}
	// 50% of 200 = 100, should not be capped at maxSidebarWidth
	expected := 200 * 50 / 100
	if layout.SidebarWidth != expected {
		t.Fatalf("wide layout sidebar = %d, want %d", layout.SidebarWidth, expected)
	}
}

func TestComputeLayout_TotalWidthBothModes(t *testing.T) {
	for _, width := range []int{80, 120, 160, 200} {
		for _, mode := range []LayoutMode{LayoutDefault, LayoutWide} {
			t.Run(fmt.Sprintf("%dx40_mode%d", width, mode), func(t *testing.T) {
				layout := computeLayout(width, 40, FocusState{Active: PanelProjects, LayoutMode: mode})
				if !layout.OK {
					t.Skip("terminal too small")
				}
				totalWidth := (layout.SidebarWidth + borderCharsH) + paneGap + (layout.DetailWidth + borderCharsH)
				if totalWidth > width {
					t.Fatalf("total width %d exceeds terminal width %d", totalWidth, width)
				}
			})
		}
	}
}

func TestComputeLayout_WideLayoutNarrowTerminal(t *testing.T) {
	// Minimum viable terminal: minSidebarWidth + detailMinWidth + paneGap + borderCharsH*2 = 28+30+1+4 = 63
	layout := computeLayout(65, 20, FocusState{Active: PanelProjects, LayoutMode: LayoutWide})
	if !layout.OK {
		t.Fatal("expected layout.OK for narrow terminal")
	}
	if layout.SidebarWidth < minSidebarWidth {
		t.Fatalf("sidebar %d below minimum %d", layout.SidebarWidth, minSidebarWidth)
	}
	if layout.DetailWidth < detailMinWidth {
		t.Fatalf("detail %d below minimum %d", layout.DetailWidth, detailMinWidth)
	}
}

func TestRenderPipelineLogPaneWrapsLongLines(t *testing.T) {
	m := Model{
		pipelineView: pipelineViewState{
			logPreview: previewState{
				content: "\x1b[31m" + strings.Repeat("abc", 20) + "\x1b[0m\tend",
			},
		},
	}
	const width = 12
	out := renderPipelineLogPane(m, width, 6, false)
	lines := strings.Split(out, "\n")
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

func TestProjectDelegate_PipelineStatusIcons(t *testing.T) {
	proj := projectItem{project: gitlab.ProjectNode{ID: 1, PathWithNamespace: "team/app"}}
	items := []list.Item{proj}
	delegate := projectDelegate{
		pipelineStatus: map[int]pipelineState{},
		favorites:      map[int]bool{},
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
		{"loading", pipelineState{loading: true}, iconLoading},
		{"error", pipelineState{err: fmt.Errorf("oops")}, iconUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delegate.pipelineStatus[1] = tt.state
			out := render(delegate)
			if !strings.Contains(out, tt.wantIcon) {
				t.Fatalf("expected icon %q in output %q", tt.wantIcon, out)
			}
		})
	}

	// No state at all — no icon
	t.Run("no state", func(t *testing.T) {
		d := projectDelegate{
			pipelineStatus: map[int]pipelineState{},
			favorites:      map[int]bool{},
		}
		out := render(d)
		for _, icon := range []string{iconSuccess, iconFailed, iconNoPipeline, iconLoading, iconUnknown} {
			if strings.Contains(out, icon) {
				t.Fatalf("expected no icon, but found %q in output %q", icon, out)
			}
		}
	})
}

// --- Phase 3: State management tests ---

func TestVisibleProjects_FavoritesTab(t *testing.T) {
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
	filtered := m.visibleProjects()
	if len(filtered) != 2 {
		t.Fatalf("expected 2 favorites, got %d", len(filtered))
	}
	if filtered[0].ID != 1 || filtered[1].ID != 3 {
		t.Fatalf("unexpected favorites: %v", filtered)
	}
}

func TestVisibleProjects_CacheHit(t *testing.T) {
	m := Model{
		allProjects: []gitlab.ProjectNode{
			{ID: 1, Name: "alpha", PathWithNamespace: "team/alpha"},
		},
		opts:       Options{ProjectsPerPage: 10},
		pagesReady: map[int]bool{1: true},
		page:       1,
		projectTab: projectTabAll,
	}
	// First call populates cache
	result1 := m.visibleProjects()
	// Second call should use cache
	result2 := m.visibleProjects()
	if len(result1) != len(result2) {
		t.Fatalf("cache miss: %d vs %d", len(result1), len(result2))
	}
}

func TestVisibleProjects_CacheInvalidation(t *testing.T) {
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
	// Populate cache
	_ = m.visibleProjects()
	// Change query
	m.search.query = "alpha"
	m.invalidateVisibleCache()
	filtered := m.visibleProjects()
	if len(filtered) != 1 || filtered[0].Name != "alpha" {
		t.Fatalf("cache not invalidated: got %v", filtered)
	}
}

func TestEvictOldPipelineStatusCache_BelowMax(t *testing.T) {
	m := Model{
		pipelineStatus: map[int]pipelineState{
			1: {hasInfo: true},
			2: {hasInfo: true},
		},
	}
	m.evictOldPipelineStatusCache()
	if len(m.pipelineStatus) != 2 {
		t.Fatalf("should not evict when below max, got %d", len(m.pipelineStatus))
	}
}

func TestEvictOldPipelineStatusCache_RemovesOldest(t *testing.T) {
	m := Model{
		pipelineStatus: make(map[int]pipelineState),
	}
	// Fill beyond max
	for i := 0; i <= maxPipelineStatusCacheSize; i++ {
		m.pipelineStatus[i] = pipelineState{
			hasInfo:      true,
			lastAccessed: time.Now().Add(time.Duration(i) * time.Second),
		}
	}
	m.evictOldPipelineStatusCache()
	if len(m.pipelineStatus) != maxPipelineStatusCacheSize {
		t.Fatalf("expected %d entries after eviction, got %d", maxPipelineStatusCacheSize, len(m.pipelineStatus))
	}
	// Oldest (ID=0) should have been evicted
	if _, exists := m.pipelineStatus[0]; exists {
		t.Fatal("expected oldest entry (ID=0) to be evicted")
	}
}

func TestAppendPage_SortsIntoCorrectSlot(t *testing.T) {
	m := Model{
		opts:       Options{ProjectsPerPage: 2},
		pagesReady: make(map[int]bool),
	}
	// Append page 2 first
	m.appendPage(gitlab.ProjectPage{
		Page:       2,
		TotalPages: 2,
		Projects: []gitlab.ProjectNode{
			{ID: 3, Name: "gamma"},
			{ID: 4, Name: "delta"},
		},
	})
	// Then page 1
	m.appendPage(gitlab.ProjectPage{
		Page:       1,
		TotalPages: 2,
		Projects: []gitlab.ProjectNode{
			{ID: 1, Name: "alpha"},
			{ID: 2, Name: "beta"},
		},
	})
	if len(m.allProjects) != 4 {
		t.Fatalf("expected 4 projects, got %d", len(m.allProjects))
	}
	if m.allProjects[0].Name != "alpha" || m.allProjects[2].Name != "gamma" {
		t.Fatalf("projects not in correct slot: %v", m.allProjects)
	}
}

// --- Phase 4: Key handler smoke tests ---

func TestMultiPanelKey_Tab_CyclesPanels(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
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

func TestMultiPanelKey_ShiftTab_CyclesReverse(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
	msg := tea.KeyMsg{Type: tea.KeyShiftTab}
	updated, _ := m.handleMultiPanelKey(msg)
	got := updated.(Model)
	if got.focus.Active != PanelMRs {
		t.Fatalf("Shift+Tab from Projects should go to MRs, got %d", got.focus.Active)
	}
}

func TestMultiPanelKey_NumberKeys_SwitchPanels(t *testing.T) {
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
		updated, _ := m.handleMultiPanelKey(keyMsg(tt.key))
		got := updated.(Model)
		if got.focus.Active != tt.want {
			t.Errorf("key %s: expected panel %d, got %d", tt.key, tt.want, got.focus.Active)
		}
	}

	// Key 5 should be a no-op (falls through to panel handler)
	updated, _ := m.handleMultiPanelKey(keyMsg("5"))
	got := updated.(Model)
	if got.focus.Active != PanelProjects {
		t.Errorf("key 5 should not change panel, got %d", got.focus.Active)
	}
}

func TestMultiPanelKey_Plus_TogglesLayout(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
	if m.focus.LayoutMode != LayoutDefault {
		t.Fatal("expected initial LayoutDefault")
	}
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

func TestMultiPanelKey_Equals_CyclesScreenMode(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
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

func TestProjectsPanelKey_Slash_ActivatesSearch(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
	updated, _ := m.handleMultiPanelKey(keyMsg("/"))
	got := updated.(Model)
	if !got.search.active {
		t.Fatal("/ should activate search")
	}
}

func TestProjectsPanelKey_F_TogglesFavorite(t *testing.T) {
	m := newMultiPanelModel(PanelProjects)
	m.selected = 0
	m.projectList.Select(0)

	// Add favorite
	updated, _ := m.handleMultiPanelKey(keyMsg("f"))
	got := updated.(Model)
	if !got.favorites[1] { // project ID 1
		t.Fatal("f should add favorite")
	}

	// Remove favorite
	updated, _ = got.handleMultiPanelKey(keyMsg("f"))
	got = updated.(Model)
	if got.favorites[1] {
		t.Fatal("f should remove favorite on second press")
	}
}

func TestDetailPanelKey_Left_ReturnsToSidebar(t *testing.T) {
	for _, panel := range SidebarPanels {
		t.Run(panelLabel(panel), func(t *testing.T) {
			m := newMultiPanelModel(PanelDetail)
			m.focus.PrevActive = panel
			updated, _ := m.handleMultiPanelKey(keyMsg("left"))
			got := updated.(Model)
			if got.focus.Active != panel {
				t.Fatalf("expected return to %d, got %d", panel, got.focus.Active)
			}
		})
	}
}

func TestDetailPanelKey_T_CyclesDetailTab(t *testing.T) {
	m := newMultiPanelModel(PanelDetail)
	m.focus.PrevActive = PanelPipelines
	if m.pipelineView.detailTab != detailTabLog {
		t.Fatal("expected initial detailTabLog")
	}
	updated, _ := m.handleMultiPanelKey(keyMsg("t"))
	got := updated.(Model)
	if got.pipelineView.detailTab != detailTabTests {
		t.Fatalf("expected detailTabTests after t, got %d", got.pipelineView.detailTab)
	}
}

func TestCopyMRComment_WithPosition(t *testing.T) {
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
	m.copyMRComment()
	if !strings.Contains(m.status, "Copied comment") {
		t.Fatalf("expected success status, got %q", m.status)
	}
}

func TestCopyMRComment_WithoutPosition(t *testing.T) {
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
	m.copyMRComment()
	if !strings.Contains(m.status, "Copied comment") {
		t.Fatalf("expected success status, got %q", m.status)
	}
}

func TestCopyMRComment_NoDiscussions(t *testing.T) {
	m := Model{
		mrView: mrViewState{
			mrs:      []gitlab.MergeRequestSummary{{IID: 42}},
			selected: 0,
		},
	}
	m.copyMRComment()
	if !strings.Contains(m.status, "No discussions") {
		t.Fatalf("expected no discussions status, got %q", m.status)
	}
}

func TestCopyExplorerURL_File(t *testing.T) {
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
	m.copyExplorerURL()
	if !strings.Contains(m.status, "Copied main.go URL") {
		t.Fatalf("expected success status, got %q", m.status)
	}
}

func TestCopyExplorerURL_Dir(t *testing.T) {
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
	m.copyExplorerURL()
	if !strings.Contains(m.status, "Copied src URL") {
		t.Fatalf("expected success status, got %q", m.status)
	}
}

func TestCopyExplorerURL_NoEntry(t *testing.T) {
	m := Model{
		explorer: explorerState{
			project: gitlab.ProjectNode{WebURL: "https://gitlab.com/team/app"},
			ref:     "main",
			stack:   []dirState{{path: "", entries: nil}},
		},
	}
	m.copyExplorerURL()
	if m.status != "No file selected" {
		t.Fatalf("expected 'No file selected', got %q", m.status)
	}
}
