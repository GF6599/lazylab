use lazylab::gitlab::{Bridge, Job};
use lazylab::tui::app::{Action, App, Cmd, Msg, Overlay};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;

const PROJECT: u64 = 101;

fn job(id: u64, name: &str, status: &str) -> Job {
    serde_json::from_value(json!({"id": id, "name": name, "stage": "test", "status": status}))
        .unwrap()
}

fn loaded() -> App {
    let mut app = App::new(30);
    let mut commands = app.start();
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, OffsetDateTime::now_utc()) {
            commands.extend(app.update(msg));
        }
    }
    app
}

/// The demo app on the jobs pane, with the newest pipeline's jobs replaced.
fn on_jobs(jobs: Vec<Job>, bridges: Vec<Bridge>) -> App {
    let mut app = loaded();
    let pipeline = app.pipeline().unwrap().id;
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs,
        bridges,
    });
    press(&mut app, '3');
    app
}

fn press(app: &mut App, c: char) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::NONE,
    )))
}

fn acts(commands: &[Cmd]) -> Vec<Action> {
    commands
        .iter()
        .filter_map(|c| match c {
            Cmd::Act(action) => Some(action.clone()),
            _ => None,
        })
        .collect()
}

#[test]
fn r_on_a_pipeline_asks_first_and_retries_it_on_yes() {
    // Given: the pipelines pane.
    let mut app = loaded();
    press(&mut app, '2');
    let pipeline = app.pipeline().unwrap().clone();

    // When: R is pressed.
    let asked = press(&mut app, 'R');

    // Then: nothing is sent yet, and the question names the pipeline.
    assert!(acts(&asked).is_empty());
    assert_eq!(app.overlay, Overlay::Confirm);
    let question = app.confirm.as_ref().unwrap().question();
    assert!(
        question.contains(&format!("#{}", pipeline.id)),
        "{question}"
    );

    // When: y is pressed.
    let sent = press(&mut app, 'y');

    // Then: the retry goes to the project, on the pipeline's ref, and the question closes.
    assert_eq!(
        acts(&sent),
        [Action::RetryPipeline {
            project: PROJECT,
            pipeline: pipeline.id,
            git_ref: pipeline.git_ref.clone(),
        }]
    );
    assert_eq!(app.overlay, Overlay::None);
}

#[test]
fn n_or_escape_closes_the_question_and_sends_nothing() {
    for key in [KeyCode::Char('n'), KeyCode::Esc] {
        // Given: the question to retry a pipeline.
        let mut app = loaded();
        press(&mut app, '2');
        press(&mut app, 'R');

        // When: the key is pressed.
        let commands = app.update(Msg::Key(KeyEvent::new(key, KeyModifiers::NONE)));

        // Then: nothing is sent, and the question is gone.
        assert!(acts(&commands).is_empty(), "{key:?}");
        assert_eq!(app.overlay, Overlay::None);
        assert!(app.confirm.is_none());
    }
}

#[test]
fn c_on_a_child_pipeline_job_cancels_it_in_the_project_it_runs_in() {
    // Given: an open bridge whose child pipeline in project 77 runs job e2e.
    let bridge: Bridge = serde_json::from_value(json!({
        "id": 4, "name": "docs", "stage": "deploy", "status": "running", "ref": "main",
        "downstream_pipeline": {"id": 900, "project_id": 77, "status": "running"},
    }))
    .unwrap();
    let mut app = on_jobs(Vec::new(), vec![bridge]);
    app.update(Msg::Key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE)));
    app.update(Msg::ChildJobsLoaded {
        pipeline: 900,
        jobs: vec![job(901, "e2e", "running")],
    });
    press(&mut app, 'j');

    // When: C is pressed and confirmed.
    press(&mut app, 'C');
    let sent = press(&mut app, 'y');

    // Then: the cancel goes to project 77, not the pipeline's own project.
    assert_eq!(
        acts(&sent),
        [Action::CancelJob {
            project: 77,
            job: 901,
            name: "e2e".into(),
        }]
    );
}

#[test]
fn an_action_with_nothing_to_do_says_so_and_asks_nothing() {
    for (status, key, notice) in [
        ("success", 'C', "nothing left to cancel"),
        ("running", 'R', "still running"),
    ] {
        // Given: one job in that state.
        let mut app = on_jobs(vec![job(1, "unit", status)], Vec::new());

        // When: the key is pressed.
        let commands = press(&mut app, key);

        // Then: no question opens, and a notice says why.
        assert!(acts(&commands).is_empty());
        assert_eq!(app.overlay, Overlay::None);
        let shown = app.notice.clone().unwrap_or_default();
        assert!(shown.contains(notice), "{status}: {shown}");
    }
}

#[test]
fn a_group_or_bridge_row_asks_for_a_single_job() {
    // Given: a folded matrix group.
    let mut app = on_jobs(
        vec![job(1, "unit: [a]", "failed"), job(2, "unit: [b]", "failed")],
        Vec::new(),
    );

    // When: R is pressed on it.
    press(&mut app, 'R');

    // Then: no question opens, and a notice asks for one job.
    assert_eq!(app.overlay, Overlay::None);
    assert!(
        app.notice
            .clone()
            .unwrap_or_default()
            .contains("single job")
    );
}

#[test]
fn a_done_action_says_what_it_did_and_loads_the_pipelines_and_jobs_again() {
    // Given: a retry of the selected pipeline was sent.
    let mut app = loaded();
    press(&mut app, '2');
    press(&mut app, 'R');
    let action = acts(&press(&mut app, 'y')).remove(0);
    let pipeline = app.pipeline().unwrap().id;

    // When: GitLab answers that it started a new pipeline.
    let commands = app.update(Msg::Acted {
        action,
        started: Some(pipeline + 7),
    });

    // Then: the notice names the new pipeline, and the lists load again at once.
    let notice = app.notice.clone().unwrap_or_default();
    assert!(notice.contains(&format!("#{}", pipeline + 7)), "{notice}");
    assert!(
        commands
            .iter()
            .any(|c| matches!(c, Cmd::LoadPipelines { .. }))
    );
    assert!(commands.iter().any(|c| matches!(c, Cmd::LoadJobs { .. })));
}

#[test]
fn a_failed_action_shows_its_reason() {
    // Given: a cancel of a running job was sent.
    let mut app = on_jobs(vec![job(1, "unit", "running")], Vec::new());
    press(&mut app, 'C');
    let action = acts(&press(&mut app, 'y')).remove(0);

    // When: GitLab refuses it.
    app.update(Msg::Failed {
        cmd: Cmd::Act(action),
        error: "cancel job: 403 Forbidden".into(),
    });

    // Then: the reason is shown.
    assert_eq!(app.error.as_deref(), Some("cancel job: 403 Forbidden"));
}
