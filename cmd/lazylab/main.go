// Package main is the entrypoint for lazylab.
//
// Lazylab is a Bubble Tea-powered terminal UI for browsing GitLab
// projects, pipelines, merge requests, and repository files. It runs on
// the alternate screen; for scripting, pressing y on a focused item
// copies the equivalent glab command and Y browses every glab command
// available for it.
//
// The Cobra root is assembled in root.go; this file is the thin shell
// that handles signal propagation, version-string wiring, and exit-code
// mapping.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/GF6599/lazylab/internal/redacting"
)

// version, commit, date are set at build time via -ldflags
// "-X main.version=...". The defaults make `go run ./cmd/lazylab
// --version` print a recognizable placeholder rather than crashing.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Hand the build identity to Cobra's native --version handler.
	// Doing it here (rather than in newRootCmd) keeps the linker-baked
	// variables, ldflags string assembly, and version-string formatting
	// in one place, so newRootCmd remains pure command wiring.
	versionString = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)

	rootCmd := newRootCmd()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", redacting.Redact(err.Error()))
		os.Exit(exitCodeFor(err))
	}
}
