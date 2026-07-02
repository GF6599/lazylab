package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestLoadDefaultsFromEnv: with only GITLAB_TOKEN set, every other setting
// resolves to its compiled default.
// Given GITLAB_TOKEN in the environment and no flags, config file, or
// GITLAB_HOST, when Load runs, then the token comes from the env var and the
// host, projects-per-page, and log level are the defaults.
// Why it matters: this is the minimal documented setup (export GITLAB_TOKEN
// and run), and a regression would either reject it at startup or send the
// token to a host other than gitlab.com.
func TestLoadDefaultsFromEnv(t *testing.T) {
	// Given: a token in the environment and nothing else configured
	t.Setenv("GITLAB_TOKEN", "env-token")
	t.Setenv("GITLAB_HOST", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	// When: we load the config
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Then: the env token is used and every other setting is its default
	if got, want := cfg.Host, defaultHost; got != want {
		t.Fatalf("host mismatch: got %s want %s", got, want)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("token mismatch: %s", cfg.Token)
	}
	if got, want := cfg.ProjectsPerPage, defaultProjectsPerPage; got != want {
		t.Fatalf("per page mismatch: %d != %d", got, want)
	}
	if got, want := cfg.LogLevel, defaultLogLevel; got != want {
		t.Fatalf("log level mismatch: %s != %s", got, want)
	}
}

// TestLoadFromConfigFile: a --config file supplies every setting when flags
// and env are silent.
// Given no GITLAB_TOKEN and --config pointing at testdata/sample.yaml, when
// Load runs, then the host, token, projects-per-page, and log level all come
// from the file and the loaded path is recorded.
// Why it matters: config-file users set their token nowhere else, so a file
// value that stopped being read would abort their startup, and losing the
// recorded path would hide which file the settings came from.
func TestLoadFromConfigFile(t *testing.T) {
	// Given: no env token and a config file supplying every setting
	t.Setenv("GITLAB_TOKEN", "")
	samplePath := filepath.Join("testdata", "sample.yaml")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse([]string{"--" + FlagConfig, samplePath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	// When: we load the config
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Then: every setting comes from the file and its path is recorded
	if cfg.Host != "https://gitlab.example.com" {
		t.Fatalf("unexpected host: %s", cfg.Host)
	}
	if cfg.Token != "file-token" {
		t.Fatalf("unexpected token: %s", cfg.Token)
	}
	if cfg.ProjectsPerPage != 12 {
		t.Fatalf("unexpected per page: %d", cfg.ProjectsPerPage)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("unexpected log level: %s", cfg.LogLevel)
	}
	if cfg.ConfigFile == "" {
		t.Fatalf("config path not recorded")
	}
}

// TestFlagOverrides: CLI flags beat both the config file and the environment
// for every setting.
// Given a config file, a GITLAB_TOKEN env var, and explicit --host, --token,
// --projects-per-page, and --log-level flags, when Load runs, then every
// resolved value is the flag's.
// Why it matters: flags are the user's most explicit instruction, so a
// precedence slip here silently ignores what they typed, e.g. keeping the env
// token while pointing it at the flag's host.
func TestFlagOverrides(t *testing.T) {
	// Given: an env token, a config file, and flags overriding every setting
	t.Setenv("GITLAB_TOKEN", "env")
	samplePath := filepath.Join("testdata", "sample.yaml")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	args := []string{
		"--" + FlagConfig, samplePath,
		"--" + FlagHost, "https://override.example.com",
		"--" + FlagProjectsPerPage, "50",
		"--" + FlagLogLevel, "debug",
		"--" + FlagToken, "flag-token",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	// When: we load the config
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Then: the flag values win across the board
	if cfg.Host != "https://override.example.com" {
		t.Fatalf("flag host not applied: %s", cfg.Host)
	}
	if cfg.ProjectsPerPage != 50 {
		t.Fatalf("flag per page not applied: %d", cfg.ProjectsPerPage)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("flag log level not applied: %s", cfg.LogLevel)
	}
	if cfg.Token != "flag-token" {
		t.Fatalf("flag token not applied: %s", cfg.Token)
	}
}

// TestLoad_MissingToken: with no token from any source, Load fails with the
// token-required error.
// Given no --token, no GITLAB_TOKEN, no config file, and no glab resolver
// wired, when Load runs, then it returns an error saying a token is required.
// Why it matters: without this guard the app would start unauthenticated and
// fail later with an opaque 401 instead of an actionable startup message.
func TestLoad_MissingToken(t *testing.T) {
	// Given: no token from flag, env, config file, or glab
	t.Setenv("GITLAB_TOKEN", "")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)

	// When: we load the config
	_, err := Load(fs)

	// Then: it fails with the token-required message
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLoad_InvalidHost: a host without a scheme or with a non-HTTP scheme is
// rejected at load time.
// Given --host values missing a scheme or using ftp://, when Load validates
// them, then each fails with an error naming the specific problem.
// Why it matters: an unvalidated host would let the client fire the user's
// token at a nonsensical endpoint and surface a confusing transport error
// instead of a fixable config message.
func TestLoad_InvalidHost(t *testing.T) {
	// Given: a token present and host values that are not valid HTTP(S) URLs
	t.Setenv("GITLAB_TOKEN", "tok")
	tests := []struct {
		name string
		host string
		want string
	}{
		{"no scheme", "gitlab.com", "must include a scheme"},
		{"ftp scheme", "ftp://gitlab.com", "http or https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
			RegisterFlags(fs)
			_ = fs.Parse([]string{"--host", tt.host})

			// When: we load the config
			_, err := Load(fs)

			// Then: it fails and the error names the problem
			if err == nil {
				t.Fatal("expected error for invalid host")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q should contain %q", err, tt.want)
			}
		})
	}
}

// TestLoad_ProjectsPerPageRange: a --projects-per-page above the GitLab API
// ceiling of 100 is rejected.
// Given --projects-per-page 200, when Load validates it, then it fails with
// an error naming the 1 to 100 range.
// Why it matters: GitLab silently caps per_page at 100, so accepting a larger
// value would make every project fetch quietly return fewer results than the
// user asked for, with no error signal.
//
// Only the ceiling can error: Load replaces zero and negative values with the
// default before validating, so there is no lower-bound failure to assert.
func TestLoad_ProjectsPerPageRange(t *testing.T) {
	// Given: a token and a per-page value above the API maximum
	t.Setenv("GITLAB_TOKEN", "tok")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--projects-per-page", "200"})

	// When: we load the config
	_, err := Load(fs)

	// Then: it fails naming the valid range
	if err == nil {
		t.Fatal("expected error for per-page > 100")
	}
	if !strings.Contains(err.Error(), "between 1 and 100") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLoad_ConfigPathNormalization: messy --config paths are cleaned before
// the file is read and the path recorded.
// Given --config spellings with a ".." segment, a trailing or doubled
// separator, and a leading "./", when Load runs, then ConfigFile records the
// cleaned path and the file behind it was actually read.
// Why it matters: users pass relative and shell-completed paths, and a
// regression could fail to read the file behind a messy-but-valid path or
// report a denormalized path when tracing where settings came from.
func TestLoad_ConfigPathNormalization(t *testing.T) {
	// Given: no env token and --config paths in assorted messy spellings
	t.Setenv("GITLAB_TOKEN", "")
	tests := []struct {
		name     string
		path     string
		wantFile string
	}{
		{
			"dotdot segment",
			filepath.Join("testdata", "subdir", "..", "sample.yaml"),
			filepath.Join("testdata", "sample.yaml"),
		},
		{
			"trailing slash removed",
			filepath.Join("testdata", "sample.yaml") + string(filepath.Separator),
			filepath.Join("testdata", "sample.yaml"),
		},
		{
			"double separator",
			"testdata" + string(filepath.Separator) + string(filepath.Separator) + "sample.yaml",
			filepath.Join("testdata", "sample.yaml"),
		},
		{
			"dot current dir",
			filepath.Join(".", "testdata", "sample.yaml"),
			filepath.Join("testdata", "sample.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
			RegisterFlags(fs)
			if err := fs.Parse([]string{"--" + FlagConfig, tt.path}); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			// When: we load the config
			cfg, err := Load(fs)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}

			// Then: the recorded path is the cleaned form
			if cfg.ConfigFile != tt.wantFile {
				t.Fatalf("config file path mismatch: got %q, want %q", cfg.ConfigFile, tt.wantFile)
			}
			// And: the file at the normalized path was actually read
			if cfg.Token != "file-token" {
				t.Fatalf("config not loaded from normalized path: token = %q", cfg.Token)
			}
		})
	}
}

// TestLoad_InvalidLogLevel: an unrecognized --log-level is rejected.
// Given --log-level verbose, when Load validates it, then it fails with an
// invalid-log-level error.
// Why it matters: an unvalidated level would flow into logger setup as an
// unusable value, silently giving the user a verbosity they did not choose
// instead of a startup message naming the valid options.
func TestLoad_InvalidLogLevel(t *testing.T) {
	// Given: a token and a log level that is not one of the known names
	t.Setenv("GITLAB_TOKEN", "tok")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--log-level", "verbose"})

	// When: we load the config
	_, err := Load(fs)

	// Then: it fails with the invalid-log-level error
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
	if !strings.Contains(err.Error(), "invalid log level") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLoad_FallsBackToGlabCredentials: with no token from flag, env, or
// config file, Load adopts the token and host the glab resolver supplies.
// Given empty GITLAB_TOKEN and GITLAB_HOST and a resolver holding stored
// credentials, when Load runs, then the config carries the glab token and the
// glab host together.
// Why it matters: this is what lets a glab-authed user run lazylab with no
// GITLAB_TOKEN, and the host must come along since the token is host-scoped.
func TestLoad_FallsBackToGlabCredentials(t *testing.T) {
	// Given: no token or host from flags, env, or config file, and a resolver
	// with stored credentials
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_HOST", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)

	resolver := func(string) (string, string, bool) {
		return "glpat-from-glab", "https://gitlab.example.com", true
	}

	// When: we load the config with the glab fallback wired
	cfg, err := Load(fs, WithGlabResolver(resolver))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Then: the glab token and its host are adopted together
	if cfg.Token != "glpat-from-glab" {
		t.Error("token was not taken from the glab fallback")
	}
	if cfg.Host != "https://gitlab.example.com" {
		t.Errorf("host = %q, want the glab host", cfg.Host)
	}
}

// TestLoad_GlabFallbackYieldsToEnvToken: an explicit token outranks the glab
// fallback, and glab's host is not adopted alongside it.
// Given GITLAB_TOKEN set and a resolver offering a different token and host,
// when Load runs, then the env token wins and the host stays the default
// rather than glab's.
// Why it matters: letting the fallback shadow an explicit token would ignore
// the credential the user chose, and adopting glab's host next to an env
// token would send that token to a host it was never issued for.
func TestLoad_GlabFallbackYieldsToEnvToken(t *testing.T) {
	// Given: a token in the environment and a resolver offering different
	// credentials
	t.Setenv("GITLAB_TOKEN", "env-token")
	t.Setenv("GITLAB_HOST", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)

	resolver := func(string) (string, string, bool) {
		return "glab-token", "https://glab.example.com", true
	}

	// When: we load the config with the glab fallback wired
	cfg, err := Load(fs, WithGlabResolver(resolver))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Then: the env token wins and glab's host is not adopted
	if cfg.Token != "env-token" {
		t.Error("env token must win over the glab fallback")
	}
	if cfg.Host != defaultHost {
		t.Errorf("host = %q, want default %q; glab host must not be adopted when env supplies the token", cfg.Host, defaultHost)
	}
}

// TestLoad_GlabFallbackKeepsExplicitHost: an explicit --host stays in force
// when the token comes from the glab fallback.
// Given no token from flags, env, or config file, an explicit --host, and a
// resolver that returns a token together with a different host of its own,
// when Load consults the glab resolver, then the config keeps the explicit
// host and carries only the resolver's token.
// Why it matters: if setting --host disabled the fallback or the fallback
// replaced the host, a glab-authed user targeting a specific instance would
// hit a spurious token-required failure or land on the wrong host.
func TestLoad_GlabFallbackKeepsExplicitHost(t *testing.T) {
	// Given: no token from other sources and an explicit --host
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("LAZYLAB_CONFIG", "")
	t.Setenv("GITLAB_TUI_CONFIG", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--host", "https://my.gitlab.test"})

	// And: a resolver whose own host differs, so keeping versus adopting is
	// distinguishable in the assertion below.
	resolver := func(hostHint string) (string, string, bool) {
		return "glab-token", "https://rogue.example.com", true
	}

	// When: we load the config with the glab fallback wired
	cfg, err := Load(fs, WithGlabResolver(resolver))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Then: the explicit host is kept, never the resolver's
	if cfg.Host != "https://my.gitlab.test" {
		t.Errorf("host = %q, want the explicit --host; glab host must not override it", cfg.Host)
	}
	if cfg.Token != "glab-token" {
		t.Errorf("token = %q, want the glab fallback token", cfg.Token)
	}
}

// TestLoad_GlabFallbackScopesToExplicitHost: the resolver is asked for
// credentials scoped to the configured host, and a not-ok answer leaves the
// token empty so Load fails.
// Given an explicit --host, no token from any source, and a resolver with
// nothing stored for that host, when Load runs, then the resolver receives
// the configured host as its hint and Load returns the token-required error.
// Why it matters: adopting glab's default-host token for a different,
// explicitly configured host would send the credential to an instance it was
// never issued for.
func TestLoad_GlabFallbackScopesToExplicitHost(t *testing.T) {
	// Given: an explicit host, no token from any source, and a resolver with
	// nothing stored for that host
	t.Setenv("GITLAB_TOKEN", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--host", "https://gitlab.internal.example"})

	var gotHint string
	resolver := func(hostHint string) (string, string, bool) {
		gotHint = hostHint
		return "", "", false
	}

	// When: we load the config
	_, err := Load(fs, WithGlabResolver(resolver))

	// Then: the resolver saw the configured host and no token was adopted
	if gotHint != "https://gitlab.internal.example" {
		t.Errorf("resolver hint = %q, want the configured host", gotHint)
	}
	if err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("err = %v, want the token-required error", err)
	}
}

// TestLoad_GlabFallbackSkippedInDemo: demo mode never consults glab.
// Given no token and the --demo flag, when Load runs, then it succeeds
// without the resolver ever being called.
// Why it matters: demo mode exists to run with zero credentials, and a glab
// call there could block startup on a keyring prompt or pull a real token
// into a mode meant to stay offline.
func TestLoad_GlabFallbackSkippedInDemo(t *testing.T) {
	// Given: no token and demo mode requested
	t.Setenv("GITLAB_TOKEN", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--demo"})

	called := false
	resolver := func(string) (string, string, bool) {
		called = true
		return "glab-token", "https://glab.example.com", true
	}

	// When: we load the config in demo mode
	if _, err := Load(fs, WithGlabResolver(resolver)); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Then: the resolver was never consulted
	if called {
		t.Error("glab resolver must not be consulted in demo mode")
	}
}
