package glabauth

import (
	"errors"
	"strings"
	"testing"
)

// resolveWith assembles the host URL from glab's stored host + protocol and
// passes the token through untouched.
// Why it matters: this is what lets a glab-authed user run lazylab without a
// separate GITLAB_TOKEN, so the token and the host it is scoped to must come
// from glab together.
func TestResolve_BuildsHostURLFromGlabConfig(t *testing.T) {
	// Given: glab reports a token, a bare host, and a protocol
	run := func(_ string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "config get token":
			return "glpat-stored-token", nil
		case "config get host":
			return "gitlab.example.com", nil
		case "config get api_protocol":
			return "https", nil
		}
		return "", nil
	}

	// When: we resolve credentials through that runner
	token, host, ok := resolveWith(run)

	// Then: the token passes through and the host is <protocol>://<host>
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if token != "glpat-stored-token" {
		t.Errorf("token = %q, want the stored token", token)
	}
	if host != "https://gitlab.example.com" {
		t.Errorf("host = %q, want https://gitlab.example.com", host)
	}
}

func TestResolve_NoTokenYieldsNotOK(t *testing.T) {
	// Given: glab is configured but has no token
	run := func(_ string, args ...string) (string, error) {
		if strings.Join(args, " ") == "config get token" {
			return "", nil
		}
		return "gitlab.com", nil
	}

	// When/Then: resolution reports no usable credentials
	if _, _, ok := resolveWith(run); ok {
		t.Error("ok = true, want false when glab has no token")
	}
}

func TestResolve_GlabUnavailableYieldsNotOK(t *testing.T) {
	// Given: glab is not on PATH, so the runner errors
	run := func(_ string, _ ...string) (string, error) {
		return "", errors.New(`exec: "glab": executable file not found in $PATH`)
	}

	// When/Then: resolution reports no usable credentials rather than panicking
	if _, _, ok := resolveWith(run); ok {
		t.Error("ok = true, want false when glab is not installed")
	}
}

func TestResolve_DefaultsHostAndProtocolWhenAbsent(t *testing.T) {
	// Given: glab returns a token but empty host and protocol
	run := func(_ string, args ...string) (string, error) {
		if strings.Join(args, " ") == "config get token" {
			return "glpat-x", nil
		}
		return "", nil
	}

	// When: we resolve
	token, host, ok := resolveWith(run)

	// Then: the host falls back to https://gitlab.com
	if !ok || token != "glpat-x" {
		t.Fatalf("ok = %v, token = %q", ok, token)
	}
	if host != "https://gitlab.com" {
		t.Errorf("host = %q, want https://gitlab.com default", host)
	}
}
