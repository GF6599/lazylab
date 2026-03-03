// Package gitlab wraps the GitLab API client with TUI-focused helpers.
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

// Client is a small facade over the GitLab client-go API that exposes higher-level types
// tailored for the TUI.
type Client struct {
	api  *gl.Client
	host string
}

// NewClient wires the GitLab client with the provided token and host.
// The token must have 'api' scope. If host is empty, defaults to https://gitlab.com.
// Returns an error if the token is empty or the host URL is invalid.
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

// Service is the interface that the UI layer depends on. It covers every
// Client method the TUI calls, making it possible to swap in a mock for tests.
type Service interface {
	ListProjects(ctx context.Context, opts ProjectListOptions) (ProjectPage, error)
	ListTree(ctx context.Context, projectID int, opts TreeListOptions) ([]TreeNode, error)
	GetFileContent(ctx context.Context, projectID int, path, ref string) (string, error)
	LatestPipeline(ctx context.Context, projectID int, ref string) (PipelineSummary, error)
	ListPipelines(ctx context.Context, projectID int, opts PipelineListOptions) (PipelinePage, error)
	PipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error)
	ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]PipelineJob, error)
	GetJobTrace(ctx context.Context, projectID, jobID int) (string, error)
	RetryPipeline(ctx context.Context, projectID, pipelineID int, ref string) (PipelineSummary, error)
	RetryJob(ctx context.Context, projectID, jobID int) (PipelineJob, error)
	CancelPipeline(ctx context.Context, projectID, pipelineID int) error
	CancelJob(ctx context.Context, projectID, jobID int) error
	PlayJob(ctx context.Context, projectID, jobID int) (PipelineJob, error)
	ListMergeRequests(ctx context.Context, projectID int, opts MRListOptions) (MRPage, error)
	ListPipelineBridges(ctx context.Context, projectID, pipelineID int) ([]PipelineBridge, error)
	GetPipelineTestReport(ctx context.Context, projectID, pipelineID int) (*TestReport, error)
	// ListProjectCommits returns recent commits for display in the detail pane.
	// Pass an empty ref to use the project's default branch.
	ListProjectCommits(ctx context.Context, projectID int, ref string, limit int) ([]CommitSummary, error)
}

// Verify at compile time that *Client satisfies Service.
var _ Service = (*Client)(nil)

// ProjectNode represents the subset of GitLab projects used by the UI.
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

// ProjectPage contains a slice of projects along with pagination metadata.
type ProjectPage struct {
	Projects   []ProjectNode
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
}

// PipelineListOptions describe pagination parameters for pipeline listings.
type PipelineListOptions struct {
	Page    int
	PerPage int
}

// PipelinePage contains a slice of pipelines along with pagination metadata.
type PipelinePage struct {
	Pipelines  []PipelineSummary
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
}

// TreeNode represents a repository tree entry (file or directory).
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

// PipelineSummary represents the last known pipeline for a project/ref.
type PipelineSummary struct {
	ID        int
	Status    string
	Ref       string
	SHA       string
	WebURL    string
	UpdatedAt time.Time
	Stages    []PipelineStage
	Source    string
	Duration  float64
	Coverage  float64
	User      string
}

// PipelineStage captures a GitLab CI stage and its aggregated status.
type PipelineStage struct {
	Name   string
	Status string
}

// PipelineJob represents a single job in a pipeline.
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

// PipelineVariable represents a CI/CD variable associated with a pipeline.
type PipelineVariable struct {
	Key          string
	Value        string
	VariableType string
}

// MergeRequestSummary represents a merge request in a project.
type MergeRequestSummary struct {
	IID          int
	Title        string
	State        string
	Author       string
	SourceBranch string
	TargetBranch string
	PipelineID   int
	WebURL       string
	UpdatedAt    time.Time
}

// MRListOptions describe pagination and filter parameters for merge request listings.
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
}

// PipelineBridge represents a bridge (child pipeline trigger) job.
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
type PipelineBridgeDownstream struct {
	ID     int
	Status string
	WebURL string
}

// TestReport contains a pipeline's test report summary.
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

// ErrNoPipelines indicates no pipeline runs were returned by GitLab.
var ErrNoPipelines = errors.New("no pipelines found")

// ErrNoJobs indicates no jobs were returned for a pipeline.
var ErrNoJobs = errors.New("no jobs found")

// TreeListOptions configures repository tree listing.
type TreeListOptions struct {
	Path string
	Ref  string
}

// ListProjects returns the authenticated user's projects ordered by recent
// activity.
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
			Page:    opts.Page,
			PerPage: opts.PerPage,
		},
	}
	projects, resp, err := c.api.Projects.ListProjects(listOpts, gl.WithContext(ctx))
	if err != nil {
		return ProjectPage{}, fmt.Errorf("list projects: %w", err)
	}
	nodes := make([]ProjectNode, len(projects))
	for i, p := range projects {
		nodes[i] = ProjectNode{
			ID:                p.ID,
			Name:              p.Name,
			PathWithNamespace: p.PathWithNamespace,
			Description:       p.Description,
			WebURL:            p.WebURL,
			SSHURLToRepo:      p.SSHURLToRepo,
			StarCount:         p.StarCount,
			Visibility:        string(p.Visibility),
			DefaultBranch:     p.DefaultBranch,
		}
		if p.LastActivityAt != nil {
			nodes[i].LastActivityAt = *p.LastActivityAt
		}
	}
	pageInfo := ProjectPage{
		Projects: nodes,
		Page:     opts.Page,
	}
	if resp != nil {
		pageInfo.Page = resp.CurrentPage
		pageInfo.PrevPage = resp.PreviousPage
		pageInfo.NextPage = resp.NextPage
		pageInfo.TotalPages = resp.TotalPages
	}
	return pageInfo, nil
}

func ensureAPIBaseURL(host string) string {
	host = strings.TrimSuffix(host, "/")
	if strings.HasSuffix(host, "/api/v4") {
		return host
	}
	return host + "/api/v4"
}

// paginate collects all pages from a GitLab list endpoint. The fetch function
// receives a 1-based page number and returns a slice of items plus the raw
// *gl.Response (which carries NextPage). paginate keeps calling until there are
// no more pages.
func paginate[T any](ctx context.Context, fetch func(page int) ([]T, *gl.Response, error)) ([]T, error) {
	var all []T
	page := 1
	for {
		items, resp, err := fetch(page)
		if err != nil {
			return all, err
		}
		all = append(all, items...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}
