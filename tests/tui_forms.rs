use lazylab::gitlab::{Job, Variable};
use lazylab::tui::app::{Action, App, Cmd, Msg, Overlay};
use lazylab::tui::demo;
use lazylab::tui::view::{Context, draw};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;

const PROJECT: u64 = 101;
const SECRET: &str = "s3cr3t-value-9f2";

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

fn key(app: &mut App, code: KeyCode) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(code, KeyModifiers::NONE)))
}

fn ctrl(app: &mut App, c: char) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::CONTROL,
    )))
}

fn type_text(app: &mut App, text: &str) {
    for c in text.chars() {
        key(app, KeyCode::Char(c));
    }
}

fn variable(key: &str, value: &str) -> Variable {
    Variable {
        key: key.into(),
        value: value.into(),
    }
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

fn screen(app: &App) -> String {
    let mut terminal = Terminal::new(TestBackend::new(120, 32)).unwrap();
    let context = Context {
        host: "gitlab.example.com".into(),
        now: OffsetDateTime::now_utc(),
    };
    terminal.draw(|frame| draw(frame, app, &context)).unwrap();
    let buffer = terminal.backend().buffer();
    (0..buffer.area.height)
        .map(|y| {
            (0..buffer.area.width)
                .map(|x| buffer[(x, y)].symbol())
                .collect::<String>()
        })
        .collect::<Vec<_>>()
        .join("\n")
}

/// The demo app on the jobs pane, with the newest pipeline's jobs replaced by one job.
fn on_job(status: &str) -> App {
    let mut app = loaded();
    let pipeline = app.pipeline().unwrap().id;
    let job: Job = serde_json::from_value(
        json!({"id": 9, "name": "deploy", "stage": "deploy", "status": status}),
    )
    .unwrap();
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs: vec![job],
        bridges: Vec::new(),
    });
    key(&mut app, KeyCode::Char('3'));
    app
}

#[test]
fn n_runs_a_pipeline_on_the_selected_ref_with_the_variables_typed() {
    // Given: the pipelines pane.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    let git_ref = app.pipeline().unwrap().git_ref.clone();

    // When: N opens the form, a variable is added and typed, and enter is pressed.
    key(&mut app, KeyCode::Char('N'));
    assert_eq!(app.overlay, Overlay::Form);
    ctrl(&mut app, 'n');
    type_text(&mut app, "DEPLOY");
    key(&mut app, KeyCode::Tab);
    type_text(&mut app, "yes");
    let sent = key(&mut app, KeyCode::Enter);

    // Then: the pipeline runs on the selected pipeline's ref with that variable, and the form
    // closes.
    assert_eq!(
        acts(&sent),
        [Action::RunPipeline {
            project: PROJECT,
            git_ref,
            variables: vec![variable("DEPLOY", "yes")],
        }]
    );
    assert_eq!(app.overlay, Overlay::None);
    assert!(app.form.is_none());
}

#[test]
fn the_ref_can_be_edited_and_rows_without_a_key_are_left_out() {
    // Given: the run form.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));

    // When: the ref is cleared and typed again, and an empty row is added.
    while app.form.as_ref().unwrap().git_ref.as_deref() != Some("") {
        key(&mut app, KeyCode::Backspace);
    }
    type_text(&mut app, "release/1.0");
    ctrl(&mut app, 'n');
    let sent = key(&mut app, KeyCode::Enter);

    // Then: the pipeline runs on the typed ref, with no variables.
    assert_eq!(
        acts(&sent),
        [Action::RunPipeline {
            project: PROJECT,
            git_ref: "release/1.0".into(),
            variables: Vec::new(),
        }]
    );
}

#[test]
fn every_key_types_into_the_form_until_it_closes() {
    // Given: the run form with a variable row.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    ctrl(&mut app, 'n');

    // When: keys that quit, move and switch panes are typed.
    type_text(&mut app, "qj1?");

    // Then: they are the key's text, and nothing else happened.
    assert!(!app.quit);
    assert_eq!(app.form.as_ref().unwrap().variables[0].0, "qj1?");

    // When: escape is pressed.
    let commands = key(&mut app, KeyCode::Esc);

    // Then: the form closes, and nothing is sent.
    assert!(acts(&commands).is_empty());
    assert_eq!(app.overlay, Overlay::None);
}

#[test]
fn control_d_removes_the_row_in_use() {
    // Given: the run form with two variable rows.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    ctrl(&mut app, 'n');
    type_text(&mut app, "FIRST");
    ctrl(&mut app, 'n');
    type_text(&mut app, "SECOND");

    // When: control d is pressed in the second row.
    ctrl(&mut app, 'd');

    // Then: only the first row is left.
    let keys: Vec<String> = (app.form.as_ref().unwrap().variables.iter())
        .map(|(key, _)| key.clone())
        .collect();
    assert_eq!(keys, ["FIRST"]);
}

#[test]
fn p_plays_a_manual_job_and_enter_on_an_untouched_form_plays_it_plain() {
    // Given: the jobs pane on a manual job.
    let mut app = on_job("manual");

    // When: P opens the form, and enter is pressed at once.
    key(&mut app, KeyCode::Char('P'));
    assert_eq!(app.overlay, Overlay::Form);
    let sent = key(&mut app, KeyCode::Enter);

    // Then: the job plays with no variables.
    assert_eq!(
        acts(&sent),
        [Action::PlayJob {
            project: PROJECT,
            job: 9,
            name: "deploy".into(),
            variables: Vec::new(),
        }]
    );
}

#[test]
fn p_on_a_job_that_is_not_manual_says_so() {
    // Given: the jobs pane on a job that ran.
    let mut app = on_job("success");

    // When: P is pressed.
    key(&mut app, KeyCode::Char('P'));

    // Then: no form opens, and a notice says why.
    assert_eq!(app.overlay, Overlay::None);
    assert!(app.notice.clone().unwrap_or_default().contains("manual"));
}

#[test]
fn a_variable_value_shows_only_in_the_open_form() {
    // Given: the run form with a secret typed as a value.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    ctrl(&mut app, 'n');
    type_text(&mut app, "TOKEN");
    key(&mut app, KeyCode::Tab);
    type_text(&mut app, SECRET);

    // Then: the form shows it while it is typed.
    assert!(screen(&app).contains(SECRET));

    // When: the form is sent, GitLab answers, and later a run fails.
    let action = acts(&key(&mut app, KeyCode::Enter)).remove(0);
    app.update(Msg::Acted {
        action: action.clone(),
        started: Some(1),
    });
    let notice = app.notice.clone().unwrap_or_default();
    let done = screen(&app);
    app.update(Msg::Failed {
        cmd: Cmd::Act(action.clone()),
        error: "create pipeline: 400 Bad Request: [REDACTED]".into(),
    });
    let failed = screen(&app);

    // Then: no notice, screen or debug print carries the value.
    for text in [notice, done, failed, format!("{action:?}")] {
        assert!(!text.contains(SECRET), "{text}");
    }
}
