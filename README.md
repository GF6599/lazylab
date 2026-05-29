# Lazylab

[![Go Version](https://img.shields.io/github/go-mod/go-version/GF6599/lazylab)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/GF6599/lazylab)](https://goreportcard.com/report/github.com/GF6599/lazylab)

A terminal UI for browsing GitLab projects, pipelines, merge requests, and repository files — without leaving the keyboard.

Navigate your GitLab instance the way lazygit navigates git: with a multi-panel TUI, instant keyboard shortcuts, and real-time updates.

![Lazylab Demo](doc/demo.gif)

## Features

- **Multi-panel layout**: Lazygit-style accordion sidebar with Projects, Pipelines, Stages, and Merge Requests panels, plus a detail pane on the right
- **Pipeline monitoring**: Real-time pipeline status with auto-refresh every 5 seconds, stage/job drill-down, and log preview with auto-follow
- **Merge Requests panel**: Browse open/merged MRs with discussions, diffs, and info tabs in the detail pane
- **Matrix job grouping**: CI matrix jobs are collapsed into expandable groups in the stages view
- **Favorites**: Mark projects with `f` for quick access via the Favorites tab; persisted across sessions
- **Explorer mode**: Yazi/ranger-inspired file browser with syntax-highlighted preview pane
- **Project search**: Fuzzy search across all projects with `/`
- **Persistent cache**: Project list cached in the system cache directory for instant startup
- **Layout modes**: Toggle between default (30/70) and wide (50/50) sidebar-to-detail split with `+`; cycle accordion sizing with `=`

## Installation

### go install

```bash
go install github.com/GF6599/lazylab/cmd/lazylab@latest
```

### From source

```bash
git clone https://github.com/GF6599/lazylab.git
cd lazylab
go build ./cmd/lazylab
```

### Requirements

- Go 1.24+
- GitLab personal access token with `api` scope

## Usage

```bash
export GITLAB_TOKEN=glpat-xxxx
# export GITLAB_HOST=https://gitlab.mycompany.com if needed

go run ./cmd/lazylab
```

Flags override env variables when needed:

```bash
go run ./cmd/lazylab --token glpat-xxxx --host https://gitlab.mycompany.com --projects-per-page 100
```

The UI opens in an alternate screen. Quit anytime with `q` or `Ctrl+C`.

## Controls

### Global

| Key | Action |
|-----|--------|
| `q` / `Ctrl+C` | Quit |
| `Tab` / `Shift+Tab` | Cycle sidebar panels forward/backward |
| `1`-`4` | Jump to sidebar panel (Projects, Pipelines, Stages, MRs) |
| `+` / `-` | Toggle layout mode (default 30/70 vs wide 50/50) |
| `=` | Cycle screen mode (Normal, Half, Full) |
| `Right` | Focus detail pane |
| `Left` / `h` / `Esc` | Return from detail pane to sidebar |

### Projects Panel

| Key | Action |
|-----|--------|
| `j` / `k` / Arrow keys | Move selection |
| `Ctrl+D` / `Ctrl+U` | Page down/up |
| `<` / `>` | Jump to start/end |
| `Enter` / `l` | Drill into pipelines for selected project |
| `e` | Open explorer (file browser) overlay |
| `/` | Search projects |
| `f` | Toggle favorite |
| `t` | Switch between Favorites and All tabs |
| `[` / `]` | Previous/next page |
| `Ctrl+R` | Force refresh project list |
| `Ctrl+O` | Copy SSH clone command |

### Pipelines Panel

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate pipelines |
| `Enter` / `l` | Drill into stages/jobs |
| `h` / `Esc` | Back to projects |
| `R` | Retry selected pipeline |
| `C` | Cancel selected pipeline |
| `r` | Reload pipeline list |
| `[` / `]` | Previous/next pipeline page |
| `Ctrl+O` | Copy pipeline URL |

### Stages Panel

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate jobs |
| `h` / `Esc` | Back to pipelines |
| `Enter` / `Space` | Toggle matrix group expand/collapse |
| `R` | Retry selected job |
| `P` | Play manual job |
| `C` | Cancel selected job |
| `Ctrl+O` | Copy job URL |

### MRs Panel

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate merge requests |
| `h` / `Esc` | Back to projects |
| `Ctrl+O` | Copy MR URL |

### Detail Pane

| Key | Action |
|-----|--------|
| `j` / `k` | Scroll viewport |
| `J` / `K` / `Ctrl+D` / `Ctrl+U` | Half-page scroll |
| `<` / `>` | Jump to top/bottom |
| `t` | Cycle detail tab (Log/Tests/Artifacts for pipelines; Info/Comments/Diff for MRs) |
| `R` | Retry pipeline or job (depending on context) |

### Explorer Mode

| Key | Action |
|-----|--------|
| `Enter` / `Right` / `l` | Open directory or preview file |
| `Left` / `h` / `Backspace` | Navigate up (from root returns to projects) |
| `J` / `K` | Scroll preview pane |
| `r` | Reload current directory |
| `Esc` | Exit explorer |

## Configuration

Lazylab reads settings from environment variables, CLI flags, or a config file. Precedence (highest to lowest): CLI flags > env vars > config file > defaults.

| Setting | Env Variable | CLI Flag | Default |
|---------|-------------|----------|---------|
| Token | `GITLAB_TOKEN` | `--token` | (required) |
| Host | `GITLAB_HOST` | `--host` | `https://gitlab.com` |
| Projects per page | — | `--projects-per-page` | `30` |
| Log level | — | `--log-level` | `info` |
| Config file | `LAZYLAB_CONFIG` | `--config` | — |

Config files can be YAML, TOML, or JSON (parsed by Viper).

## Architecture

- `internal/config`: Loads host/token settings from environment, config file, and CLI flags (Viper-based precedence)
- `internal/gitlab`: Lightweight wrapper around GitLab client-go for projects, pipelines, jobs, merge requests, trees, and file blobs
- `internal/ui`: Bubble Tea model with multi-panel layout, view logic, caching, and lipgloss styling
- `cmd/lazylab`: CLI entrypoint

### Caching

- **Project list**: Cached at `<os-cache-dir>/lazylab/projects_<host>.json` for instant startup (`~/Library/Caches` on macOS, `~/.cache` on Linux). Use `Ctrl+R` to force refresh.
- **Favorites**: Persisted at `<os-cache-dir>/lazylab/favorites_<host>.json`.
- **Pipeline status**: In-memory LRU cache (last 100 projects) with 5-second refresh interval.

## Development

### Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out | grep total

# Run tests for a single package
go test ./internal/config
go test ./internal/ui
go test ./internal/gitlab
```

### Building

```bash
# Build for current platform
go build ./cmd/lazylab

# Build for all platforms
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

## License

[MIT](LICENSE)
