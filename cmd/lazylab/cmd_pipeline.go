package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/cliout"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// newPipelineCmd is the parent of every `lazylab pipeline ...` subcommand.
// It owns no flags or RunE; Cobra prints help when a user types just
// `lazylab pipeline`. Subcommands inherit the persistent --project,
// --remote, --token, --host flags from root.
func newPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Inspect and control GitLab pipelines",
		Long: `Commands for inspecting, watching, and operating on GitLab pipelines.

Pipeline references accept several forms (in order of resolution priority):

  <numeric-id>           e.g. 12345                — direct lookup
  <pipeline-url>         e.g. https://gitlab.com/foo/bar/-/pipelines/12345
  @<ref>                 e.g. @main, @feat/auth    — latest pipeline on ref
  latest                                            — latest pipeline, any ref
  HEAD                                              — pipeline for current git commit
  (empty)                                           — same as HEAD

URLs override --project (the project comes from the URL itself). All
other forms resolve --project from the flag, $GITLAB_PROJECT (or the
legacy $LAZYLAB_PROJECT), or the current git remote, in that precedence.`,
	}
	cmd.AddCommand(newPipelineStatusCmd())
	cmd.AddCommand(newPipelineWatchCmd())
	cmd.AddCommand(newPipelineListCmd())
	return cmd
}

// pipelineRefArgs is the common flag/arg surface for pipeline ref-taking
// subcommands. status and watch both take an optional positional
// pipeline ref and a --format flag.
type pipelineRefArgs struct {
	format string
}

func (a *pipelineRefArgs) bind(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&a.format, "format", "f", "table", "Output format: table, json")
}

func newPipelineStatusCmd() *cobra.Command {
	args := &pipelineRefArgs{}
	cmd := &cobra.Command{
		Use:   "status [pipeline-ref]",
		Short: "Print a single pipeline's current state",
		Long: `Resolves the pipeline reference to a concrete pipeline and prints its
current state. Performs one API call (or two when the project is given by
path and needs lookup). Does not block — for blocking-until-terminal
semantics, use 'pipeline watch'.

Exit codes follow the standard scheme: 0 on success, 3 on auth failure,
2 on transient errors, 4 on network failure. The pipeline's status itself
does NOT affect the exit code — 'status' is a read, not a watch.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, posArgs []string) error {
			return runPipelineStatus(cmd, posArgs, args)
		},
	}
	args.bind(cmd)
	return cmd
}

func newPipelineWatchCmd() *cobra.Command {
	args := &pipelineRefArgs{}
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "watch [pipeline-ref]",
		Short: "Block until the pipeline reaches a terminal state",
		Long: `Polls the pipeline at the given interval and prints a line each time
its status changes. Exits when the pipeline reaches a terminal state
(success, failed, canceled, skipped, manual).

Exit codes: 0 if the pipeline ended in success or skipped; 5 if it ended
in failed or canceled; 3/2/4 on API failures (see 'lazylab pipeline status
--help'). The 5 exit code lets wrapping scripts distinguish "the pipeline
failed" from "I couldn't talk to GitLab".`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, posArgs []string) error {
			return runPipelineWatch(cmd, posArgs, args, interval)
		},
	}
	args.bind(cmd)
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "Polling interval (minimum 2s; e.g. 5s, 1m)")
	return cmd
}

func newPipelineListCmd() *cobra.Command {
	var format string
	var ref string
	var status string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent pipelines for a project",
		Long: `Lists pipelines ordered by most recently updated, optionally filtered
by --ref and/or --status. The result is a table by default; --format
json emits one record per pipeline for piping into jq.

--limit caps the total rows returned and pages the underlying API as
needed (the API returns 100 rows per request maximum). For very large
N, expect proportionally many requests against the GitLab API.

Examples:
  lazylab pipeline list                          # default limit=20
  lazylab pipeline list --ref main --limit 5
  lazylab pipeline list --status failed
  lazylab pipeline list --format json | jq '.[] | select(.status=="failed")'`,
		RunE: func(cmd *cobra.Command, posArgs []string) error {
			return runPipelineList(cmd, format, ref, status, limit)
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "table", "Output format: table, json")
	cmd.Flags().StringVar(&ref, "ref", "", "Filter to pipelines on this ref (branch/tag name)")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (running, success, failed, canceled, manual, skipped, pending, created)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of pipelines to return (paginated as needed)")
	return cmd
}

// resolvePipelineFromCLI gathers hints from cobra/env/git and dispatches
// to gitlab.ResolvePipelineRef. Kept as a thin per-command wrapper so the
// pipeline-specific error wrapping (the "(set --project ...)" hint) lives
// next to the verbs that surface it.
func resolvePipelineFromCLI(cmd *cobra.Command, posArgs []string) (gitlab.PipelineSpec, error) {
	ctx := cmd.Context()
	client := clientFromCtx(ctx)

	hints, _ := hintsFromCmd(cmd)

	ref := ""
	if len(posArgs) > 0 {
		ref = posArgs[0]
	}
	spec, err := gitlab.ResolvePipelineRef(ctx, client, ref, hints)
	if err != nil {
		if errors.Is(err, gitlab.ErrNoProjectContext) {
			return gitlab.PipelineSpec{}, fmt.Errorf("%w (set --project, $GITLAB_PROJECT, or run inside a GitLab clone)", err)
		}
		return gitlab.PipelineSpec{}, err
	}
	return spec, nil
}

func runPipelineStatus(cmd *cobra.Command, posArgs []string, args *pipelineRefArgs) error {
	format, err := cliout.ParseFormat(args.format)
	if err != nil {
		return err
	}
	spec, err := resolvePipelineFromCLI(cmd, posArgs)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	client := clientFromCtx(ctx)
	pipe, err := client.GetPipeline(ctx, spec.ProjectID, spec.PipelineID)
	if err != nil {
		return err
	}
	// Stage failure is non-fatal: the status row is still useful without
	// stages (e.g. for a pipeline that hasn't started yet). Surface the
	// error in the log so a debugging operator can see why stages are
	// missing; writePipelineStatus already handles a nil/empty slice.
	stages, err := client.PipelineStages(ctx, spec.ProjectID, spec.PipelineID)
	if err != nil {
		loggerFromCtx(ctx).Warn("fetch stages", "err", err)
		stages = nil
	}
	return writePipelineStatus(os.Stdout, spec, pipe, stages, format)
}

func runPipelineWatch(cmd *cobra.Command, posArgs []string, args *pipelineRefArgs, interval time.Duration) error {
	const minInterval = 2 * time.Second
	if interval < minInterval {
		// Guard against accidental tight loops that would hammer the
		// API. Two seconds is the absolute floor; the default is 5s
		// and matches what the TUI uses.
		return fmt.Errorf("--interval must be at least %s, got %s", minInterval, interval)
	}
	format, err := cliout.ParseFormat(args.format)
	if err != nil {
		return err
	}
	spec, err := resolvePipelineFromCLI(cmd, posArgs)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	client := clientFromCtx(ctx)

	// Buffer stdout once. When the watch output is piped (the common
	// case for scripts that wrap `lazylab pipeline watch`), stdout is
	// block-buffered by the kernel and individual lines would not
	// appear until the buffer filled — defeating the live-watch UX.
	// Explicit Flush after each line keeps the output appearing as
	// soon as it's written.
	w := bufio.NewWriter(os.Stdout)
	defer func() { _ = w.Flush() }()

	var lastStatus string
	for {
		pipe, err := getPipelineWithRetry(ctx, client, spec)
		if err != nil {
			return err
		}
		if pipe.Status != lastStatus {
			if err := writeWatchLine(w, spec, pipe, format); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
			lastStatus = pipe.Status
		}
		if isTerminalStatus(pipe.Status) {
			if !isSuccessfulStatus(pipe.Status) {
				return &watchedFailureError{Resource: "pipeline", Status: pipe.Status}
			}
			return nil
		}
		// Jitter the wait by up to 10% so a cluster of watchers
		// triggered by the same CI hook don't all hammer the API on
		// the same tick. Using math/rand/v2's package-scoped Int64N
		// (auto-seeded, no global mutex) keeps this allocation-free.
		jitter := time.Duration(rand.Int64N(int64(interval) / 10))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval + jitter):
		}
	}
}

// getPipelineWithRetry wraps GetPipeline with a bounded exponential
// backoff for transient (rate-limit / 5xx) failures. Three retries cap
// the wall-clock cost at ~2+4+8 = 14s before the call gives up — short
// enough that an actually-down GitLab still surfaces quickly, long
// enough to ride out a single 429 burst from a noisy neighbor.
//
// Non-transient errors (auth, 404, network) return immediately so
// scripts can branch on the right exit code without paying for retries
// that can never succeed.
func getPipelineWithRetry(ctx context.Context, client gitlab.Service, spec gitlab.PipelineSpec) (gitlab.PipelineSummary, error) {
	const maxRetries = 3
	backoff := 2 * time.Second
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		pipe, err := client.GetPipeline(ctx, spec.ProjectID, spec.PipelineID)
		if err == nil {
			return pipe, nil
		}
		if !gitlab.IsRateLimited(err) && !gitlab.IsServerError(err) {
			return gitlab.PipelineSummary{}, err
		}
		lastErr = err
		if attempt == maxRetries {
			break
		}
		select {
		case <-ctx.Done():
			return gitlab.PipelineSummary{}, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return gitlab.PipelineSummary{}, lastErr
}

func runPipelineList(cmd *cobra.Command, formatFlag, ref, status string, limit int) error {
	format, err := cliout.ParseFormat(formatFlag)
	if err != nil {
		return err
	}
	if limit <= 0 {
		return fmt.Errorf("--limit must be positive, got %d", limit)
	}

	ctx := cmd.Context()
	client := clientFromCtx(ctx)

	projectID, _, err := resolveProject(ctx, cmd, client)
	if err != nil {
		return err
	}

	pipelines, err := collectPipelines(ctx, client, projectID, gitlab.PipelineListOptions{
		Ref:    ref,
		Status: status,
	}, limit)
	if err != nil {
		return err
	}
	return writePipelineList(os.Stdout, pipelines, format)
}

// collectPipelines pages ListPipelines until limit items are gathered or
// the project is exhausted. The GitLab API maxes out at 100 rows per
// request; the per-page size is the smaller of (limit-remaining, 100)
// so we don't fetch wastefully when limit < 100.
//
// Two safeguards prevent the loop from spinning forever on a
// misbehaving server: a sanity cap on iteration count, and a
// monotonicity check that breaks when NextPage stops advancing. Both
// have only ever fired in tests against a deliberately broken mock,
// but a CLI hanging on a tight infinite loop is the kind of failure
// users see once and never trust again.
func collectPipelines(ctx context.Context, client gitlab.Service, projectID int, filter gitlab.PipelineListOptions, limit int) ([]gitlab.PipelineSummary, error) {
	const apiMaxPerPage = 100
	const maxIterations = 1000
	out := make([]gitlab.PipelineSummary, 0, limit)
	page := 1
	for iter := 0; len(out) < limit; iter++ {
		if iter >= maxIterations {
			return out, fmt.Errorf("pagination did not terminate after %d pages", maxIterations)
		}
		perPage := min(limit-len(out), apiMaxPerPage)
		filter.Page = page
		filter.PerPage = perPage
		result, err := client.ListPipelines(ctx, projectID, filter)
		if err != nil {
			if errors.Is(err, gitlab.ErrNoPipelines) && len(out) == 0 {
				return nil, err
			}
			if errors.Is(err, gitlab.ErrNoPipelines) {
				break
			}
			return out, err
		}
		out = append(out, result.Pipelines...)
		if result.NextPage == 0 {
			break
		}
		// NextPage must strictly advance — otherwise we'd re-request
		// the same page forever. Some GitLab proxies (and our own
		// mocks under bad-input conditions) have been observed to
		// echo back the requested page; break out to surface a clean
		// error instead of looping.
		if result.NextPage <= page {
			return out, fmt.Errorf("pagination stalled: NextPage %d did not advance past current page %d", result.NextPage, page)
		}
		page = result.NextPage
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// isTerminalStatus reports whether status means "nothing more is going to
// happen without external action." The watch loop stops when this fires.
// Note that "manual" is terminal: a manual job is paused waiting for a
// human, and the pipeline as a whole won't advance until someone clicks.
// Wrapping scripts can re-invoke watch after triggering the manual job.
func isTerminalStatus(s string) bool {
	switch strings.ToLower(s) {
	case "success", "failed", "canceled", "skipped", "manual":
		return true
	}
	return false
}

// isSuccessfulStatus narrows the terminal set down to those that should
// exit 0. "skipped" counts as success because no work was due (e.g. a
// merge-result pipeline that was superseded). "manual" exits 0 because
// the pipeline is paused, not failed — script should reinvoke if it
// wants to continue.
func isSuccessfulStatus(s string) bool {
	switch strings.ToLower(s) {
	case "success", "skipped", "manual":
		return true
	}
	return false
}

// projectLabel formats spec for display in the status table. When the
// caller resolved a namespace path we include both the path and the
// numeric ID so the user can correlate; otherwise we show just the ID.
func projectLabel(spec gitlab.PipelineSpec) string {
	if spec.ProjectPath != "" {
		return fmt.Sprintf("%s (id %d)", spec.ProjectPath, spec.ProjectID)
	}
	return fmt.Sprintf("id %d", spec.ProjectID)
}
