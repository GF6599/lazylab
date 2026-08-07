# Lazylab

[![Go Version](https://img.shields.io/github/go-mod/go-version/GF6599/lazylab)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/GF6599/lazylab)](https://goreportcard.com/report/github.com/GF6599/lazylab)

Keyboard-driven terminal UI for GitLab projects, pipelines, jobs, and merge requests.

![Lazylab Demo](doc/demo.gif)

## Background

Lazylab navigates GitLab the way `lazygit` navigates git. A multi-panel TUI with vim-style keys, pipeline auto-refresh, and a yazi-inspired file explorer. For scripting, lazylab does not reinvent a CLI: press `y` on any focused item to copy the equivalent [`glab`](https://gitlab.com/gitlab-org/cli) command to your clipboard, or `Y` to browse every `glab` command available for it. The TUI is for interactive discovery; `glab` is for execution.

## Install

```bash
go install github.com/GF6599/lazylab/cmd/lazylab@latest
```

Or build from source:

```bash
git clone https://github.com/GF6599/lazylab.git
cd lazylab && go build ./cmd/lazylab
```

Requires Go 1.26+ and a GitLab personal access token with `api` scope, or an authenticated [`glab`](https://gitlab.com/gitlab-org/cli) (see [Configuration](#configuration)).

## Usage

### TUI

```bash
export GITLAB_TOKEN=glpat-xxxx
lazylab
```

The UI opens on the alternate screen. Press `?` for the contextual help overlay, which lists every keybinding for the focused panel. Quit with `q` or `Ctrl+C`.

To explore the UI without a token, pass `--demo` for fake data.

### Themes

The TUI paints its own palette instead of borrowing the terminal's, so it renders the same wherever you run it. Press `~` to cycle the ten presets. The choice persists per host.

Rose Pine is the default. The others are Tokyo Night, Catppuccin Mocha, Gruvbox Dark, Dracula, Nord, Solarized Dark, Kanagawa, Everforest Dark, and One Dark.

### Emitting glab commands

lazylab is TUI-only. Rather than ship its own scripting CLI, it generates [`glab`](https://gitlab.com/gitlab-org/cli) commands for whatever you have focused:

- `y` copies the most useful `glab` command for the focused project, pipeline, job, or merge request to the clipboard.
- `Y` opens a preview overlay listing every `glab` command for that item (`j`/`k` to move, `enter` or `y` to copy, `esc` to close).

Every emitted command carries `-R <group/project>`, so it targets the project you were browsing regardless of your shell's working directory. Paste it, run it, or drop it into a script. Executing the commands needs [`glab`](https://gitlab.com/gitlab-org/cli) on your `PATH`.

## Configuration

A GitLab token is the only required value, and you can skip even that when `glab` is logged in (see below). Everything else has a default. Precedence (highest first): CLI flags > env vars (`GITLAB_*` prefix) > config file > glab credentials > compiled defaults.

Supply the token through `GITLAB_TOKEN`, a config file, or `glab auth login`. The `--token` flag works and stays supported, but prefer the others: a command-line argument is visible to every local user through the process list, and your shell writes it to the history file.

Run `lazylab --help` for the flag surface. Defaults and validation rules live in `internal/config/config.go`.

Config files are optional. Viper parses YAML, TOML, and JSON. Point at one with `--config <path>` or `$LAZYLAB_CONFIG`.

### glab credentials

If no token is supplied by flag, env, or config file, lazylab falls back to the credentials [`glab`](https://gitlab.com/gitlab-org/cli) has stored (from `glab auth login`). It borrows both the token and the host glab is configured for, so a glab-authenticated user, including on self-hosted GitLab, can run `lazylab` with no further setup. An explicit `--host` / `GITLAB_HOST` is still honored; glab only fills what you leave unset.

## Architecture

| Package               | Role                                                                  |
| --------------------- | --------------------------------------------------------------------- |
| `cmd/lazylab`         | Cobra root command that loads config and launches the TUI.            |
| `internal/config`     | Viper-driven loader with flag/env/file/default precedence.            |
| `internal/gitlab`     | Thin wrapper around `client-go` for projects, pipelines, jobs, MRs.   |
| `internal/ui`         | Bubble Tea model: multi-panel layout, state machines, caching.        |
| `internal/glabcmd`    | Maps a focused entity to its `glab` commands (pure, no I/O).          |
| `internal/glabauth`   | Reads glab's stored token/host so `GITLAB_TOKEN` is optional.         |
| `internal/redacting`  | slog handler that scrubs tokens before they reach stderr.             |
| `internal/demo`       | In-memory `gitlab.Service` for `--demo` runs.                         |

### Caching

The TUI persists three files under the OS cache directory (`~/Library/Caches/lazylab` on macOS, `~/.cache/lazylab` on Linux), each keyed by GitLab host:

- `projects_<host>.json` holds the project list. Force-refresh with `Ctrl+R`.
- `favorites_<host>.json` holds pinned projects.
- `preferences_<host>.json` holds layout, theme, and tab state.

Pipeline status lives in an in-memory LRU (last 100 projects) with a 5-second refresh tick. Delete the files on disk to start over.

## Development

The project ships a justfile. Run `just --list` for the recipe surface. Tests use `go test -race ./...`; coverage targets `>80%` for `internal/gitlab`, per `CLAUDE.md`.

## License

[MIT](LICENSE)
