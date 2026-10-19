# CLAUDE.md

## Project overview

Lazylab is a terminal UI for GitLab, written in Rust. It shows projects, pipelines, jobs and merge requests, and it acts on them: retry, cancel, run, play, comment, resolve and open a merge request. ratatui draws the screen, tokio runs the GitLab calls, reqwest talks to the REST API, clap parses the flags and figment merges the settings. `rust-toolchain.toml` pins the compiler and the clippy lints. The crate is a library (`src/lib.rs`) and a binary (`src/main.rs`), so the integration tests in `tests/` use the library.

## Layout

`src/`:

- `main.rs`: parses the flags, loads the config, opens the log, makes the `Source` and starts the TUI.
- `config.rs`: merges flags, `GITLAB_*` environment variables, a TOML or JSON file, `glab` and defaults. `Token` prints as `[REDACTED]`.
- `glab.rs`: reads the token and host that `glab` stored, with a 3 second timeout.
- `glab_command.rs`: the `glab` commands for an item, which `y` and `Y` copy.
- `gitlab/`: the typed REST client. `mod.rs` holds the client, the errors and the request log. `pipelines.rs`, `actions.rs`, `merge_requests.rs` and `repository.rs` hold the calls of each concern.
- `store.rs`: the cache folder, and the project and favorites files of each host.
- `checkout.rs`: shallow `git` checkouts under `<cache>/checkouts/<host>`.
- `explorer.rs`: gives the terminal to the explorer program, yazi by default.
- `logging.rs`: `tracing` to `lazylab.log` in the cache folder, through `redact`.
- `redact.rs`: removes GitLab credentials from text.
- `termsafe.rs`: removes the escape sequences that act on the terminal, and keeps SGR colour.
- `exit.rs`: the exit codes.
- `tui/mod.rs`: the run loop, the input thread, the tick and the terminal hand-off to the explorer.
- `tui/app.rs`: `App`, `Msg`, `Cmd` and `App::update`, the only place that state changes.
- `tui/source.rs`: `Source`, which answers each `Cmd` from GitLab and the store, or from the demo data.
- `tui/demo.rs`: sample data as GitLab JSON, for `--demo` and for tests.
- `tui/form.rs`: the run, play, comment and new merge request forms.
- `tui/jobs.rs`: the rows of the jobs pane, with matrix groups and child pipelines.
- `tui/diff.rs`: the rows of a merge request diff.
- `tui/log.rs`: a job log as lines, in its colours.
- `tui/clipboard.rs`: the OSC 52 copy.
- `tui/panel.rs`, `tui/text.rs`, `tui/theme.rs`, `tui/mark.rs`: frames and rows, text width and fit, the Rose Pine palette, the `{lazylab}` mark.
- `tui/view/mod.rs`: the layout, the `HELP` key table and the overlays. `chrome.rs` draws the prompt, the pane strip and the status bar. `sidebar.rs` draws the list panes. `detail.rs` draws the detail pane.

`tests/`:

- `gitlab_*.rs`: the GitLab client against wiremock.
- `tui_*.rs`: `App::update` driven by messages, and the screen through `TestBackend`. `tui_layout.rs` holds most snapshots. `tui_source.rs` tests `Source` against wiremock and a temporary store.
- `cli.rs`: the binary, its flags and exit codes.
- `checkout.rs`, `explorer.rs`, `store.rs`, `termsafe.rs`, `glab_command.rs`: one module each.
- `common/mod.rs`: a client for a mock host, and the test token.
- `fixtures/`: GitLab JSON answers and cache files. `snapshots/`: the insta snapshots.

## Commands

| Recipe | Runs |
|---|---|
| `just test` | `cargo test --locked` |
| `just lint` | `cargo fmt --check`, `cargo clippy --all-targets --locked -- -D warnings`, `cargo deny check advisories` |
| `just fmt` | `cargo fmt` |
| `just run [HOST]` | `cargo run --locked -- --host HOST`, default `https://gitlab.com`. `just` loads `.env`, so `GITLAB_TOKEN` can live there. |
| `just build` | `cargo build --release --locked` |
| `just demo` | the release build, then `vhs demo.tape`, which writes `doc/demo.gif` |
| `just release-dry-run` | `dist plan` and `dist build`, into `target/distrib/`. It publishes nothing. |
| `just install` | `just build`, then a copy to `~/self-made-bin/lazylab` and an ad hoc `codesign` |
| `just clean` | removes `target/distrib` and `./lazylab` |

The token is not a recipe argument, because every local user can read a command line.

## Architecture

The TUI is an Elm-like loop.

- `App::update(Msg) -> Vec<Cmd>` in `src/tui/app.rs` changes the state and answers with the work to start. It does no I/O. A `Msg` is a key, a paste, a tick or an answer. A `Cmd` is a load, a save, an action, a copy, a checkout or a browse.
- `Source::answer(Cmd) -> Option<Msg>` in `src/tui/source.rs` does the work. `Source::Live` uses the GitLab client, the store and the checkouts. `Source::Demo` answers from `demo.rs`. A failed call answers `Msg::Failed` with the command and a redacted reason.
- `run` in `src/tui/mod.rs` owns the loop. Keys come from an input thread through the same channel as the answers, and the loop makes `Msg::Tick` itself every `TICK` (100 ms), so it handles one message at a time. `Cmd::Copy` writes OSC 52 to the terminal. `Cmd::Browse` stops the input thread and gives the terminal to the explorer until it exits. The explorer runs on the tokio blocking pool, so the requests in flight go on, and the loop makes no tick until the explorer ends. The loop spawns every other command on the current-thread tokio runtime. The loop draws after each message.

Loads:

- A selection loads its lists once the cursor rests on it for 300 ms (`SETTLE`), so a held `j` asks GitLab only about the row it stops on.
- The per-tab loads go out from the `ask_*` functions at the end of `update`: the job log, the merge request threads, diff and diff refs, and the pipeline test report. Each goes out only while its pane and tab are in view (`wanted`), and an `*_asked` key stops a second request before the first answers. An answer whose key (project and merge request, or project and pipeline) is no longer the one in view is dropped.
- Each project page carries a `load` number, so `ctrl+r` drops the pages of the load it replaced.
- Every 5 seconds (`REFRESH`), when nothing waits, `refresh` asks again for the pipelines and jobs in view, the log of a running job and each open child pipeline.
- A refresh failure shows in the status bar only from the second failure in a row of the same load (`REFRESH_FAILURES_SHOWN`). A success clears it.
- `src/gitlab/mod.rs` logs each request: operation, path, status, HTTP version and duration at `debug`, and a failure at `warn`. The log goes to `lazylab.log` in the cache folder, which each start empties. The default level is `error`, so run with `--log-level debug` to see the requests.

## Testing

- Write tests in `tests/` against the public API of the library. Name a test as a sentence of what it proves, and mark its parts with `// Given:`, `// When:` and `// Then:`.
- Screen tests draw into a ratatui `TestBackend` and compare the text with an insta snapshot in `tests/snapshots/`.
- GitLab client and `Source` tests run against a wiremock server, with JSON from `tests/fixtures/`.
- `src/tui/demo.rs` holds sample data written as GitLab JSON. Most `tui_*.rs` tests answer each `Cmd` with `demo::answer`, so they drive `App` with realistic values and no server.
- To accept a snapshot change without cargo-insta:
  1. Run `INSTA_UPDATE=new cargo test --locked`.
  2. Read each `.snap.new` file under `tests/snapshots/`, then delete it.
  3. Run `INSTA_UPDATE=always cargo test --locked`, and read the diff of each `.snap` file.
- The gate is `just test` and `just lint`. Both must exit 0. `just lint` needs the network, because `cargo deny` fetches the RustSec advisory database.

## Conventions

- Commit subjects are gitmoji in the form `<emoji> <scope>: <Sentence case subject>`, for example `🐛 ui: Show a refresh failure only when it repeats`. The scopes in use are `ui`, `gitlab`, `config`, `test`, `build`, `ci`, `docs` and `repo`.
- Use test-driven development. Commit the failing test first as `🧪 test: ...`, then the change that makes it pass. A test that passes at once, because it pins behaviour that exists, is `✅ test: ...`.
- Token safety:
  - The token never goes in a command-line argument, a URL, a log line or the screen. `Token` has no `Display`, its `Debug` prints `[REDACTED]`, and `Token::expose` is the one way to read it.
  - `src/redact.rs` removes `Authorization`, `PRIVATE-TOKEN`, `private_token=`, `access_token=`, `Bearer` and `gl*-` tokens. Every log line passes through it in `logging.rs`, and so does every error that reaches stderr or the screen.
  - A checkout gives `git` the token only in its environment, as an `http.<origin>/.extraHeader` setting, with redirects off.
- Terminal-safe text:
  - Text from GitLab that leaves through a path other than the ratatui buffer goes through `termsafe::filter`: the OSC 52 clipboard payload, the `LAZYLAB_PROJECT_URL` and `LAZYLAB_REF` variables of the explorer, and the `git` error text of a checkout.
  - Drawn text needs no filter. ratatui writes cells, and its buffer drops each grapheme that holds a control character.
- A checkout folder name comes from GitLab text, so `checkout.rs` percent-encodes each byte other than letters, digits, `_`, `-` and a `.` that is not first.

## Release

- Push a tag `v<version>`. The version must equal `version` in `Cargo.toml`, so change `Cargo.toml` and `Cargo.lock` in a commit before the tag.
- `.github/workflows/release.yml` is kept by hand. It started as the output of `dist generate` from cargo-dist 0.33.0. dist gives `contents: write` to every job and installs itself with `curl` into a shell, and neither has a setting. `dist-workspace.toml` sets `allow-dirty = ["ci"]`, so dist neither rewrites the file nor rejects it. To adopt a newer dist, diff the workflow it generates against this one.
- On a tag, the workflow runs `ci.yml` as the `check` job, builds the four targets in `dist-workspace.toml` (macOS and static musl Linux, each on `aarch64` and `x86_64`), creates the GitHub release, and pushes the formula to `GF6599/homebrew-tap` with `HOMEBREW_TAP_TOKEN`. Only the `host` job has `contents: write`.
- A manual run of the workflow builds every archive and the formula, and publishes nothing. A pull request that changes `Cargo.toml`, `dist-workspace.toml` or `release.yml` runs the plan only.
- `just release-dry-run` builds the archive and the formula for this machine into `target/distrib/`, and publishes nothing.
