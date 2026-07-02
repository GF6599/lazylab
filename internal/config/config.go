// Package config resolves lazylab's runtime configuration from multiple
// sources with a strict precedence order: CLI flags beat environment
// variables, which beat config file values, which beat credentials stored by
// the glab CLI (when injected via [WithGlabResolver]), which beat compiled
// defaults.
//
// A GitLab personal access token (api scope) is required, but an
// authenticated glab satisfies it. Everything else has sensible defaults
// targeting gitlab.com. Config files are optional and can be YAML, TOML, or
// JSON; Viper auto-detects the format from the file extension.
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

// Option configures Load. With no options, Load is pure: it reads only flags,
// environment variables, the config file, and compiled defaults.
type Option func(*loadConfig)

// loadConfig holds Load's optional injected dependencies.
type loadConfig struct {
	// glabResolver, when set, supplies a token and host to fall back on when no
	// token is otherwise provided. It receives the host resolved so far (empty
	// when none was configured) so it can scope the token to that host, and
	// returns ok=false when glab has nothing usable.
	glabResolver func(hostHint string) (token, host string, ok bool)
}

// WithGlabResolver makes Load fall back to glab's stored credentials when no
// token comes from a flag, environment variable, or config file. Load passes
// the host resolved so far (empty when none was configured) so the resolver
// only returns a token scoped to that host. The resolver is injected so the
// config package never depends on glab and stays testable.
func WithGlabResolver(r func(hostHint string) (token, host string, ok bool)) Option {
	return func(lc *loadConfig) { lc.glabResolver = r }
}

// Load merges configuration from defaults, glab's stored credentials (when
// injected via [WithGlabResolver] and nothing else supplies a token), an
// optional config file, environment variables (prefix GITLAB_), and CLI
// flags, in that precedence order, then validates the result.
//
// All errors are plain formatted strings except where noted as %w-wrapped;
// none are sentinel values, so callers must not rely on errors.Is. Errors
// originate from, in order: binding the flag set into Viper (wrapped with %w),
// reading the resolved config file (wrapped with %w), a malformed host URL
// (the URL-parse failure is %w-wrapped; missing/invalid scheme or hostname are
// plain strings), projects-per-page exceeding the GitLab API maximum of 100,
// an invalid log level (must be debug, info, warn, or error), and a missing
// token. Demo mode short-circuits the host, projects-per-page, log-level, and
// token checks, so it can only fail on the bind-flags or config-file steps.
//
// The config file path is resolved from --config, then $LAZYLAB_CONFIG, then
// $GITLAB_TUI_CONFIG. The dual env var support exists for backward
// compatibility with an earlier project name.
func Load(fs *pflag.FlagSet, opts ...Option) (Config, error) {
	var lc loadConfig
	for _, opt := range opts {
		opt(&lc)
	}

	var cfg Config

	v := viper.New()
	v.SetEnvPrefix("gitlab")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.SetDefault(FlagProjectsPerPage, defaultProjectsPerPage)
	v.SetDefault(FlagLogLevel, defaultLogLevel)
	v.SetDefault(FlagDiffContextLines, defaultDiffContextLines)
	v.AutomaticEnv()
	// BindPFlags wires each pflag into viper's precedence chain. Viper
	// consults pflag.Flag.Changed() before flag.Value, so unset CLI flags
	// fall through to env vars, then the config file, then SetDefault,
	// preserving defaults < file < env < flags. The glab credential
	// fallback sits outside viper and runs after this chain resolves.
	if err := v.BindPFlags(fs); err != nil {
		return cfg, fmt.Errorf("bind flags: %w", err)
	}

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
	cfg.Demo = v.GetBool(FlagDemo)
	cfg.DiffContextLines = v.GetInt(FlagDiffContextLines)

	// glab credential fallback: when no token came from a flag, env var, or config
	// file, borrow the token that the glab CLI has stored, so a glab-authed user
	// needs no separate GITLAB_TOKEN. The host resolved so far is passed as a
	// hint so the resolver only supplies a token scoped to that host; with no
	// configured host, glab's own default host comes along with the token. Demo
	// mode never consults glab.
	if cfg.Token == "" && !cfg.Demo && lc.glabResolver != nil {
		if token, host, ok := lc.glabResolver(cfg.Host); ok {
			cfg.Token = token
			if cfg.Host == "" {
				cfg.Host = host
			}
		}
	}

	// Apply defaults uniformly. Demo mode skips network/token validation but
	// still uses the same default values as a normal run.
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if cfg.ProjectsPerPage <= 0 {
		cfg.ProjectsPerPage = defaultProjectsPerPage
	}

	if cfg.Demo {
		return cfg, nil
	}

	if err := validateHostURL(cfg.Host); err != nil {
		return cfg, err
	}
	if cfg.ProjectsPerPage > maxProjectsPerPage {
		return cfg, fmt.Errorf("projects-per-page must be between 1 and %d, got %d", maxProjectsPerPage, cfg.ProjectsPerPage)
	}
	if !validLogLevels[cfg.LogLevel] {
		return cfg, fmt.Errorf("invalid log level %q, must be one of: debug, info, warn, error", cfg.LogLevel)
	}
	if cfg.Token == "" {
		return cfg, fmt.Errorf("gitlab token is required: set --%s, $GITLAB_TOKEN, or run `glab auth login`", FlagToken)
	}

	return cfg, nil
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
