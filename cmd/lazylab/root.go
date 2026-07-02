package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/config"
	"github.com/GF6599/lazylab/internal/demo"
	"github.com/GF6599/lazylab/internal/gitlab"
	"github.com/GF6599/lazylab/internal/glabauth"
	"github.com/GF6599/lazylab/internal/redacting"
)

// ctxKey is the unexported type for context.WithValue keys stashed by
// PersistentPreRunE. Using an unexported named type makes accidental key
// collisions with values from other packages impossible.
type ctxKey int

const (
	keyConfig ctxKey = iota
	keyClient
	keyLogger
)

// clientFromCtx returns the GitLab client attached to ctx by setupContext.
// PRECONDITION: setupContext (registered as PersistentPreRunE on the root)
// must have run. The built-in --version/--help handlers short-circuit before
// PersistentPreRunE, and the help/completion verbs are excluded by
// runsWithoutToken; none of them reach launchTUI, so they never call this
// helper.
func clientFromCtx(ctx context.Context) gitlab.Service {
	return ctx.Value(keyClient).(gitlab.Service)
}

// loggerFromCtx returns the redacting slog logger attached to ctx.
// PRECONDITION: setupContext must have run.
func loggerFromCtx(ctx context.Context) *slog.Logger {
	return ctx.Value(keyLogger).(*slog.Logger)
}

// configFromCtx returns the resolved Config attached to ctx.
// PRECONDITION: setupContext must have run.
func configFromCtx(ctx context.Context) config.Config {
	return ctx.Value(keyConfig).(config.Config)
}

// versionString is the value Cobra renders for --version. It's wired from
// main.go's build vars at root construction time so the test harness can
// supply its own without rebuilding the binary.
var versionString = "dev"

// resolveGlabCredentials reads the token and host that glab has stored. It is a
// package variable so tests can substitute a deterministic resolver rather than
// shelling out to a real glab binary.
var resolveGlabCredentials = glabauth.Resolve

// newRootCmd assembles the Cobra command. The root command's RunE launches
// the TUI; persistent flags (token, host, log-level, config, demo) live here.
//
// SilenceUsage / SilenceErrors are both true: Cobra would otherwise print the
// usage banner and an "Error: ..." line on any returned error. Instead main.go
// inspects the error from ExecuteContext and reports it itself — printing a
// single redacting.Redact'd line to stderr and exiting via exitCodeFor — so
// credentials never leak and exit codes stay centralized in exitCodeFor.
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "lazylab",
		Short: "Terminal UI for GitLab",
		Long: `Lazylab is a Bubble Tea-powered terminal UI for browsing GitLab
projects, pipelines, merge requests, and repository files.

Press y on any focused item to copy the equivalent glab command to the
clipboard, or Y to browse every glab command available for it.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// A positional argument is almost always a typo'd verb; rejecting it
		// here names the offender, where launching the TUI instead would (in
		// a non-TTY pipe) die with an unrelated "could not open a new TTY".
		Args: cobra.NoArgs,
		// Version is rendered by Cobra's built-in --version handler. The
		// template strips Cobra's default "<use> version" prefix in favor of
		// the shorter "lazylab <ver>" shape.
		Version: versionString,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if runsWithoutToken(cmd) {
				return nil
			}
			return setupContext(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return launchTUI(cmd.Context())
		},
	}
	rootCmd.SetVersionTemplate("lazylab {{.Version}}\n")
	config.RegisterFlags(rootCmd.PersistentFlags())
	helpCmd := newHelpCmd(rootCmd)
	rootCmd.AddCommand(helpCmd)
	// SetHelpCommand stops Cobra's InitDefaultHelpCmd from registering a
	// second, duplicate "help" verb at Execute time.
	rootCmd.SetHelpCommand(helpCmd)
	return rootCmd
}

// newHelpCmd recreates Cobra's built-in "help" verb. Cobra only registers it
// automatically when the root already has subcommands, and lazylab's root is
// the entire command surface, so without an explicit registration
// "lazylab help" would be rejected as an unknown argument.
func newHelpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Run: func(cmd *cobra.Command, args []string) {
			target, _, err := root.Find(args)
			if target == nil || err != nil {
				cmd.Printf("Unknown help topic %#q\n", args)
				_ = root.Usage()
				return
			}
			_ = target.Help()
		},
	}
}

// noTokenCommands names the verbs that must succeed without a GitLab token.
// Users reach for help and shell completion precisely when their setup is
// broken (completion even runs from shell init on every new shell), so gating
// them on config validation would be a chicken-and-egg failure.
var noTokenCommands = map[string]bool{
	"help":                          true,
	"completion":                    true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

// runsWithoutToken reports whether cmd belongs to the help/completion family
// that skips setupContext. It walks the parent chain because the shell-
// specific completion verbs ("completion zsh", ...) are subcommands of
// "completion".
func runsWithoutToken(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if noTokenCommands[c.Name()] {
			return true
		}
	}
	return false
}

// setupContext loads config, builds the redacting logger, constructs the
// GitLab client (real or demo), and stashes all three on the command's context
// for launchTUI to retrieve via the *FromCtx helpers.
//
// Failures here bubble up through Cobra's Execute and become process exit codes
// via exitCodeFor — so a missing token surfaces as exit 1 with a redacted log
// line before the TUI ever starts.
func setupContext(cmd *cobra.Command) error {
	cfg, err := config.Load(cmd.Flags(), config.WithGlabResolver(resolveGlabCredentials))
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("invalid log level: %w", err)
	}

	baseHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(redacting.NewHandler(baseHandler))
	// Anchor slog.Default() to the redacting handler so any package that
	// reaches for the global (e.g. internal/gitlab's Warn calls) routes
	// through the same scrub pipeline as ctx-injected loggers.
	slog.SetDefault(logger)

	client, err := buildClient(cfg)
	if err != nil {
		return err
	}
	if cfg.Demo {
		cfg.Host = "https://demo.gitlab.example.com"
	}

	if cfg.ConfigFile != "" {
		logger.Debug("using config file", "path", cfg.ConfigFile)
	}

	ctx := cmd.Context()
	ctx = context.WithValue(ctx, keyConfig, cfg)
	ctx = context.WithValue(ctx, keyClient, client)
	ctx = context.WithValue(ctx, keyLogger, logger)
	cmd.SetContext(ctx)
	return nil
}

// buildClient constructs the GitLab service for the resolved config. Demo mode
// short-circuits with an in-memory fixture; otherwise config.Load has already
// guaranteed a non-empty token, so we instantiate the HTTP client.
func buildClient(cfg config.Config) (gitlab.Service, error) {
	if cfg.Demo {
		return &demo.DemoService{}, nil
	}
	c, err := gitlab.NewClient(cfg.Token, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("create gitlab client: %w", err)
	}
	return c, nil
}
