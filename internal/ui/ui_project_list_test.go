package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"gitlab-tui-codex/internal/gitlab"
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

func TestClipPreviewTruncates(t *testing.T) {
	long := strings.Repeat("a", maxPreviewLen+10)
	if !strings.Contains(clipPreview(long), "truncated") {
		t.Fatalf("expected truncation marker for long preview")
	}
	short := "hello"
	if got := clipPreview(short); got != short {
		t.Fatalf("short preview should not change, got %q", got)
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
	m := Model{
		mode: modeExplorer,
		explorer: explorerState{
			project: gitlab.ProjectNode{ID: 1},
			ref:     "main",
			stack:   []dirState{{path: ""}},
			preview: previewState{path: "dir", loading: true},
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
	m := Model{
		mode: modeExplorer,
		explorer: explorerState{
			project: gitlab.ProjectNode{ID: 1},
			ref:     "main",
			stack:   []dirState{{path: "", loading: true}},
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
	m := Model{
		mode: modeExplorer,
		explorer: explorerState{
			project: gitlab.ProjectNode{ID: 1},
			ref:     "main",
			stack:   []dirState{{path: ""}},
			preview: previewState{path: "README.md", loading: true},
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
		},
	}
	cmd := m.queueExplorerPreview()
	if cmd == nil || !m.explorer.preview.loading || m.explorer.preview.path != "src" {
		t.Fatalf("directory preview not queued: %#v", m.explorer.preview)
	}
}

func TestQueueExplorerPreviewFile(t *testing.T) {
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
