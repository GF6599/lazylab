package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

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

	defaultHost            = "https://gitlab.com"
	defaultProjectsPerPage = 30
	defaultLogLevel        = "info"
)

// Config is the fully-resolved runtime configuration for the TUI.
type Config struct {
	Host            string
	Token           string
	ProjectsPerPage int
	LogLevel        string
	ConfigFile      string
}

// RegisterFlags wires the shared flags onto the provided FlagSet so callers
// can parse CLI overrides before calling Load.
func RegisterFlags(fs *pflag.FlagSet) {
	fs.String(FlagHost, "", "GitLab host, defaults to https://gitlab.com")
	fs.String(FlagToken, "", "GitLab personal access token (api scope)")
	fs.Int(FlagProjectsPerPage, 0, "Number of projects to request per page")
	fs.String(FlagConfig, "", "Optional config file (YAML, TOML, JSON)")
	fs.String(FlagLogLevel, "", "Log level: debug, info, warn, error")
}

// Load renders the final configuration using defaults, optional config file,
// environment variables, and explicit CLI flags in that precedence order.
func Load(fs *pflag.FlagSet) (Config, error) {
	var cfg Config

	v := viper.New()
	v.SetEnvPrefix("gitlab")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.SetDefault(FlagHost, defaultHost)
	v.SetDefault(FlagProjectsPerPage, defaultProjectsPerPage)
	v.SetDefault(FlagLogLevel, defaultLogLevel)
	v.AutomaticEnv()

	configPath, _ := fs.GetString(FlagConfig)
	if configPath == "" {
		configPath = os.Getenv("LABLENSE_CONFIG")
		if configPath == "" {
			configPath = os.Getenv("GITLAB_TUI_CONFIG")
		}
	}
	if configPath != "" {
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

	overrideFromFlags(fs, &cfg)

	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.ProjectsPerPage <= 0 {
		cfg.ProjectsPerPage = defaultProjectsPerPage
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if cfg.Token == "" {
		return cfg, fmt.Errorf("gitlab token is required via --%s or $GITLAB_TOKEN", FlagToken)
	}

	return cfg, nil
}

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
		cfg.ConfigFile, _ = fs.GetString(FlagConfig)
	}
}

func mustString(val string, err error) string {
	if err != nil {
		return ""
	}
	return val
}
