// error_format.go centralizes how API load failures are rendered to the user.
// The TUI calls into many GitLab endpoints (projects, pipelines, stages, jobs,
// merge requests, files, directories) and each surface used to render its own
// flavor of "Failed to load X". That meant a 401 on the pipeline view looked
// like a generic failure even though the fix (refresh GITLAB_TOKEN) is the
// same as on the project list.
//
// formatLoadErr funnels every load-error rendering through one set of phrases
// keyed off the typed predicates in internal/gitlab. The CLI exit-code path in
// cmd/lazylab/exit.go consumes the same predicates, so the user gets parallel
// language ("401 - refresh GITLAB_TOKEN") whether they hit the failure in the
// TUI status bar or saw the process exit with code 3.

package ui

import (
	"github.com/GF6599/lazylab/internal/gitlab"
)

// formatLoadErr returns a user-friendly status string for a failed load,
// branching on the HTTP status carried by the underlying SDK error. The
// action argument is a short noun describing what was being loaded (e.g.
// "projects", "pipelines", "stages", "jobs", "file", "directory",
// "merge requests"). It appears in the generic fallback and in the 404
// message where "<resource> not found" reads more naturally than a status
// code alone.
//
// 401 prompts the user to refresh their token; 429 explains the brief
// wait; 403 hints at token scope; 5xx is treated as transient. Non-HTTP
// errors (network, context cancel) fall through to the generic message
// so the user still gets a hint about what failed.
func formatLoadErr(action string, err error) string {
	if err == nil {
		return ""
	}
	switch {
	case gitlab.IsUnauthorized(err):
		return "GitLab token rejected (401) — refresh GITLAB_TOKEN"
	case gitlab.IsForbidden(err):
		return "GitLab denied access (403) — check token scopes"
	case gitlab.IsNotFound(err):
		if action == "" {
			return "GitLab resource not found (404)"
		}
		return "GitLab " + action + " not found (404)"
	case gitlab.IsRateLimited(err):
		return "GitLab rate-limited (429) — retrying after backoff"
	case gitlab.IsServerError(err):
		return "GitLab server error — will retry"
	default:
		if action == "" {
			return "Failed to load"
		}
		return "Failed to load " + action
	}
}
