package gitcontext

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseRemoteURL covers every remote URL form that GitLab clients
// produce in the wild, including subgroups, embedded credentials, and the
// less common SSH-with-port URL form. A regression here breaks --project
// inference for the user's working tree, so the table is intentionally
// over-broad rather than minimal.
func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHost string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "scp-style gitlab.com",
			input:    "git@gitlab.com:foo/bar.git",
			wantHost: "gitlab.com",
			wantPath: "foo/bar",
		},
		{
			name:     "scp-style with subgroup",
			input:    "git@gitlab.mycompany.com:group/subgroup/project.git",
			wantHost: "gitlab.mycompany.com",
			wantPath: "group/subgroup/project",
		},
		{
			name:     "scp-style without .git suffix",
			input:    "git@gitlab.com:foo/bar",
			wantHost: "gitlab.com",
			wantPath: "foo/bar",
		},
		{
			name:     "https",
			input:    "https://gitlab.com/foo/bar.git",
			wantHost: "gitlab.com",
			wantPath: "foo/bar",
		},
		{
			name:     "https with embedded token",
			input:    "https://oauth2:glpat-secret@gitlab.com/foo/bar.git",
			wantHost: "gitlab.com",
			wantPath: "foo/bar",
		},
		{
			name:     "https with deep subgroup nesting",
			input:    "https://gitlab.example.com/a/b/c/d/project.git",
			wantHost: "gitlab.example.com",
			wantPath: "a/b/c/d/project",
		},
		{
			name:     "ssh url with port",
			input:    "ssh://git@gitlab.com:2222/foo/bar.git",
			wantHost: "gitlab.com",
			wantPath: "foo/bar",
		},
		{
			name:     "http (rare but valid)",
			input:    "http://gitlab.local/foo/bar.git",
			wantHost: "gitlab.local",
			wantPath: "foo/bar",
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "malformed scp without colon",
			input:   "git@gitlab.com/foo/bar",
			wantErr: true,
		},
		{
			name:    "url without path",
			input:   "https://gitlab.com",
			wantErr: true,
		},
		{
			// IPv6 literal — net/url.Hostname() must strip the
			// brackets and the trailing port. The previous
			// LastIndex(":") implementation truncated "[::1" which
			// then failed downstream DNS comparison.
			name:     "https ipv6 literal with port",
			input:    "https://[::1]:2222/group/project.git",
			wantHost: "::1",
			wantPath: "group/project",
		},
		{
			// Longer IPv6 form to exercise the multi-colon case
			// that most aggressively exposed the old port-stripping
			// bug.
			name:     "ssh ipv6 literal with port",
			input:    "ssh://git@[2001:db8::1]:22/group/project.git",
			wantHost: "2001:db8::1",
			wantPath: "group/project",
		},
		{
			// file:// is a local path. Allowing it would silently
			// treat "/tmp/repo" as a project path against the
			// configured GitLab host — a confusing failure mode.
			name:    "file scheme rejected",
			input:   "file:///tmp/repo",
			wantErr: true,
		},
		{
			// Any unknown scheme should fail loudly rather than
			// degrade to "host=…, path=…" guesswork.
			name:    "unsupported scheme rejected",
			input:   "gopher://gitlab.com/foo/bar",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, path, err := ParseRemoteURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got host=%q path=%q", host, path)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tc.wantHost {
				t.Errorf("host: got %q want %q", host, tc.wantHost)
			}
			if path != tc.wantPath {
				t.Errorf("path: got %q want %q", path, tc.wantPath)
			}
		})
	}
}

// TestDetect_NotInRepo verifies that a non-git directory yields the
// sentinel error rather than a generic git-invocation failure — so CLI
// commands can match on it and degrade gracefully to --project.
func TestDetect_NotInRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Detect(Options{Dir: dir})
	if !errors.Is(err, ErrNotInRepo) {
		t.Fatalf("got %v, want ErrNotInRepo", err)
	}
}

// TestDetect_FullCycle stands up a tiny git repo in a tempdir, adds a
// remote, makes a commit, and verifies all five Context fields are
// populated correctly. Exercises the integration between the SSH-form
// URL parser and the git-shellout layer in one go.
func TestDetect_FullCycle(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitInit(t, dir)
	mustGit(t, dir, "remote", "add", "origin", "git@gitlab.example.com:team/project.git")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "init")
	// Force a known branch name so the test is robust to the user's
	// init.defaultBranch config (could be main, master, or trunk).
	mustGit(t, dir, "branch", "-M", "trunk")

	ctx, err := Detect(Options{Dir: dir})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if ctx.Host != "gitlab.example.com" {
		t.Errorf("Host: got %q want gitlab.example.com", ctx.Host)
	}
	if ctx.ProjectPath != "team/project" {
		t.Errorf("ProjectPath: got %q want team/project", ctx.ProjectPath)
	}
	if ctx.Branch != "trunk" {
		t.Errorf("Branch: got %q want trunk", ctx.Branch)
	}
	if len(ctx.SHA) != 40 {
		t.Errorf("SHA: got %q (len=%d), want a 40-char SHA", ctx.SHA, len(ctx.SHA))
	}
	if ctx.Remote != "origin" {
		t.Errorf("Remote: got %q want origin", ctx.Remote)
	}
}

// TestDetect_DetachedHead verifies the documented "Branch is empty on
// detached HEAD" contract — callers depend on it to choose between
// branch-based and SHA-based pipeline lookups.
func TestDetect_DetachedHead(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitInit(t, dir)
	mustGit(t, dir, "remote", "add", "origin", "https://gitlab.com/foo/bar.git")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "first")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "second")
	// Detach by checking out HEAD~1 — current HEAD is now a SHA, not a branch.
	mustGit(t, dir, "checkout", "--detach", "HEAD~1")

	ctx, err := Detect(Options{Dir: dir})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if ctx.Branch != "" {
		t.Errorf("expected empty Branch on detached HEAD, got %q", ctx.Branch)
	}
	if len(ctx.SHA) != 40 {
		t.Errorf("SHA should still resolve on detached HEAD, got %q", ctx.SHA)
	}
}

// TestDetect_IgnoresGitEnv proves the env-scrubbing in gitOutput works:
// even with a poisoned GIT_DIR pointing at a nonexistent path, Detect
// still resolves the temp repo cleanly via cwd. Without scrubbing, git
// would prefer GIT_DIR and either fail outright or (worse) report
// state from an unrelated repo.
//
// Cannot use t.Parallel here — t.Setenv is incompatible with parallel
// tests by design (env is process-global).
func TestDetect_IgnoresGitEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Build the test repo first with a clean environment, then poison
	// GIT_DIR/GIT_WORK_TREE before calling Detect. If we set them up
	// front, the gitInit/mustGit helpers (which shell out to plain git
	// without scrubbing) would themselves fail. The contract under
	// test is specifically about Detect's own resilience.
	dir := t.TempDir()
	gitInit(t, dir)
	mustGit(t, dir, "remote", "add", "origin", "https://gitlab.com/team/proj.git")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "init")

	t.Setenv("GIT_DIR", "/nonexistent/.git")
	t.Setenv("GIT_WORK_TREE", "/nonexistent")

	ctx, err := Detect(Options{Dir: dir})
	if err != nil {
		t.Fatalf("detect with poisoned GIT_DIR: %v", err)
	}
	if ctx.ProjectPath != "team/proj" {
		t.Errorf("ProjectPath: got %q want team/proj", ctx.ProjectPath)
	}
	if ctx.Host != "gitlab.com" {
		t.Errorf("Host: got %q want gitlab.com", ctx.Host)
	}
}

// gitInit creates a minimal, isolated git repo in dir. Identity is set
// locally so the commit operations don't depend on the developer's global
// git config (CI runners often have no global identity at all).
func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "config", "commit.gpgsign", "false")
}

// mustGit runs git in dir and fails the test on non-zero exit. Output is
// surfaced into the test log so failures are diagnosable without rerunning.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), filepath.Base(dir), err, out)
	}
}
