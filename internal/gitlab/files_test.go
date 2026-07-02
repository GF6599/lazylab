package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestListTree_SortsDirsFirst: a directory listing sorts directories first, then names case-insensitively.
// Given a fixture whose entries arrive unsorted (src, README.md, go.mod,
// docs), when ListTree returns, then docs and src lead as directories and
// go.mod precedes README.md among the files.
// Why it matters: the explorer renders this order directly; regressing to raw
// API order would interleave files with directories, and case-sensitive
// sorting would jump README.md above go.mod.
//
// The fixture is deliberately shuffled so the assertions prove the client
// sorted, not that the server happened to.
func TestListTree_SortsDirsFirst(t *testing.T) {
	// Given: a server answering with the unsorted four-entry fixture.
	data, err := os.ReadFile("testdata/tree.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing the repository root.
	nodes, err := client.ListTree(context.Background(), 1, TreeListOptions{Ref: "main", Path: ""})
	if err != nil {
		t.Fatalf("ListTree: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}

	// Then: directories come first (docs, src), files after (README.md, go.mod).
	if !nodes[0].IsDir() || !nodes[1].IsDir() {
		t.Errorf("first two entries should be directories: %+v, %+v", nodes[0], nodes[1])
	}
	if nodes[2].IsDir() || nodes[3].IsDir() {
		t.Errorf("last two entries should be files: %+v, %+v", nodes[2], nodes[3])
	}

	// And: directories sort alphabetically among themselves.
	if nodes[0].Name != "docs" || nodes[1].Name != "src" {
		t.Errorf("dirs should be alphabetical: %s, %s", nodes[0].Name, nodes[1].Name)
	}
	// And: files sort case-insensitively (go.mod before README.md).
	if nodes[2].Name != "go.mod" || nodes[3].Name != "README.md" {
		t.Errorf("files should be alphabetical: %s, %s", nodes[2].Name, nodes[3].Name)
	}
}

// TestListTree_NestedPath: listing a subdirectory sends the right query and sorts its children.
// Given a request for path "src" on ref "main", when ListTree runs, then the
// GET hits /projects/1/repository/tree with path=src and ref=main, and the
// two children come back directory-first.
// Why it matters: if path or ref were dropped from the query, every
// subdirectory would silently show the repository root at the default branch.
func TestListTree_NestedPath(t *testing.T) {
	// Given: a server that records the method, path, and query it receives.
	data, err := os.ReadFile("testdata/tree_nested.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotPath, gotRef string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/projects/1/repository/tree" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotPath = r.URL.Query().Get("path")
		gotRef = r.URL.Query().Get("ref")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing the "src" subdirectory on ref "main".
	nodes, err := client.ListTree(context.Background(), 1, TreeListOptions{Ref: "main", Path: "src"})
	if err != nil {
		t.Fatalf("ListTree: %v", err)
	}

	// Then: the query carried both parameters.
	if gotPath != "src" {
		t.Errorf("expected path=src, got %q", gotPath)
	}
	if gotRef != "main" {
		t.Errorf("expected ref=main, got %q", gotRef)
	}

	// And: the children come back sorted directory-first.
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if !nodes[0].IsDir() || nodes[0].Name != "handlers" {
		t.Errorf("expected handlers dir first, got %+v", nodes[0])
	}
	if nodes[1].IsDir() || nodes[1].Name != "main.go" {
		t.Errorf("expected main.go file second, got %+v", nodes[1])
	}
}

// TestListTree_EmptyDirectory: an empty directory lists as zero nodes, not an error.
// Given a server returning an empty JSON array, when ListTree runs, then it
// returns an empty slice and a nil error.
// Why it matters: treating emptiness as failure would flash an error state
// whenever the user browses into a legitimately empty directory.
func TestListTree_EmptyDirectory(t *testing.T) {
	// Given: a server answering with an empty listing.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	// When: listing the empty directory.
	nodes, err := client.ListTree(context.Background(), 1, TreeListOptions{Ref: "main", Path: "empty"})
	// Then: no error and no nodes.
	if err != nil {
		t.Fatalf("ListTree on empty dir should not error: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

// TestListTree_MissingRef: a 404 for an unknown ref surfaces as a wrapped not-found error.
// Given a server answering 404, when ListTree is called for a ghost ref, then
// the error matches IsNotFound and carries "list tree" context.
// Why it matters: the UI distinguishes "ref does not exist" from other
// failures via IsNotFound; losing the classification or the operation label
// would leave the user with an unattributed error.
func TestListTree_MissingRef(t *testing.T) {
	// Given: a server that answers 404 to everything.
	client := newTestClient(t, statusHandler(http.StatusNotFound))

	// When: listing a ref that does not exist.
	_, err := client.ListTree(context.Background(), 1, TreeListOptions{Ref: "ghost-ref", Path: ""})

	// Then: the failure classifies as not-found and names the operation.
	if err == nil {
		t.Fatal("expected error for missing ref")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should match: %v", err)
	}
	if !strings.Contains(err.Error(), "list tree") {
		t.Errorf("error should be wrapped with 'list tree': %v", err)
	}
}

// TestGetFileContent_Success: base64 file content is decoded back to its original text.
// Given a file response carrying base64-encoded Go source, when
// GetFileContent runs, then the decoded string equals the original content
// byte for byte.
// Why it matters: GitLab serves file bodies base64-encoded, so a decode
// regression would feed scrambled bytes to the preview pane for every file.
func TestGetFileContent_Success(t *testing.T) {
	// Given: a server returning the file with its content base64-encoded.
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

	// When: fetching the file.
	got, err := client.GetFileContent(context.Background(), 1, "main.go", "main")
	// Then: the decoded content matches the original exactly.
	if err != nil {
		t.Fatalf("GetFileContent: %v", err)
	}
	if got != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

// TestGetFileContent_TooLarge: files over the 10 MB cap are refused before decoding.
// Given a response whose reported size is one byte over the limit, when
// GetFileContent runs, then it fails with a "file too large" error instead of
// decoding the body.
// Why it matters: the size gate is what stops a multi-hundred-MB blob from
// being base64-decoded in memory and taking the TUI process down with it.
func TestGetFileContent_TooLarge(t *testing.T) {
	// Given: a file whose reported size is just over the cap.
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

	// When: fetching the oversized file.
	_, err := client.GetFileContent(context.Background(), 1, "big.bin", "main")

	// Then: the size gate rejects it.
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "file too large") {
		t.Errorf("expected 'file too large' error, got: %v", err)
	}
}
