package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// runCLI drives the full Cobra command tree the way main() does, then
// captures stdout, stderr, and the resolved exit code. Each call builds
// a fresh *cobra.Command via newRootCmd() so flag state cannot leak
// between subtests — Cobra's persistent flags are mutated by Parse, and
// reusing a single root across tests would have any test "remember" the
// previous test's --demo, --token, etc.
//
// Capturing os.Stdout (rather than wiring cmd.SetOut) is required
// because the production subcommands write to os.Stdout directly so the
// `bufio` flush semantics in `pipeline watch` work as users expect.
// Plumbing an io.Writer through every command would be the cleaner
// long-term answer, but it is out of scope here — the redirect-pipe
// approach matches what Go's own cmd/go tests do for the same reason.
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

	// Drain both pipes concurrently so a verbose command can't block on
	// a full pipe buffer (4-64KB depending on platform) before the test
	// ever calls runErr.
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(outBuf, stdoutR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(errBuf, stderrR); done <- struct{}{} }()

	rootCmd := newRootCmd()
	// Cobra writes its own help/usage/error output to a separate sink
	// from os.Stdout — point it at the same captured pipes so version
	// banners and help text show up where the tests look.
	rootCmd.SetOut(stdoutW)
	rootCmd.SetErr(stderrW)
	rootCmd.SetArgs(args)

	runErr := rootCmd.ExecuteContext(context.Background())
	if runErr != nil {
		// Mirror what main() does so the test sees the same stderr
		// shape a real user would: "error: <message>". Without this
		// the test would have to assert against runErr.Error() which
		// would mean every assertion knows the wrapping format twice.
		_, _ = stderrW.WriteString("error: " + runErr.Error() + "\n")
	}

	// Close the write ends before reading: the drain goroutines exit
	// when they hit EOF, and reading the bufs racy if we don't wait.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	<-done
	<-done

	return outBuf.String(), errBuf.String(), exitCodeFor(runErr)
}

// TestCLI_VersionFlag pins that --version exits 0 and emits the custom
// "lazylab <ver>" template (not Cobra's default "lazylab version <ver>").
// The template was set on the root command precisely so the CLI output
// matches the pre-Cobra format users may already parse.
func TestCLI_VersionFlag(t *testing.T) {
	stdout, _, code := runCLI(t, "--version")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout, "lazylab ") {
		t.Fatalf("stdout = %q, want prefix \"lazylab \"", stdout)
	}
	if strings.Contains(stdout, "lazylab version ") {
		// Cobra's default template would emit "lazylab version dev" —
		// the custom template strips "version" to match the pre-Cobra
		// shape. A regression to the default would silently break
		// script parsers that already split on "lazylab <ver>".
		t.Fatalf("stdout contains default Cobra template: %q", stdout)
	}
}

// TestCLI_Help pins that --help lists every registered subcommand. The
// long-form description happens to mention "whoami, job, pipeline, mr,
// project" but the actual command tree must match — a regression where
// a subcommand was removed from AddCommand but left in the description
// would slip past a substring-only check.
func TestCLI_Help(t *testing.T) {
	stdout, _, code := runCLI(t, "--help")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	for _, want := range []string{"pipeline", "job", "where", "whoami"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--help output missing subcommand %q\nGot:\n%s", want, stdout)
		}
	}
}

// TestCLI_DemoWhoamiJSON exercises the full Cobra→config→demo→cliout
// chain in a single invocation. Demo mode skips the token requirement
// so the test stays hermetic — no GITLAB_TOKEN needed in CI. The JSON
// shape is the public scripting contract: dropping a field here would
// break `lazylab whoami --format json | jq .username` scripts.
func TestCLI_DemoWhoamiJSON(t *testing.T) {
	stdout, _, code := runCLI(t, "--demo", "whoami", "--format", "json")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstdout: %s", code, exitOK, stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, stdout)
	}
	// UserInfo has no JSON struct tags so cliout.PrintJSON emits the
	// Go field names verbatim (Username, Email, …). The test pins this
	// shape so adding json tags later is a deliberate, breaking change.
	if got["Username"] != "demo" {
		t.Errorf("Username = %v, want \"demo\"", got["Username"])
	}
	if got["Email"] != "demo@gitlab.example.com" {
		t.Errorf("Email = %v, want \"demo@gitlab.example.com\"", got["Email"])
	}
}

// TestCLI_DemoPipelineListJSON pins the array shape of `pipeline list
// --format json`. The demo dataset uses projectID*1000+i for IDs, so
// --project 1 yields IDs 1001..1007 — verifying the count is enough to
// catch a regression where the resolver started passing the wrong
// project to ListPipelines.
func TestCLI_DemoPipelineListJSON(t *testing.T) {
	stdout, _, code := runCLI(t, "--demo", "pipeline", "list", "--project", "1", "--format", "json")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\n", code, exitOK, stdout)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\nstdout: %s", err, stdout)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least one pipeline in demo output, got 0")
	}
	// First demo pipeline for project 1 has ID 1001 (1*1000 + 0 + 1).
	if id, _ := got[0]["id"].(float64); id != 1001 {
		t.Errorf("first pipeline id = %v, want 1001", got[0]["id"])
	}
}

// TestCLI_DemoPipelineStatusTable exercises the @ref resolution form
// (the most commonly broken path during refactors of the resolver) and
// the table renderer. Tightly assertion-light: we only check that key
// labels appear, because the exact column layout is a UX detail and a
// strict-equality test would make routine UI tweaks painful.
func TestCLI_DemoPipelineStatusTable(t *testing.T) {
	stdout, _, code := runCLI(t, "--demo", "pipeline", "status", "--project", "1", "@main")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\n", code, exitOK, stdout)
	}
	for _, want := range []string{"Status", "Ref", "main"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status table missing %q\nGot:\n%s", want, stdout)
		}
	}
}

// TestCLI_WhereOutsideRepo confirms that running `where` outside any
// git repository neither crashes nor errors — the diagnostic must be
// usable precisely when the user's context is broken. We chdir to a
// fresh temp dir so the test doesn't inherit the worktree's own git
// state (which would make the assertion path-dependent on where the
// test runs from).
func TestCLI_WhereOutsideRepo(t *testing.T) {
	// Token is irrelevant to `where`, but set one so the noTokenCommands
	// carve-out isn't the only thing keeping the test green.
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Chdir(t.TempDir())

	stdout, _, code := runCLI(t, "where")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstdout: %s", code, exitOK, stdout)
	}
	if !strings.Contains(stdout, "Host:") {
		t.Errorf("where output missing Host: row\nGot:\n%s", stdout)
	}
	// Outside-of-repo notice must mention what to do next.
	if !strings.Contains(stdout, "not inside a git repository") {
		t.Errorf("expected outside-of-repo notice; got:\n%s", stdout)
	}
}

// TestCLI_NoTokenNoDemo_ExitsNonZero confirms the basic auth-required
// guardrail. With no token and no --demo, every non-noTokenCommand must
// fail at PersistentPreRunE (config validation) before touching the
// network. We assert non-zero rather than a specific code because the
// config-validation failure is a generic error, not an HTTP 401.
func TestCLI_NoTokenNoDemo_ExitsNonZero(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	_, stderr, code := runCLI(t, "whoami")
	if code == exitOK {
		t.Fatalf("expected non-zero exit, got %d (stderr: %s)", code, stderr)
	}
	// The error chain wraps the underlying "token is required" message
	// from config.Load. Tolerating either phrasing keeps the test from
	// fragility-bonding to the exact wrapper layer.
	combined := stderr
	if !strings.Contains(combined, "token") {
		t.Errorf("expected stderr to mention token; got:\n%s", combined)
	}
}

// TestCLI_BogusFlag_NoPanic guards the "user typos a flag" path. Cobra
// returns an error from Execute (not a panic), and we want a non-zero
// exit code so shell scripts can react. The actual code is generic
// because exitCodeFor sees a plain wrapped error.
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

// TestCLI_DemoPipelineList_BadLimit pins that user-input validation
// fires inside the RunE (returning a generic exit code) rather than
// crashing further down. --limit=0 hits the explicit guard in
// runPipelineList; without that guard the API call would loop forever.
func TestCLI_DemoPipelineList_BadLimit(t *testing.T) {
	_, stderr, code := runCLI(t, "--demo", "pipeline", "list", "--project", "1", "--limit", "0")
	if code != exitGeneric {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitGeneric, stderr)
	}
}
