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
	pipeline string
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
}

// statusHandler returns the given status code (and empty body) for every
// request, used by the 401/404 tests so we don't have to construct a
// matching JSON error envelope per case.
func statusHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	})
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
	api, err := gl.NewClient(
		"test-token",
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
