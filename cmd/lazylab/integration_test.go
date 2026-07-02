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

// clearAmbientCredentials blanks every environment source that can feed a
// token into config.Load — the token/host env vars and both config-file
// pointers — so tokenless tests exercise the guard under test instead of
// whatever the developer's shell happens to export.
func clearAmbientCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_HOST", "")
	t.Setenv("LAZYLAB_CONFIG", "")
	t.Setenv("GITLAB_TUI_CONFIG", "")
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
	clearAmbientCredentials(t)
	// Simulate glab having no stored credentials so the token-required guard is
	// what fails, independent of whether this machine has glab authenticated.
	restore := resolveGlabCredentials
	resolveGlabCredentials = func(string) (string, string, bool) { return "", "", false }
	defer func() { resolveGlabCredentials = restore }()

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

// TestSetupContext_UsesGlabCredentialsWhenNoToken confirms the glab fallback is
// wired into startup: with no lazylab token but a resolver that yields glab's
// stored credentials, setupContext builds a client against glab's host.
func TestSetupContext_UsesGlabCredentialsWhenNoToken(t *testing.T) {
	clearAmbientCredentials(t)
	restore := resolveGlabCredentials
	resolveGlabCredentials = func(string) (string, string, bool) {
		return "glpat-fake-from-glab", "https://gl.example.com", true
	}
	defer func() { resolveGlabCredentials = restore }()

	cmd := newRootCmd()
	cmd.SetContext(context.Background()) // ExecuteContext would do this in production
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := setupContext(cmd); err != nil {
		t.Fatalf("setupContext should succeed using glab credentials: %v", err)
	}
	if got := configFromCtx(cmd.Context()).Host; got != "https://gl.example.com" {
		t.Errorf("host = %q, want the glab-provided host", got)
	}
	if clientFromCtx(cmd.Context()) == nil {
		t.Error("expected a client built from the glab credentials")
	}
}

// TestCLI_UnknownVerb_Errors pins that stray positional arguments are rejected
// with a message naming the offender. Without an Args validator the root would
// treat "lazylab whoami" as a plain TUI launch, which in a non-TTY pipe dies
// trying to open /dev/tty instead of telling the user the verb doesn't exist.
func TestCLI_UnknownVerb_Errors(t *testing.T) {
	clearAmbientCredentials(t)
	restore := resolveGlabCredentials
	resolveGlabCredentials = func(string) (string, string, bool) { return "", "", false }
	defer func() { resolveGlabCredentials = restore }()

	_, stderr, code := runCLI(t, "whoami")
	if code == exitOK {
		t.Fatalf("expected non-zero exit, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "whoami") {
		t.Errorf("expected stderr to name the offending argument; got:\n%s", stderr)
	}
}

// TestCLI_Help_NoToken_ExitsZero guards the first-run experience: a user with
// no token yet must be able to run "lazylab help" to learn how to supply one,
// so the token-required guard cannot apply to the help verb.
func TestCLI_Help_NoToken_ExitsZero(t *testing.T) {
	clearAmbientCredentials(t)
	restore := resolveGlabCredentials
	resolveGlabCredentials = func(string) (string, string, bool) { return "", "", false }
	defer func() { resolveGlabCredentials = restore }()

	stdout, stderr, code := runCLI(t, "help")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("expected usage text on stdout; got:\n%s", stdout)
	}
}

// TestCLI_CompletionZsh_NoToken_ExitsZero pins that shell completion works
// without credentials: completion scripts run from shell init, so a token
// requirement here would error on every new shell until auth is configured.
func TestCLI_CompletionZsh_NoToken_ExitsZero(t *testing.T) {
	clearAmbientCredentials(t)
	restore := resolveGlabCredentials
	resolveGlabCredentials = func(string) (string, string, bool) { return "", "", false }
	defer func() { resolveGlabCredentials = restore }()

	stdout, stderr, code := runCLI(t, "completion", "zsh")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "compdef") {
		t.Errorf("expected a zsh completion script on stdout; got:\n%s", stdout)
	}
}
