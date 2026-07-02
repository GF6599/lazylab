// Package glabauth reads the GitLab credentials that the glab CLI has stored,
// so lazylab can authenticate without a separately-configured GITLAB_TOKEN when
// glab is already logged in. Tokens are read from glab's config file (per-host
// entry first, then the top-level key); keyring logins never write the token to
// that file, so resolution falls back to `glab auth status --show-token`, which
// prints the token glab would actually use for the host.
package glabauth

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// glabTimeout caps each glab invocation. A token behind a locked OS keyring
// makes glab block on an unlock prompt (pinentry, keychain), and lazylab's
// startup must not hang waiting for it. A variable so tests can shorten it.
var glabTimeout = 3 * time.Second

// runner executes a command and returns its trimmed stdout and stderr. It is
// injected so the resolution logic can be tested without a real glab binary.
type runner func(name string, args ...string) (stdout, stderr string, err error)

// execRunner runs a real command under glabTimeout, returning trimmed stdout
// and stderr. A non-zero exit, a timeout, or a missing binary surfaces as a
// non-nil error, which the caller treats as "no credentials available."
func execRunner(name string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), glabTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

// Resolve returns the token and host URL (e.g. "https://gitlab.com") that glab
// has stored. A non-empty hostHint (URL or bare hostname) restricts the lookup
// to credentials stored for that host, so a token issued for one GitLab
// instance is never sent to another. ok is false when glab is unavailable, not
// on PATH, or has no token for the relevant host. Callers must never log the
// returned token.
func Resolve(hostHint string) (token, host string, ok bool) {
	return resolveWith(execRunner, hostHint)
}

// resolveWith is the testable core of Resolve. A token is the gate: without
// one there is nothing usable.
func resolveWith(run runner, hostHint string) (token, host string, ok bool) {
	if hostHint != "" {
		token = tokenForHost(run, hostnameOf(hostHint))
		if token == "" {
			return "", "", false
		}
		return token, hostHint, true
	}
	return resolveDefaultHost(run)
}

// resolveDefaultHost handles the no-hint case: glab's own default host decides
// which credentials apply, so the token and the host it is scoped to travel
// together. Host and protocol default sensibly so a token from a minimally
// configured glab still targets gitlab.com over https.
func resolveDefaultHost(run runner) (token, host string, ok bool) {
	storedHost, _, _ := run("glab", "config", "get", "host")
	if storedHost == "" {
		storedHost = "gitlab.com"
	}

	token, _, err := run("glab", "config", "get", "token")
	if err != nil || token == "" {
		token = tokenForHost(run, hostnameOf(storedHost))
	}
	if token == "" {
		return "", "", false
	}
	return token, hostURL(run, storedHost), true
}

// tokenForHost finds the token glab has stored for hostname: the per-host
// config entry first, then `glab auth status`, which is the only way to read a
// token that lives in the OS keyring instead of the config file.
func tokenForHost(run runner, hostname string) string {
	token, _, err := run("glab", "config", "get", "token", "--host", hostname)
	if err == nil && token != "" {
		return token
	}
	return authStatusToken(run, hostname)
}

// authStatusToken parses the token out of `glab auth status --show-token` for
// hostname. glab writes the status report to stderr, but both streams are
// scanned in case that ever moves. An unauthenticated host exits non-zero and
// prints no "Token found" line, so it falls through to "".
func authStatusToken(run runner, hostname string) string {
	stdout, stderr, err := run("glab", "auth", "status", "--hostname", hostname, "--show-token")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(stdout+"\n"+stderr, "\n") {
		if _, after, found := strings.Cut(line, "Token found:"); found {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// hostURL builds the base URL for storedHost. glab passes GITLAB_HOST/GL_HOST
// values through verbatim, so the stored host may already carry a scheme;
// prepending another would produce "https://https://<host>".
func hostURL(run runner, storedHost string) string {
	if strings.Contains(storedHost, "://") {
		return storedHost
	}
	protocol, _, _ := run("glab", "config", "get", "api_protocol")
	if protocol == "" {
		protocol = "https"
	}
	return protocol + "://" + storedHost
}

// hostnameOf reduces a host URL (or bare hostname) to the hostname glab keys
// its per-host config by.
func hostnameOf(host string) string {
	if _, after, found := strings.Cut(host, "://"); found {
		host = after
	}
	host, _, _ = strings.Cut(host, "/")
	return host
}
