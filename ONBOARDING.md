# Lazylab Onboarding (Non-Go Developers)

Welcome! This doc gives a practical map of the codebase and enough Go + Bubble Tea context to start contributing quickly.

## Quick Start

1. Install Go 1.24+.
2. Set a GitLab personal access token (api scope):

```bash
export GITLAB_TOKEN=glpat-xxxx
```

3. Run the app:

```bash
go run ./cmd/lazylab
```

Useful commands:

```bash
go test ./...
go vet ./...
go build ./cmd/lazylab
```

There is also a `justfile` with shortcuts if you use `just`.

## Go 101 (What You Need Here)

- Packages: folders are packages; files in the same folder share the same package name.
- Modules: `go.mod` defines dependencies and module path. Use `go mod tidy` after adding deps.
- Exported vs unexported: identifiers starting with a capital letter are exported.
- Formatting: `go fmt ./...` and `goimports` keep imports tidy.

## Bubble Tea 101 (How the TUI Works)

Bubble Tea uses an Elm-like architecture:

- Model: holds state (our UI state machine).
- Update: handles messages (events) and returns new state + commands.
- View: renders the current state to a string.

Commands (tea.Cmd) are functions that run and return a message (tea.Msg). This is how async work (API calls, timers) flows back into Update.

Minimal shape:

```go
type Model struct { /* state */ }

func (m Model) Init() tea.Cmd { /* start async work */ }
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) { /* handle events */ }
func (m Model) View() string { /* render */ }
```

## Project Structure (Map of the Codebase)

- `cmd/lazylab/main.go` - entrypoint: parse flags, load config, create GitLab client, start Bubble Tea.
- `pkg/config/config.go` - config loading from flags, env, optional config file.
- `internal/gitlab/client.go` - GitLab API wrapper with TUI-friendly types.
- `internal/ui/ui_project_list.go` - main Bubble Tea model, key handling, and state transitions.
- `internal/ui/ui_project_list_commands.go` - async commands + message types (API requests, ticks).
- `internal/ui/ui_project_list_views.go` - rendering for projects, explorer, pipelines, and modals.
- `internal/ui/ui_project_list_styles.go` - lipgloss styles.
- `internal/ui/cache.go` - project list cache stored in `~/.cache/lazylab/`.

## Runtime Flow (At a Glance)

1. `main.go` builds config, creates `gitlab.Client`, then `ui.NewModel`.
2. `Model.Init` kicks off:
   - cache load (if available), else project fetch
   - a ticker for pipeline refresh
3. `Update` handles messages:
   - `projectsLoadedMsg`, `treeLoadedMsg`, `pipelineLogLoadedMsg`, etc.
   - key events routed by mode (projects, explorer, actions, pipelines)
4. `View` renders based on `m.mode`.

The UI acts like a small state machine with modes:

- `projects`: list + detail
- `project_actions`: modal for "View pipelines" / "Browse files"
- `explorer`: file tree + preview
- `pipelines`: pipeline list + stages/log preview

## GitLab Client Behavior

`internal/gitlab` wraps `gitlab.com/gitlab-org/api/client-go` and normalizes data:

- Project list (paginated, sorted by recent activity).
- Repo tree and file content (base64 decoded).
- Pipeline list + stage aggregation + job logs.
- Retry pipeline and retry job helpers.

Most calls use contexts with short timeouts in UI commands.

## Caching

Project lists are cached by host under `~/.cache/lazylab/projects_<host>.json`.
Startup tries cache first to make the UI feel instant. `Ctrl+R` forces refresh.

## How to Add or Change Things

### Add a new keybinding

1. Find the mode handler in `internal/ui/ui_project_list.go`.
2. Handle the key in the relevant `handle*Key` function.
3. If you need async work, add a new command + message in
   `internal/ui/ui_project_list_commands.go`.
4. Update the view to show any new UI state.

### Add a new GitLab API call

1. Add a method to `internal/gitlab/client.go`.
2. Create a `tea.Cmd` in `internal/ui/ui_project_list_commands.go` that calls it.
3. Add a new `tea.Msg` type and handle it in `Model.Update`.

### Add a new panel or mode

1. Add a new `mode*` constant and state struct on `Model`.
2. Route key handling in `Update`.
3. Add render function(s) in `internal/ui/ui_project_list_views.go`.

## Testing Notes

- Tests live next to the code: `internal/ui/*_test.go`, `pkg/config/*_test.go`.
- Run all tests with `go test ./...`.
- For UI helpers, small table tests are common (see `internal/ui/ui_project_list_test.go`).

## Debugging Tips

- Run with `--log-level debug` to get more detail.
- Logs go to stderr so they do not break the TUI rendering.
- Some UI preview highlighting uses `bat`/`batcat` if installed; otherwise it falls back to Glamour.

## Security & Config Reminders

- Never hard-code tokens in code or test data.
- Use `GITLAB_TOKEN` or a local config file that is not committed.
- If you log or print URLs, avoid including tokens.

## Suggested First Tasks

- Tweak a style in `internal/ui/ui_project_list_styles.go`.
- Add a tiny UI message (e.g., a new hint in the footer).
- Add a small helper test in `internal/ui/ui_project_list_test.go`.
