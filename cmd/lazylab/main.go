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
	"runtime/debug"
	"syscall"

	"github.com/GF6599/lazylab/internal/redacting"
)

// version, commit, date are set at build time via -ldflags "-X main.version=...",
// which only a goreleaser build passes. Every other route leaves the defaults.
var (
	version = versionUnset
	commit  = commitUnset
	date    = dateUnset
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Resolved here rather than in newRootCmd, so newRootCmd stays pure command
	// wiring and a test can set versionString without a rebuild.
	info, _ := debug.ReadBuildInfo()
	linker := buildIdentity{version: version, commit: commit, date: date}
	versionString = resolveBuildIdentity(linker, info).String()

	rootCmd := newRootCmd()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", redacting.Redact(err.Error()))
		os.Exit(exitCodeFor(err))
	}
}
