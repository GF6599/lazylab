use std::path::PathBuf;

use lazylab::checkout::Revision;
use lazylab::config::{Mode, Token};
use lazylab::explorer::Browse;
use lazylab::gitlab::Project;
use lazylab::store::CachedProjects;
use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::{Source, demo};
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;

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
fn a_finished_checkout_says_where_its_files_are() {
    // Given: a checkout on its way.
    let mut app = loaded();
    app.update(char('e'));

    // When: it is ready.
    let commands = app.update(Msg::CheckedOut(browse()));

    // Then: nothing more goes out, and the status line names the folder.
    assert_eq!(commands, []);
    let dir = browse().dir;
    assert_eq!(
        app.notice,
        Some(format!("Checked out main in {}", dir.display()))
    );
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
