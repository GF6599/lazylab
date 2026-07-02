package glabauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// resolveWith assembles the host URL from glab's stored host + protocol and
// passes the token through untouched.
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

// resolveWith keeps a stored host verbatim when it already carries a scheme.
// Why it matters: glab passes GITLAB_HOST/GL_HOST values straight through, so
// "glab config get host" can print https://gitlab.example.com; prepending a
// protocol again would send lazylab to https://https://gitlab.example.com.
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

// resolveWith finds a token stored under glab's per-host config section.
// Why it matters: "glab auth login" stores the token under hosts.<host>, where
// a plain "glab config get token" does not see it, so the standard glab setup
// only resolves through the --host lookup.
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

// resolveWith falls back to "glab auth status --show-token" when the config
// file holds no token.
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

// resolveWith scopes the token lookup to the host hint when one is given.
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

// resolveWith refuses to adopt a token stored for another host.
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

// resolveWith consults auth status for the hinted host when its config entry
// is empty.
// Why it matters: keyring users on a self-hosted instance have no token in the
// config file either, so the hinted flow needs the same keyring fallback.
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

// execRunner gives up when glab does not respond within the deadline.
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
