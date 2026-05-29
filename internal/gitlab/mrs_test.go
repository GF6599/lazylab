package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
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

// TestCreateMergeRequest_Success verifies that a create-MR response is correctly
// mapped to a MergeRequestSummary.
func TestCreateMergeRequest_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/create_merge_request.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(data)
	}))

	mr, err := client.CreateMergeRequest(context.Background(), 1, CreateMROptions{
		Title:        "Add login feature",
		SourceBranch: "feature/login",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}
	if mr.IID != 99 {
		t.Errorf("expected IID=99, got %d", mr.IID)
	}
	if mr.Title != "Add login feature" {
		t.Errorf("expected title=%q, got %q", "Add login feature", mr.Title)
	}
	if mr.State != "opened" {
		t.Errorf("expected state=opened, got %q", mr.State)
	}
	if mr.SourceBranch != "feature/login" || mr.TargetBranch != "main" {
		t.Errorf("unexpected branches: source=%q target=%q", mr.SourceBranch, mr.TargetBranch)
	}
	if mr.Author != "Jane Dev" {
		t.Errorf("expected author=Jane Dev, got %q", mr.Author)
	}
}

// TestListBranches_Success verifies that branch list responses are correctly
// mapped to a slice of branch names.
func TestListBranches_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/branches.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	branches, err := client.ListBranches(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 3 {
		t.Fatalf("expected 3 branches, got %d", len(branches))
	}
	expected := []string{"main", "develop", "feature/login"}
	for i, want := range expected {
		if branches[i] != want {
			t.Errorf("branch[%d]: expected %q, got %q", i, want, branches[i])
		}
	}
}

// TestListBranches_WithSearchAndPagination verifies that the search filter is
// forwarded to the API and that the wrapper requests the first 100-item page.
func TestListBranches_WithSearchAndPagination(t *testing.T) {
	data, err := os.ReadFile("testdata/branches_page2.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotSearch, gotPerPage string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/repository/branches" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotSearch = r.URL.Query().Get("search")
		gotPerPage = r.URL.Query().Get("per_page")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Next-Page", "2")
		w.Header().Set("X-Total-Pages", "2")
		w.Write(data)
	}))

	branches, err := client.ListBranches(context.Background(), 1, "release")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if gotSearch != "release" {
		t.Errorf("expected search=release, got %q", gotSearch)
	}
	if gotPerPage != "100" {
		t.Errorf("expected per_page=100, got %q", gotPerPage)
	}
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}
	if branches[0] != "release/v1" || branches[1] != "release/v2" {
		t.Errorf("unexpected branches: %v", branches)
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

// readBodyJSON decodes the request body into a generic map for shape assertions.
func readBodyJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]any
	if len(body) == 0 {
		return m
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal body %q: %v", body, err)
	}
	return m
}

// TestAddMergeRequestDiscussionNote_Success verifies the POST endpoint
// path, method, and that the note body is forwarded in the request body.
func TestAddMergeRequestDiscussionNote_Success(t *testing.T) {
	noteJSON := `{
		"id": 901,
		"body": "thanks for the review",
		"author": {"id": 1, "name": "Author"},
		"system": false,
		"resolvable": true,
		"resolved": false,
		"created_at": "2025-01-15T13:00:00Z"
	}`

	var gotBody map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		wantPath := "/api/v4/projects/1/merge_requests/42/discussions/disc-1/notes"
		if r.URL.Path != wantPath {
			t.Errorf("expected path %s, got %s", wantPath, r.URL.Path)
		}
		gotBody = readBodyJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(noteJSON))
	}))

	err := client.AddMergeRequestDiscussionNote(context.Background(), 1, 42, "disc-1", "thanks for the review")
	if err != nil {
		t.Fatalf("AddMergeRequestDiscussionNote: %v", err)
	}
	if gotBody["body"] != "thanks for the review" {
		t.Errorf("expected body=thanks for the review, got %v", gotBody["body"])
	}
}

// TestCreateMergeRequestDiscussion_GeneralComment verifies the no-position
// (top-level comment) branch: only the body field should be sent, with no
// position payload.
func TestCreateMergeRequestDiscussion_GeneralComment(t *testing.T) {
	discData, err := os.ReadFile("testdata/merge_request_discussion.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotBody map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		wantPath := "/api/v4/projects/1/merge_requests/42/discussions"
		if r.URL.Path != wantPath {
			t.Errorf("expected path %s, got %s", wantPath, r.URL.Path)
		}
		gotBody = readBodyJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(discData)
	}))

	err = client.CreateMergeRequestDiscussion(context.Background(), 1, 42, "general note", nil)
	if err != nil {
		t.Fatalf("CreateMergeRequestDiscussion: %v", err)
	}
	if gotBody["body"] != "general note" {
		t.Errorf("expected body=general note, got %v", gotBody["body"])
	}
	if _, hasPos := gotBody["position"]; hasPos {
		t.Errorf("position should not be sent for general comment: %v", gotBody)
	}
}

// TestCreateMergeRequestDiscussion_InlineComment verifies the positioned
// (line-level diff) branch: position fields including SHAs and line numbers
// must be marshalled into the request payload.
func TestCreateMergeRequestDiscussion_InlineComment(t *testing.T) {
	discData, err := os.ReadFile("testdata/merge_request_discussion.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotBody map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = readBodyJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(discData)
	}))

	pos := &MRCommentPosition{
		OldPath: "src/main.go",
		NewPath: "src/main.go",
		NewLine: 42,
		DiffRefs: MRDiffRefs{
			BaseSHA:  "base123",
			HeadSHA:  "head456",
			StartSHA: "start789",
		},
	}
	if err = client.CreateMergeRequestDiscussion(context.Background(), 1, 42, "fix this line", pos); err != nil {
		t.Fatalf("CreateMergeRequestDiscussion: %v", err)
	}
	if gotBody["body"] != "fix this line" {
		t.Errorf("expected body=fix this line, got %v", gotBody["body"])
	}
	gotPos, ok := gotBody["position"].(map[string]any)
	if !ok {
		t.Fatalf("position should be a map: %v", gotBody["position"])
	}
	if gotPos["new_path"] != "src/main.go" {
		t.Errorf("expected new_path=src/main.go, got %v", gotPos["new_path"])
	}
	if gotPos["base_sha"] != "base123" || gotPos["head_sha"] != "head456" || gotPos["start_sha"] != "start789" {
		t.Errorf("expected SHAs base123/head456/start789, got %v", gotPos)
	}
	if v, _ := gotPos["new_line"].(float64); int(v) != 42 {
		t.Errorf("expected new_line=42, got %v", gotPos["new_line"])
	}
	if _, hasOldLine := gotPos["old_line"]; hasOldLine {
		t.Errorf("old_line should be omitted when zero: %v", gotPos)
	}
}

// TestResolveMergeRequestDiscussion_Success verifies the PUT endpoint and
// that the resolved boolean is forwarded.
func TestResolveMergeRequestDiscussion_Success(t *testing.T) {
	discData, err := os.ReadFile("testdata/merge_request_discussion.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotBody map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		wantPath := "/api/v4/projects/1/merge_requests/42/discussions/disc-1"
		if r.URL.Path != wantPath {
			t.Errorf("expected path %s, got %s", wantPath, r.URL.Path)
		}
		gotBody = readBodyJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write(discData)
	}))

	if err = client.ResolveMergeRequestDiscussion(context.Background(), 1, 42, "disc-1", true); err != nil {
		t.Fatalf("ResolveMergeRequestDiscussion: %v", err)
	}
	if v, ok := gotBody["resolved"].(bool); !ok || !v {
		t.Errorf("expected resolved=true in body, got %v", gotBody)
	}
}

// TestGetMergeRequestDiffRefs_Success verifies the GET endpoint and that
// the diff refs from the MR payload are extracted into MRDiffRefs.
func TestGetMergeRequestDiffRefs_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/merge_request_diff_refs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		wantPath := "/api/v4/projects/1/merge_requests/42"
		if r.URL.Path != wantPath {
			t.Errorf("expected path %s, got %s", wantPath, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	refs, err := client.GetMergeRequestDiffRefs(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("GetMergeRequestDiffRefs: %v", err)
	}
	if refs.BaseSHA != "base123" || refs.HeadSHA != "head456" || refs.StartSHA != "start789" {
		t.Errorf("unexpected refs: %+v", refs)
	}
}

// TestGetMergeRequestDiffRefs_Empty verifies the wrapper rejects MRs whose
// diff refs are all empty (e.g. unprepared MR or missing source branch).
func TestGetMergeRequestDiffRefs_Empty(t *testing.T) {
	body := `{"id": 99, "iid": 42, "title": "x", "diff_refs": {"base_sha": "", "head_sha": "", "start_sha": ""}}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))

	_, err := client.GetMergeRequestDiffRefs(context.Background(), 1, 42)
	if err == nil {
		t.Fatal("expected error for empty diff refs")
	}
	if !strings.Contains(err.Error(), "diff refs not available") {
		t.Errorf("error should mention 'diff refs not available': %v", err)
	}
}
