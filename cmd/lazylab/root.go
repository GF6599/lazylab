package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/demo"
	"github.com/GF6599/lazylab/internal/gitlab"
	"github.com/GF6599/lazylab/internal/redacting"
	"github.com/GF6599/lazylab/pkg/config"
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

// clientFromCtx returns the GitLab client attached to ctx.
// PRECONDITION: setupContext (registered as PersistentPreRunE on the
// root) must have run, AND the calling subcommand must not be in
// noTokenCommands. Subcommands that bypass PreRunE (--help, --version,
// completion) or live in noTokenCommands (where) must not call this
// helper — the type-assert will panic on the nil-shim case, by design,
// because asking for the client without a token is a wiring bug.
func clientFromCtx(ctx context.Context) gitlab.Service {
	return ctx.Value(keyClient).(gitlab.Service)
}

// loggerFromCtx returns the redacting slog logger attached to ctx.
// PRECONDITION: setupContext (registered as PersistentPreRunE on the
// root) must have run. Subcommands that bypass PreRunE (--help,
// --version, completion) must not call this helper.
func loggerFromCtx(ctx context.Context) *slog.Logger {
	return ctx.Value(keyLogger).(*slog.Logger)
}

// configFromCtx returns the resolved Config attached to ctx.
// PRECONDITION: setupContext (registered as PersistentPreRunE on the
// root) must have run. Subcommands that bypass PreRunE (--help,
// --version, completion) must not call this helper.
func configFromCtx(ctx context.Context) config.Config {
	return ctx.Value(keyConfig).(config.Config)
}

// versionString is the value Cobra renders for --version. It's wired
// from main.go's build vars at root construction time so the test
// harness can supply its own without rebuilding the binary.
var versionString = "dev"

// newRootCmd assembles the Cobra command tree. The root command's RunE
// launches the TUI; subcommands attached via AddCommand handle CLI verbs.
// All persistent flags (token, host, log-level, config, demo) live here so
// every subcommand inherits the same configuration surface.
//
// SilenceUsage / SilenceErrors are both true: Cobra would otherwise print
// the usage banner and an "Error: ..." line on any returned error. We
// instead let main.go route errors through fatal() so logging stays in the
// single redacted slog stream and exit codes come from exitCodeFor.
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "lazylab",
		Short: "Terminal UI and CLI for GitLab",
		Long: `Lazylab is a Bubble Tea-powered terminal UI for browsing GitLab
projects, pipelines, merge requests, and repository files.

Run with no arguments to launch the TUI. Subcommands (whoami, job, pipeline,
mr, project) provide a non-interactive CLI that shares the same config,
cache, and credentials as the TUI.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version is rendered by Cobra's built-in --version handler so we
		// don't have to pre-scan argv before subcommand dispatch. The
		// template strips Cobra's default "<use> version" prefix in favor
		// of the shorter "lazylab <ver>" shape we used pre-Cobra.
		Version: versionString,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return setupContext(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return launchTUI(cmd.Context())
		},
	}
	rootCmd.SetVersionTemplate("lazylab {{.Version}}\n")

	// config.RegisterFlags now owns --project and --remote alongside
	// --host/--token/etc., so they flow through the same precedence chain
	// (flag > env > config file > default) as the rest of the config.
	// The CLI subcommands read them via configFromCtx(ctx).Project / .Remote
	// instead of re-querying cobra/env directly.
	config.RegisterFlags(rootCmd.PersistentFlags())
	rootCmd.AddCommand(newWhoamiCmd())
	rootCmd.AddCommand(newWhereCmd())
	rootCmd.AddCommand(newPipelineCmd())
	rootCmd.AddCommand(newJobCmd())
	return rootCmd
}

// noTokenCommands names the verbs that must succeed without a GitLab
// token. These are diagnostics ("where") or built-in Cobra commands
// ("help", "completion") that exist precisely so users can recover from
// a misconfigured token. Cobra's auto-generated --version handler also
// short-circuits before PersistentPreRunE, so we don't need to list it
// here, but `help` and `completion` do run through PreRunE.
var noTokenCommands = map[string]bool{
	"where":      true,
	"help":       true,
	"completion": true,
}

// setupContext loads config, builds the redacting logger, constructs the
// GitLab client (real or demo), and stashes all three on the command's
// context for downstream RunE functions to retrieve via the *FromCtx
// helpers. Runs once per invocation regardless of which subcommand fires.
//
// Failures here bubble up through Cobra's Execute and become process
// exit codes via exitCodeFor — so a missing token surfaces as exit 1
// with the same redacted log line a developer would see from the TUI
// path. The noTokenCommands allowlist sidesteps that requirement for
// diagnostic verbs that need to function precisely when the token is
// the thing being debugged.
func setupContext(cmd *cobra.Command) error {
	cfg, err := loadConfigForCmd(cmd)
	if err != nil {
		return err
	}

	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("invalid log level: %w", err)
	}

	baseHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(redacting.NewHandler(baseHandler))

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

// loadConfigForCmd wraps config.Load with the noTokenCommands carve-out.
// For diagnostic verbs we tolerate a missing token (the only validation
// failure config.Load normally surfaces) so users running `lazylab
// where` to debug their auth setup don't hit a chicken-and-egg error.
func loadConfigForCmd(cmd *cobra.Command) (config.Config, error) {
	cfg, err := config.Load(cmd.Flags())
	if err == nil {
		return cfg, nil
	}
	if noTokenCommands[cmd.Name()] {
		// A bare-token validation failure is acceptable here — return
		// the partially-populated config (it still has Host, LogLevel,
		// etc. set) so `where` can report what it has.
		return cfg, nil
	}
	return cfg, fmt.Errorf("config error: %w", err)
}

// buildClient constructs the GitLab service for the resolved config.
// Demo mode short-circuits with an in-memory fixture; real mode
// instantiates the HTTP client. Returns a usable nil-safe shim for
// no-token commands so downstream helpers don't have to type-check.
func buildClient(cfg config.Config) (gitlab.Service, error) {
	if cfg.Demo {
		return &demo.DemoService{}, nil
	}
	if cfg.Token == "" {
		// Reached only via the noTokenCommands carve-out. Returning
		// nil is safe because those commands never call clientFromCtx
		// (the panic invariant documented on clientFromCtx); the value
		// is stashed in the context purely so the *FromCtx helpers
		// have a type-asserted slot even when unused.
		return (*gitlab.Client)(nil), nil
	}
	c, err := gitlab.NewClient(cfg.Token, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("create gitlab client: %w", err)
	}
	return c, nil
}
