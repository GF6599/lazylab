# Lazylab

[![Go Version](https://img.shields.io/github/go-mod/go-version/GF6599/lazylab)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/GF6599/lazylab)](https://goreportcard.com/report/github.com/GF6599/lazylab)

Keyboard-driven terminal UI and scripting CLI for GitLab projects, pipelines, jobs, and merge requests.

![Lazylab Demo](doc/demo.gif)

## Background

Lazylab navigates GitLab the way `lazygit` navigates git. A multi-panel TUI with vim-style keys, pipeline auto-refresh, and a yazi-inspired file explorer. The same binary exposes a non-interactive CLI (`whoami`, `where`, `pipeline`, `job`) that shares config, cache, and credentials with the TUI. Useful when you want to watch a pipeline from a script or pipe a job log into `grep`.

## Install

```bash
go install github.com/GF6599/lazylab/cmd/lazylab@latest
```

Or build from source:

```bash
git clone https://github.com/GF6599/lazylab.git
cd lazylab && go build ./cmd/lazylab
```

Requires Go 1.26+ and a GitLab personal access token with `api` scope.

## Usage

### TUI

```bash
export GITLAB_TOKEN=glpat-xxxx
lazylab
```

The UI opens on the alternate screen. Press `?` for the contextual help overlay, which lists every keybinding for the focused panel. Quit with `q` or `Ctrl+C`.

To explore the UI without a token, pass `--demo` for fake data.

### CLI

The same binary runs non-interactively:

```bash
lazylab whoami                     # who does my token belong to?
lazylab where                      # what context will the CLI use?
lazylab pipeline status @main      # latest pipeline on main
lazylab pipeline watch HEAD        # tail the pipeline for the current commit
lazylab job log 4567890 --follow   # tail -f a job's trace
```

Pipeline references accept `@<ref>`, `HEAD`, `latest`, a numeric ID, or a full GitLab URL. Run `lazylab <cmd> --help` for the per-command surface.

## Configuration

The token is the only required value. Everything else has a default. Precedence (highest first): CLI flags > env vars (`GITLAB_*` prefix) > config file > compiled defaults.

The same precedence applies to the TUI and the CLI subcommands. Run `lazylab --help` for the persistent flag surface. Defaults and validation rules live in `internal/config/config.go`.

Config files are optional. Viper parses YAML, TOML, and JSON. Point at one with `--config <path>` or `$LAZYLAB_CONFIG`.

## Architecture

| Package               | Role                                                                  |
| --------------------- | --------------------------------------------------------------------- |
| `cmd/lazylab`         | Cobra command tree. Root runs the TUI; subcommands handle CLI verbs.  |
| `internal/config`     | Viper-driven loader with flag/env/file/default precedence.            |
| `internal/gitlab`     | Thin wrapper around `client-go` for projects, pipelines, jobs, MRs.   |
| `internal/ui`         | Bubble Tea model: multi-panel layout, state machines, caching.        |
| `internal/cliout`     | Output formatters (table, JSON) shared by every subcommand.           |
| `internal/gitcontext` | Reads project, branch, and commit from the surrounding git remote.    |
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
