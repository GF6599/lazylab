package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// newJobCmd is the parent of every `lazylab job ...` subcommand. The
// flagship subcommand is `log <id> [--follow]` — the original motivating
// feature for the CLI restructure. Follow-up subcommands (play, retry,
// cancel, artifacts) plug in here without disturbing the existing tree.
func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect and operate on GitLab CI jobs",
		Long: `Commands for working with individual GitLab CI jobs.

Job references in v1 accept two forms:

  <numeric-id>           e.g. 4567890         — direct lookup, needs --project
  <job-url>              e.g. https://gitlab.com/foo/bar/-/jobs/4567890

Future revisions will add pipeline-scoped lookups (e.g. "the latest
test-stage job in the current pipeline") once the disambiguation rules
for matrix builds are nailed down.`,
	}
	cmd.AddCommand(newJobLogCmd())
	return cmd
}

func newJobLogCmd() *cobra.Command {
	var follow bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "log <job-ref>",
		Short: "Print a job's trace output to stdout",
		Long: `Fetches the job's trace and writes it to stdout. With --follow, polls
the trace at --interval (default 2s) and emits new bytes as they
arrive, exiting when the job reaches a terminal state.

Without --follow this is a single fetch — equivalent to opening the job
in a browser and copying the log. With --follow it's the CI equivalent
of 'tail -f': useful inside scripts that need to display live progress
of a remote pipeline run.

Exit codes:
  0  trace fetched (or job ended successfully under --follow)
  3  auth / forbidden (token issue)
  5  --follow saw the job end in failed/canceled
  2  rate limit or 5xx
  4  network failure`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJobLog(cmd, args, follow, interval)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "F", false, "Stream trace and block until the job terminates")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Polling interval when --follow is set (e.g. 2s, 10s)")
	return cmd
}

// resolveJob is the job-equivalent of resolvePipelineFromCLI: it gathers
// hints from cobra/env/git via hintsFromCmd, then dispatches to the
// job-ref resolver. The error wrapping mentions "or pass a job URL"
// instead of HEAD/@ref because v1 of the job resolver doesn't accept
// those forms.
func resolveJob(cmd *cobra.Command, arg string) (gitlab.JobSpec, error) {
	ctx := cmd.Context()
	client := clientFromCtx(ctx)

	hints, _ := hintsFromCmd(cmd)

	spec, err := gitlab.ResolveJobRef(ctx, client, arg, hints)
	if err != nil {
		if errors.Is(err, gitlab.ErrNoProjectContext) {
			return gitlab.JobSpec{}, fmt.Errorf("%w (set --project, $GITLAB_PROJECT, run inside a clone, or pass a job URL)", err)
		}
		return gitlab.JobSpec{}, err
	}
	return spec, nil
}

func runJobLog(cmd *cobra.Command, args []string, follow bool, interval time.Duration) error {
	spec, err := resolveJob(cmd, args[0])
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	client := clientFromCtx(ctx)

	if !follow {
		trace, err := client.GetJobTrace(ctx, spec.ProjectID, spec.JobID)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(os.Stdout, trace); err != nil {
			return err
		}
		// Ensure the trace ends with a newline so terminals don't run
		// the next prompt onto the last log line. Cheap and matches
		// what `cat`/`tail` users expect.
		if len(trace) == 0 || trace[len(trace)-1] != '\n' {
			_, _ = io.WriteString(os.Stdout, "\n")
		}
		return nil
	}

	status, err := gitlab.StreamJobTrace(ctx, client, spec.ProjectID, spec.JobID, gitlab.StreamTraceOptions{
		Writer:   os.Stdout,
		Interval: interval,
	})
	if err != nil {
		return err
	}
	if !isSuccessfulJobStatusCLI(status) {
		return &watchedFailureError{Resource: "job", Status: status}
	}
	return nil
}

// isSuccessfulJobStatusCLI is the CLI-level mapping from job terminal
// status to exit code. Mirrors the pipeline equivalent — "manual" and
// "skipped" both exit 0 because neither represents a build failure.
func isSuccessfulJobStatusCLI(s string) bool {
	switch s {
	case "success", "skipped", "manual":
		return true
	}
	return false
}
