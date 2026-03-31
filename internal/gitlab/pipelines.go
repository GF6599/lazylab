package gitlab

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// LatestPipeline returns the single most recent pipeline for a project/ref,
// including its stage summaries. Pass an empty ref to get the latest pipeline
// across all branches. Returns ErrNoPipelines if the project has no CI runs.
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
	stages, err := c.collectPipelineStages(ctx, projectID, int(p.ID))
	if err != nil {
		return PipelineSummary{}, fmt.Errorf("collect pipeline stages: %w", err)
	}
	summary := PipelineSummary{
		ID:     int(p.ID),
		Status: string(p.Status),
		Ref:    p.Ref,
		SHA:    p.SHA,
		WebURL: p.WebURL,
		Stages: stages,
		Source: p.Source,
	}
	if p.UpdatedAt != nil {
		summary.UpdatedAt = *p.UpdatedAt
	} else if p.CreatedAt != nil {
		summary.UpdatedAt = *p.CreatedAt
	}
	return summary, nil
}

// ListPipelines returns a single page of pipelines ordered by most recently
// updated. Unlike LatestPipeline, stages are NOT pre-loaded here — the TUI
// fetches them lazily via PipelineStages when the user selects a pipeline.
// Returns ErrNoPipelines only on the first page; later pages return an empty
// PipelinePage so the UI can display "no more results" without an error state.
func (c *Client) ListPipelines(ctx context.Context, projectID int, opts PipelineListOptions) (PipelinePage, error) {
	if opts.PerPage <= 0 {
		opts.PerPage = 25
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	apiOpts := &gl.ListProjectPipelinesOptions{
		ListOptions: gl.ListOptions{
			PerPage: int64(opts.PerPage),
			Page:    int64(opts.Page),
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
			ID:     int(p.ID),
			Status: string(p.Status),
			Ref:    p.Ref,
			SHA:    p.SHA,
			WebURL: p.WebURL,
			Source: p.Source,
		}
		if p.UpdatedAt != nil {
			summary.UpdatedAt = *p.UpdatedAt
		} else if p.CreatedAt != nil {
			summary.UpdatedAt = *p.CreatedAt
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 && opts.Page <= 1 {
		return PipelinePage{}, ErrNoPipelines
	}
	page := PipelinePage{Pipelines: summaries, Page: opts.Page}
	if resp != nil {
		page.PrevPage = int(resp.PreviousPage)
		page.NextPage = int(resp.NextPage)
		page.TotalPages = int(resp.TotalPages)
	}
	return page, nil
}

// RetryPipeline retries all failed jobs in a pipeline. When GitLab returns a
// 400 (which happens when the pipeline has no retryable jobs — e.g., it was
// cancelled before any job ran, or all jobs succeeded), it falls back to
// creating a brand-new pipeline on the same ref. The ref parameter is required
// for this fallback; if ref is empty and the retry fails, the error propagates
// directly. The error message from both the retry and create attempts is
// preserved in the wrapped error for debuggability.
func (c *Client) RetryPipeline(ctx context.Context, projectID, pipelineID int, ref string) (PipelineSummary, error) {
	pipeline, _, err := c.api.Pipelines.RetryPipelineBuild(projectID, int64(pipelineID), gl.WithContext(ctx))
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

// CancelPipeline cancels a running pipeline.
func (c *Client) CancelPipeline(ctx context.Context, projectID, pipelineID int) error {
	_, _, err := c.api.Pipelines.CancelPipelineBuild(projectID, int64(pipelineID), gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("cancel pipeline: %w", err)
	}
	return nil
}

// ListPipelineBridges returns bridge jobs (child pipeline triggers) for a
// pipeline. Bridges are not included in ListPipelineJobs — they must be
// fetched separately through this endpoint. DownstreamPipeline is nil when
// the bridge has not triggered yet or the downstream project is inaccessible.
func (c *Client) ListPipelineBridges(ctx context.Context, projectID, pipelineID int) ([]PipelineBridge, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}
	bridges, _, err := c.api.Jobs.ListPipelineBridges(projectID, int64(pipelineID), opts, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list pipeline bridges: %w", err)
	}
	out := make([]PipelineBridge, 0, len(bridges))
	for _, b := range bridges {
		pb := PipelineBridge{
			ID:           int(b.ID),
			Name:         b.Name,
			Stage:        b.Stage,
			Status:       b.Status,
			Ref:          b.Ref,
			AllowFailure: b.AllowFailure,
			Duration:     b.Duration,
		}
		if b.DownstreamPipeline != nil {
			pb.DownstreamPipeline = &PipelineBridgeDownstream{
				ID:        int(b.DownstreamPipeline.ID),
				ProjectID: int(b.DownstreamPipeline.ProjectID),
				Status:    string(b.DownstreamPipeline.Status),
				WebURL:    b.DownstreamPipeline.WebURL,
			}
		}
		out = append(out, pb)
	}
	return out, nil
}

// GetPipelineTestReport returns the JUnit test report for a pipeline, or nil
// if the pipeline has no test reports configured. Returns an error only on
// API failures — a nil *TestReport with nil error means "no reports".
func (c *Client) GetPipelineTestReport(ctx context.Context, projectID, pipelineID int) (*TestReport, error) {
	report, _, err := c.api.Pipelines.GetPipelineTestReport(projectID, int64(pipelineID), gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("get pipeline test report: %w", err)
	}
	if report == nil {
		return nil, nil
	}
	tr := &TestReport{
		TotalTime:    report.TotalTime,
		TotalCount:   int(report.TotalCount),
		SuccessCount: int(report.SuccessCount),
		FailedCount:  int(report.FailedCount),
		SkippedCount: int(report.SkippedCount),
		ErrorCount:   int(report.ErrorCount),
	}
	for _, suite := range report.TestSuites {
		ts := TestSuite{
			Name:         suite.Name,
			TotalTime:    suite.TotalTime,
			TotalCount:   int(suite.TotalCount),
			SuccessCount: int(suite.SuccessCount),
			FailedCount:  int(suite.FailedCount),
			SkippedCount: int(suite.SkippedCount),
			ErrorCount:   int(suite.ErrorCount),
		}
		for _, tc := range suite.TestCases {
			testCase := TestCase{
				Status:        tc.Status,
				Name:          tc.Name,
				Classname:     tc.Classname,
				File:          tc.File,
				ExecutionTime: tc.ExecutionTime,
				StackTrace:    tc.StackTrace,
			}
			if tc.SystemOutput != nil {
				if s, ok := tc.SystemOutput.(string); ok {
					testCase.SystemOutput = s
				} else {
					testCase.SystemOutput = fmt.Sprintf("%v", tc.SystemOutput)
				}
			}
			ts.Cases = append(ts.Cases, testCase)
		}
		tr.Suites = append(tr.Suites, ts)
	}
	return tr, nil
}

// PipelineStages returns stage summaries for a pipeline, with each stage's
// status aggregated from its constituent jobs. Stage ordering is preserved
// from the API response (which reflects .gitlab-ci.yml declaration order).
func (c *Client) PipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	return c.collectPipelineStages(ctx, projectID, pipelineID)
}

// collectPipelineStages fetches all jobs and folds them into per-stage summaries.
func (c *Client) collectPipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{PerPage: 100},
	}
	jobs, err := paginate(ctx, func(page int) ([]*gl.Job, *gl.Response, error) {
		opts.Page = int64(page)
		return c.api.Jobs.ListPipelineJobs(projectID, int64(pipelineID), opts, gl.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("list pipeline jobs: %w", err)
	}

	stageStatus := make(map[string]string)
	stageOrder := make([]string, 0)
	seenStage := make(map[string]bool)
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

// pipelineSummary converts a client-go Pipeline to our domain type, nil-safe.
func pipelineSummary(pipeline *gl.Pipeline) PipelineSummary {
	if pipeline == nil {
		return PipelineSummary{}
	}
	summary := PipelineSummary{
		ID:       int(pipeline.ID),
		Status:   string(pipeline.Status),
		Ref:      pipeline.Ref,
		SHA:      pipeline.SHA,
		WebURL:   pipeline.WebURL,
		Source:   string(pipeline.Source),
		Duration: float64(pipeline.Duration),
	}
	if cov, err := strconv.ParseFloat(pipeline.Coverage, 64); err == nil {
		summary.Coverage = cov
	}
	if pipeline.User != nil {
		summary.User = pipeline.User.Name
	}
	if pipeline.UpdatedAt != nil {
		summary.UpdatedAt = *pipeline.UpdatedAt
	} else if pipeline.CreatedAt != nil {
		summary.UpdatedAt = *pipeline.CreatedAt
	}
	return summary
}

const defaultStageStatus = "unknown"

// stageStatusPriority defines which job status "wins" when multiple jobs exist
// in the same stage. Lower numbers take precedence. The ranking reflects what a
// human cares about most: failures first, then manual-action-needed, then
// in-progress, and finally success/skipped as the least urgent.
var stageStatusPriority = map[string]int{
	"failed":               0,
	"canceled":             1,
	"manual":               2,
	"blocked":              2,
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

// mergeStageStatus picks the higher-priority status between current and candidate.
// Priority order: failed > canceled > manual/blocked > running > pending > created > success > skipped.
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
