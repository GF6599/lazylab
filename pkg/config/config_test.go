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
		{"no scheme", "gitlab.com", "http or https"},
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
