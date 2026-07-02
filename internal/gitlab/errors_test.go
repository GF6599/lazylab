package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestAsAPIError_StatusCodes: each HTTP failure class keeps its status code and matches its predicate.
// Given a server answering 401, 403, 404, 429, and 502 in turn, when
// ListProjects fails and AsAPIError inspects the wrapped error, then the
// extracted StatusCode matches the response and the corresponding Is*
// predicate returns true.
// Why it matters: the UI branches on these predicates (reauth prompt on 401,
// back-off on 429); a broken status mapping would show the generic error
// banner instead of the correct recovery path.
func TestAsAPIError_StatusCodes(t *testing.T) {
	// Given: one canned failure per status class the UI branches on.
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		predicate  func(error) bool
	}{
		{"unauthorized", http.StatusUnauthorized, `{"message":"401 Unauthorized"}`, 401, IsUnauthorized},
		{"forbidden", http.StatusForbidden, `{"message":"403 Forbidden"}`, 403, IsForbidden},
		{"not found", http.StatusNotFound, `{"message":"404 Not Found"}`, 404, IsNotFound},
		{"rate limited", http.StatusTooManyRequests, `{"message":"429 Too Many Requests"}`, 429, IsRateLimited},
		{"server error", http.StatusBadGateway, `{"message":"502 Bad Gateway"}`, 502, IsServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a server that always answers with this status and body.
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			client := newTestClient(t, handler)

			// When: a client call fails against that server.
			_, err := client.ListProjects(context.Background(), ProjectListOptions{Page: 1, PerPage: 10})

			// Then: AsAPIError extracts the status and the predicate matches.
			if err == nil {
				t.Fatal("expected error from API")
			}
			apiErr := AsAPIError(err)
			if apiErr == nil {
				t.Fatalf("AsAPIError returned nil for HTTP %d error: %v", tt.status, err)
			}
			if apiErr.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.wantStatus)
			}
			if !tt.predicate(err) {
				t.Errorf("predicate returned false for %d error", tt.status)
			}
		})
	}
}

// TestAsAPIError_NilErrorReturnsNil: a nil error classifies as no API error.
// Given a nil error, when AsAPIError inspects it, then it returns nil.
// Why it matters: callers run AsAPIError on every result without a prior nil
// check; a non-nil return here would classify successes as API failures.
func TestAsAPIError_NilErrorReturnsNil(t *testing.T) {
	// When/Then: nil in, nil out.
	if got := AsAPIError(nil); got != nil {
		t.Errorf("AsAPIError(nil) = %v, want nil", got)
	}
}

// TestAsAPIError_NonAPIErrorReturnsNil: plain errors are not misread as API errors.
// Given an ordinary errors.New value with no HTTP metadata in its chain, when
// AsAPIError and the status predicates inspect it, then AsAPIError returns
// nil and every predicate is false.
// Why it matters: if a local failure such as a cancelled context matched
// IsUnauthorized, the UI would tell the user to refresh a perfectly valid
// token.
func TestAsAPIError_NonAPIErrorReturnsNil(t *testing.T) {
	// Given: a plain error with no HTTP status anywhere in its chain.
	err := errors.New("not an API error")

	// When/Then: it classifies as no API error and no predicate matches.
	if got := AsAPIError(err); got != nil {
		t.Errorf("AsAPIError(plain error) = %v, want nil", got)
	}
	if IsUnauthorized(err) || IsRateLimited(err) || IsNotFound(err) {
		t.Error("predicates should be false for non-API errors")
	}
}

// TestAsAPIError_WrappedErrorChainResolves: status detection survives an extra fmt.Errorf wrap.
// Given a 401 API error wrapped once more with %w above the client's own
// wrapping, when IsUnauthorized inspects the outer error, then it still
// resolves to true through the chain.
// Why it matters: client methods always add their own %w context, so a
// predicate that only inspected the top of the chain would never fire in
// practice.
func TestAsAPIError_WrappedErrorChainResolves(t *testing.T) {
	// Given: a 401 failure produced through a real client call.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401"}`)
	})
	client := newTestClient(t, handler)
	_, err := client.ListProjects(context.Background(), ProjectListOptions{Page: 1, PerPage: 10})

	// When: the error gains one more %w layer of caller context.
	wrapped := fmt.Errorf("higher-level context: %w", err)

	// Then: the 401 classification still resolves through the extra layer.
	if !IsUnauthorized(wrapped) {
		t.Errorf("IsUnauthorized failed to resolve through extra wrap layer: %v", wrapped)
	}
}
