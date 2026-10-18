use base64::prelude::{BASE64_STANDARD, Engine};
use lazylab::gitlab::{Bridge, Job, Project};
use lazylab::store::CachedProjects;
use lazylab::tui::app::{App, Cmd, Msg, Pane};
use lazylab::tui::clipboard::osc52;
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;
use url::Url;

const PATH: &str = "acme/platform/api";
const NO_COMMAND: &str = "No glab command for this selection";

fn settle(app: &mut App, mut commands: Vec<Cmd>) {
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, OffsetDateTime::now_utc()) {
            commands.extend(app.update(msg));
        }
    }
}

fn loaded(app: App) -> App {
    let mut app = app;
    let commands = app.start();
    settle(&mut app, commands);
    app
}

fn press(app: &mut App, code: KeyCode, modifiers: KeyModifiers) -> Vec<Cmd> {
    let commands = app.update(Msg::Key(KeyEvent::new(code, modifiers)));
    let kept = commands.clone();
    settle(app, commands);
    kept
}

fn key(app: &mut App, c: char) -> Vec<Cmd> {
    press(app, KeyCode::Char(c), KeyModifiers::NONE)
}

fn ctrl_o(app: &mut App) -> Vec<Cmd> {
    press(app, KeyCode::Char('o'), KeyModifiers::CONTROL)
}

fn copies(commands: &[Cmd]) -> Vec<String> {
    commands
        .iter()
        .filter_map(|c| match c {
            Cmd::Copy(text) => Some(text.clone()),
            _ => None,
        })
        .collect()
}

fn job(id: u64, name: &str) -> Job {
    serde_json::from_value(json!({
        "id": id, "name": name, "stage": "test", "status": "success",
        "web_url": format!("https://gitlab.example.com/{PATH}/-/jobs/{id}"),
    }))
    .unwrap()
}

/// The demo app with the newest pipeline's jobs replaced, on the jobs pane.
fn on_jobs(jobs: Vec<Job>, bridges: Vec<Bridge>) -> App {
    let mut app = loaded(App::new(30));
    let pipeline = app.pipeline().unwrap().id;
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs,
        bridges,
    });
    key(&mut app, '3');
    app
}

#[test]
fn y_copies_the_first_glab_command_of_the_item_in_each_pane_and_its_detail() {
    // Given: the demo app, with the item each pane selects at its first row.
    let mut app = loaded(App::new(30));
    let pipeline = app.pipeline().unwrap().id;
    let job = app.job().unwrap().id;
    let merge_request = app.merge_requests[0].iid;
    let cases = [
        ('1', format!("glab repo view {PATH}")),
        ('2', format!("glab ci get -p {pipeline} -R {PATH}")),
        ('3', format!("glab ci trace {job} -R {PATH}")),
        ('4', format!("glab mr view {merge_request} -R {PATH}")),
    ];
    for (pane, want) in cases {
        for detail in [false, true] {
            key(&mut app, pane);
            key(&mut app, if detail { 'l' } else { 'h' });

            // When: y is pressed.
            let commands = key(&mut app, 'y');

            // Then: the command goes to the clipboard, and the status line says so.
            assert_eq!(
                copies(&commands),
                [want.clone()],
                "pane {pane}, detail {detail}"
            );
            assert_eq!(app.notice, Some(format!("Copied: {want}")));
        }
    }
}

#[test]
fn ctrl_o_copies_the_web_address_of_the_item_in_each_pane_and_its_detail() {
    // Given: the demo app, with the item each pane selects at its first row.
    let mut app = loaded(App::new(30));
    let pipeline = app.pipeline().unwrap().id;
    let job = app.job().unwrap().clone();
    let merge_request = app.merge_requests[0].iid;
    let web = "https://gitlab.example.com";
    let cases = [
        ('1', format!("{web}/{PATH}"), format!("Copied {PATH} URL")),
        (
            '2',
            format!("{web}/{PATH}/-/pipelines/{pipeline}"),
            format!("Copied pipeline #{pipeline} URL"),
        ),
        (
            '3',
            format!("{web}/{PATH}/-/jobs/{}", job.id),
            format!("Copied job {} URL", job.name),
        ),
        (
            '4',
            format!("{web}/merge_requests/{merge_request}"),
            format!("Copied !{merge_request} URL"),
        ),
    ];
    for (pane, want, notice) in cases {
        for detail in [false, true] {
            key(&mut app, pane);
            key(&mut app, if detail { 'l' } else { 'h' });

            // When: ctrl+o is pressed.
            let commands = ctrl_o(&mut app);

            // Then: the address goes to the clipboard, and the status line says so.
            assert_eq!(
                copies(&commands),
                [want.clone()],
                "pane {pane}, detail {detail}"
            );
            assert_eq!(app.notice.as_deref(), Some(notice.as_str()));
        }
    }
}

#[test]
fn an_empty_list_copies_nothing_and_says_why() {
    // Given: no project has loaded yet.
    let mut app = App::new(30);
    let reasons = [
        ('1', "No project selected"),
        ('2', "No pipeline selected"),
        ('3', "No job selected"),
        ('4', "No merge request selected"),
    ];
    for (pane, reason) in reasons {
        key(&mut app, pane);

        // When: y is pressed.
        let commands = key(&mut app, 'y');

        // Then: nothing is copied, and the status line says there is no command.
        assert!(copies(&commands).is_empty(), "pane {pane}");
        assert_eq!(app.notice.as_deref(), Some(NO_COMMAND));

        // When: ctrl+o is pressed.
        let commands = ctrl_o(&mut app);

        // Then: nothing is copied, and the status line says what is missing.
        assert!(copies(&commands).is_empty(), "pane {pane}");
        assert_eq!(app.notice.as_deref(), Some(reason));
    }
}

#[test]
fn an_item_without_a_web_address_copies_nothing_and_says_so() {
    // Given: a job that GitLab gave no web address.
    let mut bare = job(7, "unit");
    bare.web_url.clear();
    let mut app = on_jobs(vec![bare], Vec::new());

    // When: ctrl+o is pressed.
    let commands = ctrl_o(&mut app);

    // Then: nothing is copied, and the status line says why.
    assert!(copies(&commands).is_empty());
    assert_eq!(app.notice.as_deref(), Some("Job has no URL"));
}

#[test]
fn a_row_that_is_not_one_job_of_the_project_has_no_glab_command() {
    // Given: a matrix group, and an open bridge whose child pipeline runs in project 77.
    let bridge: Bridge = serde_json::from_value(json!({
        "id": 4, "name": "docs", "stage": "deploy", "status": "running", "ref": "main",
        "downstream_pipeline": {"id": 900, "project_id": 77, "status": "running"},
    }))
    .unwrap();
    let matrix = vec![job(1, "unit: [go, 1.21]"), job(2, "unit: [go, 1.22]")];
    let mut app = on_jobs(matrix, vec![bridge]);
    key(&mut app, 'G');
    press(&mut app, KeyCode::Enter, KeyModifiers::NONE);
    app.update(Msg::ChildJobsLoaded {
        pipeline: 900,
        jobs: vec![job(901, "e2e")],
    });
    for (row, name) in [
        (0, "the matrix group"),
        (1, "the bridge"),
        (2, "the child job"),
    ] {
        key(&mut app, 'g');
        for _ in 0..row {
            key(&mut app, 'j');
        }
        assert_eq!(app.cursor(Pane::Jobs), row, "{name}");

        // When: y is pressed.
        let commands = key(&mut app, 'y');

        // Then: nothing is copied, because no glab command could name the job and its project.
        assert!(copies(&commands).is_empty(), "{name}");
        assert_eq!(app.notice.as_deref(), Some(NO_COMMAND), "{name}");
    }

    // When: ctrl+o is pressed on the child job.
    let commands = ctrl_o(&mut app);

    // Then: its web address is copied, because that names its project by itself.
    assert_eq!(
        copies(&commands),
        [format!("https://gitlab.example.com/{PATH}/-/jobs/901")]
    );
}

#[test]
fn a_project_on_another_instance_is_named_by_its_address() {
    // Given: the demo app on a self-hosted GitLab.
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let mut app = loaded(App::new(30).with_host(host));

    // When: y is pressed on the first project.
    let commands = key(&mut app, 'y');

    // Then: -R names the project by its full address, so glab does not use its default host.
    assert_eq!(
        copies(&commands),
        [format!("glab repo view https://gitlab.example.com/{PATH}")]
    );
}

#[test]
fn an_escape_sequence_in_a_project_path_never_reaches_the_terminal() {
    // Given: a project whose path carries a clipboard write, a bell and a carriage return.
    let project: Project = serde_json::from_value(json!({
        "id": 5, "name": "widgets",
        "path_with_namespace": "acme/\x1b]52;c;ZXZpbA==\x07wid\rgets",
        "web_url": "https://gitlab.example.com/acme/widgets",
    }))
    .unwrap();
    let mut app = App::new(30);
    app.update(Msg::CacheLoaded(Some(CachedProjects {
        projects: vec![project],
        total: 1,
    })));

    // When: y is pressed, and the copy is written as OSC 52.
    let copied = copies(&key(&mut app, 'y'));
    let written = osc52(&copied[0]);

    // Then: the bytes hold one OSC 52 write of the command, with only the printable path in it.
    let clean = BASE64_STANDARD.encode("glab repo view 'acme/widgets'");
    assert_eq!(written, format!("\x1b]52;c;{clean}\x07").into_bytes());
}
