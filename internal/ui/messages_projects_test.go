package ui

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// TestFormatLoadErr verifies that the typed-error UI helper picks the right
// status line for each HTTP status the GitLab client can surface. Errors are
// fabricated directly via *gl.ErrorResponse rather than provoked through the
// SDK, because the helper only cares about what errors.As can extract from
// the chain.
func TestFormatLoadErr(t *testing.T) {
	tests := []struct {
		name   string
		action string
		err    error
		want   string
	}{
		{
			name:   "401 token rejected",
			action: "projects",
			err:    wrapHTTPErr(http.StatusUnauthorized),
			want:   "GitLab token rejected (401) — refresh GITLAB_TOKEN",
		},
		{
			name:   "429 rate limited",
			action: "projects",
			err:    wrapHTTPErr(http.StatusTooManyRequests),
			want:   "GitLab rate-limited (429) — retrying after backoff",
		},
		{
			name:   "403 forbidden",
			action: "projects",
			err:    wrapHTTPErr(http.StatusForbidden),
			want:   "GitLab denied access (403) — check token scopes",
		},
		{
			name:   "404 not found uses action noun",
			action: "pipelines",
			err:    wrapHTTPErr(http.StatusNotFound),
			want:   "GitLab pipelines not found (404)",
		},
		{
			name:   "502 server error",
			action: "projects",
			err:    wrapHTTPErr(http.StatusBadGateway),
			want:   "GitLab server error — will retry",
		},
		{
			name:   "non-API error falls back to generic with action",
			action: "projects",
			err:    errors.New("network unreachable"),
			want:   "Failed to load projects",
		},
		{
			name:   "generic fallback with custom action",
			action: "merge requests",
			err:    errors.New("network unreachable"),
			want:   "Failed to load merge requests",
		},
		{
			name:   "wrapped 401 still detected through extra layer",
			action: "projects",
			err:    fmt.Errorf("higher-level: %w", wrapHTTPErr(http.StatusUnauthorized)),
			want:   "GitLab token rejected (401) — refresh GITLAB_TOKEN",
		},
		{
			name:   "nil error returns empty string",
			action: "projects",
			err:    nil,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLoadErr(tt.action, tt.err); got != tt.want {
				t.Errorf("formatLoadErr(%q, err) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

// wrapHTTPErr fabricates an error chain that mirrors what the gitlab SDK
// produces for a given HTTP status — a *gl.ErrorResponse with the requisite
// *http.Response embedded — wrapped once with fmt.Errorf the way client.go
// would.
func wrapHTTPErr(status int) error {
	sdkErr := &gl.ErrorResponse{
		Response: &http.Response{
			StatusCode: status,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    must(url.Parse("https://gitlab.com/api/v4/projects")),
			},
		},
		Message: fmt.Sprintf("%d test", status),
	}
	return fmt.Errorf("list projects: %w", sdkErr)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
