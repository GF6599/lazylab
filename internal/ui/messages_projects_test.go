package ui

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// TestFormatLoadErr: each class of load failure maps to its own exact status-bar message.
// Given error chains for 401, 403, 404, 429, and 502 plus a plain error and nil, when formatLoadErr
// renders each, then every case yields its precise phrase, from token guidance for 401 through the
// "Failed to load <action>" fallback down to an empty string for nil.
// Why it matters: this string is the only explanation a user gets for a failed load, and a wrong
// mapping would tell someone with an expired token that GitLab is down instead of pointing them at
// GITLAB_TOKEN.
//
// Errors are fabricated directly via *gl.ErrorResponse rather than provoked through the SDK, because
// the helper only cares about what errors.As can extract from the chain.
func TestFormatLoadErr(t *testing.T) {
	// Given: error chains for each failure class with their expected status lines
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
			// When/Then: rendering the error yields the exact expected phrase
			if got := formatLoadErr(tt.action, tt.err); got != tt.want {
				t.Errorf("formatLoadErr(%q, err) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

// wrapHTTPErr fabricates an error chain that mirrors what the gitlab SDK
// produces for a given HTTP status: a *gl.ErrorResponse with the requisite
// *http.Response embedded, wrapped once with fmt.Errorf the way client.go
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
