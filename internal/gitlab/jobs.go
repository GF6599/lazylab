package gitlab

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// MaxTraceSize bounds how many bytes GetJobTrace will read from a single
// trace endpoint call. Set to 10 MB — enough for the long tail of normal
// jobs without exposing the process to runaway-log OOM. Traces exceeding
// this cap surface as ErrTraceTooLarge; the streamer treats that as a
// soft signal (degrade to status-only polling) rather than a hard error.
const MaxTraceSize = 10 * 1024 * 1024

// ErrTraceTooLarge signals that a job's trace exceeded MaxTraceSize. The
// SDK does not expose HTTP Range support for the trace endpoint, so we
// can't stream past the cap; callers either accept the first MaxTraceSize
// bytes (via GetJobTraceCapped) or drop streaming and poll status only.
var ErrTraceTooLarge = errors.New("job trace exceeds cap")

// StreamTraceOptions configures StreamJobTrace's polling cadence and
// output target. Interval governs how often the trace is re-fetched
// while the job is still running; Writer receives each newly-appended
// byte block. Both are required at the call site (StreamJobTrace
// returns an error if Writer is nil; Interval defaults to 2s when zero).
type StreamTraceOptions struct {
	// Writer receives trace bytes as they appear. Typically os.Stdout
	// for the CLI; a viewport-bound writer for the future TUI live-tail.
	Writer io.Writer
	// Interval is the time between trace re-fetches. Two seconds is a
	// reasonable default for interactive feel; CI wrappers can dial it
	// up to 10–30s to reduce API load on long-running jobs.
	Interval time.Duration
}

// cappedTraceFetcher is an optional capability some Services expose:
// "give me the trace, but if it overflows MaxTraceSize, return the first
// MaxTraceSize bytes without erroring." The streamer type-asserts against
// this to power its best-effort tail when GetJobTrace has already
// surfaced ErrTraceTooLarge — keeping Service itself unchanged.
type cappedTraceFetcher interface {
	GetJobTraceCapped(ctx context.Context, projectID, jobID int) (string, error)
}

// StreamJobTrace tails a job's trace output, writing each newly-appended
// block to opts.Writer until the job reaches a terminal state. Returns
// the final job status string (e.g. "success", "failed") so callers can
// map it to an exit code. On poll errors the last successfully-observed
// status is returned alongside the error so callers can still surface
// "was running when we lost it" diagnostics.
//
// The implementation polls the full trace each interval and emits only
// the suffix beyond what was last written. The GitLab SDK does not
// expose HTTP Range support for the trace endpoint, so this "diff and
// append" approach is the simplest correct strategy — at the cost of
// re-downloading the cumulative trace each poll. For very long jobs
// users can raise --interval to amortize this.
//
// StreamJobTrace performs a deliberate "one more fetch" after detecting
// terminal state because GitLab runners sometimes flip job.Status to
// "success" a fraction of a second before the final log bytes are
// flushed to the trace endpoint. Without the extra fetch, the last few
// lines of output would be lost on roughly 5% of jobs in our observation.
//
// When the trace grows past MaxTraceSize mid-stream, GetJobTrace
// returns ErrTraceTooLarge. The streamer treats that as a soft signal:
// it logs a warning, stops emitting trace bytes, and continues polling
// GetJob only to detect terminal status. On terminal status it makes
// one final best-effort fetch via GetJobTraceCapped (if the Service
// implements it) and emits whatever fit in the first MaxTraceSize bytes
// — accepting that the tail of the log is unrecoverable without
// HTTP Range support on the trace endpoint.
//
// Concurrency and ownership: StreamJobTrace blocks in the caller's
// goroutine for the lifetime of the watch and spawns none of its own, so
// cancellation is the caller's responsibility — cancel ctx to make it
// return promptly (with ctx.Err()). It writes to but never closes
// opts.Writer; the caller owns that writer's lifecycle.
func StreamJobTrace(ctx context.Context, c Service, projectID, jobID int, opts StreamTraceOptions) (string, error) {
	if opts.Writer == nil {
		return "", fmt.Errorf("stream trace: writer is required")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}

	var written int
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	emit := func(trace string) error {
		if len(trace) <= written {
			return nil
		}
		if _, err := opts.Writer.Write([]byte(trace[written:])); err != nil {
			return err
		}
		written = len(trace)
		return nil
	}

	// lastStatus tracks the most recent observed job status so that an
	// error mid-stream still gives the caller useful context ("we lost
	// it while it was running" beats a bare "").
	var lastStatus string
	// degraded flips true once the trace blew past MaxTraceSize: we stop
	// emitting bytes but keep polling status so the watch still
	// terminates correctly.
	var degraded bool
	for {
		if !degraded {
			trace, err := c.GetJobTrace(ctx, projectID, jobID)
			switch {
			case err == nil:
				if emitErr := emit(trace); emitErr != nil {
					return lastStatus, emitErr
				}
			case errors.Is(err, ErrTraceTooLarge):
				slog.Default().Warn("job trace exceeded cap; degrading to status-only polling",
					"project_id", projectID, "job_id", jobID, "cap_bytes", MaxTraceSize)
				degraded = true
			default:
				return lastStatus, err
			}
		}

		job, err := c.GetJob(ctx, projectID, jobID)
		if err != nil {
			return lastStatus, err
		}
		lastStatus = job.Status
		if isTerminalJobStatus(job.Status) {
			// Race-recovery refetch: the job's status can flip to
			// terminal before its last trace bytes have flushed.
			// Use the capped variant if available so a final trace
			// that just crossed the cap still produces "best effort"
			// output instead of an empty tail.
			finalTrace, ferr := finalTraceFetch(ctx, c, projectID, jobID, degraded)
			if ferr != nil {
				slog.Default().Warn("final trace fetch failed; emitting nothing",
					"project_id", projectID, "job_id", jobID, "err", ferr)
				return job.Status, nil
			}
			if emitErr := emit(finalTrace); emitErr != nil {
				return job.Status, fmt.Errorf("emit final trace: %w", emitErr)
			}
			return job.Status, nil
		}

		select {
		case <-ctx.Done():
			return lastStatus, ctx.Err()
		case <-ticker.C:
		}
	}
}

// finalTraceFetch picks the right "give me everything you've got" call
// for the race-recovery refetch. When the watch was already degraded by
// ErrTraceTooLarge there's no point asking for the full trace (we know
// it overflows); we go straight to the capped variant. Otherwise we try
// the normal trace first and fall back to capped if it just crossed the
// cap on the final read.
func finalTraceFetch(ctx context.Context, c Service, projectID, jobID int, degraded bool) (string, error) {
	if cf, ok := c.(cappedTraceFetcher); ok && degraded {
		return cf.GetJobTraceCapped(ctx, projectID, jobID)
	}
	trace, err := c.GetJobTrace(ctx, projectID, jobID)
	if err == nil {
		return trace, nil
	}
	if errors.Is(err, ErrTraceTooLarge) {
		if cf, ok := c.(cappedTraceFetcher); ok {
			return cf.GetJobTraceCapped(ctx, projectID, jobID)
		}
		// No capped fetcher available — surface the cap signal so the
		// caller logs it; the watch still returns its terminal status.
		return "", err
	}
	return "", err
}

// isTerminalJobStatus reports whether a job's status means "won't
// change without external action." Mirrors the pipeline-status terminal
// set; "manual" is included because a manual job is paused waiting for
// a human click, not making forward progress.
func isTerminalJobStatus(s string) bool {
	switch strings.ToLower(s) {
	case "success", "failed", "canceled", "skipped", "manual":
		return true
	}
	return false
}

// derefTime returns *t when non-nil, or the zero time otherwise. Mappers use
// this to flatten the SDK's *time.Time fields into PipelineJob's value-typed
// timestamps without an if-nil check at every site.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// GetJob fetches a single job by ID. Pairs with GetJobTrace in the
// streaming-log loop: the trace endpoint returns bytes, GetJob returns
// the current status, and the streamer stops once status becomes terminal.
//
// A zero jobID is rejected up front with a plain "missing job id" error to
// catch the common "nothing selected" caller bug before the round trip. SDK
// failures are returned %w-wrapped (as "get job %d") so AsAPIError and
// friends can classify the HTTP status.
func (c *Client) GetJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	if jobID == 0 {
		return PipelineJob{}, fmt.Errorf("get job: missing job id")
	}
	job, _, err := c.api.Jobs.GetJob(projectID, int64(jobID), gl.WithContext(ctx))
	if err != nil {
		return PipelineJob{}, fmt.Errorf("get job %d: %w", jobID, err)
	}
	return mapJob(job), nil
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
// Traces are capped at MaxTraceSize to prevent OOM on jobs with massive
// output. Returns an error wrapping ErrTraceTooLarge if the trace exceeds
// this limit; callers that want best-effort partial content can use
// GetJobTraceCapped instead.
func (c *Client) GetJobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	data, capped, err := c.fetchJobTrace(ctx, projectID, jobID)
	if err != nil {
		return "", err
	}
	if capped {
		return "", fmt.Errorf("job trace too large: exceeds %d bytes: %w", MaxTraceSize, ErrTraceTooLarge)
	}
	return string(data), nil
}

// GetJobTraceCapped returns the first MaxTraceSize bytes of a job's trace,
// suppressing the ErrTraceTooLarge error. Intended for the streaming-log
// fallback: when the live tail has hit the cap, the caller still wants
// "best effort, first chunk only" content to display before exiting.
func (c *Client) GetJobTraceCapped(ctx context.Context, projectID, jobID int) (string, error) {
	data, _, err := c.fetchJobTrace(ctx, projectID, jobID)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// fetchJobTrace is the shared trace download path. The capped return value
// reports whether the underlying response exceeded MaxTraceSize so each
// public wrapper can decide whether to surface ErrTraceTooLarge or treat
// the partial content as success.
func (c *Client) fetchJobTrace(ctx context.Context, projectID, jobID int) (data []byte, capped bool, err error) {
	trace, _, err := c.api.Jobs.GetTraceFile(projectID, int64(jobID), gl.WithContext(ctx))
	if err != nil {
		return nil, false, fmt.Errorf("get job trace: %w", err)
	}
	if trace == nil {
		return nil, false, fmt.Errorf("no trace data available")
	}
	data, err = io.ReadAll(io.LimitReader(trace, MaxTraceSize+1))
	if err != nil {
		return nil, false, fmt.Errorf("read job trace: %w", err)
	}
	if len(data) > MaxTraceSize {
		return data[:MaxTraceSize], true, nil
	}
	return data, false, nil
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

// CancelJob requests cancellation of a single job and returns any SDK
// failure %w-wrapped as "cancel job" (so AsAPIError can classify it). Unlike
// its sibling RetryJob, it does not guard against a zero jobID; a zero id is
// passed straight through and surfaces as a server-side 404.
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
