// refs.go turns the many ways a CLI user might name a pipeline into a
// concrete (project ID, pipeline ID) pair. The forms accepted are
// documented on ResolvePipelineRef; the resolution is layered so the
// most-specific form (a URL containing both project and pipeline ID)
// short-circuits before the more inferred forms (HEAD, @ref) ever query.
//
// This package does not know about git; the caller passes any inferred
// project path / SHA / branch in via ResolveHints. Keeping the resolver
// git-agnostic means the same code path is reused if a future caller
// derives hints from a different source (e.g. a CI environment variable).

package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PipelineSpec is the fully-resolved identity of a pipeline returned by
// ResolvePipelineRef. ProjectPath is informational — present when we
// looked the project up by path, empty when the user passed a numeric ID
// and we skipped the lookup to save a round trip.
type PipelineSpec struct {
	ProjectID   int
	PipelineID  int
	ProjectPath string
}

// JobSpec is the analog of PipelineSpec for jobs. Jobs are globally
// unique by ID but the API endpoint is project-scoped, so the resolver
// always produces both project and job IDs.
type JobSpec struct {
	ProjectID   int
	JobID       int
	ProjectPath string
}

// ResolveHints carries the contextual data the resolver uses to fill in
// gaps in the user's input. All fields are optional: when a hint is
// missing and the user's input requires it, ResolvePipelineRef returns a
// targeted error (e.g. "HEAD requires a git context") rather than guessing.
type ResolveHints struct {
	// ProjectFlag is the value of --project, if the user provided one.
	// Accepts numeric IDs or namespace paths.
	ProjectFlag string
	// GitProjectPath is the namespace/project derived from `git remote`
	// (typically `origin`). Used as the project default when ProjectFlag
	// is empty.
	GitProjectPath string
	// GitSHA is the commit SHA at git HEAD. Used to resolve the "HEAD" ref.
	GitSHA string
	// GitBranch is the current branch name. Fallback for HEAD resolution
	// when GitSHA is empty (rare in practice).
	GitBranch string
}

// ErrNoProjectContext signals that the user did not supply --project and
// no git context was detected. CLI commands typically wrap this with a
// usage hint pointing at both inputs the user could fix.
var ErrNoProjectContext = errors.New("no project: pass --project or run inside a GitLab git clone")

// ResolvePipelineRef turns a user-supplied pipeline reference into a
// PipelineSpec. The accepted forms, in resolution priority:
//
//   - URL ("https://gitlab.com/foo/bar/-/pipelines/12345"): both project
//     and pipeline ID come from the URL; --project flag is ignored.
//   - Numeric ID ("12345"): used as the pipeline ID; project comes from
//     ProjectFlag or GitProjectPath.
//   - "@<ref>" ("@main", "@feat/auth"): latest pipeline on that ref.
//   - "latest": most recent pipeline on the project, any ref.
//   - "HEAD" or empty string: pipeline matching the current git HEAD's
//     SHA (preferred) or branch (fallback).
//
// Empty arg combined with no git context is an error rather than
// silently expanding to "latest" — guessing wrong here would have users
// watching the wrong pipeline.
func ResolvePipelineRef(ctx context.Context, c Service, arg string, hints ResolveHints) (PipelineSpec, error) {
	arg = strings.TrimSpace(arg)

	// URL path: fully self-contained input. Resolve the project from
	// the URL's path, look up its numeric ID, attach the parsed
	// pipeline ID. The --project flag is ignored on purpose — pasting
	// a Slack link should "just work" without coordinating flags.
	if isGitLabResourceURL(arg) {
		projPath, pipeID, err := ParsePipelineURL(arg)
		if err != nil {
			return PipelineSpec{}, err
		}
		proj, err := c.GetProject(ctx, projPath)
		if err != nil {
			return PipelineSpec{}, fmt.Errorf("resolve project from url: %w", err)
		}
		return PipelineSpec{ProjectID: proj.ID, PipelineID: pipeID, ProjectPath: proj.PathWithNamespace}, nil
	}

	// Fast path: reject obviously-bad arguments before any I/O. This
	// keeps the diagnostic for typos like "deploy-pipeline" focused on
	// "unrecognized reference" rather than dragging the user through a
	// project-lookup error chain that ultimately has nothing to do with
	// what they actually got wrong.
	if !isRecognizedRefForm(arg) {
		return PipelineSpec{}, fmt.Errorf("unrecognized pipeline reference %q (try: numeric id, @branch, latest, HEAD, or a pipeline URL)", arg)
	}

	// All non-URL forms require a project context first.
	projectID, projectPath, err := resolveProject(ctx, c, hints)
	if err != nil {
		return PipelineSpec{}, err
	}

	// Numeric pipeline ID — most direct form.
	if id, perr := strconv.Atoi(arg); perr == nil {
		return PipelineSpec{ProjectID: projectID, PipelineID: id, ProjectPath: projectPath}, nil
	}

	// @<ref> form — latest pipeline on the named branch/tag.
	if ref, ok := strings.CutPrefix(arg, "@"); ok {
		if ref == "" {
			return PipelineSpec{}, errors.New("empty ref after '@'")
		}
		pipe, err := c.LatestPipeline(ctx, projectID, ref)
		if err != nil {
			return PipelineSpec{}, fmt.Errorf("latest pipeline on @%s: %w", ref, err)
		}
		return PipelineSpec{ProjectID: projectID, PipelineID: pipe.ID, ProjectPath: projectPath}, nil
	}

	// "latest" — most recent pipeline, any ref.
	if strings.EqualFold(arg, "latest") {
		pipe, err := c.LatestPipeline(ctx, projectID, "")
		if err != nil {
			return PipelineSpec{}, fmt.Errorf("latest pipeline: %w", err)
		}
		return PipelineSpec{ProjectID: projectID, PipelineID: pipe.ID, ProjectPath: projectPath}, nil
	}

	// HEAD or empty — resolve via git context.
	if arg == "" || strings.EqualFold(arg, "HEAD") {
		if hints.GitSHA != "" {
			pipe, err := c.LatestPipelineForSHA(ctx, projectID, hints.GitSHA)
			if err != nil {
				return PipelineSpec{}, fmt.Errorf("pipeline for HEAD (%s): %w", shortSHA(hints.GitSHA), err)
			}
			return PipelineSpec{ProjectID: projectID, PipelineID: pipe.ID, ProjectPath: projectPath}, nil
		}
		if hints.GitBranch != "" {
			pipe, err := c.LatestPipeline(ctx, projectID, hints.GitBranch)
			if err != nil {
				return PipelineSpec{}, fmt.Errorf("pipeline for branch %s: %w", hints.GitBranch, err)
			}
			return PipelineSpec{ProjectID: projectID, PipelineID: pipe.ID, ProjectPath: projectPath}, nil
		}
		return PipelineSpec{}, errors.New("HEAD requires a git context (run inside a clone, or pass an explicit pipeline ref)")
	}

	return PipelineSpec{}, fmt.Errorf("unrecognized pipeline reference %q (try: numeric id, @branch, latest, HEAD, or a pipeline URL)", arg)
}

// resolveProject decides which project the resolver should target.
// Precedence: --project flag > git remote project path. Numeric flag
// values skip the API lookup since they're already the ID we need;
// path-form values require a Projects.Get call.
func resolveProject(ctx context.Context, c Service, hints ResolveHints) (int, string, error) {
	proj := strings.TrimSpace(hints.ProjectFlag)
	if proj == "" {
		proj = strings.TrimSpace(hints.GitProjectPath)
	}
	if proj == "" {
		return 0, "", ErrNoProjectContext
	}

	// Pure numeric: trust it; skip the lookup. The downstream API call
	// will surface "404 not found" if the ID is wrong, which is a
	// faster failure mode than two round trips.
	if id, err := strconv.Atoi(proj); err == nil {
		return id, "", nil
	}

	node, err := c.GetProject(ctx, proj)
	if err != nil {
		return 0, "", fmt.Errorf("resolve project %q: %w", proj, err)
	}
	return node.ID, node.PathWithNamespace, nil
}

// ParsePipelineURL extracts the project path and pipeline ID from a
// GitLab pipeline URL. The URL format is stable:
// `<host>/<namespace>/<project>/-/pipelines/<id>[/anything]`.
//
// The "/-/" delimiter cleanly separates the namespace from the resource
// path — GitLab uses it precisely so namespaces can contain slashes
// without ambiguity. The pipeline ID may be followed by `/builds`,
// `?ref_type=...`, or `#anchor`; this parser truncates at the first such
// suffix to remain robust to URL bookmarklets and Slack unfurls.
func ParsePipelineURL(raw string) (projectPath string, pipelineID int, err error) {
	return parseGitLabResourceURL(raw, "/-/pipelines/", "pipeline")
}

// ParseJobURL extracts the project path and job ID from a GitLab job
// URL of the form `<host>/<namespace>/<project>/-/jobs/<id>[/...]`.
// Same parsing strategy as ParsePipelineURL — the only thing that varies
// between GitLab resource URLs is the "/-/<resource>/" delimiter.
func ParseJobURL(raw string) (projectPath string, jobID int, err error) {
	return parseGitLabResourceURL(raw, "/-/jobs/", "job")
}

// parseGitLabResourceURL is the shared spine for ParsePipelineURL and
// ParseJobURL. Every GitLab resource URL follows the same pattern:
// `https://host/<namespace>/<project>/-/<resource>/<id>[/?#...]`. The
// delimiter parameter selects which resource type we're extracting; the
// label is used in error messages so users see "not a job url" rather
// than the generic "not a pipeline url" when they paste the wrong URL.
func parseGitLabResourceURL(raw, delimiter, label string) (projectPath string, resourceID int, err error) {
	u, perr := url.Parse(strings.TrimSpace(raw))
	if perr != nil {
		return "", 0, fmt.Errorf("parse %s url %q: %w", label, raw, perr)
	}
	// Block non-http(s) schemes outright. The CLI accepts pasted URLs
	// from chat clients and browsers; allowing ftp://, ssh://, file://
	// would let a hostile paste route an API call somewhere unexpected
	// (the SDK does its own scheme rewriting but the project-path lookup
	// still proceeds, surfacing the malicious host in error messages).
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", 0, fmt.Errorf("unsupported url scheme %q: only http(s) allowed", u.Scheme)
	}
	if u.Host == "" {
		return "", 0, fmt.Errorf("%s url has no host: %q", label, raw)
	}
	path := strings.TrimPrefix(u.Path, "/")
	before, after, ok := strings.Cut(path, delimiter)
	if !ok || before == "" || after == "" {
		return "", 0, fmt.Errorf("not a %s url: %q", label, raw)
	}
	if i := strings.IndexAny(after, "/?#"); i >= 0 {
		after = after[:i]
	}
	id, perr := strconv.Atoi(after)
	if perr != nil {
		return "", 0, fmt.Errorf("invalid %s id %q in url %q", label, after, raw)
	}
	// Reject zero/negative IDs. GitLab IDs are always positive integers;
	// an "id=0" URL is either a typo or a hand-crafted probe, and either
	// way the downstream API call would 404 with a less-clear diagnostic.
	if id <= 0 {
		return "", 0, fmt.Errorf("invalid %s id %q: must be positive", label, after)
	}
	return before, id, nil
}

// ResolveJobRef turns a user-supplied job reference into a JobSpec.
// Accepts numeric IDs and job URLs in v1; future revisions may add
// pipeline-scoped lookups like `--stage test --name unit-tests`. Unlike
// pipeline refs there is no HEAD form — jobs are far more granular than
// the commit-level abstraction.
func ResolveJobRef(ctx context.Context, c Service, arg string, hints ResolveHints) (JobSpec, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return JobSpec{}, errors.New("job id or URL is required")
	}

	// URL form short-circuits project resolution by carrying the
	// project path inside itself.
	if isGitLabResourceURL(arg) {
		projPath, jobID, err := ParseJobURL(arg)
		if err != nil {
			return JobSpec{}, err
		}
		proj, err := c.GetProject(ctx, projPath)
		if err != nil {
			return JobSpec{}, fmt.Errorf("resolve project from url: %w", err)
		}
		return JobSpec{ProjectID: proj.ID, JobID: jobID, ProjectPath: proj.PathWithNamespace}, nil
	}

	id, err := strconv.Atoi(arg)
	if err != nil {
		return JobSpec{}, fmt.Errorf("unrecognized job reference %q (try: numeric id or a job URL)", arg)
	}
	projectID, projectPath, err := resolveProject(ctx, c, hints)
	if err != nil {
		return JobSpec{}, err
	}
	return JobSpec{ProjectID: projectID, JobID: id, ProjectPath: projectPath}, nil
}

// isGitLabResourceURL is the cheap pre-check that short-circuits the URL
// path of ResolvePipelineRef and ResolveJobRef. Either resource flavour
// (pipeline or job) starts the same way — http(s) scheme — so a single
// recognizer covers both. A full parseGitLabResourceURL still runs after,
// validating scheme/host/id; we only need to *recognize* a URL here.
func isGitLabResourceURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// isRecognizedRefForm validates that arg superficially matches one of the
// supported pipeline-reference shapes (numeric, @ref, latest, HEAD, or
// empty/HEAD). URLs are handled before this is called. The check is
// intentionally lenient — "latest" and "head" are case-insensitive, ref
// names after @ aren't pattern-validated since git itself accepts a wide
// surface.
func isRecognizedRefForm(arg string) bool {
	if arg == "" {
		return true
	}
	if strings.EqualFold(arg, "HEAD") || strings.EqualFold(arg, "latest") {
		return true
	}
	if strings.HasPrefix(arg, "@") {
		return true
	}
	if _, err := strconv.Atoi(arg); err == nil {
		return true
	}
	return false
}

// shortSHA truncates a SHA to its abbreviated form for human-readable
// error messages. Returns the input unchanged when shorter than 8 chars.
func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}
