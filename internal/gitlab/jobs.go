package gitlab

import (
	"context"
	"fmt"
	"io"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ListPipelineJobs returns every job across all pages for the given pipeline.
// Returns ErrNoJobs when the pipeline exists but has no jobs yet (e.g., right
// after creation before GitLab schedules them).
func (c *Client) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]PipelineJob, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{PerPage: 100},
	}
	rawJobs, err := paginate(ctx, func(page int) ([]*gl.Job, *gl.Response, error) {
		opts.Page = page
		return c.api.Jobs.ListPipelineJobs(projectID, pipelineID, opts, gl.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("list pipeline jobs: %w", err)
	}
	jobs := make([]PipelineJob, len(rawJobs))
	for i, j := range rawJobs {
		jobs[i] = mapJob(j)
	}
	if len(jobs) == 0 {
		return nil, ErrNoJobs
	}
	return jobs, nil
}

// GetJobTrace returns the full log output for a job as a single string.
// For running jobs this returns whatever output has been captured so far;
// the TUI polls this periodically to simulate live log tailing.
func (c *Client) GetJobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	trace, _, err := c.api.Jobs.GetTraceFile(projectID, jobID, gl.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("get job trace: %w", err)
	}
	if trace == nil {
		return "", fmt.Errorf("no trace data available")
	}
	data, err := io.ReadAll(trace)
	if err != nil {
		return "", fmt.Errorf("read job trace: %w", err)
	}
	return string(data), nil
}

// RetryJob retries a single job, creating a new run of that job within the
// same pipeline. Returns an error if jobID is zero (a common caller bug when
// no job is selected).
func (c *Client) RetryJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	if jobID == 0 {
		return PipelineJob{}, fmt.Errorf("retry job: missing job id")
	}
	job, _, err := c.api.Jobs.RetryJob(projectID, jobID, gl.WithContext(ctx))
	if err != nil {
		return PipelineJob{}, fmt.Errorf("retry job: %w", err)
	}
	return mapJob(job), nil
}

// CancelJob cancels a running job.
func (c *Client) CancelJob(ctx context.Context, projectID, jobID int) error {
	_, _, err := c.api.Jobs.CancelJob(projectID, jobID, gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}
	return nil
}

// PlayJob triggers a manual (when:manual) job that is waiting for user
// action and returns its updated state. Has no effect on non-manual jobs.
func (c *Client) PlayJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	job, _, err := c.api.Jobs.PlayJob(projectID, jobID, nil, gl.WithContext(ctx))
	if err != nil {
		return PipelineJob{}, fmt.Errorf("play job: %w", err)
	}
	return mapJob(job), nil
}

// mapJob converts a client-go Job to our flat PipelineJob, nil-safe.
func mapJob(job *gl.Job) PipelineJob {
	if job == nil {
		return PipelineJob{}
	}
	pj := PipelineJob{
		ID:            job.ID,
		Name:          job.Name,
		Stage:         job.Stage,
		Status:        job.Status,
		WebURL:        job.WebURL,
		Duration:      job.Duration,
		FailureReason: job.FailureReason,
		AllowFailure:  job.AllowFailure,
	}
	if job.StartedAt != nil {
		pj.StartedAt = *job.StartedAt
	}
	if job.FinishedAt != nil {
		pj.FinishedAt = *job.FinishedAt
	}
	pj.RunnerDescription = job.Runner.Description
	if job.Artifacts != nil {
		pj.ArtifactsCount = len(job.Artifacts)
		for _, a := range job.Artifacts {
			pj.Artifacts = append(pj.Artifacts, JobArtifact{
				FileType:   a.FileType,
				Filename:   a.Filename,
				Size:       a.Size,
				FileFormat: a.FileFormat,
			})
		}
	}
	if job.ArtifactsExpireAt != nil {
		pj.ArtifactsExpireAt = *job.ArtifactsExpireAt
	}
	return pj
}
