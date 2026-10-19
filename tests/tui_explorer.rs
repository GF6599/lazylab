mod common;

use std::path::PathBuf;
use std::sync::mpsc::{Sender, channel};
use std::time::{Duration, Instant};

use lazylab::checkout::Revision;
use lazylab::config::{Mode, Token};
use lazylab::explorer::{Browse, Ended};
use lazylab::gitlab::Project;
use lazylab::store::CachedProjects;
use lazylab::tui::app::{App, Cmd, Msg, TICK};
use lazylab::tui::{Source, demo, run};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;
use tokio::sync::mpsc;
use wiremock::matchers::{method, path};
use wiremock::{Mock, MockServer, Request, Respond, ResponseTemplate};

const PATH: &str = "acme/platform/api";
const WEB_URL: &str = "https://gitlab.example.com/acme/platform/api";

fn settle(app: &mut App, mut commands: Vec<Cmd>) {
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, OffsetDateTime::now_utc()) {
            commands.extend(app.update(msg));
        }
    }
}

fn loaded() -> App {
    let mut app = App::new(30);
    let commands = app.start();
    settle(&mut app, commands);
    app
}

fn char(c: char) -> Msg {
    Msg::Key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE))
}

/// Presses `c` and returns the commands it gave, which are then answered by the demo source.
fn key(app: &mut App, c: char) -> Vec<Cmd> {
    let commands = app.update(char(c));
    settle(app, commands.clone());
    commands
}

fn check_out(revision: Revision) -> Cmd {
    Cmd::CheckOut {
        project: PATH.into(),
        web_url: WEB_URL.into(),
        revision,
    }
}

fn browse() -> Browse {
    Browse {
        dir: PathBuf::from("/cache/lazylab/checkouts/https_gitlab_example_com/x/main"),
        project_url: WEB_URL.into(),
        git_ref: "main".into(),
    }
}

#[test]
fn e_checks_out_the_ref_of_the_item_in_view_in_each_pane() {
    // Given: the demo app, with the item each pane selects at its first row.
    let mut app = loaded();
    let sha = app.pipeline().unwrap().sha.clone();
    let source = app.merge_requests[0].source_branch.clone();
    let cases = [
        ('1', Revision::Branch("main".into())),
        ('2', Revision::Commit(sha.clone())),
        ('3', Revision::Commit(sha)),
        ('4', Revision::Branch(source)),
    ];
    for (pane, revision) in cases {
        key(&mut app, pane);

        // When: e is pressed.
        let commands = app.update(char('e'));

        // Then: the project is checked out at the default branch, the pipeline commit, or the
        // merge request source branch, and the status line says so.
        let name = revision.name().to_string();
        assert_eq!(commands, [check_out(revision)], "pane {pane}");
        assert_eq!(app.notice, Some(format!("Checking out {PATH} at {name}")));
        settle(&mut app, commands);
    }
}

#[test]
fn a_project_without_a_default_branch_is_checked_out_at_main() {
    // Given: a project that names no default branch.
    let mut app = App::new(30);
    let project: Project = serde_json::from_value(json!({
        "id": 1, "name": "api", "path_with_namespace": PATH, "web_url": WEB_URL,
    }))
    .unwrap();
    app.update(Msg::CacheLoaded(Some(CachedProjects {
        projects: vec![project],
        total: 1,
    })));

    // When: e is pressed.
    let commands = app.update(char('e'));

    // Then: main is checked out, as in the Go release.
    assert_eq!(commands, [check_out(Revision::Branch("main".into()))]);
}

#[test]
fn a_second_e_waits_for_the_checkout_and_a_failed_checkout_says_why() {
    // Given: a checkout on its way.
    let mut app = loaded();
    let asked = app.update(char('e'));

    // When: e is pressed again, then the checkout fails.
    let again = app.update(char('e'));
    let failed = Msg::Failed {
        cmd: asked[0].clone(),
        error: "git fetch failed: fatal: couldn't find remote ref".into(),
    };
    app.update(failed);

    // Then: the second e asks for nothing, and the failure names the project, the ref and why.
    assert_eq!(again, []);
    assert_eq!(
        app.error.as_deref(),
        Some(
            "Could not check out acme/platform/api at main: \
             git fetch failed: fatal: couldn't find remote ref"
        )
    );
    // And: the next e asks again.
    assert_eq!(app.update(char('e')), asked);
}

#[test]
fn a_finished_checkout_goes_to_the_explorer_and_its_end_shows_in_the_status_line() {
    // Given: a checkout on its way.
    let mut app = loaded();
    app.update(char('e'));

    // When: it is ready.
    let commands = app.update(Msg::CheckedOut(browse()));

    // Then: the terminal goes to the explorer.
    assert_eq!(commands, [Cmd::Browse(browse())]);
    // And: each end of the explorer reads as a status, and a clean end clears the line.
    let ends = [
        (Ended::Missing, Some("yazi is not installed")),
        (Ended::Exited(Some(3)), Some("yazi exited with code 3")),
        (Ended::Exited(None), Some("yazi was stopped by a signal")),
        (
            Ended::Relative,
            Some("yazi is a relative path, set explorer to a name or an absolute path"),
        ),
        (
            Ended::NotStarted("Permission denied".into()),
            Some("yazi did not start: Permission denied"),
        ),
        (Ended::Closed, None),
    ];
    for (ended, error) in ends {
        app.update(Msg::Browsed {
            program: "yazi".into(),
            ended,
        });
        assert_eq!(app.error.as_deref(), error);
        assert_eq!(app.notice, None);
    }
}

#[tokio::test]
async fn the_loop_hands_the_terminal_to_the_explorer_and_reads_how_it_ended() {
    // Given: a screen that reports a finished checkout once the projects are shown, and keeps
    // the first error the status line shows, then quits.
    let mut terminal = Terminal::new(TestBackend::new(80, 24)).unwrap();
    let (outbox, inbox) = mpsc::unbounded_channel();
    let keys = outbox.clone();
    let mut sent = false;
    let mut shown = None;
    let draw = |_: &mut ratatui::Frame, app: &App| {
        if !app.projects.is_empty() && !sent {
            keys.send(Msg::CheckedOut(browse())).unwrap();
            sent = true;
        }
        if app.error.is_some() && shown.is_none() {
            shown = app.error.clone();
            keys.send(char('q')).unwrap();
        }
    };
    let (seen, browsed) = channel();
    let explore = move |request: &Browse| {
        seen.send(request.clone()).unwrap();
        Msg::Browsed {
            program: "yazi".into(),
            ended: Ended::Exited(Some(2)),
        }
    };

    let mut clipboard = Vec::new();

    // When: the loop runs, for at most five seconds.
    let running = run(
        &mut terminal,
        App::new(30),
        Source::Demo,
        (outbox, inbox),
        &mut clipboard,
        draw,
        explore,
    );
    tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("the loop ends once q arrives")
        .unwrap();

    // Then: the explorer got the checkout once, and its end reached the status line.
    assert_eq!(browsed.try_iter().collect::<Vec<_>>(), [browse()]);
    assert_eq!(shown.as_deref(), Some("yazi exited with code 2"));
}

/// Answers with an empty list, and tells the test each time it gets a request.
struct Served(Sender<()>);

impl Respond for Served {
    fn respond(&self, _: &Request) -> ResponseTemplate {
        let _ = self.0.send(());
        ResponseTemplate::new(200).set_body_json(json!([]))
    }
}

#[tokio::test]
async fn a_load_in_flight_goes_on_while_the_explorer_is_open() {
    // Given: a GitLab that answers the projects page with a redirect after half a second, and
    // reports when the client follows it. A client that cannot run while the explorer is open
    // never reads the redirect, even when it sent the first request before the explorer opened.
    let server = MockServer::start().await;
    let moved = format!("{}/api/v4/moved", server.uri());
    Mock::given(method("GET"))
        .and(path("/api/v4/projects"))
        .respond_with(
            ResponseTemplate::new(302)
                .insert_header("location", moved.as_str())
                .set_delay(Duration::from_millis(500)),
        )
        .mount(&server)
        .await;
    let (served, requests) = channel();
    Mock::given(method("GET"))
        .and(path("/api/v4/moved"))
        .respond_with(Served(served))
        .mount(&server)
        .await;
    let source = Source::live(common::client_for(&server.uri()), None);
    let mut terminal = Terminal::new(TestBackend::new(80, 24)).unwrap();
    let (outbox, inbox) = mpsc::unbounded_channel();
    let keys = outbox.clone();
    let mut sent = false;
    let draw = move |_: &mut ratatui::Frame, app: &App| {
        if app.loading && !sent {
            keys.send(Msg::CheckedOut(browse())).unwrap();
            sent = true;
        }
    };
    // And: an explorer that stays open until the redirect is followed, for at most two seconds.
    let quit = outbox.clone();
    let (heard, reached) = channel();
    let explore = move |_: &Browse| {
        let request = requests.recv_timeout(Duration::from_secs(2));
        heard.send(request.is_ok()).unwrap();
        quit.send(char('q')).unwrap();
        Msg::Browsed {
            program: "yazi".into(),
            ended: Ended::Closed,
        }
    };
    let mut clipboard = Vec::new();

    // When: the loop runs, for at most five seconds.
    let running = run(
        &mut terminal,
        App::new(30),
        source,
        (outbox, inbox),
        &mut clipboard,
        draw,
        explore,
    );
    tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("the loop ends once q arrives")
        .unwrap();

    // Then: the client read the redirect and followed it while the explorer was still open.
    assert_eq!(reached.try_iter().collect::<Vec<_>>(), [true]);
}

#[tokio::test]
async fn the_ticks_the_explorer_spans_do_not_arrive_at_once_when_it_ends() {
    // Given: a screen that reports a finished checkout once the projects are shown and notes the
    // clock. It notes the clock again half a tick after the end of the explorer shows, so any
    // ticks that wait in the loop have arrived, then quits.
    let mut terminal = Terminal::new(TestBackend::new(80, 24)).unwrap();
    let (outbox, inbox) = mpsc::unbounded_channel();
    let keys = outbox.clone();
    let mut before = None;
    let mut ended = None;
    let mut after = None;
    let draw = |_: &mut ratatui::Frame, app: &App| {
        if !app.projects.is_empty() && before.is_none() {
            before = Some(app.tick);
            keys.send(Msg::CheckedOut(browse())).unwrap();
        }
        if app.error.is_some() && ended.is_none() {
            ended = Some(Instant::now());
        }
        if ended.is_some_and(|at| at.elapsed() >= TICK / 2) && after.is_none() {
            after = Some(app.tick);
            keys.send(char('q')).unwrap();
        }
    };
    // And: an explorer that stays open for ten ticks.
    let open = TICK * 10;
    let explore = move |_: &Browse| {
        std::thread::sleep(open);
        Msg::Browsed {
            program: "yazi".into(),
            ended: Ended::Exited(Some(2)),
        }
    };
    let mut clipboard = Vec::new();

    // When: the loop runs, for at most five seconds.
    let running = run(
        &mut terminal,
        App::new(30),
        Source::Demo,
        (outbox, inbox),
        &mut clipboard,
        draw,
        explore,
    );
    tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("the loop ends once q arrives")
        .unwrap();

    // Then: the clock moved a few steps around the explorer, not the ten it was open for.
    let moved = after.unwrap() - before.unwrap();
    assert!(moved < 5, "the clock moved {moved} steps");
}

fn git(dir: &std::path::Path, args: &[&str]) {
    let status = std::process::Command::new("git")
        .args(["-c", "user.name=t", "-c", "user.email=t@example.com"])
        .args(["-c", "commit.gpgsign=false"])
        .args(args)
        .current_dir(dir)
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .status()
        .unwrap();
    assert!(status.success(), "git {args:?}");
}

#[tokio::test]
async fn the_live_source_answers_a_checkout_with_the_folder_or_the_reason() {
    // Given: a GitLab at a local folder that serves the project with one commit on main.
    let temp = tempfile::TempDir::new().unwrap();
    let bare = temp.path().join("remote").join(format!("{PATH}.git"));
    std::fs::create_dir_all(&bare).unwrap();
    git(&bare, &["init", "--quiet", "--bare"]);
    git(temp.path(), &["init", "--quiet", "work"]);
    let work = temp.path().join("work");
    git(&work, &["commit", "--quiet", "--allow-empty", "-m", "one"]);
    git(
        &work,
        &["push", "--quiet", bare.to_str().unwrap(), "HEAD:main"],
    );
    let host = url::Url::from_directory_path(temp.path().join("remote")).unwrap();
    let cache = temp.path().join("cache");
    let live = Mode::Live(Token::new("t0ken"));
    let source = Source::new(&host, &live, Some(cache.clone())).unwrap();
    let without = Source::new(&host, &live, None).unwrap();

    // When: main is checked out, then a branch the project lacks, then main with no cache.
    let main = check_out(Revision::Branch("main".into()));
    let lacking = check_out(Revision::Branch("lacking".into()));
    let found = source.answer(main.clone()).await;
    let missing = source.answer(lacking.clone()).await;
    let uncached = without.answer(main.clone()).await;

    // Then: the found branch is a folder to browse, and each failure says why.
    let Some(Msg::CheckedOut(found)) = found else {
        panic!("not checked out: {found:?}")
    };
    assert!(found.dir.starts_with(&cache) && found.dir.join(".git").is_dir());
    assert_eq!(
        (found.project_url, found.git_ref),
        (WEB_URL.into(), "main".into())
    );
    let reason = |answer: Option<Msg>, cmd: &Cmd| match answer {
        Some(Msg::Failed { cmd: failed, error }) if failed == *cmd => error,
        other => panic!("not a failure of {cmd:?}: {other:?}"),
    };
    assert!(reason(missing, &lacking).contains("git fetch failed"));
    assert!(reason(uncached, &main).contains("no cache folder"));
}

#[test]
fn e_in_the_demo_says_it_has_no_files_and_asks_for_no_checkout() {
    // Given: a demo session with its sample data.
    let mut app = App::new(30).demo();
    let commands = app.start();
    settle(&mut app, commands);
    for pane in ['1', '2', '3', '4'] {
        key(&mut app, pane);

        // When: e is pressed.
        let commands = app.update(char('e'));

        // Then: no command goes out, and the status line says why.
        assert_eq!(commands, [], "pane {pane}");
        assert_eq!(
            app.notice.as_deref(),
            Some("The demo has no files to browse")
        );
    }
}

#[tokio::test]
async fn a_demo_run_answers_e_with_the_demo_message() {
    // Given: a demo run that presses e once the projects are shown, and keeps what the status
    // line shows after it.
    let mut terminal = Terminal::new(TestBackend::new(80, 24)).unwrap();
    let (outbox, inbox) = mpsc::unbounded_channel();
    let keys = outbox.clone();
    let mut pressed = false;
    let mut shown = None;
    let draw = |_: &mut ratatui::Frame, app: &App| {
        if pressed && shown.is_none() && app.notice.is_some() {
            shown = app.notice.clone();
            keys.send(char('q')).unwrap();
        }
        if !app.projects.is_empty() && !pressed {
            keys.send(char('e')).unwrap();
            pressed = true;
        }
    };
    let mut clipboard = Vec::new();

    // When: the loop runs, for at most five seconds.
    let running = run(
        &mut terminal,
        App::new(30),
        Source::Demo,
        (outbox, inbox),
        &mut clipboard,
        draw,
        |_: &Browse| -> Msg { unreachable!("the demo has no checkout to browse") },
    );
    tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("the loop ends once q arrives")
        .unwrap();

    // Then: the status line said the demo has no files.
    assert_eq!(shown.as_deref(), Some("The demo has no files to browse"));
}
