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
// via newRootCmd() so flag state cannot leak between subtests: Cobra's
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
	// os.Stdout; point it at the same captured pipes so version banners and
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
// token into config.Load, the token/host env vars and both config-file
// pointers, so tokenless tests exercise the guard under test instead of
// whatever the developer's shell happens to export.
func clearAmbientCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_HOST", "")
	t.Setenv("LAZYLAB_CONFIG", "")
	t.Setenv("GITLAB_TUI_CONFIG", "")
}

// stubGlabCredentials swaps the glab resolver for a canned answer and restores
// it when the test ends, so outcomes never depend on whether the developer's
// machine has a real authenticated glab.
func stubGlabCredentials(t *testing.T, token, host string, ok bool) {
	t.Helper()
	restore := resolveGlabCredentials
	resolveGlabCredentials = func(string) (string, string, bool) { return token, host, ok }
	t.Cleanup(func() { resolveGlabCredentials = restore })
}

// TestCLI_VersionFlag: --version prints the "lazylab <ver>" banner and exits 0.
// Given a fresh root command, when --version runs, then stdout carries the
// custom template rather than Cobra's default "lazylab version <ver>".
// Why it matters: scripts parse this exact banner shape, so falling back to the
// default template would silently break them.
func TestCLI_VersionFlag(t *testing.T) {
	// When: the version flag runs
	stdout, _, code := runCLI(t, "--version")

	// Then: it exits OK with the custom banner
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout, "lazylab ") {
		t.Fatalf("stdout = %q, want prefix \"lazylab \"", stdout)
	}
	// And: Cobra's default template stayed overridden
	if strings.Contains(stdout, "lazylab version ") {
		t.Fatalf("stdout contains default Cobra template: %q", stdout)
	}
}

// TestCLI_TokenlessFirstRun: with no credentials anywhere, the bare launch is
// refused with guidance while help and completion still work.
// Given no token, config file, or glab login, when the user runs lazylab, then
// help, then completion zsh, then only the bare launch fails and it names the
// missing token.
// Why it matters: the first-run path must teach setup, not lock the manual and
// shell integration behind the very credential the user is trying to configure.
func TestCLI_TokenlessFirstRun(t *testing.T) {
	// Given: no ambient credentials and a glab with nothing stored
	clearAmbientCredentials(t)
	stubGlabCredentials(t, "", "", false)

	// When: the TUI is launched bare
	_, launchErr, launchCode := runCLI(t)

	// Then: the launch is refused before the TUI starts, naming the token.
	// Non-zero (not a specific code) because config validation is a generic
	// error, and "token" (not an exact phrase) to avoid bonding to the wrapper
	// layer's wording.
	if launchCode == exitOK {
		t.Fatalf("expected non-zero exit for a tokenless launch (stderr: %s)", launchErr)
	}
	if !strings.Contains(launchErr, "token") {
		t.Errorf("expected stderr to mention the missing token; got:\n%s", launchErr)
	}

	// And: the help verb still serves the manual
	helpOut, helpErr, helpCode := runCLI(t, "help")
	if helpCode != exitOK {
		t.Fatalf("help exit code = %d, want %d (stderr: %s)", helpCode, exitOK, helpErr)
	}
	if !strings.Contains(helpOut, "Usage:") {
		t.Errorf("expected usage text on stdout; got:\n%s", helpOut)
	}

	// And: shell completion still emits its script, since it runs from shell
	// init files where a token requirement would error on every new shell
	compOut, compErr, compCode := runCLI(t, "completion", "zsh")
	if compCode != exitOK {
		t.Fatalf("completion exit code = %d, want %d (stderr: %s)", compCode, exitOK, compErr)
	}
	if !strings.Contains(compOut, "compdef") {
		t.Errorf("expected a zsh completion script on stdout; got:\n%s", compOut)
	}
}

// TestCLI_BogusFlag_NoPanic: a typoed flag becomes an error exit, not a panic.
// Given a fresh root command, when --bogus is parsed, then Execute returns an
// error that maps to a non-zero exit code.
// Why it matters: shell wrappers need an exit code they can react to instead of
// a crash.
func TestCLI_BogusFlag_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked on bogus flag: %v", r)
		}
	}()

	// When: an unknown flag is parsed
	_, stderr, code := runCLI(t, "--bogus")

	// Then: the process reports failure through the exit code
	if code == exitOK {
		t.Fatalf("expected non-zero exit, got %d (stderr: %s)", code, stderr)
	}
}

// TestCLI_UnknownVerb_Errors: stray positional arguments are rejected by name.
// Given the CLI verbs no longer exist, when "lazylab whoami" runs, then the
// exit is non-zero and stderr names the offending argument.
// Why it matters: without the validator a script invoking a removed verb would
// open a full-screen TUI, or die on /dev/tty in a pipe, instead of failing
// loudly with the reason.
func TestCLI_UnknownVerb_Errors(t *testing.T) {
	// Given: no ambient credentials, so a rejection cannot come from the auth
	// guard instead of the argument validator
	clearAmbientCredentials(t)
	stubGlabCredentials(t, "", "", false)

	// When: a removed verb is invoked
	_, stderr, code := runCLI(t, "whoami")

	// Then: the verb is rejected and named
	if code == exitOK {
		t.Fatalf("expected non-zero exit, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "whoami") {
		t.Errorf("expected stderr to name the offending argument; got:\n%s", stderr)
	}
}

// TestSetupContext_UsesGlabCredentialsWhenNoToken: startup adopts glab's stored
// credentials when lazylab itself has none.
// Given no lazylab token and a resolver yielding glab's token and host, when
// setupContext runs, then the client is built against the glab-provided host.
// Why it matters: a glab-authenticated user must get a working TUI with zero
// lazylab-specific setup, pointed at the host glab is actually logged into.
func TestSetupContext_UsesGlabCredentialsWhenNoToken(t *testing.T) {
	// Given: no ambient credentials, and glab holding a token for its host
	clearAmbientCredentials(t)
	stubGlabCredentials(t, "glpat-fake-from-glab", "https://gl.example.com", true)

	cmd := newRootCmd()
	cmd.SetContext(context.Background()) // ExecuteContext would do this in production
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	// When: startup assembles the command context
	if err := setupContext(cmd); err != nil {
		t.Fatalf("setupContext should succeed using glab credentials: %v", err)
	}

	// Then: the resolved config and client reflect the glab-provided host
	if got := configFromCtx(cmd.Context()).Host; got != "https://gl.example.com" {
		t.Errorf("host = %q, want the glab-provided host", got)
	}
	if clientFromCtx(cmd.Context()) == nil {
		t.Error("expected a client built from the glab credentials")
	}
}
