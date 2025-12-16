package config

import (
	"errors"
	"net/url"
	"strings"

	"github.com/spf13/viper"
)

const (
	// DefaultHost is the fallback GitLab base URL.
	DefaultHost = "https://gitlab.com"
	// DefaultProjectPage declares how many membership projects to pull by default.
	DefaultProjectPage = 50

	// KeyHost represents the host configuration key.
	KeyHost = "host"
	// KeyToken represents the token configuration key.
	KeyToken = "token"
	// KeyProjectsPerPage configures how many projects to fetch.
	KeyProjectsPerPage = "projects_per_page"
)

// Config represents the runtime configuration for connecting to GitLab.
type Config struct {
	Host            string
	Token           string
	ProjectsPerPage int
}

// NewViper returns a Viper instance configured to read GitLab TUI settings.
func NewViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("gitlab")
	v.AutomaticEnv()
	v.SetDefault(KeyHost, DefaultHost)
	v.SetDefault(KeyProjectsPerPage, DefaultProjectPage)
	return v
}

// Load transforms the current Viper values into a Config structure.
func Load(v *viper.Viper) (Config, error) {
	if v == nil {
		v = NewViper()
	}

	host := strings.TrimSpace(v.GetString(KeyHost))
	if host == "" {
		host = DefaultHost
	}
	host = normalizeHost(host)

	token := strings.TrimSpace(v.GetString(KeyToken))
	if token == "" {
		return Config{}, errors.New("gitlab token must be provided via flag or GITLAB_TOKEN")
	}

	perPage := v.GetInt(KeyProjectsPerPage)
	if perPage <= 0 {
		perPage = DefaultProjectPage
	}

	return Config{
		Host:            host,
		Token:           token,
		ProjectsPerPage: perPage,
	}, nil
}

func normalizeHost(raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return ensureTrailingSlash(raw)
	}
	return ensureTrailingSlash("https://" + raw)
}

func ensureTrailingSlash(host string) string {
	parsed, err := url.Parse(host)
	if err != nil {
		return host
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return parsed.String()
}
