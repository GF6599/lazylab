package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestLoadDefaultsFromEnv(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "env-token")
	t.Setenv("GITLAB_HOST", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

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

func TestLoadFromConfigFile(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	samplePath := filepath.Join("testdata", "sample.yaml")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse([]string{"--" + FlagConfig, samplePath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

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

func TestFlagOverrides(t *testing.T) {
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

	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

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

func TestLoad_MissingToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)

	_, err := Load(fs)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidHost(t *testing.T) {
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
			_, err := Load(fs)
			if err == nil {
				t.Fatal("expected error for invalid host")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q should contain %q", err, tt.want)
			}
		})
	}
}

func TestLoad_ProjectsPerPageRange(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--projects-per-page", "200"})

	_, err := Load(fs)
	if err == nil {
		t.Fatal("expected error for per-page > 100")
	}
	if !strings.Contains(err.Error(), "between 1 and 100") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_ConfigPathNormalization(t *testing.T) {
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

			cfg, err := Load(fs)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}

			if cfg.ConfigFile != tt.wantFile {
				t.Fatalf("config file path mismatch: got %q, want %q", cfg.ConfigFile, tt.wantFile)
			}
			// Verify the config was actually loaded correctly.
			if cfg.Token != "file-token" {
				t.Fatalf("config not loaded from normalized path: token = %q", cfg.Token)
			}
		})
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--log-level", "verbose"})

	_, err := Load(fs)
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
	if !strings.Contains(err.Error(), "invalid log level") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLoad_FallsBackToGlabCredentials pins that, with no token from flag, env, or
// config file, Load adopts the token and host the glab resolver supplies.
// Why it matters: this is what lets a glab-authed user run lazylab with no
// GITLAB_TOKEN, and the host must come along since the token is host-scoped.
func TestLoad_FallsBackToGlabCredentials(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_HOST", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)

	resolver := func(string) (string, string, bool) {
		return "glpat-from-glab", "https://gitlab.example.com", true
	}
	cfg, err := Load(fs, WithGlabResolver(resolver))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "glpat-from-glab" {
		t.Error("token was not taken from the glab fallback")
	}
	if cfg.Host != "https://gitlab.example.com" {
		t.Errorf("host = %q, want the glab host", cfg.Host)
	}
}

// TestLoad_GlabFallbackYieldsToEnvToken pins that an explicit token outranks the
// glab fallback, and that glab's host is not adopted when the token came from env.
func TestLoad_GlabFallbackYieldsToEnvToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "env-token")
	t.Setenv("GITLAB_HOST", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)

	resolver := func(string) (string, string, bool) {
		return "glab-token", "https://glab.example.com", true
	}
	cfg, err := Load(fs, WithGlabResolver(resolver))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "env-token" {
		t.Error("env token must win over the glab fallback")
	}
	if cfg.Host != defaultHost {
		t.Errorf("host = %q, want default %q; glab host must not be adopted when env supplies the token", cfg.Host, defaultHost)
	}
}

// TestLoad_GlabFallbackKeepsExplicitHost pins that an explicit --host is kept even
// when the token comes from the glab fallback.
func TestLoad_GlabFallbackKeepsExplicitHost(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--host", "https://my.gitlab.test"})

	resolver := func(hostHint string) (string, string, bool) {
		return "glab-token", hostHint, true
	}
	cfg, err := Load(fs, WithGlabResolver(resolver))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Host != "https://my.gitlab.test" {
		t.Errorf("host = %q, want the explicit --host; glab host must not override it", cfg.Host)
	}
	if cfg.Token == "" {
		t.Error("expected the token to come from the glab fallback")
	}
}

// TestLoad_GlabFallbackScopesToExplicitHost pins that the resolver is asked for
// credentials scoped to the configured host, and that a not-ok answer leaves the
// token empty so Load fails.
// Why it matters: adopting glab's default-host token for a different, explicitly
// configured host would send the credential to an instance it was never issued
// for.
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

// TestLoad_GlabFallbackSkippedInDemo pins that demo mode never consults glab.
func TestLoad_GlabFallbackSkippedInDemo(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--demo"})

	called := false
	resolver := func(string) (string, string, bool) {
		called = true
		return "glab-token", "https://glab.example.com", true
	}
	if _, err := Load(fs, WithGlabResolver(resolver)); err != nil {
		t.Fatalf("load: %v", err)
	}
	if called {
		t.Error("glab resolver must not be consulted in demo mode")
	}
}
