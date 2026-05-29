package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/config"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// runCLIWithService is the M2 sibling of runCLI: instead of relying on
// --demo to populate the context, it short-circuits PersistentPreRunE
// and installs the caller-provided service/config/logger directly. This
// lets a test inject a service that returns a canned error (401, 404,
// 429) and verify the exit code translation without spinning up an
// httptest server per case.
//
// The harness:
//  1. Builds a fresh root command via newRootCmd so flag state cannot
//     leak between subtests (same rationale as runCLI).
//  2. Replaces the root's PersistentPreRunE with one that stuffs svc,
//     a minimal cfg, and a discarding logger into the cobra context.
//     This bypasses config.Load, the redacting handler setup, and the
//     real client construction — all already covered by other tests.
//  3. Captures os.Stdout/os.Stderr the same way runCLI does, because
//     the subcommands write directly to those file descriptors.
func runCLIWithService(t *testing.T, svc gitlab.Service, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	defer func() {
		os.Stdout, os.Stderr = origStdout, origStderr
	}()

	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(outBuf, stdoutR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(errBuf, stderrR); done <- struct{}{} }()

	rootCmd := newRootCmd()
	// Override PersistentPreRunE to install the test fixtures. The
	// original setupContext call would call config.Load (which insists
	// on a token), build a redacting logger, and construct a real HTTP
	// client. None of that is relevant when the test owns the service.
	cfg := config.Config{
		Host:     "https://gitlab.example.com",
		Token:    "test-token",
		LogLevel: "error",
		Remote:   "origin",
		// Pre-seed Project so the per-test args don't need --project; the
		// resolver reads cfg.Project via hintsFromCmd. Tests that care
		// about a different project can set --project on the args, but
		// would also need to wire cfg through to a flag-parsing PreRunE
		// — out of scope for this harness which exists to inject errors.
		Project: "1",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rootCmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		ctx := c.Context()
		ctx = context.WithValue(ctx, keyConfig, cfg)
		ctx = context.WithValue(ctx, keyClient, svc)
		ctx = context.WithValue(ctx, keyLogger, logger)
		c.SetContext(ctx)
		return nil
	}
	ctx := context.Background()

	rootCmd.SetOut(stdoutW)
	rootCmd.SetErr(stderrW)
	rootCmd.SetArgs(args)

	runErr := rootCmd.ExecuteContext(ctx)
	if runErr != nil {
		_, _ = stderrW.WriteString("error: " + runErr.Error() + "\n")
	}

	_ = stdoutW.Close()
	_ = stderrW.Close()
	<-done
	<-done

	return outBuf.String(), errBuf.String(), exitCodeFor(runErr)
}

func TestCLI_PipelineStatus_Unauthorized(t *testing.T) {
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			return gitlab.PipelineSummary{}, apiErrorForTest(t, http.StatusUnauthorized)
		},
	}
	_, stderr, code := runCLIWithService(t, svc, "pipeline", "status", "12345")
	if code != exitUnauthorized {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUnauthorized, stderr)
	}
}

func TestCLI_PipelineStatus_NotFound(t *testing.T) {
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			return gitlab.PipelineSummary{}, apiErrorForTest(t, http.StatusNotFound)
		},
	}
	_, stderr, code := runCLIWithService(t, svc, "pipeline", "status", "12345")
	if code != exitGeneric {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitGeneric, stderr)
	}
}

func TestCLI_PipelineStatus_RateLimited(t *testing.T) {
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			return gitlab.PipelineSummary{}, apiErrorForTest(t, http.StatusTooManyRequests)
		},
	}
	_, stderr, code := runCLIWithService(t, svc, "pipeline", "status", "12345")
	if code != exitTransient {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitTransient, stderr)
	}
	if !strings.Contains(stderr, "error:") {
		t.Errorf("expected 'error:' prefix in stderr, got: %s", stderr)
	}
}

func TestCLI_PipelineStatus_ServerError(t *testing.T) {
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			return gitlab.PipelineSummary{}, apiErrorForTest(t, http.StatusBadGateway)
		},
	}
	_, stderr, code := runCLIWithService(t, svc, "pipeline", "status", "12345")
	if code != exitTransient {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitTransient, stderr)
	}
}
