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

func main() {
	fs := pflag.NewFlagSet("lazylab", pflag.ExitOnError)
	config.RegisterFlags(fs)
	fs.BoolP("version", "v", false, "Print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "parse flags:", err)
		os.Exit(1)
	}

	if v, _ := fs.GetBool("version"); v {
		fmt.Printf("lazylab %s (commit: %s, built: %s)\n", version, commit, date)
		return
	}

	cfg, err := config.Load(fs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid log level:", err)
		os.Exit(1)
	}

	baseHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(ui.NewRedactingHandler(baseHandler))

	var client gitlab.Service
	if cfg.Demo {
		client = &demo.DemoService{}
		cfg.Host = "https://demo.gitlab.example.com"
	} else {
		c, err := gitlab.NewClient(cfg.Token, cfg.Host)
		if err != nil {
			logger.Error("create gitlab client", "err", err)
			os.Exit(1)
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
		logger.Error("tui exited", "err", err)
		os.Exit(1)
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
