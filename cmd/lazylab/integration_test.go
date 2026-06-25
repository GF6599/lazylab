package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// runCLI drives the Cobra root the way main() does, then captures stdout,
// stderr, and the resolved exit code. Each call builds a fresh *cobra.Command
// via newRootCmd() so flag state cannot leak between subtests — Cobra's
// persistent flags are mutated by Parse, and reusing a single root would have
// any test "remember" the previous test's --demo, --token, etc.
//
// Capturing os.Stdout (rather than wiring cmd.SetOut) is required because the
// version/usage banners ultimately reach os.Stdout; the redirect-pipe approach
// matches what Go's own cmd/go tests do for the same reason.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
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

	// Drain both pipes concurrently so a verbose command can't block on a full
	// pipe buffer before the test ever reads the result.
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(outBuf, stdoutR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(errBuf, stderrR); done <- struct{}{} }()

	rootCmd := newRootCmd()
	// Cobra writes its own help/usage/error output to a separate sink from
	// os.Stdout — point it at the same captured pipes so version banners and
	// help text show up where the tests look.
	rootCmd.SetOut(stdoutW)
	rootCmd.SetErr(stderrW)
	rootCmd.SetArgs(args)

	runErr := rootCmd.ExecuteContext(context.Background())
	if runErr != nil {
		// Mirror what main() does so the test sees the same stderr shape a
		// real user would: "error: <message>".
		_, _ = stderrW.WriteString("error: " + runErr.Error() + "\n")
	}

	// Close the write ends before reading: the drain goroutines exit on EOF.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	<-done
	<-done

	return outBuf.String(), errBuf.String(), exitCodeFor(runErr)
}

// TestCLI_VersionFlag pins that --version exits 0 and emits the custom
// "lazylab <ver>" template (not Cobra's default "lazylab version <ver>"). The
// template was set on the root command precisely so the output matches the
// pre-Cobra format users may already parse.
func TestCLI_VersionFlag(t *testing.T) {
	stdout, _, code := runCLI(t, "--version")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout, "lazylab ") {
		t.Fatalf("stdout = %q, want prefix \"lazylab \"", stdout)
	}
	if strings.Contains(stdout, "lazylab version ") {
		// Cobra's default template would emit "lazylab version dev" — the
		// custom template strips "version" to match the pre-Cobra shape.
		t.Fatalf("stdout contains default Cobra template: %q", stdout)
	}
}

// TestCLI_NoTokenNoDemo_ExitsNonZero confirms the auth-required guardrail. With
// no token and no --demo, setupContext (PersistentPreRunE) fails config
// validation before the TUI ever launches. We assert non-zero rather than a
// specific code because the config-validation failure is a generic error.
func TestCLI_NoTokenNoDemo_ExitsNonZero(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	_, stderr, code := runCLI(t)
	if code == exitOK {
		t.Fatalf("expected non-zero exit, got %d (stderr: %s)", code, stderr)
	}
	// The error chain wraps the underlying "token is required" message from
	// config.Load. Tolerating either phrasing keeps the test from
	// fragility-bonding to the exact wrapper layer.
	if !strings.Contains(stderr, "token") {
		t.Errorf("expected stderr to mention token; got:\n%s", stderr)
	}
}

// TestCLI_BogusFlag_NoPanic guards the "user typos a flag" path. Cobra returns
// an error from Execute (not a panic), and we want a non-zero exit code so
// shell scripts can react.
func TestCLI_BogusFlag_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked on bogus flag: %v", r)
		}
	}()
	_, stderr, code := runCLI(t, "--bogus")
	if code == exitOK {
		t.Fatalf("expected non-zero exit, got %d (stderr: %s)", code, stderr)
	}
}
