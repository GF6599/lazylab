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

// TestListMergeRequests_Success: an MR list response maps to summaries with author and branch fields.
// Given a canned two-MR page, when ListMergeRequests runs, then both MRs
// return and the first carries IID 42, its title, author name "Jane Dev", and
// source/target branches.
// Why it matters: the MR panel keys navigation on IID and renders author and
// branches; a mapping slip would leave every row unusable for review
// workflows.
func TestListMergeRequests_Success(t *testing.T) {
	// Given: a server answering with the two-MR fixture and page headers.
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

	// When: listing opened merge requests.
	page, err := client.ListMergeRequests(context.Background(), 1, MRListOptions{State: "opened", Page: 1})
	if err != nil {
		t.Fatalf("ListMergeRequests: %v", err)
	}

	// Then: both MRs map with their display fields intact.
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

// TestListMRDiscussions_Success: a discussion thread maps with its notes, authors, and bodies intact.
// Given one canned discussion holding two notes, when
// ListMergeRequestDiscussions runs, then the discussion keeps ID "disc-1",
// both notes survive in order, and the first carries author "Reviewer" and
// its body.
// Why it matters: review threads are read entirely through this mapping;
// dropping notes or author names would make discussions unreadable in the MR
// view.
func TestListMRDiscussions_Success(t *testing.T) {
	// Given: a server answering with the single-discussion fixture.
	data, err := os.ReadFile("testdata/mr_discussions.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing the MR's discussions.
	discussions, err := client.ListMergeRequestDiscussions(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("ListMergeRequestDiscussions: %v", err)
	}

	// Then: the discussion and both of its notes map through.
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

// TestListMRDiscussions_PositionExtraction: diff-anchored notes keep their file position, general notes stay zeroed.
// Given a discussion with one positioned note (new_path src/main.go,
// new_line 42, old_line 40) and one note without a position, when
// ListMergeRequestDiscussions runs, then the first note carries
// FilePath/Line/OldLine/NewLine and the second leaves all four at zero.
// Why it matters: the UI jumps to file:line from these fields; a mix-up would
// anchor inline comments to the wrong line or invent positions for general
// comments.
func TestListMRDiscussions_PositionExtraction(t *testing.T) {
	// Given: one positioned note and one position-less note in a thread.
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

	// When: listing the MR's discussions.
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

	// Then: the positioned note extracts NewPath plus both line numbers.
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

	// And: the note without a position keeps all four fields at zero.
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

// TestCreateMergeRequest_Success: a created MR response maps back into a summary.
// Given a server expecting POST and answering 201 with a canned MR, when
// CreateMergeRequest runs, then the summary carries IID 99, the title, state
// "opened", both branches, and author "Jane Dev".
// Why it matters: the create-MR flow shows this summary as its confirmation;
// a wrong verb would fail creation outright and a mapping slip would report
// the wrong MR back to the user.
func TestCreateMergeRequest_Success(t *testing.T) {
	// Given: a server expecting POST and answering 201 with the created MR.
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

	// When: creating the merge request.
	mr, err := client.CreateMergeRequest(context.Background(), 1, CreateMROptions{
		Title:        "Add login feature",
		SourceBranch: "feature/login",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}

	// Then: the returned summary maps every display field.
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

// TestCreateMergeRequest_EmptyTargetBranchFails: a missing target branch fails before any API call.
// Given options with no target branch, when CreateMergeRequest runs, then it returns an error
// naming the target branch and the server never sees a request.
// Why it matters: GitLab rejects target_branch="" with an opaque 400, so validating locally turns
// a confusing server error into a message that names the missing field.
func TestCreateMergeRequest_EmptyTargetBranchFails(t *testing.T) {
	// Given: a server that must never be reached
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))

	// When: creating a merge request with no target branch
	_, err := client.CreateMergeRequest(context.Background(), 1, CreateMROptions{
		Title:        "Add login feature",
		SourceBranch: "feature/login",
	})

	// Then: the call fails locally with a message naming the field
	if err == nil {
		t.Fatal("expected an error for an empty target branch")
	}
	if !strings.Contains(err.Error(), "target branch") {
		t.Fatalf("error should name the target branch, got: %v", err)
	}
}

// TestListBranches_Success: a branch list response flattens to branch names in order.
// Given a canned three-branch response, when ListBranches runs with no
// search, then it returns main, develop, and feature/login in fixture order.
// Why it matters: the branch picker for MR creation is fed from this slice;
// dropped or reordered names would offer the wrong target branch.
func TestListBranches_Success(t *testing.T) {
	// Given: a server answering with the three-branch fixture.
	data, err := os.ReadFile("testdata/branches.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing branches without a search filter.
	branches, err := client.ListBranches(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	// Then: all three names come back in order.
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

// TestListBranches_WithSearchAndPagination: the search filter and page size are forwarded to the API.
// Given a request with search "release", when ListBranches issues the call,
// then the query carries search=release and per_page=100, and the single
// first page's two branches come back with no follow-up page fetch.
// Why it matters: dropping the search parameter would fetch every branch of a
// large repository, and a smaller page size would truncate the picker after a
// handful of branches.
func TestListBranches_WithSearchAndPagination(t *testing.T) {
	// Given: a server that records the query and advertises a second page.
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

	// When: listing branches with a search filter.
	branches, err := client.ListBranches(context.Background(), 1, "release")
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	// Then: the query carried the filter and the 100-item page size.
	if gotSearch != "release" {
		t.Errorf("expected search=release, got %q", gotSearch)
	}
	if gotPerPage != "100" {
		t.Errorf("expected per_page=100, got %q", gotPerPage)
	}

	// And: only the first page's branches are returned.
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}
	if branches[0] != "release/v1" || branches[1] != "release/v2" {
		t.Errorf("unexpected branches: %v", branches)
	}
}

// TestListMRDiffs_Success: MR diff entries map to MRDiffFile with correct change-type flags.
// Given a canned two-file diff response, when ListMergeRequestDiffs runs,
// then the edited file keeps matching old/new paths with all flags false and
// the added file reports NewFile true.
// Why it matters: the diff viewer styles files by these flags; a flag mix-up
// would present an added file as an edit and misdirect review.
func TestListMRDiffs_Success(t *testing.T) {
	// Given: a server answering with the two-file diff fixture.
	data, err := os.ReadFile("testdata/mr_diffs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing the MR's diffs.
	diffs, err := client.ListMergeRequestDiffs(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("ListMergeRequestDiffs: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(diffs))
	}

	// Then: the edited file keeps its paths and no change-type flags.
	d0 := diffs[0]
	if d0.OldPath != "main.go" || d0.NewPath != "main.go" {
		t.Errorf("unexpected diff[0] paths: old=%q new=%q", d0.OldPath, d0.NewPath)
	}
	if d0.NewFile || d0.DeletedFile || d0.RenamedFile {
		t.Errorf("diff[0] should not be new/deleted/renamed")
	}

	// And: the added file reports NewFile.
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

// TestAddMergeRequestDiscussionNote_Success: replying to a thread POSTs the note body to the right endpoint.
// Given a reply for discussion disc-1 on MR 42, when
// AddMergeRequestDiscussionNote runs, then the request is a POST to
// /merge_requests/42/discussions/disc-1/notes whose JSON body carries the
// note text.
// Why it matters: a path or payload slip would post review replies to the
// wrong thread, or as empty comments, on a live merge request.
func TestAddMergeRequestDiscussionNote_Success(t *testing.T) {
	// Given: a server that checks method and path and records the body.
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

	// When: posting the reply.
	err := client.AddMergeRequestDiscussionNote(context.Background(), 1, 42, "disc-1", "thanks for the review")
	if err != nil {
		t.Fatalf("AddMergeRequestDiscussionNote: %v", err)
	}

	// Then: the note text travelled in the request body.
	if gotBody["body"] != "thanks for the review" {
		t.Errorf("expected body=thanks for the review, got %v", gotBody["body"])
	}
}

// TestCreateMergeRequestDiscussion_GeneralComment: a nil position creates a top-level comment with no position payload.
// Given a comment body and a nil position, when CreateMergeRequestDiscussion
// runs, then it POSTs to the discussions endpoint with only the body field
// and no "position" key in the JSON.
// Why it matters: sending even an empty position object would make GitLab
// reject or misfile ordinary comments as diff comments.
func TestCreateMergeRequestDiscussion_GeneralComment(t *testing.T) {
	// Given: a server that checks method and path and records the body.
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

	// When: creating a discussion without a position.
	err = client.CreateMergeRequestDiscussion(context.Background(), 1, 42, "general note", nil)
	if err != nil {
		t.Fatalf("CreateMergeRequestDiscussion: %v", err)
	}

	// Then: the payload carries the body and omits position entirely.
	if gotBody["body"] != "general note" {
		t.Errorf("expected body=general note, got %v", gotBody["body"])
	}
	if _, hasPos := gotBody["position"]; hasPos {
		t.Errorf("position should not be sent for general comment: %v", gotBody)
	}
}

// TestCreateMergeRequestDiscussion_InlineComment: a positioned comment marshals its anchor SHAs and line into the payload.
// Given a position with old/new paths, new line 42, and the three diff SHAs,
// when CreateMergeRequestDiscussion runs, then the request JSON carries
// new_path, base/head/start SHAs, and new_line 42, and omits old_line because
// it is zero.
// Why it matters: GitLab rejects positioned comments with an incomplete SHA
// triad, and a stray old_line=0 would anchor the comment to a nonexistent
// deleted line.
func TestCreateMergeRequestDiscussion_InlineComment(t *testing.T) {
	// Given: a server that records the request body.
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

	// When: creating a discussion anchored to a new line in the diff.
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

	// Then: the payload carries the body and the full position anchor.
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

	// And: the zero old_line is omitted from the payload.
	if _, hasOldLine := gotPos["old_line"]; hasOldLine {
		t.Errorf("old_line should be omitted when zero: %v", gotPos)
	}
}

// TestResolveMergeRequestDiscussion_Success: resolving a thread PUTs the resolved flag to the discussion endpoint.
// Given a resolve request for discussion disc-1 on MR 42, when
// ResolveMergeRequestDiscussion runs, then the request is a PUT to the
// discussion path with resolved=true in the JSON body.
// Why it matters: a verb or payload regression would silently leave threads
// unresolved while the UI marks them done.
func TestResolveMergeRequestDiscussion_Success(t *testing.T) {
	// Given: a server that checks method and path and records the body.
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

	// When: resolving the discussion.
	if err = client.ResolveMergeRequestDiscussion(context.Background(), 1, 42, "disc-1", true); err != nil {
		t.Fatalf("ResolveMergeRequestDiscussion: %v", err)
	}

	// Then: the resolved flag travelled in the request body.
	if v, ok := gotBody["resolved"].(bool); !ok || !v {
		t.Errorf("expected resolved=true in body, got %v", gotBody)
	}
}

// TestGetMergeRequestDiffRefs_Success: an MR's diff refs are extracted into the SHA triad.
// Given a canned MR payload with diff_refs, when GetMergeRequestDiffRefs
// runs, then it GETs /merge_requests/42 and returns the base/head/start SHAs
// intact.
// Why it matters: inline commenting needs this exact triad; a swapped or
// dropped SHA makes GitLab reject every positioned comment.
func TestGetMergeRequestDiffRefs_Success(t *testing.T) {
	// Given: a server answering the MR GET with a diff_refs payload.
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

	// When: fetching the MR's diff refs.
	refs, err := client.GetMergeRequestDiffRefs(context.Background(), 1, 42)
	if err != nil {
		t.Fatalf("GetMergeRequestDiffRefs: %v", err)
	}

	// Then: all three SHAs come through unchanged.
	if refs.BaseSHA != "base123" || refs.HeadSHA != "head456" || refs.StartSHA != "start789" {
		t.Errorf("unexpected refs: %+v", refs)
	}
}

// TestGetMergeRequestDiffRefs_Empty: an MR with blank diff refs is rejected with a clear error.
// Given an MR payload whose three SHAs are all empty strings, when
// GetMergeRequestDiffRefs runs, then it returns an error mentioning "diff
// refs not available".
// Why it matters: unprepared MRs really do ship empty diff_refs; passing the
// empty triad through would fail later at comment creation with a far more
// cryptic API error.
func TestGetMergeRequestDiffRefs_Empty(t *testing.T) {
	// Given: an MR whose diff_refs SHAs are all empty.
	body := `{"id": 99, "iid": 42, "title": "x", "diff_refs": {"base_sha": "", "head_sha": "", "start_sha": ""}}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))

	// When: fetching the MR's diff refs.
	_, err := client.GetMergeRequestDiffRefs(context.Background(), 1, 42)

	// Then: the empty triad is refused with the availability message.
	if err == nil {
		t.Fatal("expected error for empty diff refs")
	}
	if !strings.Contains(err.Error(), "diff refs not available") {
		t.Errorf("error should mention 'diff refs not available': %v", err)
	}
}
