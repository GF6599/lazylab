package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/gitcontext"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// hintsFromCmd builds gitlab.ResolveHints from the resolved Config plus
// the surrounding git repository. It is the single place every subcommand
// goes through to learn the user's "where am I" context, so resolution
// rules stay identical across `pipeline status`, `pipeline list`,
// `job log`, and any future verbs.
//
// Inputs flow through internal/config in this precedence (highest first):
//
//  1. --project / --remote flags
//  2. GITLAB_PROJECT / LAZYLAB_PROJECT, GITLAB_REMOTE env vars
//  3. Config file values
//  4. Defaults (project="" → use git; remote="origin")
//
// After config resolution this function adds the runtime git-inference
// step using cfg.Remote. The git inference is best-effort: a missing repo
// or remote is not a fatal error here — callers decide whether the empty
// hints leave them with enough information to proceed.
func hintsFromCmd(cmd *cobra.Command) (gitlab.ResolveHints, error) {
	cfg := configFromCtx(cmd.Context())
	remote := cfg.Remote
	if remote == "" {
		// Defensive default: a non-Demo Load always populates Remote, but
		// tests that exercise hintsFromCmd with a partially-built config
		// shouldn't blow up on an empty remote name.
		remote = "origin"
	}

	hints := gitlab.ResolveHints{ProjectFlag: cfg.Project}
	if gc, err := gitcontext.Detect(gitcontext.Options{Remote: remote}); err == nil {
		hints.GitProjectPath = gc.ProjectPath
		hints.GitSHA = gc.SHA
		hints.GitBranch = gc.Branch
	}
	// Returning a nil error is intentional even when gitcontext.Detect
	// fails: ProjectFlag alone may be enough for the caller. The error
	// return is kept on the signature so future hints (e.g. workspace
	// inference) can surface fatal failures without a breaking API change.
	return hints, nil
}

// resolveProject is the common project-only resolution path for verbs
// that don't take a positional pipeline/job ref (e.g. `pipeline list`).
// It returns the numeric project ID and, when available, the
// namespace-qualified path that GitLab knows the project by.
//
// Precedence mirrors ResolvePipelineRef's project half: ProjectFlag wins,
// falling back to the git remote's path. A numeric flag value is taken
// at face value (no GitLab round-trip needed); a path-style value
// triggers a GetProject lookup to translate it into an ID.
//
// Returns ErrNoProjectContext (wrapped with a usage hint) when neither
// the flag nor the git remote yields a project.
func resolveProject(ctx context.Context, cmd *cobra.Command, client gitlab.Service) (int, string, error) {
	hints, _ := hintsFromCmd(cmd)

	proj := strings.TrimSpace(hints.ProjectFlag)
	if proj == "" {
		proj = hints.GitProjectPath
	}
	if proj == "" {
		return 0, "", fmt.Errorf("%w (set --project, $GITLAB_PROJECT, or run inside a GitLab clone)", gitlab.ErrNoProjectContext)
	}
	if id, perr := strconv.Atoi(proj); perr == nil {
		return id, "", nil
	}
	node, err := client.GetProject(ctx, proj)
	if err != nil {
		return 0, "", fmt.Errorf("resolve project %q: %w", proj, err)
	}
	return node.ID, node.PathWithNamespace, nil
}
