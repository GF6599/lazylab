// Package gitlab wraps the GitLab API client with TUI-focused helpers.
package gitlab

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// Client is a small façade over the GitLab client-go API that exposes higher-level types
// tailored for the TUI.
type Client struct {
	api  *gl.Client
	host string
}

// NewClient wires the GitLab client with the provided token and host.
func NewClient(token, host string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("gitlab token must not be empty")
	}
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		trimmedHost = "https://gitlab.com"
	}
	baseURL := ensureAPIBaseURL(trimmedHost)
	api, err := gl.NewClient(token, gl.WithBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}
	return &Client{api: api, host: trimmedHost}, nil
}

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
}

// PipelineStage captures a GitLab CI stage and its aggregated status.
type PipelineStage struct {
	Name   string
	Status string
}

// PipelineJob represents a single job in a pipeline.
type PipelineJob struct {
	ID     int
	Name   string
	Stage  string
	Status string
	WebURL string
}

// ErrNoPipelines indicates no pipeline runs were returned by GitLab.
var ErrNoPipelines = errors.New("no pipelines found")

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
		return ProjectPage{}, err
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

// ListTree returns the immediate children of the path for the given project.
func (c *Client) ListTree(ctx context.Context, projectID int, opts TreeListOptions) ([]TreeNode, error) {
	treeOpts := &gl.ListTreeOptions{
		ListOptions: gl.ListOptions{
			PerPage: 200,
			Page:    1,
		},
		Ref:       gl.Ptr(opts.Ref),
		Path:      gl.Ptr(opts.Path),
		Recursive: gl.Ptr(false),
	}
	nodes, _, err := c.api.Repositories.ListTree(projectID, treeOpts, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list tree: %w", err)
	}
	out := make([]TreeNode, len(nodes))
	for i, node := range nodes {
		out[i] = TreeNode{
			Path: node.Path,
			Name: node.Name,
			Type: node.Type,
			Mode: node.Mode,
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir() && !out[j].IsDir() {
			return true
		}
		if !out[i].IsDir() && out[j].IsDir() {
			return false
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// GetFileContent fetches the contents of a GitLab repository file at the given ref.
func (c *Client) GetFileContent(ctx context.Context, projectID int, path, ref string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("file path required")
	}
	file, _, err := c.api.RepositoryFiles.GetFile(projectID, path, &gl.GetFileOptions{
		Ref: gl.Ptr(ref),
	}, gl.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return "", fmt.Errorf("decode file: %w", err)
	}
	return string(data), nil
}

// LatestPipeline returns the most recent pipeline for the given project/ref.
func (c *Client) LatestPipeline(ctx context.Context, projectID int, ref string) (PipelineSummary, error) {
	opts := &gl.ListProjectPipelinesOptions{
		ListOptions: gl.ListOptions{
			PerPage: 1,
			Page:    1,
		},
		OrderBy: gl.Ptr("updated_at"),
		Sort:    gl.Ptr("desc"),
	}
	if strings.TrimSpace(ref) != "" {
		opts.Ref = gl.Ptr(ref)
	}
	pipelines, _, err := c.api.Pipelines.ListProjectPipelines(projectID, opts, gl.WithContext(ctx))
	if err != nil {
		return PipelineSummary{}, fmt.Errorf("list pipelines: %w", err)
	}
	if len(pipelines) == 0 {
		return PipelineSummary{}, ErrNoPipelines
	}
	p := pipelines[0]
	stages, err := c.collectPipelineStages(ctx, projectID, p.ID)
	if err != nil {
		return PipelineSummary{}, err
	}
	summary := PipelineSummary{
		ID:     p.ID,
		Status: string(p.Status),
		Ref:    p.Ref,
		SHA:    p.SHA,
		WebURL: p.WebURL,
		Stages: stages,
	}
	if p.UpdatedAt != nil {
		summary.UpdatedAt = *p.UpdatedAt
	} else if p.CreatedAt != nil {
		summary.UpdatedAt = *p.CreatedAt
	}
	return summary, nil
}

// ListPipelines returns a page of pipelines for a project ordered by most recently updated.
func (c *Client) ListPipelines(ctx context.Context, projectID int, opts PipelineListOptions) (PipelinePage, error) {
	if opts.PerPage <= 0 {
		opts.PerPage = 25
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	apiOpts := &gl.ListProjectPipelinesOptions{
		ListOptions: gl.ListOptions{
			PerPage: opts.PerPage,
			Page:    opts.Page,
		},
		OrderBy: gl.Ptr("updated_at"),
		Sort:    gl.Ptr("desc"),
	}
	pipelines, resp, err := c.api.Pipelines.ListProjectPipelines(projectID, apiOpts, gl.WithContext(ctx))
	if err != nil {
		return PipelinePage{}, fmt.Errorf("list pipelines: %w", err)
	}
	summaries := make([]PipelineSummary, 0, len(pipelines))
	for _, p := range pipelines {
		summary := PipelineSummary{
			ID:     p.ID,
			Status: string(p.Status),
			Ref:    p.Ref,
			SHA:    p.SHA,
			WebURL: p.WebURL,
		}
		if p.UpdatedAt != nil {
			summary.UpdatedAt = *p.UpdatedAt
		} else if p.CreatedAt != nil {
			summary.UpdatedAt = *p.CreatedAt
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 {
		if opts.Page <= 1 {
			return PipelinePage{}, ErrNoPipelines
		}
		return PipelinePage{
			Pipelines:  summaries,
			Page:       opts.Page,
			PrevPage:   resp.PreviousPage,
			NextPage:   resp.NextPage,
			TotalPages: resp.TotalPages,
		}, nil
	}
	return PipelinePage{
		Pipelines:  summaries,
		Page:       opts.Page,
		PrevPage:   resp.PreviousPage,
		NextPage:   resp.NextPage,
		TotalPages: resp.TotalPages,
	}, nil
}

// RetryPipeline retries failed jobs in a pipeline, falling back to a fresh run when needed.
func (c *Client) RetryPipeline(ctx context.Context, projectID, pipelineID int, ref string) (PipelineSummary, error) {
	pipeline, _, err := c.api.Pipelines.RetryPipelineBuild(projectID, pipelineID, gl.WithContext(ctx))
	if err != nil {
		if ref == "" || !gl.HasStatusCode(err, 400) {
			return PipelineSummary{}, fmt.Errorf("retry pipeline: %w", err)
		}
		created, _, createErr := c.api.Pipelines.CreatePipeline(projectID, &gl.CreatePipelineOptions{
			Ref: gl.Ptr(ref),
		}, gl.WithContext(ctx))
		if createErr != nil {
			return PipelineSummary{}, fmt.Errorf("retry pipeline: %v; run pipeline: %w", err, createErr)
		}
		return pipelineSummary(created), nil
	}
	return pipelineSummary(pipeline), nil
}

// RetryJob retries a single job run.
func (c *Client) RetryJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	if jobID == 0 {
		return PipelineJob{}, fmt.Errorf("retry job: missing job id")
	}
	job, _, err := c.api.Jobs.RetryJob(projectID, jobID, gl.WithContext(ctx))
	if err != nil {
		return PipelineJob{}, fmt.Errorf("retry job: %w", err)
	}
	if job == nil {
		return PipelineJob{}, nil
	}
	return PipelineJob{
		ID:     job.ID,
		Name:   job.Name,
		Stage:  job.Stage,
		Status: job.Status,
		WebURL: job.WebURL,
	}, nil
}

func pipelineSummary(pipeline *gl.Pipeline) PipelineSummary {
	if pipeline == nil {
		return PipelineSummary{}
	}
	summary := PipelineSummary{
		ID:     pipeline.ID,
		Status: pipeline.Status,
		Ref:    pipeline.Ref,
		SHA:    pipeline.SHA,
		WebURL: pipeline.WebURL,
	}
	if pipeline.UpdatedAt != nil {
		summary.UpdatedAt = *pipeline.UpdatedAt
	} else if pipeline.CreatedAt != nil {
		summary.UpdatedAt = *pipeline.CreatedAt
	}
	return summary
}

// PipelineStages returns stage summaries for a pipeline.
func (c *Client) PipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	return c.collectPipelineStages(ctx, projectID, pipelineID)
}

// ListPipelineJobs returns all jobs for a pipeline.
func (c *Client) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]PipelineJob, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}
	var jobs []PipelineJob
	page := 1
	for {
		opts.Page = page
		items, resp, err := c.api.Jobs.ListPipelineJobs(projectID, pipelineID, opts, gl.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("list pipeline jobs: %w", err)
		}
		for _, job := range items {
			jobs = append(jobs, PipelineJob{
				ID:     job.ID,
				Name:   job.Name,
				Stage:  job.Stage,
				Status: string(job.Status),
				WebURL: job.WebURL,
			})
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	if len(jobs) == 0 {
		return nil, ErrNoPipelines
	}
	return jobs, nil
}

// GetJobTrace returns the log output for a job.
func (c *Client) GetJobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	trace, _, err := c.api.Jobs.GetTraceFile(projectID, jobID, gl.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("get job trace: %w", err)
	}
	data, err := io.ReadAll(trace)
	if err != nil {
		return "", fmt.Errorf("read job trace: %w", err)
	}
	return string(data), nil
}

func (c *Client) collectPipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}
	stageStatus := make(map[string]string)
	stageOrder := make([]string, 0)
	seenStage := make(map[string]bool)
	page := 1
	for {
		opts.Page = page
		jobs, resp, err := c.api.Jobs.ListPipelineJobs(projectID, pipelineID, opts, gl.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("list pipeline jobs: %w", err)
		}
		for _, job := range jobs {
			stageName := strings.TrimSpace(job.Stage)
			if stageName == "" {
				stageName = "(unknown stage)"
			}
			if !seenStage[stageName] {
				seenStage[stageName] = true
				stageOrder = append(stageOrder, stageName)
			}
			stageStatus[stageName] = mergeStageStatus(stageStatus[stageName], string(job.Status))
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	stages := make([]PipelineStage, 0, len(stageOrder))
	for _, stage := range stageOrder {
		status := stageStatus[stage]
		if status == "" {
			status = defaultStageStatus
		}
		stages = append(stages, PipelineStage{
			Name:   stage,
			Status: status,
		})
	}
	return stages, nil
}

func ensureAPIBaseURL(host string) string {
	host = strings.TrimSuffix(host, "/")
	if strings.HasSuffix(host, "/api/v4") {
		return host
	}
	return host + "/api/v4"
}

const defaultStageStatus = "unknown"

var stageStatusPriority = map[string]int{
	"failed":               0,
	"canceled":             1,
	"manual":               2,
	"running":              3,
	"pending":              4,
	"waiting_for_resource": 4,
	"scheduled":            4,
	"created":              5,
	"success":              6,
	"skipped":              7,
	"default":              8,
	"unknown":              9,
}

func mergeStageStatus(current, candidate string) string {
	candidate = normalizeStageStatus(candidate)
	if current == "" {
		return candidate
	}
	current = normalizeStageStatus(current)
	if rank(candidate) < rank(current) {
		return candidate
	}
	return current
}

func normalizeStageStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return defaultStageStatus
	}
	return status
}

func rank(status string) int {
	if r, ok := stageStatusPriority[status]; ok {
		return r
	}
	return stageStatusPriority["unknown"]
}
