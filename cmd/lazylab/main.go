// Package main is the entrypoint for lazylab, a terminal UI for browsing
// GitLab projects, pipelines, and repository files.
//
// It wires together configuration loading, GitLab client creation, and the
// Bubble Tea program. Logs are emitted to stderr through a redacting handler
// so that token values never appear in output, even at debug level. The TUI
// itself renders on the alternate screen to avoid polluting the user's
// scrollback buffer.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/pflag"

	"github.com/GF6599/lazylab/internal/demo"
	"github.com/GF6599/lazylab/internal/gitlab"
	"github.com/GF6599/lazylab/internal/ui"
	"github.com/GF6599/lazylab/pkg/config"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// fatal is the single exit-with-error path for startup failures. Pre-logger
// errors print to stderr directly with a labelled prefix; once a logger is
// available, callers should pass it so the failure is recorded in the same
// redacted, structured stream as the rest of the application's diagnostics.
//
// Always exits with code 1; never returns.
func fatal(logger *slog.Logger, label string, err error) {
	if logger != nil {
		logger.Error(label, "err", err)
	} else {
		fmt.Fprintln(os.Stderr, label+":", err)
	}
	os.Exit(1)
}

// applyColorProfile honors the NO_COLOR convention (https://no-color.org) by
// downgrading lipgloss to plain ASCII output. Without this, lipgloss still
// auto-detects via termenv's TTY/COLORTERM heuristics, but explicit handling
// here guarantees deterministic behavior across pipes, CI runners, and exotic
// terminals where auto-detect can be wrong.
func applyColorProfile() {
	if os.Getenv("NO_COLOR") != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

func main() {
	fs := pflag.NewFlagSet("lazylab", pflag.ExitOnError)
	config.RegisterFlags(fs)
	fs.BoolP("version", "v", false, "Print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fatal(nil, "parse flags", err)
	}

	if v, _ := fs.GetBool("version"); v {
		fmt.Printf("lazylab %s (commit: %s, built: %s)\n", version, commit, date)
		return
	}

	cfg, err := config.Load(fs)
	if err != nil {
		fatal(nil, "config error", err)
	}

	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		fatal(nil, "invalid log level", err)
	}

	applyColorProfile()

	baseHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(ui.NewRedactingHandler(baseHandler))

	var client gitlab.Service
	if cfg.Demo {
		client = &demo.DemoService{}
		cfg.Host = "https://demo.gitlab.example.com"
	} else {
		c, err := gitlab.NewClient(cfg.Token, cfg.Host)
		if err != nil {
			fatal(logger, "create gitlab client", err)
		}
		client = c
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	model := ui.NewModel(ctx, client, ui.Options{ProjectsPerPage: cfg.ProjectsPerPage, Logger: logger, Host: cfg.Host, DiffContextLines: cfg.DiffContextLines})
	program := tea.NewProgram(model, tea.WithAltScreen())

	if cfg.ConfigFile != "" {
		logger.Debug("using config file", "path", cfg.ConfigFile)
	}
	logger.Info("connecting to GitLab", "host", cfg.Host)

	if _, err := program.Run(); err != nil {
		fatal(logger, "tui exited", err)
	}
}

// parseLogLevel accepts loose aliases ("warn", "warning", "err") so users
// don't need to memorize the exact slog constant names.
func parseLogLevel(lvl string) (slog.Level, error) {
	switch strings.ToLower(lvl) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "err":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("%s is not a supported log level", lvl)
	}
}
