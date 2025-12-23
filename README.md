# GitLab TUI

GitLab TUI is a Bubble Tea–powered terminal UI for browsing your GitLab projects without leaving the keyboard.

## Requirements

- Go 1.24+
- GitLab personal access token with `api` scope (`GITLAB_TOKEN`)
- Optional: custom host via `GITLAB_HOST` (defaults to `https://gitlab.com`)

## Usage

```bash
export GITLAB_TOKEN=glpat-xxxx
# export GITLAB_HOST=https://gitlab.mycompany.com if needed

go run ./cmd/gitlab-tui
```

Flags override env variables when needed:

```bash
go run ./cmd/gitlab-tui --token glpat-xxxx --host https://gitlab.mycompany.com --projects-per-page 100
```

You can also point to a config file (`--config ./gitlab-tui.yaml`) that Viper can parse as YAML/TOML/JSON. The UI opens in an alternate screen. Quit anytime with `q` or `Ctrl+C`.

Set `--log-level debug` (or INFO/WARN/ERROR) to control verbosity. Logs are emitted to stderr in text format for easy piping or redirection.

## Controls

- Global:
  - Arrow keys / `j` `k`: move selection
  - `Ctrl+D` / `Ctrl+U`: page down/up (half screen)
  - `<` / `>`: jump to start/end
  - `Ctrl+R`: refresh the cached project list from GitLab
  - `Ctrl+O`: copy the selected project's SSH `git clone` command
  - `q` / `Ctrl+C`: quit
- Project list:
  - `Enter`: open the project action menu (browse files or view pipelines)
- Project actions:
  - `Enter`: open the highlighted action
  - `Esc`: return to the project list
- Explorer mode (yazi/ranger style):
  - `Enter` / `Right` / `l`: open a directory or preview a file
  - `Left` / `h` / `Backspace`: move up one directory (from root returns to the project list)
  - `J` / `K`: scroll the preview pane
  - `r`: reload the current directory
  - `Esc`: exit the explorer
- Pipeline view:
  - `Left` / `h` / `Esc`: back to project actions
  - `Right` / `l`: focus stages (logs preview on the right)
  - `J` / `K`: scroll the log preview
  - `r`: reload the pipeline view
  - Auto-refreshes every 5 seconds; log preview auto-follows only when you are at the bottom
<!-- keep future controls commented until implemented -->
<!-- - `Tab`, `p`, `f`: toggle between Pipelines and Files views -->
<!-- - `Left`/`Right`: move focus between project and content panes -->
<!---->
<!-- The left pane lists your GitLab projects, the middle pane alternates between pipelines and repository trees, and the right pane shows pipeline metadata or file previews. -->

## Architecture

- `pkg/config`: loads host/token settings from the environment
- `internal/gitlab`: lightweight wrapper around `go-gitlab` for projects, pipelines, trees, and file blobs
- `internal/ui`: Bubble Tea model, view logic, caching, and lipgloss styling (includes the project list, explorer, and pipeline/log views)
- `cmd/gitlab-tui`: CLI entrypoint

Project listings are cached between runs under `~/.cache/gitlab-tui/` (keyed per host) so subsequent launches open instantly. Use `Ctrl+R` or delete the cache file if you need to force a refresh.

The explorer is inspired by TUI file managers like yazi/ranger: once inside a project you can walk directories, preview files, and navigate back without leaving the keyboard. The pipeline view auto-refreshes and keeps the log preview pinned to the newest output when you are already at the end.
