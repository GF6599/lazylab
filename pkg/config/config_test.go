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

// TestLoad_ProjectRemoteDefaults pins the Remote default and the
// empty-Project default. These two flags are the surface area subcommands
// rely on for project resolution, so a regression in defaults would
// silently change "where" diagnostics and "pipeline list" routing.
func TestLoad_ProjectRemoteDefaults(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	// Explicitly clear any inherited env that might shadow the defaults
	// when the test runs from a developer shell with these already set.
	t.Setenv("GITLAB_PROJECT", "")
	t.Setenv("LAZYLAB_PROJECT", "")
	t.Setenv("GITLAB_REMOTE", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Project != "" {
		t.Fatalf("default Project = %q, want empty", cfg.Project)
	}
	if cfg.Remote != defaultRemote {
		t.Fatalf("default Remote = %q, want %q", cfg.Remote, defaultRemote)
	}
}

// TestLoad_ProjectFromEnvPreferredName covers the canonical
// GITLAB_PROJECT env path (no legacy fallback in play).
func TestLoad_ProjectFromEnvPreferredName(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	t.Setenv("GITLAB_PROJECT", "group/canonical")
	t.Setenv("LAZYLAB_PROJECT", "")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Project != "group/canonical" {
		t.Fatalf("Project from env = %q, want %q", cfg.Project, "group/canonical")
	}
}

// TestLoad_ProjectLegacyEnvFallback pins that LAZYLAB_PROJECT still
// works when GITLAB_PROJECT is unset. This is the backward-compat
// contract for users who set the legacy var before the rename.
func TestLoad_ProjectLegacyEnvFallback(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	t.Setenv("GITLAB_PROJECT", "")
	t.Setenv("LAZYLAB_PROJECT", "legacy/project")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Project != "legacy/project" {
		t.Fatalf("Project from LAZYLAB_PROJECT = %q, want %q", cfg.Project, "legacy/project")
	}
}

// TestLoad_ProjectEnvPrecedence pins that GITLAB_PROJECT outranks
// LAZYLAB_PROJECT when both are set. viper's BindEnv stops on the first
// hit in registration order, which is the contract we want.
func TestLoad_ProjectEnvPrecedence(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	t.Setenv("GITLAB_PROJECT", "winner")
	t.Setenv("LAZYLAB_PROJECT", "loser")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse(nil)
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Project != "winner" {
		t.Fatalf("Project = %q, want GITLAB_PROJECT (\"winner\") to win", cfg.Project)
	}
}

// TestLoad_RemoteFlagOverridesEnv pins the standard precedence: a
// CLI --remote flag beats GITLAB_REMOTE.
func TestLoad_RemoteFlagOverridesEnv(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	t.Setenv("GITLAB_REMOTE", "from-env")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--remote", "from-flag"})
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Remote != "from-flag" {
		t.Fatalf("Remote = %q, want from-flag", cfg.Remote)
	}
}

// TestLoad_ProjectFlagOverridesEnv pins --project beating $GITLAB_PROJECT.
func TestLoad_ProjectFlagOverridesEnv(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "tok")
	t.Setenv("GITLAB_PROJECT", "env-project")

	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	RegisterFlags(fs)
	_ = fs.Parse([]string{"--project", "flag-project"})
	cfg, err := Load(fs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Project != "flag-project" {
		t.Fatalf("Project = %q, want flag-project", cfg.Project)
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
