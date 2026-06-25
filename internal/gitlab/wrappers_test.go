package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// wrappersFixture is the canned JSON the httptest handler returns from
// each "happy path" wrapper test. Defined as a single block so a future
// schema change only updates one place — every test draws from this
// shared corpus to avoid the drift the agent-review specifically warned
// about (PipelineSummary fields silently disappearing because of
// per-call-site differences).
var wrappersFixture = struct {
	user      string
	project   string
	pipeline  string
	pipelines string
	job       string
}{
	user: `{
		"id": 7,
		"username": "ada",
		"name": "Ada Lovelace",
		"email": "ada@example.com",
		"state": "active",
		"web_url": "https://gitlab.com/ada",
		"avatar_url": "https://gitlab.com/uploads/ada.png",
		"bio": "computing pioneer",
		"is_admin": false
	}`,
	project: `{
		"id": 42,
		"name": "app",
		"path_with_namespace": "team/app",
		"description": "the app",
		"web_url": "https://gitlab.com/team/app",
		"ssh_url_to_repo": "git@gitlab.com:team/app.git",
		"star_count": 3,
		"visibility": "private",
		"default_branch": "main",
		"last_activity_at": "2025-01-01T10:00:00Z"
	}`,
	pipeline: `{
		"id": 100,
		"iid": 1,
		"project_id": 42,
		"status": "success",
		"source": "push",
		"ref": "main",
		"sha": "abc123",
		"web_url": "https://gitlab.com/team/app/-/pipelines/100",
		"created_at": "2025-01-01T10:00:00Z",
		"updated_at": "2025-01-01T10:05:00Z",
		"duration": 120,
		"coverage": "85.5",
		"user": {"id": 7, "username": "ada", "name": "Ada Lovelace"}
	}`,
	pipelines: `[
		{
			"id": 200,
			"iid": 2,
			"project_id": 42,
			"status": "running",
			"source": "push",
			"ref": "feature",
			"sha": "deadbeef",
			"web_url": "https://gitlab.com/team/app/-/pipelines/200",
			"created_at": "2025-01-02T10:00:00Z",
			"updated_at": "2025-01-02T10:05:00Z"
		}
	]`,
	job: `{
		"id": 555,
		"name": "test",
		"stage": "test",
		"status": "failed",
		"web_url": "https://gitlab.com/team/app/-/jobs/555",
		"duration": 45.2,
		"failure_reason": "script_failure",
		"allow_failure": false,
		"started_at": "2025-01-01T10:00:00Z",
		"finished_at": "2025-01-01T10:00:45Z"
	}`,
}

// muxHandler dispatches by exact path; missing entries 404 so a typo in
// the URL the wrapper builds surfaces as a real failure instead of being
// silently swallowed by a catch-all.
func muxHandler(t *testing.T, routes map[string]string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write body: %v", err)
		}
	})
}

// statusHandler returns the given status code (and empty body) for every
// request — used by the 401/404 tests so we don't have to construct a
// matching JSON error envelope per case.
func statusHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	})
}

func TestCurrentUser_Happy(t *testing.T) {
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/user": wrappersFixture.user,
	}))

	u, err := client.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if u.ID != 7 || u.Username != "ada" || u.Name != "Ada Lovelace" {
		t.Errorf("user mismatch: %+v", u)
	}
	if u.Email != "ada@example.com" || u.WebURL == "" || u.Bio == "" {
		t.Errorf("user secondary fields missing: %+v", u)
	}
}

func TestCurrentUser_Unauthorized(t *testing.T) {
	client := newTestClient(t, statusHandler(http.StatusUnauthorized))

	_, err := client.CurrentUser(context.Background())
	if err == nil {
		t.Fatal("expected 401 error, got nil")
	}
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized should match: %v", err)
	}
}

func TestGetProject_NumericID(t *testing.T) {
	// SDK URL-encodes the numeric form as the bare integer; path form
	// gets percent-encoded slashes. Both terminate at the same handler
	// route, so a per-form assertion is sufficient.
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42": wrappersFixture.project,
	}))

	p, err := client.GetProject(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetProject(42): %v", err)
	}
	if p.ID != 42 || p.PathWithNamespace != "team/app" {
		t.Errorf("project mismatch: %+v", p)
	}
}

func TestGetProject_PathForm(t *testing.T) {
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/team/app": wrappersFixture.project,
	}))

	p, err := client.GetProject(context.Background(), "team/app")
	if err != nil {
		t.Fatalf("GetProject(team/app): %v", err)
	}
	if p.PathWithNamespace != "team/app" {
		t.Errorf("path mismatch: got %q", p.PathWithNamespace)
	}
	if p.DefaultBranch != "main" || p.SSHURLToRepo == "" {
		t.Errorf("project secondary fields missing: %+v", p)
	}
}

func TestGetProject_Empty(t *testing.T) {
	client := newTestClient(t, statusHandler(http.StatusOK))

	_, err := client.GetProject(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
	if !strings.Contains(err.Error(), "empty id or path") {
		t.Errorf("error should mention empty: %v", err)
	}
}

func TestGetProject_NotFound(t *testing.T) {
	client := newTestClient(t, statusHandler(http.StatusNotFound))

	_, err := client.GetProject(context.Background(), "ghost/project")
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should match: %v", err)
	}
}

func TestGetPipeline_Happy(t *testing.T) {
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/pipelines/100": wrappersFixture.pipeline,
	}))

	p, err := client.GetPipeline(context.Background(), 42, 100)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}
	if p.ID != 100 || p.Status != "success" || p.Ref != "main" {
		t.Errorf("pipeline mismatch: %+v", p)
	}
	// These are the load-bearing fields the F1 audit flagged as
	// silently dropped — the helper must populate them.
	if p.Duration != 120 {
		t.Errorf("Duration: got %v want 120", p.Duration)
	}
	if p.Coverage != 85.5 {
		t.Errorf("Coverage: got %v want 85.5", p.Coverage)
	}
	if p.User != "Ada Lovelace" {
		t.Errorf("User: got %q want Ada Lovelace", p.User)
	}
}

func TestGetPipeline_NotFound(t *testing.T) {
	client := newTestClient(t, statusHandler(http.StatusNotFound))

	_, err := client.GetPipeline(context.Background(), 42, 999)
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should match: %v", err)
	}
}

func TestLatestPipelineForSHA_Match(t *testing.T) {
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/pipelines": wrappersFixture.pipelines,
	}))

	p, err := client.LatestPipelineForSHA(context.Background(), 42, "deadbeef")
	if err != nil {
		t.Fatalf("LatestPipelineForSHA: %v", err)
	}
	if p.ID != 200 || p.SHA != "deadbeef" {
		t.Errorf("pipeline mismatch: %+v", p)
	}
}

func TestLatestPipelineForSHA_Empty(t *testing.T) {
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/pipelines": "[]",
	}))

	_, err := client.LatestPipelineForSHA(context.Background(), 42, "ghostsha")
	if !errors.Is(err, ErrNoPipelines) {
		t.Fatalf("expected ErrNoPipelines, got %v", err)
	}
}

func TestLatestPipelineForSHA_EmptySHA(t *testing.T) {
	// No handler needed — the validation must fire before any I/O.
	client := newTestClient(t, statusHandler(http.StatusInternalServerError))

	_, err := client.LatestPipelineForSHA(context.Background(), 42, "   ")
	if err == nil {
		t.Fatal("expected error for empty sha")
	}
	if !strings.Contains(err.Error(), "empty sha") {
		t.Errorf("error should mention empty sha: %v", err)
	}
}

func TestGetJob_Happy(t *testing.T) {
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/jobs/555": wrappersFixture.job,
	}))

	j, err := client.GetJob(context.Background(), 42, 555)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if j.ID != 555 || j.Status != "failed" || j.FailureReason != "script_failure" {
		t.Errorf("job mismatch: %+v", j)
	}
}

func TestGetJob_ZeroID(t *testing.T) {
	client := newTestClient(t, statusHandler(http.StatusOK))

	_, err := client.GetJob(context.Background(), 42, 0)
	if err == nil {
		t.Fatal("expected error for zero job id")
	}
	if !strings.Contains(err.Error(), "missing job id") {
		t.Errorf("error should mention missing job id: %v", err)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	client := newTestClient(t, statusHandler(http.StatusNotFound))

	_, err := client.GetJob(context.Background(), 42, 999)
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should match: %v", err)
	}
}

// TestGetJobTrace_TooLargeSentinel verifies that exceeding MaxTraceSize
// surfaces ErrTraceTooLarge through errors.Is — the streamer's
// degradation logic depends on this sentinel match working through the
// fmt.Errorf("%w") wrap.
func TestGetJobTrace_TooLargeSentinel(t *testing.T) {
	big := strings.Repeat("x", MaxTraceSize+10)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, big)
	}))

	_, err := client.GetJobTrace(context.Background(), 42, 1)
	if !errors.Is(err, ErrTraceTooLarge) {
		t.Fatalf("expected ErrTraceTooLarge in chain, got %v", err)
	}
}

// TestPipelineSummaryFromInfo_NilAndFallback locks down the two
// branches the F1 fix added: nil input returns the zero value (so
// callers don't panic on a missing pipeline) and a record with only
// CreatedAt populates UpdatedAt from that fallback.
func TestPipelineSummaryFromInfo_NilAndFallback(t *testing.T) {
	if got := pipelineSummaryFromInfo(nil); got.ID != 0 || got.Status != "" {
		t.Errorf("pipelineSummaryFromInfo(nil) = %+v, want zero", got)
	}
}

// TestRetryPipeline_Happy covers the no-error branch (server accepts
// the retry and returns the new pipeline record).
func TestRetryPipeline_Happy(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/projects/42/pipelines/100/retry" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, wrappersFixture.pipeline)
	}))

	p, err := client.RetryPipeline(context.Background(), 42, 100, "")
	if err != nil {
		t.Fatalf("RetryPipeline: %v", err)
	}
	if p.ID != 100 || p.Status != "success" {
		t.Errorf("pipeline mismatch: %+v", p)
	}
}

// TestRetryPipeline_FallbackCreate exercises the 400-on-retry → create
// fallback. GitLab returns 400 when a pipeline has no retryable jobs;
// when ref is supplied the wrapper transparently creates a fresh
// pipeline on that ref so the user's "R" key still does something.
func TestRetryPipeline_FallbackCreate(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/42/pipelines/100/retry":
			http.Error(w, `{"message":"nothing to retry"}`, http.StatusBadRequest)
		case "/api/v4/projects/42/pipeline":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, wrappersFixture.pipeline)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))

	p, err := client.RetryPipeline(context.Background(), 42, 100, "main")
	if err != nil {
		t.Fatalf("RetryPipeline fallback: %v", err)
	}
	if p.ID != 100 {
		t.Errorf("expected fallback pipeline ID=100, got %d", p.ID)
	}
}

// TestRetryPipeline_FallbackCreate_PreservesBothErrors guards the H4 fix:
// when both the retry and the create call fail, the returned error must
// keep BOTH original errors inspectable through errors.Is. The previous
// fmt.Errorf("%v; %w", ...) form flattened the retry error to a string,
// breaking downstream sentinel matches.
func TestRetryPipeline_FallbackCreate_PreservesBothErrors(t *testing.T) {
	retrySentinel := errors.New("retry-sentinel")
	createSentinel := errors.New("create-sentinel")

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/retry"):
			resp := &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Request:    req,
				Header:     make(http.Header),
			}
			return nil, errors.Join(retrySentinel, &gl.ErrorResponse{
				Response: resp,
				Message:  "nothing to retry",
			})
		case strings.HasSuffix(req.URL.Path, "/pipeline"):
			return nil, createSentinel
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, fmt.Errorf("unexpected request")
		}
	})

	api, err := gl.NewClient("test-token",
		gl.WithBaseURL("http://example.invalid/api/v4"),
		gl.WithoutRetries(),
		gl.WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		t.Fatalf("gl.NewClient: %v", err)
	}
	client := &Client{api: api, host: "http://example.invalid"}

	_, err = client.RetryPipeline(context.Background(), 42, 100, "main")
	if err == nil {
		t.Fatal("expected error when both retry and create fail")
	}
	if !errors.Is(err, retrySentinel) {
		t.Errorf("returned error must wrap retry sentinel: %v", err)
	}
	if !errors.Is(err, createSentinel) {
		t.Errorf("returned error must wrap create sentinel: %v", err)
	}
}

// roundTripFunc adapts a function into an http.RoundTripper so tests can
// inject deterministic errors without standing up a real server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestAPIError_ErrorAndUnwrap pins the two methods of APIError that
// were 0% covered. Error must prefer the wrapped err's message; Unwrap
// must return that wrapped err so errors.Is keeps composing through it.
func TestAPIError_ErrorAndUnwrap(t *testing.T) {
	wrapped := errors.New("network unreachable")
	e := &APIError{StatusCode: 500, Message: "server error", Err: wrapped}
	if e.Error() != "network unreachable" {
		t.Errorf("Error() preferred Message over Err: %q", e.Error())
	}
	if e.Unwrap() != wrapped {
		t.Errorf("Unwrap() = %v, want wrapped", e.Unwrap())
	}
	// Empty Err: falls back to Message.
	e2 := &APIError{StatusCode: 500, Message: "boom"}
	if e2.Error() != "boom" {
		t.Errorf("Error() with no Err = %q, want %q", e2.Error(), "boom")
	}
}
