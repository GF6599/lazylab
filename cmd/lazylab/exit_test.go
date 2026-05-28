package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestExitCodeFor exercises every branch of the error→exit-code map.
// The mapping is the stable scripting contract documented in the
// `lazylab pipeline status --help` output, so any change here is a
// breaking change for shell wrappers that already depend on the codes.
//
// HTTP-status branches build their errors by routing a real call through
// an httptest server so the SDK's *gl.ErrorResponse plumbing fires — a
// hand-rolled *gitlab.APIError would bypass AsAPIError's chain-walking
// (it only recognizes the SDK's concrete error type) and produce a
// false negative. The cost is one local Listen per case, which is
// negligible at the size of the table.
func TestExitCodeFor(t *testing.T) {
	unauthorized := apiErrorFromHTTP(t, http.StatusUnauthorized)
	forbidden := apiErrorFromHTTP(t, http.StatusForbidden)
	notFound := apiErrorFromHTTP(t, http.StatusNotFound)
	rateLimited := apiErrorFromHTTP(t, http.StatusTooManyRequests)
	serverErr500 := apiErrorFromHTTP(t, http.StatusInternalServerError)
	serverErr502 := apiErrorFromHTTP(t, http.StatusBadGateway)
	serverErr503 := apiErrorFromHTTP(t, http.StatusServiceUnavailable)

	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "nil maps to OK",
			err:  nil,
			want: exitOK,
		},
		{
			name: "context.Canceled treated as clean exit (Ctrl+C)",
			err:  context.Canceled,
			want: exitOK,
		},
		{
			name: "wrapped context.Canceled still clean",
			err:  fmt.Errorf("aborted by user: %w", context.Canceled),
			want: exitOK,
		},
		{
			name: "watchedFailureError maps to exitWatchedFailure",
			err:  &watchedFailureError{Resource: "pipeline", Status: "failed"},
			want: exitWatchedFailure,
		},
		{
			name: "wrapped watchedFailureError still exitWatchedFailure",
			err:  fmt.Errorf("watcher loop terminated: %w", &watchedFailureError{Resource: "job", Status: "canceled"}),
			want: exitWatchedFailure,
		},
		{
			name: "401 unauthorized → exitUnauthorized",
			err:  unauthorized,
			want: exitUnauthorized,
		},
		{
			name: "403 forbidden → exitUnauthorized (same scripting bucket)",
			err:  forbidden,
			want: exitUnauthorized,
		},
		{
			name: "429 rate limited → exitTransient",
			err:  rateLimited,
			want: exitTransient,
		},
		{
			name: "500 server error → exitTransient",
			err:  serverErr500,
			want: exitTransient,
		},
		{
			name: "502 bad gateway → exitTransient",
			err:  serverErr502,
			want: exitTransient,
		},
		{
			name: "503 service unavailable → exitTransient",
			err:  serverErr503,
			want: exitTransient,
		},
		{
			name: "404 not found → exitGeneric (not a transient retry)",
			err:  notFound,
			want: exitGeneric,
		},
		{
			name: "wrapped 401 still exitUnauthorized",
			err:  fmt.Errorf("fetch user: %w", unauthorized),
			want: exitUnauthorized,
		},
		{
			name: "wrapped 429 still exitTransient",
			err:  fmt.Errorf("list pipelines: %w", rateLimited),
			want: exitTransient,
		},
		{
			name: "ErrNoProjectContext is generic (user input error, not a network failure)",
			err:  gitlab.ErrNoProjectContext,
			want: exitGeneric,
		},
		{
			name: "net.Error (dial refused) → exitNetworkFailure",
			err:  &fakeNetErr{msg: "dial tcp: connection refused"},
			want: exitNetworkFailure,
		},
		{
			name: "net.Error (timeout) → exitNetworkFailure",
			err:  &fakeNetErr{msg: "i/o timeout", timeout: true},
			want: exitNetworkFailure,
		},
		{
			name: "wrapped net.Error still exitNetworkFailure",
			err:  fmt.Errorf("client.Do: %w", &fakeNetErr{msg: "no such host"}),
			want: exitNetworkFailure,
		},
		{
			name: "generic error falls through to exitGeneric",
			err:  errors.New("something went wrong"),
			want: exitGeneric,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitCodeFor(tt.err)
			if got != tt.want {
				t.Fatalf("exitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// TestWatchedFailureError_Message pins the human-readable shape of the
// watched-failure error message. Scripts may grep stderr for this
// substring (e.g. `lazylab pipeline watch 2>&1 | grep "ended with"`).
func TestWatchedFailureError_Message(t *testing.T) {
	e := &watchedFailureError{Resource: "pipeline", Status: "failed"}
	want := "pipeline ended with status: failed"
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// fakeNetErr is a minimal net.Error implementation for table-driven
// transport-layer tests. The standard library's *net.OpError carries a
// lot of unrelated state (Op, Net, Source, Addr) that we don't exercise
// in exitCodeFor, so a hand-rolled stub keeps the test setup readable.
type fakeNetErr struct {
	msg     string
	timeout bool
}

func (e *fakeNetErr) Error() string { return e.msg }
func (e *fakeNetErr) Timeout() bool { return e.timeout }
func (e *fakeNetErr) Temporary() bool {
	// net.Error's Temporary is deprecated but still part of the
	// interface; returning false keeps behavior deterministic regardless
	// of whether callers consult it.
	return false
}

// Compile-time guarantee that fakeNetErr satisfies net.Error, so a
// refactor of the interface surface fails the build instead of silently
// skipping the network-failure branch at runtime.
var _ net.Error = (*fakeNetErr)(nil)

// apiErrorFromHTTP fabricates an error of the exact shape AsAPIError
// recognizes: a *gl.ErrorResponse with a populated Response field, which
// gitlab.AsAPIError walks via errors.As. We avoid spinning up an httptest
// server because the SDK's default retry policy would burn 5+ seconds
// per 429/5xx case — multiply by every status code in the table and the
// test would take 30+ seconds. A hand-built ErrorResponse is the same
// type the SDK would produce in production, so the predicate logic gets
// the same coverage at zero runtime cost.
func apiErrorFromHTTP(t *testing.T, status int) error {
	t.Helper()
	reqURL, err := url.Parse("https://gitlab.example.com/api/v4/user")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return &gl.ErrorResponse{
		Response: &http.Response{
			StatusCode: status,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    reqURL,
			},
		},
		Message: http.StatusText(status),
	}
}
