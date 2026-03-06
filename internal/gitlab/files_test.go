package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestListTree_SortsDirsFirst(t *testing.T) {
	data, err := os.ReadFile("testdata/tree.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	nodes, err := client.ListTree(context.Background(), 1, TreeListOptions{Ref: "main", Path: ""})
	if err != nil {
		t.Fatalf("ListTree: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}

	// Directories should come first (docs, src) then files (README.md, go.mod)
	if !nodes[0].IsDir() || !nodes[1].IsDir() {
		t.Errorf("first two entries should be directories: %+v, %+v", nodes[0], nodes[1])
	}
	if nodes[2].IsDir() || nodes[3].IsDir() {
		t.Errorf("last two entries should be files: %+v, %+v", nodes[2], nodes[3])
	}

	// Within dirs, alphabetical
	if nodes[0].Name != "docs" || nodes[1].Name != "src" {
		t.Errorf("dirs should be alphabetical: %s, %s", nodes[0].Name, nodes[1].Name)
	}
	// Within files, alphabetical (case-insensitive)
	if nodes[2].Name != "go.mod" || nodes[3].Name != "README.md" {
		t.Errorf("files should be alphabetical: %s, %s", nodes[2].Name, nodes[3].Name)
	}
}

func TestGetFileContent_Success(t *testing.T) {
	content := "package main\n\nfunc main() {}\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	resp := map[string]any{
		"file_name": "main.go",
		"file_path": "main.go",
		"size":      len(content),
		"encoding":  "base64",
		"content":   encoded,
		"ref":       "main",
	}
	data, _ := json.Marshal(resp)

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	got, err := client.GetFileContent(context.Background(), 1, "main.go", "main")
	if err != nil {
		t.Fatalf("GetFileContent: %v", err)
	}
	if got != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func TestGetFileContent_TooLarge(t *testing.T) {
	const maxFileSize = 10 * 1024 * 1024 // must match the constant in files.go

	resp := map[string]any{
		"file_name": "big.bin",
		"file_path": "big.bin",
		"size":      maxFileSize + 1,
		"encoding":  "base64",
		"content":   base64.StdEncoding.EncodeToString([]byte("x")),
		"ref":       "main",
	}
	data, _ := json.Marshal(resp)

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	_, err := client.GetFileContent(context.Background(), 1, "big.bin", "main")
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !contains(err.Error(), "file too large") {
		t.Errorf("expected 'file too large' error, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
