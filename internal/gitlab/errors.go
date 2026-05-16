// errors.go exposes typed HTTP status information from underlying SDK errors so
// the UI can react differentially to 401 (auth expired), 429 (rate limited),
// and other common GitLab API failures without depending on the SDK's concrete
// error type. All public client methods continue to return errors wrapped with
// fmt.Errorf("%w"); the helpers here walk the chain via errors.As and surface
// status info when it's there.

package gitlab

import (
	"errors"
	"net/http"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// APIError surfaces HTTP status metadata from a wrapped GitLab SDK error.
// It implements error so it can sit on the chain, and Unwrap so existing
// errors.Is/As against sentinel errors keep working.
type APIError struct {
	StatusCode int    // HTTP status code from the API response.
	Message    string // Server-provided error message, may be empty.
	Body       []byte // Raw response body for diagnostics.
	Err        error  // Underlying SDK error retained for Unwrap.
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

func (e *APIError) Unwrap() error { return e.Err }

// AsAPIError walks the error chain and returns an APIError if any link
// originated from a GitLab API HTTP response with status metadata. Returns nil
// if err is nil or the chain has no HTTP-typed error.
//
// The SDK special-cases 404 responses with a sentinel (gl.ErrNotFound) rather
// than the usual *gl.ErrorResponse, so AsAPIError detects that path too and
// synthesizes a 404 APIError to keep callers' status-code logic uniform.
func AsAPIError(err error) *APIError {
	if err == nil {
		return nil
	}
	var sdkErr *gl.ErrorResponse
	if errors.As(err, &sdkErr) && sdkErr.Response != nil {
		return &APIError{
			StatusCode: sdkErr.Response.StatusCode,
			Message:    sdkErr.Message,
			Body:       sdkErr.Body,
			Err:        sdkErr,
		}
	}
	if errors.Is(err, gl.ErrNotFound) {
		return &APIError{StatusCode: http.StatusNotFound, Err: err}
	}
	return nil
}

// IsUnauthorized reports whether err originated from a 401 response. Use to
// prompt the user to refresh their GitLab token.
func IsUnauthorized(err error) bool {
	e := AsAPIError(err)
	return e != nil && e.StatusCode == http.StatusUnauthorized
}

// IsForbidden reports whether err originated from a 403 response. Distinct
// from 401: the token is valid but lacks the scope or permission required.
func IsForbidden(err error) bool {
	e := AsAPIError(err)
	return e != nil && e.StatusCode == http.StatusForbidden
}

// IsNotFound reports whether err originated from a 404 response. Sometimes
// indicates the user doesn't have read access (GitLab returns 404 instead of
// 403 to avoid leaking project existence).
func IsNotFound(err error) bool {
	e := AsAPIError(err)
	return e != nil && e.StatusCode == http.StatusNotFound
}

// IsRateLimited reports whether err originated from a 429 response. Use to
// display a "rate limited, retrying soon" UI message and avoid hammering
// during the back-off window.
func IsRateLimited(err error) bool {
	e := AsAPIError(err)
	return e != nil && e.StatusCode == http.StatusTooManyRequests
}

// IsServerError reports whether err originated from a 5xx response. Useful for
// distinguishing transient infrastructure failures from client-side mistakes.
func IsServerError(err error) bool {
	e := AsAPIError(err)
	return e != nil && e.StatusCode >= 500 && e.StatusCode < 600
}
