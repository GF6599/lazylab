package ui

import (
	"testing"

	gclient "gitlab-tui-codex/internal/gitlab"
)

func TestCurrentPath(t *testing.T) {
	m := model{}
	if got := m.currentPath(); got != "" {
		t.Fatalf("expected empty path, got %q", got)
	}

	m.pathStack = []string{"dir", "sub"}
	if got := m.currentPath(); got != "dir/sub" {
		t.Fatalf("expected \"dir/sub\", got %q", got)
	}
}

func TestFileEntriesIncludesParent(t *testing.T) {
	m := model{
		pathStack: []string{"dir", "sub"},
		treeNodes: []gclient.TreeNode{
			{Name: "file.txt", Path: "dir/sub/file.txt", Type: "blob"},
		},
	}

	entries := m.fileEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Type != "up" || entries[0].Name != ".." || entries[0].Path != "dir" {
		t.Fatalf("unexpected parent entry: %+v", entries[0])
	}
	if entries[1].Name != "file.txt" || entries[1].Type != "blob" {
		t.Fatalf("unexpected child entry: %+v", entries[1])
	}
}

func TestFileEntriesRoot(t *testing.T) {
	nodes := []gclient.TreeNode{
		{Name: "README.md", Type: "blob"},
		{Name: "pkg", Type: "tree"},
	}
	m := model{treeNodes: nodes}
	entries := m.fileEntries()

	if len(entries) != len(nodes) {
		t.Fatalf("expected %d entries, got %d", len(nodes), len(entries))
	}
	for i, entry := range entries {
		if entry.Name != nodes[i].Name || entry.Type != nodes[i].Type {
			t.Fatalf("entry %d mismatch: got %+v want %+v", i, entry, nodes[i])
		}
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(5, 0, 10); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
	if got := clamp(-2, 0, 10); got != 0 {
		t.Fatalf("expected clamp to min 0, got %d", got)
	}
	if got := clamp(12, 0, 10); got != 10 {
		t.Fatalf("expected clamp to max 10, got %d", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("expected unchanged string, got %q", got)
	}
	if got := truncate("long-string", 4); got != "long" {
		t.Fatalf("expected truncated string 'long', got %q", got)
	}
}

func TestSortProjectsByName(t *testing.T) {
	projects := []gclient.Project{
		{Name: "zeta", PathWithNamespace: "group/zeta"},
		{Name: "Alpha", PathWithNamespace: "group/alpha"},
		{Name: "alpha", PathWithNamespace: "another/alpha"},
	}

	sortProjectsByName(projects)

	want := []string{"alpha", "Alpha", "zeta"}
	for i, proj := range projects {
		if proj.Name != want[i] {
			t.Fatalf("project %d mismatch: got %s want %s", i, proj.Name, want[i])
		}
	}
	if projects[0].PathWithNamespace != "another/alpha" {
		t.Fatalf("expected tie breaker to respect namespace ordering, got %s", projects[0].PathWithNamespace)
	}
}
