package gitlab

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// derefTime returns *t when non-nil, or the zero time otherwise. Mappers use
// this to flatten the SDK's *time.Time fields into PipelineJob's value-typed
// timestamps without an if-nil check at every site.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ListPipelineJobs returns every job across all pages for the given pipeline.
// Returns ErrNoJobs when the pipeline exists but has no jobs yet (e.g., right
// after creation before GitLab schedules them).
func (c *Client) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]PipelineJob, error) {
	opts := &gl.ListJobsOptions{
		ListOptions: gl.ListOptions{PerPage: 100},
	}
	rawJobs, err := paginate(ctx, func(page int) ([]*gl.Job, *gl.Response, error) {
		opts.Page = int64(page)
		return c.api.Jobs.ListPipelineJobs(projectID, int64(pipelineID), opts, gl.WithContext(ctx))
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
	slices.SortFunc(jobs, func(a, b PipelineJob) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return jobs, nil
}

// GetJobTrace returns the full log output for a job as a single string.
// For running jobs this returns whatever output has been captured so far;
// the TUI polls this periodically to simulate live log tailing.
//
// Traces are capped at 10 MB to prevent OOM on jobs with massive output.
// Returns an error if the trace exceeds this limit.
func (c *Client) GetJobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	trace, _, err := c.api.Jobs.GetTraceFile(projectID, int64(jobID), gl.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("get job trace: %w", err)
	}
	if trace == nil {
		return "", fmt.Errorf("no trace data available")
	}
	const maxTraceSize = 10 * 1024 * 1024 // 10 MB
	data, err := io.ReadAll(io.LimitReader(trace, maxTraceSize+1))
	if err != nil {
		return "", fmt.Errorf("read job trace: %w", err)
	}
	if len(data) > maxTraceSize {
		return "", fmt.Errorf("job trace too large: exceeds %d bytes", maxTraceSize)
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
	job, _, err := c.api.Jobs.RetryJob(projectID, int64(jobID), gl.WithContext(ctx))
	if err != nil {
		return PipelineJob{}, fmt.Errorf("retry job: %w", err)
	}
	return mapJob(job), nil
}

// CancelJob cancels a running job.
func (c *Client) CancelJob(ctx context.Context, projectID, jobID int) error {
	_, _, err := c.api.Jobs.CancelJob(projectID, int64(jobID), gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}
	return nil
}

// PlayJob triggers a manual (when:manual) job that is waiting for user
// action and returns its updated state. Has no effect on non-manual jobs.
func (c *Client) PlayJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	job, _, err := c.api.Jobs.PlayJob(projectID, int64(jobID), nil, gl.WithContext(ctx))
	if err != nil {
		return PipelineJob{}, fmt.Errorf("play job: %w", err)
	}
	return mapJob(job), nil
}

// mapJob converts a client-go Job to our flat PipelineJob, nil-safe.
// Runner is a value struct in client-go; a zero ID indicates no runner is
// assigned (pending, manual, or created jobs).
func mapJob(job *gl.Job) PipelineJob {
	if job == nil {
		return PipelineJob{}
	}
	pj := PipelineJob{
		ID:                int(job.ID),
		Name:              job.Name,
		Stage:             job.Stage,
		Status:            job.Status,
		WebURL:            job.WebURL,
		Duration:          job.Duration,
		FailureReason:     job.FailureReason,
		AllowFailure:      job.AllowFailure,
		StartedAt:         derefTime(job.StartedAt),
		FinishedAt:        derefTime(job.FinishedAt),
		ArtifactsExpireAt: derefTime(job.ArtifactsExpireAt),
	}
	if job.Runner.ID != 0 {
		pj.RunnerDescription = job.Runner.Description
	}
	if job.Artifacts != nil {
		pj.ArtifactsCount = len(job.Artifacts)
		for _, a := range job.Artifacts {
			pj.Artifacts = append(pj.Artifacts, JobArtifact{
				FileType:   a.FileType,
				Filename:   a.Filename,
				Size:       int(a.Size),
				FileFormat: a.FileFormat,
			})
		}
	}
	return pj
}
