package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/GF6599/lazylab/internal/ui"
)

// launchTUI runs the Bubble Tea program for the no-subcommand case. It
// pulls its dependencies from the context populated by setupContext, so
// its signature stays untyped and stable as more subcommands are added.
//
// tea.WithAltScreen ensures the TUI does not pollute the user's
// scrollback buffer — on exit the terminal restores whatever was there
// before launch. tea.WithoutSignalHandler hands sole ownership of
// SIGINT/SIGTERM to the cobra-installed signal.NotifyContext in main;
// the default Bubble Tea handler would otherwise compete with cobra's
// for context cancellation, producing a race where one teardown sees
// ctx.Done before the other and the exit sequence diverges by run.
func launchTUI(ctx context.Context) error {
	client := clientFromCtx(ctx)
	logger := loggerFromCtx(ctx)
	cfg := configFromCtx(ctx)

	// Color profile is a TUI-only concern: pure-CLI subcommands never
	// instantiate a lipgloss style, so the NO_COLOR downgrade only
	// needs to happen when we're about to render. Keeping this here
	// instead of in PersistentPreRunE also means `lazylab whoami
	// --format json` doesn't pay the cost.
	applyColorProfile()

	logger.Info("connecting to GitLab", "host", cfg.Host)

	model := ui.NewModel(ctx, client, ui.Options{
		ProjectsPerPage:  cfg.ProjectsPerPage,
		Logger:           logger,
		Host:             cfg.Host,
		DiffContextLines: cfg.DiffContextLines,
	})
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())

	if _, err := program.Run(); err != nil {
		// No wrapping prefix: main.go already prepends "error:" to the
		// process-level error line, and a "tui exited:" wrap was
		// producing "error: tui exited: <underlying>" — two prefixes
		// for one event. The underlying error is descriptive enough.
		return err
	}
	return nil
}

// applyColorProfile honors the NO_COLOR convention (https://no-color.org)
// by downgrading lipgloss to plain ASCII. Without this, lipgloss still
// auto-detects via termenv heuristics, but explicit handling guarantees
// deterministic behavior across pipes, CI runners, and exotic terminals.
//
// Invoked only from launchTUI: CLI subcommands never construct lipgloss
// styles so they need not pay for this check.
func applyColorProfile() {
	if os.Getenv("NO_COLOR") != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
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
