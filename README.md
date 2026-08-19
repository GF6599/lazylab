# Lazylab

[![Go Version](https://img.shields.io/github/go-mod/go-version/GF6599/lazylab)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/GF6599/lazylab)](https://goreportcard.com/report/github.com/GF6599/lazylab)

Keyboard-driven terminal UI for GitLab projects, pipelines, jobs, and merge requests.

![Lazylab Demo](doc/demo.gif)

## Background

Lazylab navigates GitLab the way `lazygit` navigates git. A multi-panel TUI with vim-style keys, pipeline auto-refresh, and a yazi-inspired file explorer. For scripting, lazylab does not reinvent a CLI. Press `y` on any focused item to copy the equivalent [`glab`](https://gitlab.com/gitlab-org/cli) command. Press `Y` to browse every `glab` command for it. The TUI is for interactive discovery. `glab` is for execution.

## Install

Homebrew, on macOS. Homebrew serves lazylab as a cask, and a cask does not install on Linux:

```bash
brew install --cask GF6599/tap/lazylab
```

With a Go toolchain:

```bash
go install github.com/GF6599/lazylab/cmd/lazylab@latest
```

Or take a binary from the [releases page](https://github.com/GF6599/lazylab/releases). Each archive holds the binary for one platform, and `checksums.txt` covers every archive in the release:

```bash
tar -xzf lazylab_<version>_<os>_<arch>.tar.gz
sudo mv lazylab /usr/local/bin/
```

Or build from source:

```bash
git clone https://github.com/GF6599/lazylab.git
cd lazylab && go build ./cmd/lazylab
```

Every route needs a GitLab personal access token with `api` scope, or an authenticated [`glab`](https://gitlab.com/gitlab-org/cli). See [Configuration](#configuration). The two Go routes also need Go 1.26 or later. Homebrew and the released binaries need no toolchain.

Run `lazylab --version` to report which build you have.

## Usage

### TUI

```bash
export GITLAB_TOKEN=glpat-xxxx
lazylab
```

The UI opens on the alternate screen. Press `?` for the contextual help overlay, which lists every keybinding for the focused panel. Quit with `q` or `Ctrl+C`.

To explore the UI without a token, pass `--demo` for fake data.

### Themes

The TUI paints its own palette and does not borrow the terminal's, so it renders the same wherever you run it. Press `~` to cycle the ten presets. The choice persists per host.

Rose Pine is the default. The others are Tokyo Night, Catppuccin Mocha, Gruvbox Dark, Dracula, Nord, Solarized Dark, Kanagawa, Everforest Dark, and One Dark.

### Emitting glab commands

lazylab is TUI-only. Rather than ship its own scripting CLI, it generates [`glab`](https://gitlab.com/gitlab-org/cli) commands for whatever you have focused:

- `y` copies the most useful `glab` command for the focused project, pipeline, job, or merge request to the clipboard.
- `Y` opens a preview overlay listing every `glab` command for that item (`j`/`k` to move, `enter` or `y` to copy, `esc` to close).

Every emitted command carries `-R <group/project>`, so it targets the project you were browsing regardless of your shell's working directory. Paste it, run it, or drop it into a script. You need [`glab`](https://gitlab.com/gitlab-org/cli) on your `PATH` to run them.

## Configuration

A GitLab token is the only required value, and you can skip even that when `glab` is logged in (see below). Everything else has a default. Precedence (highest first): CLI flags > env vars (`GITLAB_*` prefix) > config file > glab credentials > compiled defaults.

Supply the token through `GITLAB_TOKEN`, a config file, or `glab auth login`. The `--token` flag works and stays supported, but prefer the others. A command-line argument is visible to every local user through the process list. Your shell also writes it to the history file.

Run `lazylab --help` for the flag surface. Defaults and validation rules live in `internal/config/config.go`.

Config files are optional. Viper parses YAML, TOML, and JSON. Point at one with `--config <path>` or `$LAZYLAB_CONFIG`.

### glab credentials

If no token is supplied by flag, env, or config file, lazylab falls back to the credentials [`glab`](https://gitlab.com/gitlab-org/cli) has stored (from `glab auth login`). It borrows both the token and the host glab is configured for. A glab-authenticated user can then run `lazylab` with no further setup, including on self-hosted GitLab. An explicit `--host` / `GITLAB_HOST` is still honored. glab only fills what you leave unset.

## How it works

Lazylab is a Bubble Tea state machine. Every GitLab call runs as a command that returns a message. No fetch blocks a keystroke, and the panels repaint from state on the next frame. Pipeline data refreshes on a 5-second tick.

The `glab` emitters are pure. They map whatever you have focused to a command string and perform no I/O, which is why `y` works with `glab` absent from your `PATH`. You need it only to run the command.

The tree assumes two rules. Every byte the TUI writes passes one ANSI filter first, so remote content cannot move the cursor or repaint the screen. No token reaches the terminal unscrubbed either. The log handler redacts what it writes, and every error shown in the UI passes the same redactor.

### Caching

The TUI persists three files under the OS cache directory (`~/Library/Caches/lazylab` on macOS, `~/.cache/lazylab` on Linux), each keyed by GitLab host:

- `projects_<host>.json` holds the project list. Force-refresh with `Ctrl+R`.
- `favorites_<host>.json` holds pinned projects.
- `preferences_<host>.json` holds layout, theme, and tab state.

Pipeline status lives in an in-memory LRU (last 100 projects) with a 5-second refresh tick. Delete the files on disk to start over.

## Development

The project ships a justfile. Run `just --list` for the recipe surface. Tests run with `go test -race ./...`. Coverage targets `>80%` for `internal/gitlab`.

## License

[MIT](LICENSE)
