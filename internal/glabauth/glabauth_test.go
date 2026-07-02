package glabauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestResolve_BuildsHostURLFromGlabConfig: the host URL is assembled from
// glab's stored host and protocol, and the token passes through untouched.
// Given glab reporting a token, a bare host, and a protocol, when resolveWith
// runs without a host hint, then it returns the token with the host spelled
// as <protocol>://<host>.
// Why it matters: this is what lets a glab-authed user run lazylab without a
// separate GITLAB_TOKEN, so the token and the host it is scoped to must come
// from glab together.
func TestResolve_BuildsHostURLFromGlabConfig(t *testing.T) {
	// Given: glab reports a token, a bare host, and a protocol
	run := func(_ string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "config get token":
			return "glpat-stored-token", "", nil
		case "config get host":
			return "gitlab.example.com", "", nil
		case "config get api_protocol":
			return "https", "", nil
		}
		return "", "", nil
	}

	// When: we resolve credentials through that runner
	token, host, ok := resolveWith(run, "")

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

// TestResolve_NoTokenYieldsNotOK: a glab with a host but no stored token
// resolves to not-ok.
// Given glab reporting a host but no token from any lookup, when resolveWith
// runs, then it reports no usable credentials.
// Why it matters: a half-configured glab (host set, never logged in) is
// common, and answering ok here would hand config.Load an empty token and a
// host as if they were real credentials.
func TestResolve_NoTokenYieldsNotOK(t *testing.T) {
	// Given: glab is configured but has no token anywhere
	run := func(_ string, args ...string) (string, string, error) {
		if strings.Join(args, " ") == "config get host" {
			return "gitlab.com", "", nil
		}
		return "", "", nil
	}

	// When/Then: resolution reports no usable credentials
	if _, _, ok := resolveWith(run, ""); ok {
		t.Error("ok = true, want false when glab has no token")
	}
}

// TestResolve_GlabUnavailableYieldsNotOK: a missing glab binary resolves to
// not-ok.
// Given a runner that errors the way exec does when glab is not on PATH, when
// resolveWith runs, then it reports no usable credentials.
// Why it matters: glab is optional, so a machine without it must get a quiet
// "no credentials" answer and proceed to the normal token-required guidance
// instead of crashing at startup.
func TestResolve_GlabUnavailableYieldsNotOK(t *testing.T) {
	// Given: glab is not on PATH, so the runner errors
	run := func(_ string, _ ...string) (string, string, error) {
		return "", "", errors.New(`exec: "glab": executable file not found in $PATH`)
	}

	// When/Then: resolution reports no usable credentials rather than panicking
	if _, _, ok := resolveWith(run, ""); ok {
		t.Error("ok = true, want false when glab is not installed")
	}
}

// TestResolve_DefaultsHostAndProtocolWhenAbsent: a token with no stored host
// or protocol resolves against https://gitlab.com.
// Given glab returning a token but empty host and protocol, when resolveWith
// runs, then the host falls back to https://gitlab.com.
// Why it matters: glab itself defaults to gitlab.com over https, so any other
// fallback would send the stored token to a host glab never meant it for.
func TestResolve_DefaultsHostAndProtocolWhenAbsent(t *testing.T) {
	// Given: glab returns a token but empty host and protocol
	run := func(_ string, args ...string) (string, string, error) {
		if strings.Join(args, " ") == "config get token" {
			return "glpat-x", "", nil
		}
		return "", "", nil
	}

	// When: we resolve
	token, host, ok := resolveWith(run, "")

	// Then: the host falls back to https://gitlab.com
	if !ok || token != "glpat-x" {
		t.Fatalf("ok = %v, token = %q", ok, token)
	}
	if host != "https://gitlab.com" {
		t.Errorf("host = %q, want https://gitlab.com default", host)
	}
}

// TestResolve_HostWithSchemeUsedVerbatim: a stored host that already carries
// a scheme is used as-is.
// Given glab's stored host already including https:// (a GL_HOST
// passthrough), when resolveWith runs, then the returned host is that value
// verbatim with no second scheme prepended.
// Why it matters: glab passes GITLAB_HOST/GL_HOST values straight through, so
// "glab config get host" can print https://gitlab.example.com, and prepending
// a protocol again would send lazylab to https://https://gitlab.example.com.
func TestResolve_HostWithSchemeUsedVerbatim(t *testing.T) {
	// Given: glab's stored host already includes a scheme (GL_HOST passthrough)
	run := func(_ string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "config get token":
			return "glpat-x", "", nil
		case "config get host":
			return "https://gitlab.example.com", "", nil
		}
		return "", "", nil
	}

	// When: we resolve
	_, host, ok := resolveWith(run, "")

	// Then: the host is used as-is, with no second scheme prepended
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if host != "https://gitlab.example.com" {
		t.Errorf("host = %q, want https://gitlab.example.com verbatim", host)
	}
}

// TestResolve_ReadsPerHostTokenFromConfig: a token stored under glab's
// per-host config section is found and paired with its host.
// Given no top-level token but one stored for glab's default host, when
// resolveWith runs without a host hint, then the per-host token comes back
// together with that host.
// Why it matters: "glab auth login" stores the token under hosts.<host>,
// where a plain "glab config get token" does not see it, so without the
// --host lookup the standard glab setup would resolve to nothing.
func TestResolve_ReadsPerHostTokenFromConfig(t *testing.T) {
	// Given: no top-level token, but one stored for glab's default host
	run := func(_ string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "config get host":
			return "gitlab.com", "", nil
		case "config get token --host gitlab.com":
			return "glpat-per-host", "", nil
		}
		return "", "", nil
	}

	// When: we resolve without a host hint
	token, host, ok := resolveWith(run, "")

	// Then: the per-host token is found and paired with its host
	if !ok || token != "glpat-per-host" {
		t.Fatalf("ok = %v, token = %q, want the per-host token", ok, token)
	}
	if host != "https://gitlab.com" {
		t.Errorf("host = %q, want https://gitlab.com", host)
	}
}

// TestResolve_KeyringTokenViaAuthStatus: a keyring-stored token is recovered
// via "glab auth status --show-token" when the config file holds none.
// Given no token in glab's config but an auth status report naming one on
// stderr, when resolveWith runs without a host hint, then the full token,
// dotted suffix included, is parsed out of the report.
// Why it matters: keyring logins never write the token to glab's config file,
// so a user who ran "glab auth login" with keyring storage would otherwise be
// told no token exists.
func TestResolve_KeyringTokenViaAuthStatus(t *testing.T) {
	// Given: no token in glab's config, but auth status reports one on stderr
	statusReport := "gitlab.com\n" +
		"  ✓ Logged in to gitlab.com as someone (keyring)\n" +
		"  ✓ Token found: glpat-keyring.01.suffix"
	run := func(_ string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "config get host":
			return "gitlab.com", "", nil
		case "auth status --hostname gitlab.com --show-token":
			return "", statusReport, nil
		}
		return "", "", nil
	}

	// When: we resolve without a host hint
	token, host, ok := resolveWith(run, "")

	// Then: the full token, dotted suffix included, comes from the status report
	if !ok {
		t.Fatal("ok = false, want true when auth status reports a token")
	}
	if token != "glpat-keyring.01.suffix" {
		t.Errorf("token = %q, want the keyring token from auth status", token)
	}
	if host != "https://gitlab.com" {
		t.Errorf("host = %q, want https://gitlab.com", host)
	}
}

// TestResolve_HostHintScopesTokenLookup: a host hint restricts the token
// lookup to that host.
// Given glab holding both a default-host token and a different one for the
// hinted host, when resolveWith runs with the hint, then only the hinted
// host's token is returned and the hint comes back verbatim as the host.
// Why it matters: a token glab stored for gitlab.com must never be sent to a
// different, explicitly configured host, so the hinted lookup goes through
// "glab config get token --host <hostname>" only.
func TestResolve_HostHintScopesTokenLookup(t *testing.T) {
	// Given: glab has a default-host token and a different one for the hinted host
	run := func(_ string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "config get token":
			return "glpat-default", "", nil
		case "config get token --host gitlab.internal.example":
			return "glpat-internal", "", nil
		}
		return "", "", nil
	}

	// When: we resolve with an explicit host hint
	token, host, ok := resolveWith(run, "https://gitlab.internal.example")

	// Then: only the token stored for that host is returned, host kept verbatim
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if token != "glpat-internal" {
		t.Errorf("token = %q, want the token scoped to the hinted host", token)
	}
	if host != "https://gitlab.internal.example" {
		t.Errorf("host = %q, want the hint verbatim", host)
	}
}

// TestResolve_HostHintWithoutStoredTokenYieldsNotOK: a hint for a host glab
// holds no token for yields nothing, not the default-host token.
// Given glab holding only a default-host token and an auth status that
// rejects the hinted host, when resolveWith runs with the hint, then it
// returns empty credentials and not-ok.
// Why it matters: pairing glab's default-host token with an explicitly
// configured host would send the credential to an instance it was never
// issued for.
func TestResolve_HostHintWithoutStoredTokenYieldsNotOK(t *testing.T) {
	// Given: glab has a default-host token but nothing for the hinted host
	run := func(_ string, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "config get token":
			return "glpat-default", "", nil
		case "auth status --hostname gitlab.internal.example --show-token":
			return "", "x gitlab.internal.example has not been authenticated with glab", errors.New("exit status 1")
		}
		return "", "", nil
	}

	// When: we resolve with an explicit host hint
	token, host, ok := resolveWith(run, "https://gitlab.internal.example")

	// Then: no credentials are returned at all
	if ok || token != "" || host != "" {
		t.Errorf("got (%q, %q, %v), want empty not-ok result", token, host, ok)
	}
}

// TestResolve_HostHintKeyringTokenViaAuthStatus: the hinted lookup falls back
// to auth status when the host's config entry is empty.
// Given no config-file token for the hinted host but an auth status report
// naming one, when resolveWith runs with the hint, then that keyring token is
// returned paired with the hinted host.
// Why it matters: keyring users on a self-hosted instance have no token in
// the config file either, so without the same fallback the hinted flow would
// report an authenticated glab as having no credentials.
func TestResolve_HostHintKeyringTokenViaAuthStatus(t *testing.T) {
	// Given: no config token for the hinted host, but auth status reports one
	run := func(_ string, args ...string) (string, string, error) {
		if strings.Join(args, " ") == "auth status --hostname gitlab.internal.example --show-token" {
			return "", "  ✓ Token found: glpat-internal.01.suffix", nil
		}
		return "", "", nil
	}

	// When: we resolve with an explicit host hint
	token, host, ok := resolveWith(run, "https://gitlab.internal.example")

	// Then: the keyring token for that host is paired with the hint
	if !ok || token != "glpat-internal.01.suffix" {
		t.Fatalf("ok = %v, token = %q, want the hinted host's keyring token", ok, token)
	}
	if host != "https://gitlab.internal.example" {
		t.Errorf("host = %q, want the hint verbatim", host)
	}
}

// TestExecRunner_TimesOutOnHangingCommand: a command that outlives the
// deadline is cut off with an error.
// Given glabTimeout shortened to 50ms and a command that sleeps for 5s, when
// execRunner runs it, then an error comes back well before the sleep could
// finish.
// Why it matters: a token behind a locked OS keyring makes glab block on an
// unlock prompt, and lazylab must not freeze at startup waiting for it.
func TestExecRunner_TimesOutOnHangingCommand(t *testing.T) {
	// Given: a command that outlives the (shortened) deadline
	restore := glabTimeout
	glabTimeout = 50 * time.Millisecond
	defer func() { glabTimeout = restore }()

	// When: the runner executes it
	start := time.Now()
	_, _, err := execRunner("sleep", "5")

	// Then: it errors out promptly instead of hanging
	if err == nil {
		t.Fatal("err = nil, want a deadline error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("runner took %v, want a prompt timeout", elapsed)
	}
}
