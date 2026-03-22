// Package config resolves lazylab's runtime configuration from multiple
// sources with a strict precedence order: CLI flags beat environment
// variables, which beat config file values, which beat compiled defaults.
//
// The only required value is a GitLab personal access token (api scope).
// Everything else has sensible defaults targeting gitlab.com. Config files
// are optional and can be YAML, TOML, or JSON — Viper auto-detects the
// format from the file extension.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var validLogLevels = map[string]bool{
	"debug": true, "info": true, "warn": true, "error": true,
}

// maxProjectsPerPage mirrors the GitLab API's hard ceiling; requesting more
// would silently return fewer results without any error signal.
const maxProjectsPerPage = 100

const (
	// FlagHost selects the GitLab base URL.
	FlagHost = "host"
	// FlagToken holds the personal access token.
	FlagToken = "token"
	// FlagProjectsPerPage controls pagination for project fetches.
	FlagProjectsPerPage = "projects-per-page"
	// FlagConfig points to an optional config file understood by Viper.
	FlagConfig = "config"
	// FlagLogLevel chooses the log level emitted to stderr.
	FlagLogLevel = "log-level"
	// FlagDemo enables demo mode with fake data (no token required).
	FlagDemo = "demo"
	// FlagDiffContextLines controls how many unified-diff lines surround
	// positioned MR comments in the Comments tab. Env: GITLAB_DIFF_CONTEXT_LINES.
	FlagDiffContextLines = "diff-context-lines"

	defaultHost             = "https://gitlab.com"
	defaultProjectsPerPage  = 30
	defaultLogLevel         = "error"
	defaultDiffContextLines = 10
)

// Config holds the fully-resolved runtime settings after all sources have
// been merged and validated. All fields are guaranteed non-zero after a
// successful call to [Load], except ConfigFile which is empty when no
// config file was used.
type Config struct {
	Host            string
	Token           string
	ProjectsPerPage int
	LogLevel        string
	// Demo enables demo mode with fake data and no token requirement.
	Demo bool
	// DiffContextLines is the number of diff context lines to show around
	// positioned MR comments. 0 disables inline diff context.
	DiffContextLines int
	// ConfigFile records which file was loaded, if any. Empty when
	// configuration came entirely from flags, env vars, and defaults.
	ConfigFile string
}

// RegisterFlags defines the CLI flags on fs without parsing them. Call this
// before fs.Parse, then pass the parsed FlagSet to [Load]. Flags use empty
// defaults so that [Load] can distinguish "not set" from "set to the default
// value" and apply the correct precedence.
func RegisterFlags(fs *pflag.FlagSet) {
	fs.String(FlagHost, "", "GitLab host, defaults to https://gitlab.com")
	fs.String(FlagToken, "", "GitLab personal access token (api scope)")
	fs.Int(FlagProjectsPerPage, 0, "Number of projects to request per page")
	fs.String(FlagConfig, "", "Optional config file (YAML, TOML, JSON)")
	fs.String(FlagLogLevel, "", "Log level: debug, info, warn, error")
	fs.Bool(FlagDemo, false, "Run in demo mode with fake data (no token required)")
	fs.Int(FlagDiffContextLines, 0, "Number of diff context lines around MR comments (default 10, 0 = disabled)")
}

// Load merges configuration from defaults, an optional config file,
// environment variables (prefix GITLAB_), and CLI flags — in that precedence
// order — then validates the result. It returns an error if the token is
// missing, the host URL is malformed, or projects-per-page exceeds the
// GitLab API maximum of 100.
//
// The config file path is resolved from --config, then $LAZYLAB_CONFIG, then
// $GITLAB_TUI_CONFIG. The dual env var support exists for backward
// compatibility with an earlier project name.
func Load(fs *pflag.FlagSet) (Config, error) {
	var cfg Config

	v := viper.New()
	v.SetEnvPrefix("gitlab")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.SetDefault(FlagHost, defaultHost)
	v.SetDefault(FlagProjectsPerPage, defaultProjectsPerPage)
	v.SetDefault(FlagLogLevel, defaultLogLevel)
	v.SetDefault(FlagDiffContextLines, defaultDiffContextLines)
	v.AutomaticEnv()

	configPath, _ := fs.GetString(FlagConfig)
	if configPath == "" {
		configPath = os.Getenv("LAZYLAB_CONFIG")
		if configPath == "" {
			configPath = os.Getenv("GITLAB_TUI_CONFIG")
		}
	}
	if configPath != "" {
		configPath = filepath.Clean(configPath)
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return cfg, fmt.Errorf("load config %s: %w", configPath, err)
		}
		cfg.ConfigFile = configPath
	}

	cfg.Host = v.GetString(FlagHost)
	cfg.Token = v.GetString(FlagToken)
	cfg.ProjectsPerPage = v.GetInt(FlagProjectsPerPage)
	cfg.LogLevel = strings.ToLower(v.GetString(FlagLogLevel))
	cfg.DiffContextLines = v.GetInt(FlagDiffContextLines)

	overrideFromFlags(fs, &cfg)

	if cfg.Demo {
		if cfg.Host == "" {
			cfg.Host = defaultHost
		}
		if cfg.LogLevel == "" {
			cfg.LogLevel = defaultLogLevel
		}
		if cfg.ProjectsPerPage <= 0 {
			cfg.ProjectsPerPage = defaultProjectsPerPage
		}
		return cfg, nil
	}

	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if err := validateHostURL(cfg.Host); err != nil {
		return cfg, err
	}
	if cfg.ProjectsPerPage <= 0 {
		cfg.ProjectsPerPage = defaultProjectsPerPage
	}
	if cfg.ProjectsPerPage > maxProjectsPerPage {
		return cfg, fmt.Errorf("projects-per-page must be between 1 and %d, got %d", maxProjectsPerPage, cfg.ProjectsPerPage)
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if !validLogLevels[cfg.LogLevel] {
		return cfg, fmt.Errorf("invalid log level %q, must be one of: debug, info, warn, error", cfg.LogLevel)
	}
	if cfg.Token == "" {
		return cfg, fmt.Errorf("gitlab token is required via --%s or $GITLAB_TOKEN", FlagToken)
	}

	return cfg, nil
}

// overrideFromFlags applies only explicitly-set CLI flags, leaving Viper's
// merged values intact for anything the user didn't pass on the command line.
func overrideFromFlags(fs *pflag.FlagSet, cfg *Config) {
	if fs.Changed(FlagHost) {
		cfg.Host, _ = fs.GetString(FlagHost)
	}
	if fs.Changed(FlagToken) {
		cfg.Token, _ = fs.GetString(FlagToken)
	}
	if fs.Changed(FlagProjectsPerPage) {
		cfg.ProjectsPerPage, _ = fs.GetInt(FlagProjectsPerPage)
	}
	if fs.Changed(FlagLogLevel) {
		cfg.LogLevel = strings.ToLower(mustString(fs.GetString(FlagLogLevel)))
	}
	if fs.Changed(FlagConfig) {
		v, _ := fs.GetString(FlagConfig)
		cfg.ConfigFile = filepath.Clean(v)
	}
	if fs.Changed(FlagDemo) {
		cfg.Demo, _ = fs.GetBool(FlagDemo)
	}
	if fs.Changed(FlagDiffContextLines) {
		cfg.DiffContextLines, _ = fs.GetInt(FlagDiffContextLines)
	}
}

func mustString(val string, err error) string {
	if err != nil {
		return ""
	}
	return val
}

// validateHostURL rejects URLs without a scheme or hostname early, before
// the GitLab client attempts a request to a nonsensical endpoint.
func validateHostURL(host string) error {
	u, err := url.Parse(host)
	if err != nil {
		return fmt.Errorf("invalid host URL %q: %w", host, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("host URL must include a scheme (e.g., https://%s)", host)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("host URL must use http or https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("host URL must include a hostname")
	}
	return nil
}
