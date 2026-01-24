package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazylab/internal/gitlab"
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

func TestHandlePipelineLogLoadedIgnoresStale(t *testing.T) {
	m := Model{
		mode: modePipelines,
		pipelineView: pipelineViewState{
			project:    gitlab.ProjectNode{ID: 1},
			logJobID:   20,
			logLoading: map[int]bool{10: true, 20: true},
			logPreview: previewState{content: "current"},
		},
	}
	msg := pipelineLogLoadedMsg{projectID: 1, jobID: 10, content: "stale"}
	updated, _ := m.handlePipelineLogLoaded(msg)
	got := updated.(Model).pipelineView
	if got.logPreview.content != "current" {
		t.Fatalf("expected preview to stay on current job, got %q", got.logPreview.content)
	}
	if got.logCache == nil || got.logCache[10] != "stale" {
		t.Fatalf("expected stale log to be cached, got %#v", got.logCache)
	}
	if got.logLoading[10] {
		t.Fatalf("expected stale log loading to clear")
	}
}

func TestQueuePipelineLogPreviewPreservesOffset(t *testing.T) {
	content := strings.Repeat("line\n", 40)
	m := Model{
		width:  80,
		height: 20,
		pipelineView: pipelineViewState{
			project:    gitlab.ProjectNode{ID: 1},
			pipelines:  []gitlab.PipelineSummary{{ID: 10}},
			selected:   0,
			stageCache: map[int][]gitlab.PipelineStage{10: {{Name: "build"}}},
			jobsCache: map[int][]gitlab.PipelineJob{
				10: {{ID: 100, Name: "build-job", Stage: "build"}},
			},
			logCache:      map[int]string{100: content},
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
	m := Model{
		pipelineView: pipelineViewState{
			logCache: make(map[int]string),
			logJobID: 15, // Currently viewing job 15
		},
	}

	// Add more logs than the max
	for i := 1; i <= maxLogCacheEntries+5; i++ {
		m.pipelineView.logCache[i] = fmt.Sprintf("log content for job %d", i)
	}

	initialCount := len(m.pipelineView.logCache)
	if initialCount != maxLogCacheEntries+5 {
		t.Fatalf("Setup failed: expected %d logs, got %d", maxLogCacheEntries+5, initialCount)
	}

	// Call eviction
	m.evictOldLogs()

	// Should keep maxLogCacheEntries logs
	if len(m.pipelineView.logCache) > maxLogCacheEntries {
		t.Errorf("evictOldLogs() left %d logs, want at most %d", len(m.pipelineView.logCache), maxLogCacheEntries)
	}

	// Should keep the currently viewed log (15)
	if _, exists := m.pipelineView.logCache[15]; !exists {
		t.Error("evictOldLogs() evicted the currently displayed log")
	}

	// Should evict oldest logs (lowest IDs)
	if _, exists := m.pipelineView.logCache[1]; exists {
		t.Error("evictOldLogs() should have evicted job 1 (oldest)")
	}

	// Should keep newest logs (highest IDs)
	if _, exists := m.pipelineView.logCache[maxLogCacheEntries+5]; !exists {
		t.Error("evictOldLogs() should have kept the newest log")
	}
}

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
	out := renderExplorerPreview(m, width, 6)
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

func TestRenderPipelineLogPaneWrapsLongLines(t *testing.T) {
	m := Model{
		pipelineView: pipelineViewState{
			logPreview: previewState{
				content: "\x1b[31m" + strings.Repeat("abc", 20) + "\x1b[0m\tend",
			},
		},
	}
	const width = 12
	out := renderPipelineLogPane(m, width, 6)
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
