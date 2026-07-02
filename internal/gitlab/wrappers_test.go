package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// wrappersFixture is the canned JSON the httptest handler returns from each
// "happy path" wrapper test. It is a single shared corpus so a schema change
// only needs updating in one place and every test asserts against the same
// field set; per-test copies invite drift where PipelineSummary fields
// silently disappear at individual call sites.
var wrappersFixture = struct {
	pipeline  string
	pipelines string
	job       string
}{
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
// request, used by the 401/404 tests so we don't have to construct a
// matching JSON error envelope per case.
func statusHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	})
}

// TestGetPipeline_Happy: a single-pipeline GET maps every summary field, including the enriched ones.
// Given the canned pipeline served at /projects/42/pipelines/100, when
// GetPipeline runs, then the summary carries ID, status, and ref plus
// Duration 120, Coverage 85.5, and User "Ada Lovelace".
// Why it matters: Duration, Coverage, and User exist only on the full
// per-pipeline payload; a mapper that forgets them leaves those columns
// permanently blank in the detail view while every other field still works.
func TestGetPipeline_Happy(t *testing.T) {
	// Given: the pipeline GET endpoint serving the shared fixture.
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/pipelines/100": wrappersFixture.pipeline,
	}))

	// When: fetching the pipeline.
	p, err := client.GetPipeline(context.Background(), 42, 100)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}

	// Then: the core identity fields map through.
	if p.ID != 100 || p.Status != "success" || p.Ref != "main" {
		t.Errorf("pipeline mismatch: %+v", p)
	}

	// And: the fields only present on the full payload are populated too.
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

// TestGetPipeline_NotFound: a 404 for an unknown pipeline maps to a not-found error.
// Given a server answering 404, when GetPipeline runs, then the error is
// non-nil and matches IsNotFound.
// Why it matters: the UI treats not-found (deleted or inaccessible pipeline)
// differently from transport failures; a misclassification would suggest
// retrying something that will never exist.
func TestGetPipeline_NotFound(t *testing.T) {
	// Given: a server that answers 404 to everything.
	client := newTestClient(t, statusHandler(http.StatusNotFound))

	// When: fetching a pipeline that does not exist.
	_, err := client.GetPipeline(context.Background(), 42, 999)

	// Then: the failure surfaces and classifies as not-found.
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should match: %v", err)
	}
}

// TestLatestPipelineForSHA_Match: the newest pipeline row for a SHA lookup maps into a summary.
// Given a pipelines listing containing one run for SHA deadbeef, when
// LatestPipelineForSHA runs, then the summary keeps ID 200 and that SHA.
// Why it matters: the HEAD-resolution flow trusts this row to describe "what
// ran for my commit"; picking the wrong row or dropping the SHA would point
// the user at someone else's pipeline.
func TestLatestPipelineForSHA_Match(t *testing.T) {
	// Given: the pipelines list endpoint serving one matching row.
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/pipelines": wrappersFixture.pipelines,
	}))

	// When: resolving the latest pipeline for the SHA.
	p, err := client.LatestPipelineForSHA(context.Background(), 42, "deadbeef")
	if err != nil {
		t.Fatalf("LatestPipelineForSHA: %v", err)
	}

	// Then: the row maps through with its ID and SHA intact.
	if p.ID != 200 || p.SHA != "deadbeef" {
		t.Errorf("pipeline mismatch: %+v", p)
	}
}

// TestLatestPipelineForSHA_Empty: no pipelines for a SHA surfaces ErrNoPipelines.
// Given an empty pipelines listing, when LatestPipelineForSHA runs, then the
// error matches ErrNoPipelines via errors.Is.
// Why it matters: callers show a "pipeline not created yet, wait" hint on
// this sentinel; a generic error would read as a failure right after a
// successful push.
func TestLatestPipelineForSHA_Empty(t *testing.T) {
	// Given: a pipelines list endpoint with no rows for the SHA.
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/pipelines": "[]",
	}))

	// When/Then: the lookup yields the ErrNoPipelines sentinel.
	_, err := client.LatestPipelineForSHA(context.Background(), 42, "ghostsha")
	if !errors.Is(err, ErrNoPipelines) {
		t.Fatalf("expected ErrNoPipelines, got %v", err)
	}
}

// TestLatestPipelineForSHA_EmptySHA: a blank SHA is rejected before any request is sent.
// Given a whitespace-only SHA and a server that would answer 500, when
// LatestPipelineForSHA runs, then it fails with an "empty sha" message rather
// than the server's error.
// Why it matters: an unguarded blank SHA would query the unfiltered pipeline
// list and confidently return some unrelated latest pipeline.
//
// The 500 handler is the tripwire: if validation ever slipped through to the
// network, the assertion would see the server error instead of "empty sha".
func TestLatestPipelineForSHA_EmptySHA(t *testing.T) {
	// Given: a server that would fail loudly if it were ever reached.
	client := newTestClient(t, statusHandler(http.StatusInternalServerError))

	// When: resolving with a whitespace-only SHA.
	_, err := client.LatestPipelineForSHA(context.Background(), 42, "   ")

	// Then: the empty-sha guard rejects the call before any I/O.
	if err == nil {
		t.Fatal("expected error for empty sha")
	}
	if !strings.Contains(err.Error(), "empty sha") {
		t.Errorf("error should mention empty sha: %v", err)
	}
}

// TestGetJob_Happy: a single-job GET maps ID, status, and failure reason.
// Given the canned failed job served at /projects/42/jobs/555, when GetJob
// runs, then the job carries ID 555, status failed, and failure_reason
// script_failure.
// Why it matters: the log-streaming poller decides when to stop from this
// status; a mapping slip would keep polling a finished job or hide why it
// failed.
func TestGetJob_Happy(t *testing.T) {
	// Given: the job GET endpoint serving the shared fixture.
	client := newTestClient(t, muxHandler(t, map[string]string{
		"/api/v4/projects/42/jobs/555": wrappersFixture.job,
	}))

	// When: fetching the job.
	j, err := client.GetJob(context.Background(), 42, 555)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	// Then: identity, status, and failure reason map through.
	if j.ID != 555 || j.Status != "failed" || j.FailureReason != "script_failure" {
		t.Errorf("job mismatch: %+v", j)
	}
}

// TestGetJob_ZeroID: a zero job ID is rejected before any request is sent.
// Given jobID 0 and a server that would answer 200, when GetJob runs, then it
// fails with a "missing job id" message.
// Why it matters: zero is the UI's "nothing selected" value; forwarding it
// would GET /jobs/0 and dress a caller bug up as an API failure.
func TestGetJob_ZeroID(t *testing.T) {
	// Given: a server that would happily answer if it were reached.
	client := newTestClient(t, statusHandler(http.StatusOK))

	// When: fetching job ID zero.
	_, err := client.GetJob(context.Background(), 42, 0)

	// Then: the zero-id guard rejects the call.
	if err == nil {
		t.Fatal("expected error for zero job id")
	}
	if !strings.Contains(err.Error(), "missing job id") {
		t.Errorf("error should mention missing job id: %v", err)
	}
}

// TestGetJob_NotFound: a 404 for an unknown job maps to a not-found error.
// Given a server answering 404, when GetJob runs, then the error is non-nil
// and matches IsNotFound.
// Why it matters: pollers use the not-found classification to stop following
// a deleted job instead of retrying a permanent failure forever.
func TestGetJob_NotFound(t *testing.T) {
	// Given: a server that answers 404 to everything.
	client := newTestClient(t, statusHandler(http.StatusNotFound))

	// When: fetching a job that does not exist.
	_, err := client.GetJob(context.Background(), 42, 999)

	// Then: the failure surfaces and classifies as not-found.
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should match: %v", err)
	}
}

// TestGetJobTrace_TooLargeSentinel: a trace over MaxTraceSize surfaces ErrTraceTooLarge through the wrap.
// Given a trace body ten bytes past the cap, when GetJobTrace runs, then the
// returned error matches ErrTraceTooLarge via errors.Is.
// Why it matters: the log streamer degrades gracefully (stop tailing, offer
// the web URL) only if this sentinel survives the fmt.Errorf("%w") wrap; a
// broken chain would turn oversized logs into a dead-end generic error.
func TestGetJobTrace_TooLargeSentinel(t *testing.T) {
	// Given: a server serving a trace just past the cap.
	big := strings.Repeat("x", MaxTraceSize+10)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, big)
	}))

	// When/Then: fetching the trace yields the ErrTraceTooLarge sentinel.
	_, err := client.GetJobTrace(context.Background(), 42, 1)
	if !errors.Is(err, ErrTraceTooLarge) {
		t.Fatalf("expected ErrTraceTooLarge in chain, got %v", err)
	}
}

// TestPipelineSummaryFromInfo_NilAndFallback: a nil PipelineInfo maps to the
// zero PipelineSummary, and a missing UpdatedAt falls back to CreatedAt.
// Given a nil pipeline-info pointer and one whose UpdatedAt is nil but
// CreatedAt is set, when pipelineSummaryFromInfo converts them, then the nil
// input yields the zero summary and the fallback input carries CreatedAt as
// its UpdatedAt.
// Why it matters: list responses can carry nil rows mid-refresh, and a freshly
// created pipeline has no UpdatedAt yet; a crash or zero timestamp here would
// break the pipelines panel's newest-first ordering.
func TestPipelineSummaryFromInfo_NilAndFallback(t *testing.T) {
	// When/Then: a nil info converts to the zero value without panicking.
	if got := pipelineSummaryFromInfo(nil); got.ID != 0 || got.Status != "" {
		t.Errorf("pipelineSummaryFromInfo(nil) = %+v, want zero", got)
	}

	// And: a pipeline with no UpdatedAt reports CreatedAt as its update time.
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	got := pipelineSummaryFromInfo(&gl.PipelineInfo{ID: 7, CreatedAt: &created})
	if !got.UpdatedAt.Equal(created) {
		t.Errorf("UpdatedAt = %v, want the CreatedAt fallback %v", got.UpdatedAt, created)
	}
}

// TestRetryPipeline_Happy: a successful retry POST maps the new pipeline record.
// Given a server expecting POST /projects/42/pipelines/100/retry and
// answering with the canned pipeline, when RetryPipeline runs, then the
// summary keeps ID 100 and status success.
// Why it matters: the retry hotkey lands here; a wrong path or verb would
// break every pipeline retry while looking fine locally.
func TestRetryPipeline_Happy(t *testing.T) {
	// Given: a retry endpoint that checks the method and path.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/projects/42/pipelines/100/retry" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, wrappersFixture.pipeline)
	}))

	// When: retrying the pipeline.
	p, err := client.RetryPipeline(context.Background(), 42, 100, "")
	if err != nil {
		t.Fatalf("RetryPipeline: %v", err)
	}

	// Then: the returned pipeline record maps through.
	if p.ID != 100 || p.Status != "success" {
		t.Errorf("pipeline mismatch: %+v", p)
	}
}

// TestRetryPipeline_FallbackCreate: a 400 on retry falls back to creating a fresh pipeline on the ref.
// Given a retry endpoint answering 400 "nothing to retry" and a create
// endpoint answering with a pipeline, when RetryPipeline runs with ref
// "main", then it returns the created pipeline instead of the 400.
// Why it matters: GitLab answers 400 when a pipeline has no retryable jobs;
// without the create fallback the retry key would dead-end exactly when the
// user wants a re-run of a clean pipeline.
func TestRetryPipeline_FallbackCreate(t *testing.T) {
	// Given: a retry endpoint that 400s and a create endpoint that succeeds.
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

	// When: retrying with a ref available for the fallback.
	p, err := client.RetryPipeline(context.Background(), 42, 100, "main")
	// Then: the freshly created pipeline is returned instead of the 400.
	if err != nil {
		t.Fatalf("RetryPipeline fallback: %v", err)
	}
	if p.ID != 100 {
		t.Errorf("expected fallback pipeline ID=100, got %d", p.ID)
	}
}

// TestRetryPipeline_FallbackCreate_PreservesBothErrors: when retry and fallback create both fail, both errors stay inspectable.
// Given a transport that fails the retry with one sentinel (wrapped in a 400
// response) and the create with another, when RetryPipeline runs, then the
// returned error matches both sentinels via errors.Is.
// Why it matters: flattening the retry error into a string instead of
// wrapping it would break downstream sentinel matches and hide which of the
// two calls actually failed.
func TestRetryPipeline_FallbackCreate_PreservesBothErrors(t *testing.T) {
	// Given: a transport failing retry and create with distinct sentinels.
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

	// And: a client wired to that transport, no real server involved.
	api, err := gl.NewClient("test-token",
		gl.WithBaseURL("http://example.invalid/api/v4"),
		gl.WithoutRetries(),
		gl.WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		t.Fatalf("gl.NewClient: %v", err)
	}
	client := &Client{api: api, host: "http://example.invalid"}

	// When: retrying with a ref so the fallback create is attempted too.
	_, err = client.RetryPipeline(context.Background(), 42, 100, "main")

	// Then: both sentinels remain matchable in the returned error chain.
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

// TestAPIError_ErrorAndUnwrap: APIError prefers the wrapped error's message and unwraps to it.
// Given an APIError wrapping an underlying error, when Error and Unwrap are
// called, then Error returns the wrapped error's message, Unwrap returns the
// wrapped error, and with no wrapped error Error falls back to Message.
// Why it matters: Error surfacing the wrong string would hide the failing
// request from logs, and a broken Unwrap would cut the errors.Is chains the
// status predicates depend on.
func TestAPIError_ErrorAndUnwrap(t *testing.T) {
	// Given: an APIError carrying both a Message and a wrapped error.
	wrapped := errors.New("network unreachable")
	e := &APIError{StatusCode: 500, Message: "server error", Err: wrapped}

	// Then: Error prefers the wrapped error's message and Unwrap returns it.
	if e.Error() != "network unreachable" {
		t.Errorf("Error() preferred Message over Err: %q", e.Error())
	}
	if e.Unwrap() != wrapped {
		t.Errorf("Unwrap() = %v, want wrapped", e.Unwrap())
	}

	// And: with no wrapped error, Error falls back to Message.
	e2 := &APIError{StatusCode: 500, Message: "boom"}
	if e2.Error() != "boom" {
		t.Errorf("Error() with no Err = %q, want %q", e2.Error(), "boom")
	}
}
