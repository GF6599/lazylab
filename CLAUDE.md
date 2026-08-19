# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Lazylab is a Bubble Tea-powered terminal UI for browsing GitLab projects, pipelines, and repository files without leaving the keyboard. It features a yazi/ranger-inspired file explorer, real-time pipeline monitoring with auto-refresh, and persistent caching for instant startup.

## Development Commands

### Running the Application
```bash
# Run with environment variables
export GITLAB_TOKEN=glpat-xxxx
go run ./cmd/lazylab

# Run with flags (overrides env vars). Keep the token out of the command line:
# a flag value is visible in the process list and lands in shell history.
go run ./cmd/lazylab --host https://gitlab.mycompany.com --projects-per-page 100

# Run with config file
go run ./cmd/lazylab --config ./lazylab.yaml

# Run with debug logging
go run ./cmd/lazylab --log-level debug

# Using justfile shortcuts (reads GITLAB_TOKEN from the environment or .env)
just run https://gitlab.com
```

### Testing
```bash
# Run all tests
go test ./...
just test

# Run tests with coverage
go test ./... -coverprofile=coverage.out

# Run tests for a single package
go test ./internal/config
go test ./internal/ui
go test ./internal/gitlab

# Run a specific test
go test ./internal/ui -run TestPipelineModel_Refresh
```

### Building
```bash
# Build for current platform
go build ./cmd/lazylab

# Build for all platforms (creates build/ directory)
just build

# Clean build artifacts
just clean
```

### Code Quality
```bash
# Format code
go fmt ./...

# Run static analysis
go vet ./...

# Keep dependencies tidy
go mod tidy
```

## Architecture Overview

### Bubble Tea State Machine

The UI uses Bubble Tea's Elm-like architecture with a single `Model` that acts as a state machine with multiple modes:

- **`modeMultiPanel`**: Default startup mode: a four-pane layout (projects, MRs, pipelines, explorer) with overlay-driven modals
- **`modeProjects`**: Legacy standalone project list with search and pagination
- **`modeExplorer`**: Legacy standalone file tree (still in the enum for message guards in `messages_explorer.go`, the explorer is now an in-panel surface plus a modal overlay)
- **`modePipelines`**: Legacy standalone pipeline view (still in the enum for message guards in `messages_pipelines.go`, pipelines are now a panel inside `modeMultiPanel`)

Mode transitions follow this flow:
```
multipanel (default) ─┬─ panel: projects
                      ├─ panel: pipelines
                      ├─ panel: mrs
                      └─ panel: explorer (also surfaces as a modal overlay)
```

The legacy standalone `modeExplorer` and `modePipelines` constants remain load-bearing for the per-message mode guards (e.g., `messages_pipelines.go` discards stale messages when `m.mode != modePipelines`) and for a few tests, but they no longer have dedicated view branches as a normal startup path.

### Message Flow and Async Operations

All async work (API calls, file operations) happens via `tea.Cmd` functions that return messages. The Update function handles these messages and returns new state + optional commands:

1. User presses key -> `Update` handles `tea.KeyMsg`
2. `Update` returns a `tea.Cmd` (e.g., `fetchProjectsCmd`)
3. Command runs async and produces a message (e.g., `projectsLoadedMsg`)
4. `Update` receives the message and updates state

Key message types in `internal/ui/project_list_cmds.go`:
- `projectsLoadedMsg`, `cacheLoadedMsg`, `cacheSavedMsg`
- `treeLoadedMsg`, `fileLoadedMsg`
- `pipelinesLoadedMsg`, `pipelineStagesLoadedMsg`, `pipelineJobsLoadedMsg`, `pipelineLogLoadedMsg`
- `pipelineStatusMsg`, `pipelineRetriedMsg`, `pipelineJobRetriedMsg`
- `pipelineTickMsg` (triggers auto-refresh every 5 seconds)

### File Organization by Concern

**UI Layer** (`internal/ui/`):
- `project_list_model.go`: Core model, state machine, and key routing by mode
- `project_list_cmds.go`: Async commands and message type definitions
- `project_list_view.go`: All rendering functions (project list, explorer, pipelines, modals)
- `project_list_style.go`: Lipgloss styles and color definitions
- `project_list_helpers.go`: Pure helper functions (formatting, scrolling, selection)
- `cache.go`: Project list caching under `<os-cache-dir>/lazylab/projects_<host>.json`

**GitLab Client** (`internal/gitlab/client.go`):
- Wraps `gitlab.com/gitlab-org/api/client-go` with TUI-friendly types
- All methods accept `context.Context` for timeout control
- Pagination is handled via `ProjectPage` and `PipelinePage` types
- Stage aggregation merges job statuses with priority (failed > canceled > manual > running > success > skipped)

**Configuration** (`internal/config/config.go`):
- Precedence: defaults -> config file -> env vars -> CLI flags
- Uses Viper for file parsing (YAML/TOML/JSON)
- Env prefix: `GITLAB_*` (e.g., `GITLAB_TOKEN`, `GITLAB_HOST`)

### Caching Strategy

On first run, the app fetches page 1 of projects in the foreground, then background-loads remaining pages. All projects are cached to `<os-cache-dir>/lazylab/projects_<host>.json` (`~/Library/Caches` on macOS, `~/.cache` on Linux) keyed by GitLab host.

Subsequent launches:
1. `Init()` tries cache load first
2. If cache exists and valid, display instantly
3. User can force refresh with `Ctrl+R`

Pipeline data auto-refreshes every 5 seconds when:
- In `modeProjects`: refreshes the selected project's latest pipeline status
- In `modePipelines`: refreshes pipeline list, stages, jobs, and logs

### Explorer Mode Implementation

The explorer maintains a stack of `dirState` representing the navigation path:
- Root directory is `stack[0]` with `path: ""`
- Descending into `foo/bar` pushes `dirState{path: "foo/bar"}` onto the stack
- Going up pops from the stack
- Preview pane shows either directory listing or file content with syntax highlighting

Syntax highlighting uses:
1. `bat`/`batcat` if installed (respects terminal width)
2. Falls back to `glamour` for markdown
3. Raw content if both fail

Preview scrolling with `J`/`K` is independent of file tree selection. The `previewState.offset` tracks scroll position and is preserved across preview refreshes unless `logAutoFollow` is true (for pipeline logs).

### Pipeline View Dual-Focus Design

The pipeline view has two focus modes (`pipelineFocusPipelines` and `pipelineFocusStages`):

**Pipelines Focus** (`left`/`h` to return):
- List of pipelines sorted by `UpdatedAt` descending
- Navigation: `j`/`k`, `Ctrl+D`/`Ctrl+U`, `<`/`>`
- Page navigation: `[`/`]`

**Stages Focus** (`right`/`l` to enter):
- Shows stages for the selected pipeline
- Log preview on right auto-follows newest output when at bottom
- `J`/`K` scrolls the log preview
- Selecting a stage loads the latest job's trace for that stage

Retry behavior (`R` key):
- In pipelines focus: retries entire pipeline (or creates new run if needed)
- In stages focus: retries the selected stage's job
- Confirmation modal appears before retry

### State Management Patterns

**Selection Persistence**: When pipelines reload, the UI tries to keep the same pipeline selected by matching on `pipelineID`. This prevents jarring jumps during auto-refresh.

**Loading States**: Most async operations have three states tracked:
- `loading bool`: operation in progress
- `cache map[int]T`: results keyed by ID
- `err map[int]error`: errors keyed by ID

Example: `stageCache`, `stageLoading`, `stageErr` in `pipelineViewState`.

**Preview Auto-Follow**: Pipeline logs have `logAutoFollow bool` that:
- Stays true until user manually scrolls
- When true, new log content auto-scrolls to bottom
- When false, scroll position is preserved across refreshes

### Key Handling Architecture

Key events are routed by mode in `Model.Update`:
```go
case tea.KeyMsg:
    switch m.mode {
    case modeExplorer:
        return m.handleExplorerKey(msg)
    case modeProjectActions:
        return m.handleProjectActionKey(msg)
    case modePipelines:
        return m.handlePipelineViewKey(msg)
    default:
        return m.handleProjectKey(msg)
    }
```

Each handler returns `(tea.Model, tea.Cmd)` to allow chaining async operations. Common pattern:
```go
func (m Model) handleSomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    // Update state
    m.selected++

    // Return command for async work
    return m, m.queueSomeAsyncWork()
}
```

### Configuration Precedence Details

The config system uses Viper with this exact precedence (highest to lowest):
1. CLI flags (`--token`, `--host`, etc.)
2. Environment variables (`GITLAB_TOKEN`, `GITLAB_HOST`)
3. Config file (if `--config` or `LAZYLAB_CONFIG`/`GITLAB_TUI_CONFIG` env set)
4. glab's stored credentials (token + host) when no token came from the above. `internal/glabauth` shells out to `glab config get`. It is injected into `config.Load` via `WithGlabResolver`, so the config package never imports glab and stays testable.
5. Defaults (`https://gitlab.com`, 30 projects per page, info log level)

Note: a token is required, but it can come from `--token`, `GITLAB_TOKEN`, a config file, or an authenticated `glab`. The app exits with an error only when none of these supply one.

### Coding Conventions

When adding features or fixing bugs:

- **File Naming**: Group UI files by concern: `state_<area>.go` for per-mode state, `view_<area>.go` for renderers, `panel_<name>.go` for panel implementations, `keys_<area>.go` for key handlers, `messages_<area>.go` for `tea.Cmd` message handlers. The original `project_list_*.go` files (model, cmds, helpers, style, view, keys) remain as the central Bubble Tea wiring.
- **Bubble Tea Patterns**: Keep `Update` functions under ~40 lines. Extract mode-specific handlers
- **Function Size**: Keep functions under ~40 lines. Extract helpers for clarity
- **Comments**: Only add comments for non-obvious logic (e.g., tricky Bubble Tea layout math)
- **Commit style**: gitmoji scope-form (`✨ ui: ...`, `🐛 gitlab: ...`), per the repo history and `~/.claude/commands/commit-with-emoji.md`.
- **Testing**: Aim for >80% coverage on `internal/gitlab` (it hides API failure modes)
- **Test Naming**: `Test<Component>_<Behavior>` (e.g., `TestCache_SaveAndLoad`)
- **Caching**: Store under `<os-cache-dir>/lazylab/` (via `os.UserCacheDir`) and document paths for users
- **Token Security**: Never log or display tokens. Redact in log output

### Testing Guidelines

Tests live alongside code as `*_test.go` files. Sample data goes in `testdata/` directories.

For GitLab client tests:
- Use canned JSON in `testdata/` for predictable responses
- Guard live API tests behind `GITLAB_TOKEN` env check
- Use table-driven tests for multiple scenarios

For UI tests:
- Test pure helpers (formatting, selection bounds, fuzzy matching)
- Integration tests should exercise the full Bubble Tea program
- Keep under `cmd/lazylab` if they need the entire app context

## Build and Development Notes

- **Go Version**: Requires Go 1.24+
- **Dependencies**: Managed via `go.mod`. Run `go mod tidy` after adding deps
- **Cross-compilation**: `just build` creates binaries for Darwin (current arch) and Linux AMD64
- **Cache Location**: `<os-cache-dir>/lazylab/`: `~/Library/Caches` on macOS, `~/.cache` on Linux (users can delete to force refresh)
- **Logs**: Emitted to stderr in text format (safe for TUI rendering on alternate screen)
