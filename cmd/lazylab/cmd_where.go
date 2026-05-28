package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/cliout"
	"github.com/GF6599/lazylab/internal/gitcontext"
)

// newWhereCmd implements `lazylab where` — the diagnostic that explains
// what lazylab thinks the current execution context is. Useful when the
// CLI is making the "wrong" API call and the user wants to know whether
// the config, the git remote, or the active branch is the culprit.
//
// Performs no GitLab API calls. Reads only local state (config flags,
// environment variables, the git repository at cwd).
func newWhereCmd() *cobra.Command {
	var formatFlag string
	cmd := &cobra.Command{
		Use:   "where",
		Short: "Show the configuration and git context lazylab will use",
		Long: `Prints the resolved configuration (host, token presence, log level)
and any git context inferred from the current working directory. Use this
to debug "why is lazylab hitting the wrong project / host / branch?"
without making any GitLab API calls.

The host/token are read from --host/--token flags, env vars, or config
file (in that precedence). The project/branch/commit are read from the
git repository at the current working directory; outside a git repo
those fields are blank with a "no git context" notice.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cliout.ParseFormat(formatFlag)
			if err != nil {
				return err
			}
			cfg := configFromCtx(cmd.Context())

			// Honor cfg.Remote (driven by --remote / GITLAB_REMOTE /
			// config file) so the diagnostic matches what the project-
			// resolution path will use. Otherwise `where` would always
			// read `origin` even when the user has --remote upstream
			// set globally, leading to the classic "where says X but
			// pipeline list hits Y" foot-gun.
			gc, gcErr := gitcontext.Detect(gitcontext.Options{Remote: cfg.Remote})
			report := buildWhereReport(cfg.Host, cfg.Token, cfg.LogLevel, cfg.Remote, cfg.Project, gc, gcErr)
			return writeWhere(os.Stdout, report, format)
		},
	}
	cmd.Flags().StringVarP(&formatFlag, "format", "f", "table", "Output format: table, json")
	return cmd
}

// whereReport is the structured view returned by `lazylab where`. Kept as
// an explicit struct so the JSON output has a stable, documented shape
// for scripts that grep `lazylab where --format json | jq .host`.
type whereReport struct {
	Host     string `json:"host"`
	TokenSet bool   `json:"token_set"`
	LogLevel string `json:"log_level"`
	// Remote is the git remote name the project-resolution path will
	// inspect (default "origin"). Surfaced even when not inside a git
	// repo so users can see whether their --remote / GITLAB_REMOTE
	// override took effect.
	Remote string `json:"remote"`
	// Project is the project hint flowing in from --project /
	// GITLAB_PROJECT / config file. Empty means "fall back to git
	// inference"; non-empty means the project resolver will take this
	// value verbatim before consulting the git remote.
	Project     string `json:"project,omitempty"`
	InGitRepo   bool   `json:"in_git_repo"`
	HostMatch   bool   `json:"host_match"`
	RemoteURL   string `json:"remote_url,omitempty"`
	RemoteHost  string `json:"remote_host,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	SHA         string `json:"sha,omitempty"`
	Notice      string `json:"notice,omitempty"`
}

// buildWhereReport composes a whereReport from the resolved config and the
// (possibly nil) git context. Pure function — no I/O — so it can be tested
// without spawning git or hitting the file system.
//
// The HostMatch field is the cross-check that catches the multi-instance
// confusion case: user has GITLAB_HOST=gitlab.mycompany.com configured but
// is sitting in a clone of a gitlab.com project. Without this check the
// CLI would silently issue API calls to the wrong instance.
func buildWhereReport(cfgHost, token, logLevel, remote, project string, gc *gitcontext.Context, gcErr error) whereReport {
	r := whereReport{
		Host:     cfgHost,
		TokenSet: token != "",
		LogLevel: logLevel,
		Remote:   remote,
		Project:  project,
	}
	if gc != nil {
		r.InGitRepo = true
		r.RemoteURL = gc.RemoteURL
		r.RemoteHost = gc.Host
		r.ProjectPath = gc.ProjectPath
		r.Branch = gc.Branch
		r.SHA = gc.SHA
		r.HostMatch = hostsMatch(cfgHost, gc.Host)
		if !r.HostMatch {
			r.Notice = fmt.Sprintf("configured host (%s) does not match git remote host (%s) — CLI calls will target the configured host", cfgHost, gc.Host)
		}
	} else if errors.Is(gcErr, gitcontext.ErrNotInRepo) {
		r.Notice = "not inside a git repository; --project will be required for project-scoped commands"
	} else if errors.Is(gcErr, gitcontext.ErrNoRemote) {
		r.Notice = "git repo has no usable remote; --project will be required"
	} else if gcErr != nil {
		r.Notice = fmt.Sprintf("git context unavailable: %v", gcErr)
	}
	return r
}

// hostsMatch compares the hostname component of two host references,
// tolerating scheme differences and trailing slashes. cfgHost is the
// configured GitLab URL (https://gitlab.com); remoteHost is just a
// hostname (gitlab.com).
func hostsMatch(cfgHost, remoteHost string) bool {
	cfg := cfgHost
	if u, err := url.Parse(cfgHost); err == nil && u.Host != "" {
		cfg = u.Host
	}
	cfg = strings.ToLower(strings.TrimSuffix(cfg, "/"))
	remote := strings.ToLower(remoteHost)
	return cfg == remote
}

// writeWhere renders a whereReport to w. The table form is a KV block
// with `(source)` annotations so users can trace which input drove each
// row; JSON is the raw struct for scripting.
//
// The writer is io.Writer (not *os.File) so unit tests can capture
// output into a bytes.Buffer without spawning a real file. The
// production caller passes os.Stdout.
func writeWhere(w io.Writer, r whereReport, format cliout.Format) error {
	if format == cliout.FormatJSON {
		return cliout.PrintJSON(w, r)
	}

	tokenDisplay := "(unset)"
	if r.TokenSet {
		tokenDisplay = "set"
	}

	remote := r.Remote
	if remote == "" {
		remote = "(unset)"
	}
	projectDisplay := r.Project
	if projectDisplay == "" {
		projectDisplay = "(from git remote)"
	}
	rows := []cliout.KV{
		{Key: "Host", Value: r.Host},
		{Key: "Token", Value: tokenDisplay},
		{Key: "Log level", Value: r.LogLevel},
		{Key: "Remote", Value: remote},
		{Key: "Project", Value: projectDisplay},
	}
	if r.InGitRepo {
		rows = append(rows,
			cliout.KV{Key: "Git remote", Value: r.RemoteURL},
			cliout.KV{Key: "Remote host", Value: r.RemoteHost},
			cliout.KV{Key: "Project path", Value: r.ProjectPath},
		)
		branch := r.Branch
		if branch == "" {
			branch = "(detached HEAD)"
		}
		rows = append(rows,
			cliout.KV{Key: "Branch", Value: branch},
			cliout.KV{Key: "Commit", Value: r.SHA},
		)
	}
	if err := cliout.PrintKV(w, rows); err != nil {
		return err
	}
	if r.Notice != "" {
		if _, err := fmt.Fprintf(w, "\nNotice: %s\n", r.Notice); err != nil {
			return err
		}
	}
	return nil
}
