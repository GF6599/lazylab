package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestAsAPIError_StatusCodes(t *testing.T) {
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
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			client := newTestClient(t, handler)

			_, err := client.ListProjects(context.Background(), ProjectListOptions{Page: 1, PerPage: 10})
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

func TestAsAPIError_NilErrorReturnsNil(t *testing.T) {
	if got := AsAPIError(nil); got != nil {
		t.Errorf("AsAPIError(nil) = %v, want nil", got)
	}
}

func TestAsAPIError_NonAPIErrorReturnsNil(t *testing.T) {
	err := errors.New("not an API error")
	if got := AsAPIError(err); got != nil {
		t.Errorf("AsAPIError(plain error) = %v, want nil", got)
	}
	if IsUnauthorized(err) || IsRateLimited(err) || IsNotFound(err) {
		t.Error("predicates should be false for non-API errors")
	}
}

func TestAsAPIError_WrappedErrorChainResolves(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401"}`)
	})
	client := newTestClient(t, handler)
	_, err := client.ListProjects(context.Background(), ProjectListOptions{Page: 1, PerPage: 10})
	// Wrap once more to confirm errors.As still finds the SDK error.
	wrapped := fmt.Errorf("higher-level context: %w", err)
	if !IsUnauthorized(wrapped) {
		t.Errorf("IsUnauthorized failed to resolve through extra wrap layer: %v", wrapped)
	}
}
