# GitLab TUI

GitLab TUI is a Bubble Tea–powered terminal UI for browsing your GitLab projects without leaving the keyboard.

## Requirements

- Go 1.22+
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

- Arrow keys / `j` `k`: move selection
- `Enter`: open the selected project in the explorer (press `Esc` to return)
- `Ctrl+R`: refresh the cached project list from GitLab
- `Ctrl+O`: copy the selected project's SSH `git clone` command
- Explorer mode (yazi/ranger style):
  - `Enter` / `Right` / `l`: open a directory or preview a file
  - `Left` / `h` / `Backspace`: move up one directory (from root returns to the project list)
  - `r`: reload the current directory
  - `Esc`: exit the explorer
<!-- keep future controls commented until implemented -->
<!-- - `Tab`, `p`, `f`: toggle between Pipelines and Files views -->
<!-- - `Left`/`Right`: move focus between project and content panes -->
<!---->
<!-- The left pane lists your GitLab projects, the middle pane alternates between pipelines and repository trees, and the right pane shows pipeline metadata or file previews. -->

## Architecture

- `pkg/config`: loads host/token settings from the environment
- `internal/gitlab`: lightweight wrapper around `go-gitlab` for projects, pipelines, trees, and file blobs
- `internal/ui`: Bubble Tea model, view logic, caching, and lipgloss styling (includes the project list and repository explorer)
- `cmd/gitlab-tui`: CLI entrypoint

Project listings are cached between runs under `~/.cache/gitlab-tui/` (keyed per host) so subsequent launches open instantly. Use `Ctrl+R` or delete the cache file if you need to force a refresh.

The explorer is inspired by TUI file managers like yazi/ranger: once inside a project you can walk directories, preview files, and navigate back without leaving the keyboard.
