// Package glabauth reads the GitLab credentials that the glab CLI has stored, so
// lazylab can authenticate without a separately-configured GITLAB_TOKEN when glab
// is already logged in. It shells out to glab (rather than parsing glab's config
// file directly) because that transparently handles the OS-keyring case, where
// the token never touches the config file.
package glabauth

import (
	"os/exec"
	"strings"
)

// runner executes a command and returns its trimmed stdout. It is injected so
// the resolution logic can be tested without a real glab binary.
type runner func(name string, args ...string) (string, error)

// execRunner runs a real command, returning trimmed stdout. A non-zero exit (or
// a missing binary) surfaces as a non-nil error, which the caller treats as
// "no credentials available."
func execRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// Resolve returns the token and host URL (e.g. "https://gitlab.com") that glab is
// configured to use. ok is false when glab is unavailable, not on PATH, or has no
// token. Callers must never log the returned token.
func Resolve() (token, host string, ok bool) {
	return resolveWith(execRunner)
}

// resolveWith is the testable core of Resolve. A token is the gate: without one
// there is nothing usable. The host URL is assembled from glab's bare host and
// api_protocol, both of which default sensibly so a token from a minimally
// configured glab still targets gitlab.com over https.
func resolveWith(run runner) (token, host string, ok bool) {
	token, err := run("glab", "config", "get", "token")
	if err != nil || token == "" {
		return "", "", false
	}

	bareHost, _ := run("glab", "config", "get", "host")
	if bareHost == "" {
		bareHost = "gitlab.com"
	}

	protocol, _ := run("glab", "config", "get", "api_protocol")
	if protocol == "" {
		protocol = "https"
	}

	return token, protocol + "://" + bareHost, true
}
