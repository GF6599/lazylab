// Package gitlab provides a TUI-oriented facade over gitlab.com/gitlab-org/api/client-go.
//
// Rather than exposing the upstream library's deeply nested types and pointer-heavy
// options structs, this package defines flat domain types (ProjectNode, PipelineSummary,
// TreeNode, etc.) that the Bubble Tea UI layer can consume without importing the GitLab
// SDK directly. This isolation means the UI tests can run against a mock [Service]
// without standing up an HTTP server.
//
// All methods accept a context.Context so the TUI can enforce per-request timeouts
// and cancel in-flight calls when the user navigates away.
package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// Client is a thin wrapper around the upstream GitLab SDK. It translates between
// client-go's pointer-heavy response types and the flat domain types that the TUI
// consumes. The host field is retained for cache-key derivation elsewhere.
type Client struct {
	api  *gl.Client
	host string
}

// NewClient wires the GitLab client with the provided token and host.
// The token must have 'api' scope. If host is empty, defaults to https://gitlab.com.
// Returns an error if the token is empty or the host URL is invalid.
//
// Only the url.Parse failure is %w-wrapped (so callers can inspect the
// underlying parse error); the empty-token, bad-scheme, and missing-host
// cases are plain fmt.Errorf messages because they are validation verdicts
// with no inner error worth unwrapping.
func NewClient(token, host string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("gitlab token must not be empty")
	}
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		trimmedHost = "https://gitlab.com"
	}

	// Validate URL format
	parsedURL, err := url.Parse(trimmedHost)
	if err != nil {
		return nil, fmt.Errorf("invalid host URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("host URL must use http or https scheme, got: %s", parsedURL.Scheme)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("host URL must include a hostname")
	}

	baseURL := ensureAPIBaseURL(trimmedHost)
	api, err := gl.NewClient(token, gl.WithBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}
	return &Client{api: api, host: trimmedHost}, nil
}

// Service is the contract between the UI layer and the GitLab API. The UI
// imports only this interface — never *Client — so that tests can substitute a
// mock without network access. Every method must be safe for concurrent use;
// the TUI may fire several calls in parallel (e.g., pipeline status + stage
// list) from a single Bubble Tea Cmd.
//
// Service is a compound of per-domain sub-interfaces (ProjectService,
// PipelineService, MRService, RepoService). Call sites that need only a
// slice of the surface can depend on the relevant sub-interface directly,
// which keeps test doubles small. *Client satisfies every sub-interface,
// so existing consumers that take Service continue to work unchanged.
type Service interface {
	ProjectService
	PipelineService
	MRService
	RepoService
}

// Verify at compile time that *Client satisfies Service.
var _ Service = (*Client)(nil)

// ProjectNode is a deliberately minimal view of a GitLab project. It carries
// only the fields the TUI needs for display and navigation, keeping JSON
// cache files small and avoiding accidental token leakage through serialisation.
type ProjectNode struct {
	ID                int
	Name              string
	PathWithNamespace string
	Description       string
	WebURL            string
	SSHURLToRepo      string
	LastActivityAt    time.Time
	StarCount         int
	Visibility        string
	DefaultBranch     string
}

// ProjectListOptions describe pagination parameters for project listings.
type ProjectListOptions struct {
	Page    int
	PerPage int
}

// ProjectPage bundles a slice of projects with cursor-style pagination metadata.
// PrevPage/NextPage are zero when there is no previous/next page, matching the
// convention used by the GitLab API response headers.
type ProjectPage struct {
	Projects   []ProjectNode
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
	TotalItems int
}

// PipelineListOptions describe pagination + filter parameters for pipeline
// listings. Ref and Status are empty by default; when set they map to the
// equivalent GitLab API query parameters. Status accepts the canonical
// GitLab build states ("running", "success", "failed", "canceled",
// "manual", "skipped", "pending", "created"); invalid values surface as
// an empty result rather than an error, matching server-side behavior.
type PipelineListOptions struct {
	Page    int
	PerPage int
	Ref     string
	Status  string
}

// PipelinePage contains a slice of pipelines along with pagination metadata.
// Zero-value PrevPage/NextPage indicate no previous/next page, mirroring the
// convention used by GitLab API response headers (same as ProjectPage).
type PipelinePage struct {
	Pipelines  []PipelineSummary
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
	TotalItems int
}

// TreeNode represents a repository tree entry (file or directory).
// Type is one of "tree" (directory), "blob" (file), or "commit" (submodule link).
// Mode is the git file mode string (e.g., "040000" for dirs, "100644" for files);
// retained for potential future use in permission display.
type TreeNode struct {
	Path string
	Name string
	Type string
	Mode string
}

// IsDir reports whether the node represents a directory.
func (n TreeNode) IsDir() bool {
	return n.Type == "tree"
}

// PipelineSummary is the primary pipeline representation used throughout the TUI.
// Stages may be nil when the summary comes from a list endpoint (populated lazily
// by a separate PipelineStages call), or pre-filled when fetched via LatestPipeline.
type PipelineSummary struct {
	ID        int
	Status    string
	Ref       string
	SHA       string
	WebURL    string
	UpdatedAt time.Time
	// StartedAt is the moment the run began, and it is zero unless the summary came from
	// GetPipeline. The pipelines list is built from a lighter type that does not carry it.
	StartedAt time.Time
	Stages    []PipelineStage
	Source    string
	Duration  float64
	Coverage  float64
	User      string
}

// PipelineStage captures a GitLab CI stage with a single aggregated status
// derived from all jobs in that stage. See mergeStageStatus for the priority
// rules that determine which job status "wins".
type PipelineStage struct {
	Name   string
	Status string
}

// PipelineJob represents a single job in a pipeline. AllowFailure is
// significant for status display: a failed job with AllowFailure=true should
// be shown as a warning rather than a hard failure. FailureReason is only
// populated when the job has actually failed.
type PipelineJob struct {
	ID                int
	Name              string
	Stage             string
	Status            string
	WebURL            string
	Duration          float64
	StartedAt         time.Time
	FinishedAt        time.Time
	FailureReason     string
	AllowFailure      bool
	RunnerDescription string
	ArtifactsCount    int
	Artifacts         []JobArtifact
	ArtifactsExpireAt time.Time
}

// MRDiffRefs captures the three SHAs that anchor a merge request diff.
// These are required when creating line-level (positioned) discussion comments.
type MRDiffRefs struct {
	BaseSHA  string
	HeadSHA  string
	StartSHA string
}

// MRCommentPosition describes the file and line to anchor a diff comment on.
// Used with CreateMergeRequestDiscussion to create line-level comments.
type MRCommentPosition struct {
	OldPath  string
	NewPath  string
	OldLine  int
	NewLine  int
	DiffRefs MRDiffRefs
}

// MergeRequestSummary represents a merge request in a project. IID is the
// project-scoped merge request number (used in URLs like !42), while the
// global ID is intentionally omitted since all MR API calls use IID.
type MergeRequestSummary struct {
	IID          int
	Title        string
	State        string
	Author       string
	SourceBranch string
	TargetBranch string
	WebURL       string
	UpdatedAt    time.Time
}

// MRListOptions describe pagination and filter parameters for merge request listings.
// State accepts "opened", "closed", "merged", or "all"; an empty string omits the
// filter, letting GitLab's server-side default ("all") apply.
type MRListOptions struct {
	State   string
	Page    int
	PerPage int
}

// MRPage contains a slice of merge requests along with pagination metadata.
type MRPage struct {
	MergeRequests []MergeRequestSummary
	Page          int
	PrevPage      int
	NextPage      int
	TotalPages    int
	TotalItems    int
}

// CreateMROptions holds the parameters for creating a new merge request.
type CreateMROptions struct {
	Title        string
	SourceBranch string
	TargetBranch string
	Description  string
}

// PipelineBridge represents a bridge (child pipeline trigger) job. Bridges
// are GitLab's mechanism for multi-project and parent-child pipelines.
// DownstreamPipeline is nil when the trigger hasn't fired yet or when the
// current user lacks access to the downstream project.
type PipelineBridge struct {
	ID                 int
	Name               string
	Stage              string
	Status             string
	Ref                string
	AllowFailure       bool
	Duration           float64
	DownstreamPipeline *PipelineBridgeDownstream
}

// PipelineBridgeDownstream is the downstream pipeline triggered by a bridge.
// ProjectID is carried because the downstream pipeline may belong to a different
// project than the parent — the TUI needs it to fetch child jobs via the correct
// project-scoped API endpoint.
type PipelineBridgeDownstream struct {
	ID        int
	ProjectID int
	Status    string
	WebURL    string
}

// TestReport contains a pipeline's test report summary. A nil *TestReport
// means the pipeline has no test artifact — callers must nil-check before access.
type TestReport struct {
	TotalTime    float64
	TotalCount   int
	SuccessCount int
	FailedCount  int
	SkippedCount int
	ErrorCount   int
	Suites       []TestSuite
}

// TestSuite contains results for a test suite.
type TestSuite struct {
	Name         string
	TotalTime    float64
	TotalCount   int
	SuccessCount int
	FailedCount  int
	SkippedCount int
	ErrorCount   int
	Cases        []TestCase
}

// TestCase represents a single test case result.
type TestCase struct {
	Status        string
	Name          string
	Classname     string
	File          string
	ExecutionTime float64
	SystemOutput  string
	StackTrace    string
}

// JobArtifact represents a single artifact file associated with a job.
type JobArtifact struct {
	FileType   string
	Filename   string
	Size       int
	FileFormat string
}

// CommitSummary is a lightweight view of a Git commit for display in the
// detail pane. Only the fields needed for the "recent commits" section are
// included — full diffs and file lists are intentionally omitted to keep
// the API response small and the UI snappy.
type CommitSummary struct {
	ShortID   string
	Title     string
	Author    string
	CreatedAt time.Time
}

// MRDiscussion represents a threaded discussion on a merge request.
// Notes are ordered chronologically; the first note is the discussion opener.
type MRDiscussion struct {
	ID    string
	Notes []MRNote
}

// MRNote is a single note (comment) within a merge request discussion.
// System notes are auto-generated by GitLab (e.g., "assigned to @user") and
// callers typically filter them out for display. Resolvable/Resolved only
// apply to diff-line comments — general comments have Resolvable=false.
//
// Position fields (FilePath, Line, OldLine, NewLine) are populated only for
// DiffNotes anchored to a specific line in the MR diff. General comments
// leave all four at their zero values. OldLine and NewLine are stored
// separately because GitLab's Position model treats additions (NewLine only),
// deletions (OldLine only), and context lines (both) differently — the UI
// needs both to locate the correct line when extracting diff context snippets.
type MRNote struct {
	ID         int
	Author     string
	Body       string
	System     bool
	Resolvable bool
	Resolved   bool
	CreatedAt  time.Time
	FilePath   string
	Line       int // display line: NewLine if available, else OldLine
	OldLine    int // Position.OldLine; 0 for additions (new-only lines)
	NewLine    int // Position.NewLine; 0 for deletions (old-only lines)
}

// MRDiffFile represents a single changed file in a merge request diff.
// OldPath and NewPath differ only for renames; for additions/deletions/edits
// they are the same. The Diff field contains the raw unified diff text.
type MRDiffFile struct {
	OldPath     string
	NewPath     string
	Diff        string
	NewFile     bool
	RenamedFile bool
	DeletedFile bool
}

// ErrNoPipelines is returned when a project (or ref) has no pipeline runs.
// Callers should treat this as a normal "empty" state, not a fatal error —
// many projects simply haven't configured CI yet.
var ErrNoPipelines = errors.New("no pipelines found")

// ErrNoJobs is returned when a pipeline exists but has no associated jobs.
// This can happen briefly after a pipeline is created but before GitLab has
// scheduled its jobs.
var ErrNoJobs = errors.New("no jobs found")

// TreeListOptions configures repository tree listing.
type TreeListOptions struct {
	Path string
	Ref  string
}

// ListProjects returns projects the authenticated user is a member of, ordered
// by most recent activity. Only "simple" project data is requested to minimise
// response size — fields like statistics and custom attributes are omitted.
func (c *Client) ListProjects(ctx context.Context, opts ProjectListOptions) (ProjectPage, error) {
	if opts.PerPage <= 0 {
		opts.PerPage = 30
	}
	listOpts := &gl.ListProjectsOptions{
		Membership: gl.Ptr(true),
		OrderBy:    gl.Ptr("last_activity_at"),
		Sort:       gl.Ptr("desc"),
		Simple:     gl.Ptr(true),
		ListOptions: gl.ListOptions{
			Page:    int64(opts.Page),
			PerPage: int64(opts.PerPage),
		},
	}
	projects, resp, err := c.api.Projects.ListProjects(listOpts, gl.WithContext(ctx))
	if err != nil {
		return ProjectPage{}, fmt.Errorf("list projects: %w", err)
	}
	nodes := make([]ProjectNode, len(projects))
	for i, p := range projects {
		nodes[i] = ProjectNode{
			ID:                int(p.ID),
			Name:              p.Name,
			PathWithNamespace: p.PathWithNamespace,
			Description:       p.Description,
			WebURL:            p.WebURL,
			SSHURLToRepo:      p.SSHURLToRepo,
			StarCount:         int(p.StarCount),
			Visibility:        string(p.Visibility),
			DefaultBranch:     p.DefaultBranch,
		}
		if p.LastActivityAt != nil {
			nodes[i].LastActivityAt = *p.LastActivityAt
		}
	}
	meta := extractPageMeta(resp, opts.Page)
	return ProjectPage{
		Projects:   nodes,
		Page:       meta.Page,
		PrevPage:   meta.PrevPage,
		NextPage:   meta.NextPage,
		TotalPages: meta.TotalPages,
		TotalItems: meta.TotalItems,
	}, nil
}

// ensureAPIBaseURL appends /api/v4 if not already present.
func ensureAPIBaseURL(host string) string {
	host = strings.TrimSuffix(host, "/")
	if strings.HasSuffix(host, "/api/v4") {
		return host
	}
	return host + "/api/v4"
}

// pageMeta is the common pagination cursor returned by every List* endpoint.
// It exists so the response → page-info translation isn't duplicated in
// each ListXxx method.
type pageMeta struct {
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
	// TotalItems is zero when GitLab withholds the count, which it does once a
	// collection passes ten thousand items. Zero means unknown, never empty.
	TotalItems int
}

// extractPageMeta reads pagination headers from resp into a pageMeta. When
// resp is nil (e.g., the SDK returned an early error before issuing the HTTP
// request) the caller-supplied fallbackPage is used so the result still
// reflects which page was being fetched.
func extractPageMeta(resp *gl.Response, fallbackPage int) pageMeta {
	m := pageMeta{Page: fallbackPage}
	if resp != nil {
		m.Page = int(resp.CurrentPage)
		m.PrevPage = int(resp.PreviousPage)
		m.NextPage = int(resp.NextPage)
		m.TotalPages = int(resp.TotalPages)
		m.TotalItems = int(resp.TotalItems)
	}
	return m
}

// paginate exhausts a GitLab list endpoint by following NextPage links until
// none remain. It returns partial results on error so callers can degrade
// gracefully rather than losing all data when a mid-sequence page fails.
// The fetch function receives a 1-based page number and must return the raw
// *gl.Response so paginate can read the NextPage cursor.
//
// The outer loop checks ctx.Done() between pages so user-initiated
// cancellation (e.g. Ctrl+R, mode change) aborts mid-pagination without
// waiting for the next page fetch to time out. Each individual fetch is
// already context-aware via gl.WithContext at the call site.
func paginate[T any](ctx context.Context, fetch func(page int) ([]T, *gl.Response, error)) ([]T, error) {
	var all []T
	page := 1
	for {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}
		items, resp, err := fetch(page)
		if err != nil {
			return all, err
		}
		all = append(all, items...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = int(resp.NextPage)
	}
	return all, nil
}
