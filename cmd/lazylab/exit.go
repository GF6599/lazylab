package main

import (
	"context"
	"errors"
	"net"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// Exit codes for CLI subcommands. The TUI also routes its startup failures
// through these so scripts that wrap `lazylab` in a watchdog can distinguish
// transient retry-worthy failures (rate limiting, network blips) from
// permanent ones (auth, bad config).
//
// Stable contract — do not renumber. Documented in README.
const (
	exitOK             = 0
	exitGeneric        = 1 // catch-all: 404, validation, bad flags, unknown
	exitTransient      = 2 // 429 rate limited, 5xx; safe to retry with backoff
	exitUnauthorized   = 3 // 401 / 403; token is missing, invalid, or under-scoped
	exitNetworkFailure = 4 // DNS/TCP/TLS errors before a response arrived
	exitWatchedFailure = 5 // operation succeeded but the watched resource ended in a failure state (e.g. `pipeline watch` saw a failed pipeline)
)

// watchedFailureError is the sentinel returned by `pipeline watch` (and
// future watch-style commands) when the operation completed cleanly but
// the resource being watched ended in a non-success terminal state. The
// distinction matters for scripting: `lazylab pipeline watch` exiting 5
// means "the pipeline failed", while exit 4 would mean "I couldn't reach
// GitLab to find out". Wrapping scripts can branch on this.
type watchedFailureError struct {
	// Resource describes what was being watched, for the error message.
	Resource string
	// Status is the terminal status the resource reached.
	Status string
}

func (e *watchedFailureError) Error() string {
	return e.Resource + " ended with status: " + e.Status
}

// exitCodeFor maps an error from anywhere in the command pipeline (config
// load, client construction, subcommand RunE) to a process exit code. It
// walks the error chain so wrapped errors from internal/gitlab still
// surface their underlying HTTP status.
//
// Nil errors map to exitOK so callers can use this unconditionally:
//
//	os.Exit(exitCodeFor(rootCmd.Execute()))
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}

	// Context cancellation (Ctrl+C, parent shutdown) is not a failure;
	// treat it as a clean exit so signal handlers don't trip watchdogs.
	if errors.Is(err, context.Canceled) {
		return exitOK
	}

	var watched *watchedFailureError
	if errors.As(err, &watched) {
		return exitWatchedFailure
	}

	switch {
	case gitlab.IsUnauthorized(err), gitlab.IsForbidden(err):
		return exitUnauthorized
	case gitlab.IsRateLimited(err), gitlab.IsServerError(err):
		return exitTransient
	case gitlab.IsNotFound(err):
		return exitGeneric
	}

	// Transport-layer failures (no HTTP response ever arrived) — DNS,
	// TCP refused, TLS handshake, etc. Distinct from server-side errors
	// because retry strategy differs: network issues benefit from longer
	// backoffs and DNS re-resolution.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return exitNetworkFailure
	}

	return exitGeneric
}
