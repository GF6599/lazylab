package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// newTestClient creates a *Client backed by the given HTTP handler via httptest.
// Retries are disabled so 429/5xx tests don't burn 10+ seconds waiting for the
// SDK's exponential backoff to give up.
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	api, err := gl.NewClient(
		"test-token",
		gl.WithBaseURL(server.URL+"/api/v4"),
		gl.WithoutRetries(),
	)
	if err != nil {
		t.Fatalf("gl.NewClient: %v", err)
	}
	return &Client{api: api, host: server.URL}
}

// TestNewClient_EmptyToken: constructing a client with an empty token is rejected.
// Given an empty token string, when NewClient is called, then it returns an
// error containing "token must not be empty".
// Why it matters: without this guard the client would start up unauthenticated
// and every API call would surface as a confusing 401 instead of one clear
// startup error.
func TestNewClient_EmptyToken(t *testing.T) {
	// When: constructing a client with no token.
	_, err := NewClient("", "https://gitlab.com")

	// Then: construction fails with the empty-token message.
	if err == nil {
		t.Fatal("Expected error for empty token")
	}
	if !strings.Contains(err.Error(), "token must not be empty") {
		t.Fatalf("Unexpected error message: %v", err)
	}
}

// TestNewClient_InvalidURL: malformed or non-HTTP host URLs are rejected at construction.
// Given hosts that are unparseable, missing a scheme, or using ftp/file
// schemes, when NewClient is called, then each returns an error naming the
// specific validation failure.
// Why it matters: an unvalidated host like "gitlab.com" or a file:// URL
// would fail deep inside the SDK with an opaque transport error instead of an
// actionable message at startup.
func TestNewClient_InvalidURL(t *testing.T) {
	// Given: host URLs that must not produce a working client.
	tests := []struct {
		name    string
		host    string
		wantErr string
	}{
		{
			name:    "malformed URL",
			host:    "ht!tp://invalid",
			wantErr: "invalid host URL",
		},
		{
			name:    "missing scheme",
			host:    "gitlab.com",
			wantErr: "host URL must use http or https scheme",
		},
		{
			name:    "ftp scheme",
			host:    "ftp://gitlab.com",
			wantErr: "host URL must use http or https scheme",
		},
		{
			name:    "file scheme",
			host:    "file:///etc/passwd",
			wantErr: "host URL must use http or https scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: constructing a client with the invalid host.
			_, err := NewClient("valid-token", tt.host)

			// Then: construction fails with the expected message.
			if err == nil {
				t.Fatal("Expected error for invalid host URL")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestNewClient_ValidURLs: every legitimate host shape constructs a working client.
// Given hosts covering the empty default, http and https schemes, ports,
// paths, and self-hosted domains, when NewClient is called, then each returns
// a non-nil client with a wired API handle.
// Why it matters: over-strict URL validation would lock out self-hosted
// GitLab instances on custom ports or subpaths, exactly the setups this tool
// targets.
func TestNewClient_ValidURLs(t *testing.T) {
	// Given: host URLs that must all be accepted.
	tests := []struct {
		name string
		host string
	}{
		{
			name: "default gitlab.com",
			host: "",
		},
		{
			name: "https gitlab.com",
			host: "https://gitlab.com",
		},
		{
			name: "http gitlab",
			host: "http://gitlab.example.com",
		},
		{
			name: "with port",
			host: "https://gitlab.com:8080",
		},
		{
			name: "with path",
			host: "https://gitlab.com/api/v4",
		},
		{
			name: "self-hosted",
			host: "https://git.mycompany.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: constructing a client with the valid host.
			client, err := NewClient("valid-token", tt.host)
			// Then: construction succeeds and the API handle is wired.
			if err != nil {
				t.Fatalf("Unexpected error for valid URL %q: %v", tt.host, err)
			}
			if client == nil {
				t.Fatal("Expected non-nil client")
			}
			if client.api == nil {
				t.Fatal("Expected non-nil API client")
			}
		})
	}
}

// TestNewClient_WhitespaceHandling: surrounding whitespace in the host URL is trimmed.
// Given a host string padded with spaces, when NewClient is called, then the
// stored host equals the trimmed URL.
// Why it matters: a host pasted from a config file with stray spaces would
// otherwise poison every request URL and the cache key derived from the host.
func TestNewClient_WhitespaceHandling(t *testing.T) {
	// When: constructing a client with a space-padded host.
	client, err := NewClient("valid-token", "  https://gitlab.com  ")
	// Then: the client is built and stores the trimmed host.
	if err != nil {
		t.Fatalf("Unexpected error for URL with whitespace: %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	if client.host != "https://gitlab.com" {
		t.Fatalf("Expected trimmed host, got: %s", client.host)
	}
}

// TestEnsureAPIBaseURL: hosts are normalised to end in exactly one /api/v4.
// Given hosts with and without trailing slashes or an existing /api/v4
// suffix, when ensureAPIBaseURL runs, then each yields the canonical base URL
// with a single /api/v4 suffix.
// Why it matters: a doubled /api/v4/api/v4 or a missing suffix would 404
// every API call for users who paste either form of their instance URL.
func TestEnsureAPIBaseURL(t *testing.T) {
	// Given: host URLs in every form users paste them.
	tests := []struct {
		input string
		want  string
	}{
		{"https://gitlab.com", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/api/v4", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/api/v4/", "https://gitlab.com/api/v4"},
		{"http://git.local:8080", "http://git.local:8080/api/v4"},
	}

	for _, tt := range tests {
		// When/Then: each host normalises to the canonical API base URL.
		got := ensureAPIBaseURL(tt.input)
		if got != tt.want {
			t.Errorf("ensureAPIBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestMergeStageStatus: the higher-priority job status wins when folding a stage.
// Given pairs of current and candidate job statuses, when mergeStageStatus
// merges them, then the more urgent status (failed over canceled over manual
// over running over success over skipped) is kept, equal-rank candidates do
// not displace the current status, and the preparing and waiting_for_callback
// runner states outrank success.
// Why it matters: an inverted priority would let a stage with one failed job
// among successes render green in the pipeline panel and hide a broken build.
func TestMergeStageStatus(t *testing.T) {
	// Given: current/candidate pairs covering the priority table's edges.
	tests := []struct {
		name      string
		current   string
		candidate string
		want      string
	}{
		{"failed wins over success", "success", "failed", "failed"},
		{"failed wins over running", "running", "failed", "failed"},
		{"canceled wins over success", "success", "canceled", "canceled"},
		{"running wins over pending", "pending", "running", "running"},
		{"running wins over success", "success", "running", "running"},
		{"manual wins over success", "success", "manual", "manual"},
		{"success stays if no higher priority", "success", "skipped", "success"},
		{"first status when empty", "", "running", "running"},
		{"preparing outranks success", "success", "preparing", "preparing"},
		{"preparing tied with running stays running", "running", "preparing", "running"},
		{"waiting_for_callback outranks success", "success", "waiting_for_callback", "waiting_for_callback"},
		{"failed beats preparing", "preparing", "failed", "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: merging the candidate into the current status.
			got := mergeStageStatus(tt.current, tt.candidate)

			// Then: the higher-priority status survives.
			if got != tt.want {
				t.Errorf("mergeStageStatus(%q, %q) = %q, want %q",
					tt.current, tt.candidate, got, tt.want)
			}
		})
	}
}

// TestNormalizeStageStatus: raw job statuses are lowercased, trimmed, and defaulted.
// Given statuses with mixed case, surrounding padding, or nothing at all,
// when normalizeStageStatus runs, then each collapses to its canonical
// lowercase form and blank input becomes "unknown".
// Why it matters: an unnormalised "SUCCESS" would miss the priority table
// lookup and rank as unknown, scrambling stage aggregation.
func TestNormalizeStageStatus(t *testing.T) {
	// Given: raw statuses as the API or config might deliver them.
	tests := []struct {
		input string
		want  string
	}{
		{"SUCCESS", "success"},
		{"  Failed  ", "failed"},
		{"Running", "running"},
		{"", "unknown"},
		{"  ", "unknown"},
	}

	for _, tt := range tests {
		// When/Then: each raw status normalises to its canonical form.
		got := normalizeStageStatus(tt.input)
		if got != tt.want {
			t.Errorf("normalizeStageStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestRank: rank orders failed as most urgent and defaults unrecognised statuses.
// Given the built-in priority table, when rank is queried for failed,
// running, success, and an unrecognised status, then failed ranks lowest
// (most urgent), success ranks above running, and unknown statuses fall back
// to the "unknown" rank.
// Why it matters: a wrong ordering silently flips which job status wins a
// stage merge, for example showing success while a job is still running.
func TestRank(t *testing.T) {
	// When/Then: failed ranks lower (more urgent) than success and running.
	if rank("failed") >= rank("success") {
		t.Error("failed should have lower rank than success")
	}
	if rank("failed") >= rank("running") {
		t.Error("failed should have lower rank than running")
	}

	// And: success ranks higher (less urgent) than running.
	if rank("success") <= rank("running") {
		t.Error("success should have higher rank than running")
	}

	// And: an unrecognised status falls back to the "unknown" rank.
	unknownRank := rank("unknown-status")
	if unknownRank != stageStatusPriority["unknown"] {
		t.Errorf("Unknown status should get default rank %d, got %d",
			stageStatusPriority["unknown"], unknownRank)
	}
}

// TestTreeNode_IsDir: only nodes of type "tree" report as directories.
// Given tree, blob, and empty-typed nodes, when IsDir is called, then it
// returns true for "tree" and false otherwise.
// Why it matters: a blob misclassified as a directory would make the explorer
// try to descend into a file and wedge navigation on a garbage listing.
func TestTreeNode_IsDir(t *testing.T) {
	// Given: nodes covering the three type values the API can return.
	tests := []struct {
		name string
		node TreeNode
		want bool
	}{
		{
			name: "directory",
			node: TreeNode{Type: "tree", Path: "src", Name: "src"},
			want: true,
		},
		{
			name: "file",
			node: TreeNode{Type: "blob", Path: "README.md", Name: "README.md"},
			want: false,
		},
		{
			name: "empty type",
			node: TreeNode{Type: "", Path: "unknown", Name: "unknown"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When/Then: only the "tree" node classifies as a directory.
			got := tt.node.IsDir()
			if got != tt.want {
				t.Errorf("TreeNode.IsDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestErrNoPipelines: the ErrNoPipelines sentinel exists with a stable message.
// Given the package-level sentinel, when it is inspected, then it is non-nil
// and reads "no pipelines found".
// Why it matters: callers match this sentinel with errors.Is to render an
// empty state; losing the value or silently changing the message would turn
// every CI-less project into a spurious failure screen.
func TestErrNoPipelines(t *testing.T) {
	// When/Then: the sentinel is defined and keeps its documented message.
	err := ErrNoPipelines
	if err == nil {
		t.Fatal("ErrNoPipelines should not be nil")
	}
	if err.Error() != "no pipelines found" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestErrNoJobs: the ErrNoJobs sentinel exists with a stable message.
// Given the package-level sentinel, when it is inspected, then it is non-nil
// and reads "no jobs found".
// Why it matters: the UI matches this sentinel to show a waiting state for
// pipelines whose jobs have not been scheduled yet; losing it would render
// that routine moment as an error.
func TestErrNoJobs(t *testing.T) {
	// When/Then: the sentinel is defined and keeps its documented message.
	err := ErrNoJobs
	if err == nil {
		t.Fatal("ErrNoJobs should not be nil")
	}
	if err.Error() != "no jobs found" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestGetFileContent_EmptyPath: requesting a file with an empty path fails before any I/O.
// Given a client and an empty path, when GetFileContent is called, then it
// returns a "file path required" error.
// Why it matters: an empty path forwarded to the API would produce a cryptic
// 404 for what is really a caller bug (nothing selected in the explorer).
func TestGetFileContent_EmptyPath(t *testing.T) {
	// Given: a client whose path validation runs before any request.
	client, err := NewClient("valid-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// When: fetching a file with an empty path.
	ctx := context.Background()
	_, err = client.GetFileContent(ctx, 123, "", "main")

	// Then: the empty-path guard rejects the call.
	if err == nil {
		t.Fatal("Expected error for empty file path")
	}
	if !strings.Contains(err.Error(), "file path required") {
		t.Errorf("Expected 'file path required' error, got: %v", err)
	}
}

// TestGetFileContent_PathTraversalValidation: traversal-shaped paths are rejected client-side.
// Given paths with parent-directory hops, nested "..", or a leading slash,
// when GetFileContent is called, then each is refused with a "path traversal
// not allowed" error before any request is issued.
// Why it matters: without this guard a crafted path could steer file reads
// outside the intended repository file endpoint, and the request would also
// leak onto the network.
func TestGetFileContent_PathTraversalValidation(t *testing.T) {
	// Given: a client; every case below is caught by validation, not I/O.
	client, err := NewClient("valid-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	// And: traversal attempts via parent hops, nesting, and absolute paths.
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "parent directory traversal",
			path:    "../etc/passwd",
			wantErr: "path traversal not allowed",
		},
		{
			name:    "nested traversal",
			path:    "src/../../etc/passwd",
			wantErr: "path traversal not allowed",
		},
		{
			name:    "absolute path",
			path:    "/etc/passwd",
			wantErr: "path traversal not allowed",
		},
		{
			name:    "dotdot in middle",
			path:    "foo/../bar",
			wantErr: "path traversal not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: fetching the traversal-shaped path.
			_, err := client.GetFileContent(ctx, 123, tt.path, "main")

			// Then: validation refuses it with the traversal message.
			if err == nil {
				t.Fatalf("Expected error for path %q", tt.path)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestRetryJob_ZeroJobID: retrying with a zero job ID is rejected up front.
// Given a client and jobID 0, when RetryJob is called, then it returns a
// "missing job id" error without hitting the API.
// Why it matters: zero is the UI's "nothing selected" value; passing it
// through would POST to /jobs/0/retry and dress the caller bug up as a
// confusing 404.
func TestRetryJob_ZeroJobID(t *testing.T) {
	// Given: a client whose job-id validation runs before any request.
	client, err := NewClient("valid-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// When: retrying job ID zero.
	ctx := context.Background()
	_, err = client.RetryJob(ctx, 123, 0)

	// Then: the zero-id guard rejects the call.
	if err == nil {
		t.Fatal("Expected error for zero job ID")
	}
	if !strings.Contains(err.Error(), "missing job id") {
		t.Errorf("Expected 'missing job id' error, got: %v", err)
	}
}

// TestPipelineSummary_Nil: a nil *gl.Pipeline maps to the zero PipelineSummary.
// Given a nil pipeline pointer, when pipelineSummary converts it, then the
// result has zero-valued ID, Status, and Ref instead of panicking.
// Why it matters: API responses can hand the mapper a nil pipeline; a nil
// dereference here would crash the whole TUI rather than render a blank row.
func TestPipelineSummary_Nil(t *testing.T) {
	// When: converting a nil pipeline.
	got := pipelineSummary(nil)

	// Then: every field stays at its zero value.
	if got.ID != 0 || got.Status != "" || got.Ref != "" {
		t.Errorf("pipelineSummary(nil) should return zero PipelineSummary, got: %+v", got)
	}
}

// TestPipelineStage_StatusPriority: folding a job-status sequence yields the most urgent stage status.
// Given sequences of job statuses as they would arrive within one stage, when
// they are folded through mergeStageStatus, then the survivor is the highest
// priority status in the sequence and success only survives an all-success
// stage.
// Why it matters: this fold is exactly how stage badges are computed; an
// ordering bug would let a green badge mask a failed or stuck job in that
// stage.
func TestPipelineStage_StatusPriority(t *testing.T) {
	// Given: per-stage status sequences with a known most-urgent member.
	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{
			name:     "failed wins",
			statuses: []string{"success", "running", "failed"},
			want:     "failed",
		},
		{
			name:     "canceled beats success",
			statuses: []string{"success", "canceled", "pending"},
			want:     "canceled",
		},
		{
			name:     "running beats success",
			statuses: []string{"success", "running", "skipped"},
			want:     "running",
		},
		{
			name:     "manual beats success",
			statuses: []string{"success", "manual", "skipped"},
			want:     "manual",
		},
		{
			name:     "success when all success",
			statuses: []string{"success", "success"},
			want:     "success",
		},
		{
			name:     "single status",
			statuses: []string{"pending"},
			want:     "pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: folding the statuses in arrival order.
			result := ""
			for _, status := range tt.statuses {
				result = mergeStageStatus(result, status)
			}

			// Then: the most urgent status in the sequence survives.
			if result != tt.want {
				t.Errorf("mergeStageStatus(%v) = %q, want %q", tt.statuses, result, tt.want)
			}
		})
	}
}

// TestGetFileContent_PathTraversal: traversal attempts are refused before any
// request leaves the client.
// Given raw, percent-encoded, absolute, and normalised-to-parent traversal
// paths, when GetFileContent validates them, then each is rejected with an
// "invalid file path" error and zero API hits, while the double-encoded form
// is forwarded exactly once and surfaces the API's rejection.
// Why it matters: a validation gap would let a crafted path read outside the
// repository tree via percent-encoding tricks the raw ".." check misses.
//
// The double-encoded case is intentional passthrough: %252e decodes to the
// literal "%2e", which is safe to forward, so the client leaves it for the
// API to reject; the hit count pins that it really is forwarded rather than
// silently mangled or dropped.
func TestGetFileContent_PathTraversal(t *testing.T) {
	// Given: traversal attempts in every encoding the validator handles.
	tests := []struct {
		name      string
		path      string
		wantErr   string
		forwarded bool
	}{
		{
			name:    "simple dot-dot",
			path:    "../etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "encoded dot-dot",
			path:    "%2e%2e/etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:      "double encoded (passthrough)",
			path:      "%252e%252e/etc/passwd",
			forwarded: true,
		},
		{
			name:    "absolute path",
			path:    "/etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "mixed encoding",
			path:    "foo/%2e%2e/%2e%2e/etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "normalized to parent",
			path:    "foo/../../bar",
			wantErr: "invalid file path",
		},
	}

	// And: a canned API that counts hits and rejects whatever reaches it, so
	// the tests prove where each rejection happens without any live network.
	hits := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"400 Bad Request"}`))
	}))

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits = 0

			// When: fetching the traversal-shaped path.
			_, err := client.GetFileContent(ctx, 12345, tt.path, "main")

			// Then: every case errors; client-side cases carry the validation
			// message and never reach the API.
			if err == nil {
				t.Fatalf("expected error for path %q, got nil", tt.path)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
			wantHits := 0
			if tt.forwarded {
				wantHits = 1
			}
			if hits != wantHits {
				t.Errorf("API hits for %q = %d, want %d", tt.path, hits, wantHits)
			}
		})
	}
}

// TestPaginate_RespectsContextCancellation: cancelling mid-pagination stops before the next page fetch.
// Given a fetch function that cancels the context during page one of an
// advertised five, when paginate continues, then it observes ctx.Done()
// before page two, returns context.Canceled, and keeps page one's items.
// Why it matters: without the between-pages check, a user navigating away
// mid-refresh would leave paginate crawling every remaining page, burning API
// quota behind a screen nobody is watching.
func TestPaginate_RespectsContextCancellation(t *testing.T) {
	// Given: a fetch that cancels the context during the first page.
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	fetch := func(page int) ([]int, *gl.Response, error) {
		calls++
		// Cancel after the first successful fetch. The next iteration of
		// paginate must observe ctx.Done() and abort, not call fetch again.
		if page == 1 {
			cancel()
		}
		resp := &gl.Response{Response: &http.Response{}, NextPage: int64(page + 1), TotalPages: 5}
		return []int{page}, resp, nil
	}

	// When: paginating with more pages still advertised.
	items, err := paginate(ctx, fetch)

	// Then: the run aborts with context.Canceled after exactly one fetch,
	// keeping page one's items.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 fetch call after cancellation, got %d", calls)
	}
	if len(items) != 1 || items[0] != 1 {
		t.Errorf("expected partial results [1], got %v", items)
	}
}

// TestPaginate_ReturnsPartialResultsOnMidPageError: a mid-sequence page failure returns earlier pages plus the error.
// Given page one succeeding and page two failing with a sentinel error, when
// paginate runs, then it returns page one's items alongside an error chain
// containing the sentinel.
// Why it matters: dropping already-fetched items on a late page error would
// blank a list the user could still browse, and dropping the error would hide
// that the results are incomplete.
func TestPaginate_ReturnsPartialResultsOnMidPageError(t *testing.T) {
	// Given: a fetch where page one succeeds and page two fails.
	sentinel := errors.New("page 2 boom")
	fetch := func(page int) ([]int, *gl.Response, error) {
		switch page {
		case 1:
			return []int{10, 20, 30}, &gl.Response{Response: &http.Response{}, NextPage: 2, TotalPages: 2}, nil
		case 2:
			return nil, &gl.Response{Response: &http.Response{StatusCode: 500}}, sentinel
		}
		t.Fatalf("unexpected page %d", page)
		return nil, nil, nil
	}

	// When: paginating across both pages.
	items, err := paginate(context.Background(), fetch)

	// Then: the sentinel is in the error chain and page one's items survive.
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel in chain, got %v", err)
	}
	if len(items) != 3 || items[0] != 10 || items[1] != 20 || items[2] != 30 {
		t.Errorf("expected partial page-1 items [10 20 30], got %v", items)
	}
}

// TestPaginate_StopsAtZeroNextPage: pagination halts when the server reports no next page.
// Given a fetch function whose third page sets NextPage to zero, when
// paginate runs, then exactly three fetches happen and three pages of items
// are returned.
// Why it matters: ignoring the NextPage=0 cursor would loop forever on the
// last page, hanging the fetch command behind the UI.
func TestPaginate_StopsAtZeroNextPage(t *testing.T) {
	// Given: a fetch whose third page reports NextPage=0.
	calls := 0
	fetch := func(page int) ([]int, *gl.Response, error) {
		calls++
		next := int64(page + 1)
		if page == 3 {
			next = 0 // last page
		}
		resp := &gl.Response{Response: &http.Response{}, NextPage: next, TotalPages: 3}
		return []int{page}, resp, nil
	}

	// When: paginating to exhaustion.
	items, err := paginate(context.Background(), fetch)
	// Then: fetch ran exactly three times and every page's items came back.
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 fetch calls (until NextPage=0), got %d", calls)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
}

// TestGetFileContent_ValidPaths: ordinary repository paths pass traversal
// validation and fetch cleanly.
// Given legitimate paths including nested files and dotfiles served by a
// canned API, when GetFileContent fetches each, then every call succeeds and
// returns the decoded canned content.
// Why it matters: an over-eager traversal filter would make real files like
// .gitignore or docs/api/v1/spec.yaml unopenable in the explorer.
func TestGetFileContent_ValidPaths(t *testing.T) {
	// Given: ordinary repository paths that must never trip validation.
	tests := []string{
		"README.md",
		"src/main.go",
		"docs/api/v1/spec.yaml",
		"config.yml",
		".gitignore",
	}

	// And: a canned API serving the same small base64 file for any path.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"file_name":"f","file_path":"f","size":6,"encoding":"base64","ref":"main","content":"aGVsbG8K"}`))
	}))

	ctx := context.Background()
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			// When: fetching the path.
			content, err := client.GetFileContent(ctx, 12345, path, "main")
			// Then: the fetch succeeds with the decoded canned content.
			if err != nil {
				t.Fatalf("GetFileContent(%q) error: %v", path, err)
			}
			if content != "hello\n" {
				t.Errorf("content = %q, want the decoded canned file", content)
			}
		})
	}
}
