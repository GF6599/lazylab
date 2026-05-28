// Package gitcontext extracts the surrounding git repository's identity
// (remote host, project path, branch, commit SHA) so CLI subcommands can
// default their --project and pipeline-ref arguments without forcing the
// user to type them out.
//
// The package shells out to the `git` binary rather than depending on a
// pure-Go git library. Every lazylab user already has git installed (it's a
// hard prerequisite for using GitLab), and the snapshot of repo state we
// need does not justify a heavy dependency. All git invocations are
// read-only — gitcontext never writes to .git, HEAD, or the working tree.
package gitcontext

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// Sentinel errors that callers may match with errors.Is to distinguish
// "not in a git repo" (often a graceful fallback to --project flag) from
// "repo has no usable remote" (often a hard error).
var (
	// ErrNotInRepo signals that the working directory is not inside a git
	// work tree. CLI subcommands should treat this as "git inference
	// unavailable" rather than a fatal error.
	ErrNotInRepo = errors.New("not inside a git repository")
	// ErrNoRemote signals that the repo exists but does not have the
	// configured remote (default "origin"). The repo is otherwise usable
	// — Branch and SHA can still be read — but no project can be inferred.
	ErrNoRemote = errors.New("repo has no usable remote")
)

// Context is a snapshot of git state at one point in time. Returned by
// Detect; consumers should treat it as immutable and re-Detect on
// long-running commands if they care about up-to-the-second freshness
// (the TUI does not; the CLI runs are short enough not to matter).
//
// Branch is empty when HEAD is detached — callers should fall back to SHA
// for ref lookups in that case rather than treating empty as an error.
type Context struct {
	// RemoteURL is the raw value of `git remote get-url <remote>`.
	RemoteURL string
	// Host is the hostname extracted from RemoteURL (no scheme, no port).
	// Compare to the configured GitLab host's hostname to detect
	// repo/host mismatch in multi-instance setups.
	Host string
	// ProjectPath is the namespace-qualified project slug
	// ("group/subgroup/project") with the .git suffix stripped. This is
	// what the GitLab API accepts as an alternative to a numeric project ID.
	ProjectPath string
	// Branch is the abbreviated HEAD ref. Empty for detached HEAD.
	Branch string
	// SHA is the full 40-character commit SHA at HEAD.
	SHA string
	// Remote records which remote name was used. Useful for diagnostics
	// when a user has both `origin` (their fork) and `upstream` (the main
	// project) configured.
	Remote string
}

// Options tweaks Detect's behavior. Both fields zero-default to the most
// common case (cwd, origin), so callers can pass Options{} for the default.
type Options struct {
	// Dir is the directory whose git state to inspect. Empty means cwd.
	Dir string
	// Remote is the remote name to read the URL from. Empty defaults to
	// "origin". Override when the user's workflow uses "upstream" or
	// similar.
	Remote string
}

// Detect inspects the working directory's git repository and returns a
// populated Context. Returns ErrNotInRepo when no .git is found upward
// from Dir, or ErrNoRemote when the repo exists but lacks the requested
// remote.
//
// Other failures (git binary missing, malformed remote URL, etc.) return
// a wrapped error from the underlying git invocation.
func Detect(opts Options) (*Context, error) {
	remote := opts.Remote
	if remote == "" {
		remote = "origin"
	}

	if out, err := gitOutput(opts.Dir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return nil, ErrNotInRepo
	}

	sha, err := gitOutput(opts.Dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("read HEAD: %w", err)
	}

	// `git symbolic-ref --short HEAD` cleanly distinguishes detached HEAD
	// (non-zero exit) from a real branch. `rev-parse --abbrev-ref HEAD`
	// returns the literal string "HEAD" in the detached case, which is a
	// less reliable signal.
	branch, _ := gitOutput(opts.Dir, "symbolic-ref", "--short", "HEAD")

	remoteURL, err := gitOutput(opts.Dir, "remote", "get-url", remote)
	if err != nil {
		return nil, ErrNoRemote
	}

	host, path, err := ParseRemoteURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("parse remote %q: %w", remote, err)
	}

	return &Context{
		RemoteURL:   remoteURL,
		Host:        host,
		ProjectPath: path,
		Branch:      branch,
		SHA:         sha,
		Remote:      remote,
	}, nil
}

// ParseRemoteURL extracts hostname and project path from any of the
// common git remote URL forms:
//
//   - SCP-style:  git@gitlab.com:group/project.git
//   - SSH URL:    ssh://git@gitlab.com:22/group/project.git
//   - HTTPS:      https://gitlab.com/group/project.git
//   - HTTPS+auth: https://user:token@gitlab.com/group/project.git
//
// Subgroups are preserved as path segments. The trailing ".git" and any
// leading/trailing slashes are stripped. Port numbers in the URL form
// are dropped from the returned host since the GitLab API does not vary
// by SSH port.
//
// Exposed (capital P) so other tools can normalize remotes without
// invoking git themselves.
func ParseRemoteURL(raw string) (host, path string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("empty remote URL")
	}

	// SCP form distinguished by the absence of "://" and the presence of
	// "@" — `git` itself uses this same heuristic in connect.c.
	if !strings.Contains(raw, "://") && strings.Contains(raw, "@") {
		_, rest, _ := strings.Cut(raw, "@")
		h, p, ok := strings.Cut(rest, ":")
		if !ok {
			return "", "", fmt.Errorf("malformed scp-style remote %q", raw)
		}
		host = h
		path = p
	} else {
		u, perr := url.Parse(raw)
		if perr != nil {
			return "", "", fmt.Errorf("parse url %q: %w", raw, perr)
		}
		// Restrict to schemes git itself transports over: anything else
		// (file://, gopher://, ftp://) is either nonsensical for GitLab
		// or a hint that the caller passed us something we should not be
		// silently massaging into a host/path pair.
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "ssh", "git":
			// supported
		default:
			return "", "", fmt.Errorf("unsupported remote url scheme %q in %q", u.Scheme, raw)
		}
		if u.Host == "" {
			return "", "", fmt.Errorf("remote url has no host: %q", raw)
		}
		// Hostname() correctly strips the port and handles bracketed
		// IPv6 literals (e.g. "[::1]:2222" → "::1"). LastIndex(":") on
		// the raw Host mangles IPv6 by trimming inside the address.
		host = u.Hostname()
		path = strings.TrimPrefix(u.Path, "/")
	}

	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" {
		return "", "", fmt.Errorf("remote url has no project path: %q", raw)
	}
	return host, path, nil
}

// gitOutput runs a git subcommand and returns its trimmed stdout. Stderr
// is captured and included in the error message so callers see the real
// git diagnostic instead of just an opaque exit-1.
//
// The parent environment is filtered before invocation so git's
// "context-overriding" env vars (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE)
// cannot redirect us to a different repo than `dir`. This matters when
// lazylab is launched from inside a rebase, a git hook, or a CI runner
// that exports those vars for its own purposes. We also forcibly disable
// interactive credential prompts (GIT_TERMINAL_PROMPT) and the optional-
// locks code path (GIT_OPTIONAL_LOCKS) since the CLI is short-lived and
// should never block on a tty input or contend on .git/index.lock.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	env := filterEnv(os.Environ(), "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE")
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// filterEnv returns env with any KEY=value entries whose KEY matches one
// of names removed. Case-sensitive (git env vars are all uppercase, and
// process env on macOS/Linux is case-sensitive too).
func filterEnv(env []string, names ...string) []string {
	skip := make(map[string]struct{}, len(names))
	for _, n := range names {
		skip[n] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		// Match KEY up to the first '='. Entries without '=' (rare but
		// legal) are kept as-is.
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}
