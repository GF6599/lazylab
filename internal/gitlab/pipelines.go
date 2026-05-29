package gitlab

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// GetPipeline fetches a single pipeline by ID. Stages are not pre-loaded —
// callers needing them should pair this with PipelineStages, matching the
// lazy-loading pattern ListPipelines already uses.
func (c *Client) GetPipeline(ctx context.Context, projectID, pipelineID int) (PipelineSummary, error) {
	p, _, err := c.api.Pipelines.GetPipeline(projectID, int64(pipelineID), gl.WithContext(ctx))
	if err != nil {
		return PipelineSummary{}, fmt.Errorf("get pipeline %d: %w", pipelineID, err)
	}
	return pipelineSummary(p), nil
}

// LatestPipelineForSHA returns the most recent pipeline whose commit
// matches sha. Distinct from LatestPipeline (which filters by ref) because
// a single SHA can have multiple pipelines (push pipeline + detached MR
// pipeline) — sorting by updated_at desc and taking the first row matches
// the "what just ran for what I pushed" intuition.
//
// Returns ErrNoPipelines if no pipeline exists for that SHA. The CLI's
// HEAD-resolution path treats this as a soft signal (maybe the user just
// pushed and the pipeline hasn't been created yet) and surfaces it with a
// hint to wait.
func (c *Client) LatestPipelineForSHA(ctx context.Context, projectID int, sha string) (PipelineSummary, error) {
	if strings.TrimSpace(sha) == "" {
		return PipelineSummary{}, fmt.Errorf("latest pipeline for sha: empty sha")
	}
	opts := &gl.ListProjectPipelinesOptions{
		ListOptions: gl.ListOptions{PerPage: 1, Page: 1},
		OrderBy:     gl.Ptr("updated_at"),
		Sort:        gl.Ptr("desc"),
		SHA:         gl.Ptr(sha),
	}
	pipelines, _, err := c.api.Pipelines.ListProjectPipelines(projectID, opts, gl.WithContext(ctx))
	if err != nil {
		return PipelineSummary{}, fmt.Errorf("list pipelines for sha %s: %w", sha, err)
	}
	if len(pipelines) == 0 {
		return PipelineSummary{}, ErrNoPipelines
	}
	return pipelineSummaryFromInfo(pipelines[0]), nil
}

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
	summary := pipelineSummaryFromInfo(p)
	summary.Stages = stages
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
	if opts.Ref != "" {
		apiOpts.Ref = gl.Ptr(opts.Ref)
	}
	if opts.Status != "" {
		apiOpts.Status = gl.Ptr(gl.BuildStateValue(opts.Status))
	}
	pipelines, resp, err := c.api.Pipelines.ListProjectPipelines(projectID, apiOpts, gl.WithContext(ctx))
	if err != nil {
		return PipelinePage{}, fmt.Errorf("list pipelines: %w", err)
	}
	summaries := make([]PipelineSummary, 0, len(pipelines))
	for _, p := range pipelines {
		summaries = append(summaries, pipelineSummaryFromInfo(p))
	}
	if len(summaries) == 0 && opts.Page <= 1 {
		return PipelinePage{}, ErrNoPipelines
	}
	meta := extractPageMeta(resp, opts.Page)
	return PipelinePage{
		Pipelines:  summaries,
		Page:       meta.Page,
		PrevPage:   meta.PrevPage,
		NextPage:   meta.NextPage,
		TotalPages: meta.TotalPages,
	}, nil
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
			return PipelineSummary{}, fmt.Errorf("retry pipeline fallback to create failed: %w", errors.Join(err, createErr))
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
	slices.SortFunc(bridges, func(a, b *gl.Bridge) int {
		return cmp.Compare(a.ID, b.ID)
	})
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

	slices.SortFunc(jobs, func(a, b *gl.Job) int {
		return cmp.Compare(a.ID, b.ID)
	})

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

// pipelineSummaryFromInfo converts a client-go PipelineInfo (the lighter
// payload returned by the list endpoints) to our domain type, nil-safe.
// PipelineInfo lacks Duration, Coverage, and User — those fields are only
// populated by the per-pipeline GET endpoint and so stay at zero values here.
// Keeping the conversion in one place ensures every list-derived summary
// matches the same shape (UpdatedAt fallback, source/ref/sha mapping).
func pipelineSummaryFromInfo(pipeline *gl.PipelineInfo) PipelineSummary {
	if pipeline == nil {
		return PipelineSummary{}
	}
	summary := PipelineSummary{
		ID:     int(pipeline.ID),
		Status: pipeline.Status,
		Ref:    pipeline.Ref,
		SHA:    pipeline.SHA,
		WebURL: pipeline.WebURL,
		Source: pipeline.Source,
	}
	if pipeline.UpdatedAt != nil {
		summary.UpdatedAt = *pipeline.UpdatedAt
	} else if pipeline.CreatedAt != nil {
		summary.UpdatedAt = *pipeline.CreatedAt
	}
	return summary
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
	"preparing":            3, // job runner is preparing the environment — treat like running
	"pending":              4,
	"waiting_for_resource": 4,
	"waiting_for_callback": 4, // bridge/trigger awaiting downstream callback
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
