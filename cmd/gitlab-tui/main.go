package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/pflag"

	"gitlab-tui-codex/internal/gitlab"
	"gitlab-tui-codex/internal/ui"
	"gitlab-tui-codex/pkg/config"
)

func main() {
	fs := pflag.NewFlagSet("gitlab-tui", pflag.ExitOnError)
	config.RegisterFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "parse flags:", err)
		os.Exit(1)
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

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	client, err := gitlab.NewClient(cfg.Token, cfg.Host)
	if err != nil {
		logger.Error("create gitlab client", "err", err)
		os.Exit(1)
	}

	model := ui.NewModel(client, ui.Options{ProjectsPerPage: cfg.ProjectsPerPage, Logger: logger})
	program := tea.NewProgram(model, tea.WithAltScreen())

	if cfg.ConfigFile != "" {
		logger.Debug("using config file", "path", cfg.ConfigFile)
	}
	logger.Info("connecting to GitLab", "host", cfg.Host)

	if err := program.Start(); err != nil {
		logger.Error("tui exited", "err", err)
		os.Exit(1)
	}
}

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
