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
  <!-- - `Tab`, `p`, `f`: toggle between Pipelines and Files views -->
  <!-- - `Enter`: open a folder or preview a file -->
  <!-- - `Left`/`Right`: move focus between project and content panes -->
  <!---->
  <!-- The left pane lists your GitLab projects, the middle pane alternates between pipelines and repository trees, and the right pane shows pipeline metadata or file previews. -->

## Architecture

- `pkg/config`: loads host/token settings from the environment
- `internal/gitlab`: lightweight wrapper around `go-gitlab` for projects, pipelines, trees, and file blobs
- `internal/ui`: Bubble Tea model, view logic, and lipgloss styling
- `cmd/gitlab-tui`: CLI entrypoint
