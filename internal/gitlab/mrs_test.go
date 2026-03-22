package gitlab

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// TestListMergeRequests_Success verifies that MR list responses are correctly
// mapped to MergeRequestSummary values, including author name extraction and
// branch fields.
func TestListMergeRequests_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/merge_requests.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Total-Pages", "1")
		w.Write(data)
	}))

	page, err := client.ListMergeRequests(context.Background(), 1, MRListOptions{State: "opened", Page: 1})
	if err != nil {
		t.Fatalf("ListMergeRequests: %v", err)
	}
	if len(page.MergeRequests) != 2 {
		t.Fatalf("expected 2 MRs, got %d", len(page.MergeRequests))
	}

	mr := page.MergeRequests[0]
	if mr.IID != 42 || mr.Title != "Add feature X" {
		t.Errorf("unexpected MR[0]: %+v", mr)
	}
	if mr.Author != "Jane Dev" {
		t.Errorf("expected author=Jane Dev, got %q", mr.Author)
	}
	if mr.SourceBranch != "feature-x" || mr.TargetBranch != "main" {
		t.Errorf("unexpected branches: source=%q target=%q", mr.SourceBranch, mr.TargetBranch)
	}
}

// TestListMRDiscussions_Success verifies pagination exhaust and note mapping,
// including author extraction and chronological ordering.
func TestListMRDiscussions_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/mr_discussions.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	discussions, err := client.ListMergeRequestDiscussions(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("ListMergeRequestDiscussions: %v", err)
	}
	if len(discussions) != 1 {
		t.Fatalf("expected 1 discussion, got %d", len(discussions))
	}
	if discussions[0].ID != "disc-1" {
		t.Errorf("unexpected discussion ID: %q", discussions[0].ID)
	}
	if len(discussions[0].Notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(discussions[0].Notes))
	}
	note := discussions[0].Notes[0]
	if note.Author != "Reviewer" || note.Body != "Looks good overall!" {
		t.Errorf("unexpected note[0]: %+v", note)
	}
}

// TestListMRDiscussions_PositionExtraction verifies that diff-line positions
// are extracted from notes (NewPath preferred over OldPath) and that notes
// without positions leave FilePath/Line at zero values.
func TestListMRDiscussions_PositionExtraction(t *testing.T) {
	json := `[{
		"id": "disc-pos",
		"notes": [{
			"id": 601,
			"body": "Fix this line",
			"author": {"id": 1, "name": "Reviewer"},
			"system": false,
			"resolvable": true,
			"resolved": false,
			"created_at": "2025-01-15T13:00:00Z",
			"position": {
				"new_path": "src/main.go",
				"new_line": 42,
				"old_path": "src/main.go",
				"old_line": 40
			}
		}, {
			"id": 602,
			"body": "No position here",
			"author": {"id": 2, "name": "Author"},
			"system": false,
			"resolvable": false,
			"resolved": false,
			"created_at": "2025-01-15T14:00:00Z"
		}]
	}]`

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(json))
	}))

	discussions, err := client.ListMergeRequestDiscussions(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("ListMergeRequestDiscussions: %v", err)
	}
	if len(discussions) != 1 {
		t.Fatalf("expected 1 discussion, got %d", len(discussions))
	}
	notes := discussions[0].Notes
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	// Note with position: should extract NewPath/NewLine and both OldLine/NewLine
	if notes[0].FilePath != "src/main.go" {
		t.Errorf("expected FilePath=src/main.go, got %q", notes[0].FilePath)
	}
	if notes[0].Line != 42 {
		t.Errorf("expected Line=42, got %d", notes[0].Line)
	}
	if notes[0].OldLine != 40 {
		t.Errorf("expected OldLine=40, got %d", notes[0].OldLine)
	}
	if notes[0].NewLine != 42 {
		t.Errorf("expected NewLine=42, got %d", notes[0].NewLine)
	}
	// Note without position: should be empty
	if notes[1].FilePath != "" {
		t.Errorf("expected empty FilePath, got %q", notes[1].FilePath)
	}
	if notes[1].Line != 0 {
		t.Errorf("expected Line=0, got %d", notes[1].Line)
	}
	if notes[1].OldLine != 0 {
		t.Errorf("expected OldLine=0, got %d", notes[1].OldLine)
	}
	if notes[1].NewLine != 0 {
		t.Errorf("expected NewLine=0, got %d", notes[1].NewLine)
	}
}

// TestListMRDiffs_Success verifies that diff responses are mapped to MRDiffFile
// values with correct change-type flags (NewFile, RenamedFile, DeletedFile).
func TestListMRDiffs_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/mr_diffs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	diffs, err := client.ListMergeRequestDiffs(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("ListMergeRequestDiffs: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(diffs))
	}

	d0 := diffs[0]
	if d0.OldPath != "main.go" || d0.NewPath != "main.go" {
		t.Errorf("unexpected diff[0] paths: old=%q new=%q", d0.OldPath, d0.NewPath)
	}
	if d0.NewFile || d0.DeletedFile || d0.RenamedFile {
		t.Errorf("diff[0] should not be new/deleted/renamed")
	}

	d1 := diffs[1]
	if !d1.NewFile {
		t.Error("diff[1] should be a new file")
	}
}
