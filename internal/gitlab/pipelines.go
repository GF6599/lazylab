package gitlab

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

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
		return PipelineSummary{}, fmt.Errorf("collect pipeline stages: %w", err)
	}
	summary := PipelineSummary{
		ID:     p.ID,
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
			Source: p.Source,
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
		if resp == nil {
			return PipelinePage{
				Pipelines:  []PipelineSummary{},
				Page:       opts.Page,
				PrevPage:   0,
				NextPage:   0,
				TotalPages: 0,
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
	if resp == nil {
		return PipelinePage{
			Pipelines:  summaries,
			Page:       opts.Page,
			PrevPage:   0,
			NextPage:   0,
			TotalPages: 0,
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

// CancelPipeline cancels a running pipeline.
func (c *Client) CancelPipeline(ctx context.Context, projectID, pipelineID int) error {
	_, _, err := c.api.Pipelines.CancelPipelineBuild(projectID, pipelineID, gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("cancel pipeline: %w", err)
	}
	return nil
}

// GetPipelineVariables returns the variables associated with a pipeline.
func (c *Client) GetPipelineVariables(ctx context.Context, projectID, pipelineID int) ([]PipelineVariable, error) {
	vars, _, err := c.api.Pipelines.GetPipelineVariables(projectID, pipelineID, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("get pipeline variables: %w", err)
	}
	out := make([]PipelineVariable, len(vars))
	for i, v := range vars {
		out[i] = PipelineVariable{
			Key:          v.Key,
			Value:        v.Value,
			VariableType: string(v.VariableType),
		}
	}
	return out, nil
}

// ListPipelineBridges returns bridge (child pipeline trigger) jobs for a pipeline.
func (c *Client) ListPipelineBridges(ctx context.Context, projectID, pipelineID int) ([]PipelineBridge, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}
	bridges, _, err := c.api.Jobs.ListPipelineBridges(projectID, pipelineID, opts, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list pipeline bridges: %w", err)
	}
	out := make([]PipelineBridge, 0, len(bridges))
	for _, b := range bridges {
		pb := PipelineBridge{
			ID:           b.ID,
			Name:         b.Name,
			Stage:        b.Stage,
			Status:       b.Status,
			Ref:          b.Ref,
			AllowFailure: b.AllowFailure,
			Duration:     b.Duration,
		}
		if b.DownstreamPipeline != nil {
			pb.DownstreamPipeline = &PipelineBridgeDownstream{
				ID:     b.DownstreamPipeline.ID,
				Status: string(b.DownstreamPipeline.Status),
				WebURL: b.DownstreamPipeline.WebURL,
			}
		}
		out = append(out, pb)
	}
	return out, nil
}

// GetPipelineTestReport returns the test report for a pipeline.
func (c *Client) GetPipelineTestReport(ctx context.Context, projectID, pipelineID int) (*TestReport, error) {
	report, _, err := c.api.Pipelines.GetPipelineTestReport(projectID, pipelineID, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("get pipeline test report: %w", err)
	}
	if report == nil {
		return nil, nil
	}
	tr := &TestReport{
		TotalTime:    report.TotalTime,
		TotalCount:   report.TotalCount,
		SuccessCount: report.SuccessCount,
		FailedCount:  report.FailedCount,
		SkippedCount: report.SkippedCount,
		ErrorCount:   report.ErrorCount,
	}
	for _, suite := range report.TestSuites {
		ts := TestSuite{
			Name:         suite.Name,
			TotalTime:    suite.TotalTime,
			TotalCount:   suite.TotalCount,
			SuccessCount: suite.SuccessCount,
			FailedCount:  suite.FailedCount,
			SkippedCount: suite.SkippedCount,
			ErrorCount:   suite.ErrorCount,
		}
		for _, tc := range suite.TestCases {
			c := TestCase{
				Status:        tc.Status,
				Name:          tc.Name,
				Classname:     tc.Classname,
				File:          tc.File,
				ExecutionTime: tc.ExecutionTime,
				StackTrace:    tc.StackTrace,
			}
			if tc.SystemOutput != nil {
				if s, ok := tc.SystemOutput.(string); ok {
					c.SystemOutput = s
				} else {
					c.SystemOutput = fmt.Sprintf("%v", tc.SystemOutput)
				}
			}
			ts.Cases = append(ts.Cases, c)
		}
		tr.Suites = append(tr.Suites, ts)
	}
	return tr, nil
}

// PipelineStages returns stage summaries for a pipeline.
func (c *Client) PipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	return c.collectPipelineStages(ctx, projectID, pipelineID)
}

func (c *Client) collectPipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{PerPage: 100},
	}
	jobs, err := paginate(ctx, func(page int) ([]*gl.Job, *gl.Response, error) {
		opts.Page = page
		return c.api.Jobs.ListPipelineJobs(projectID, pipelineID, opts, gl.WithContext(ctx))
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

func pipelineSummary(pipeline *gl.Pipeline) PipelineSummary {
	if pipeline == nil {
		return PipelineSummary{}
	}
	summary := PipelineSummary{
		ID:       pipeline.ID,
		Status:   pipeline.Status,
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
