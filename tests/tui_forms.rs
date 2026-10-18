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

/// Draws the app and checks that the cursor sits inside the box of the field being typed, on
/// its free cell, after `last`, the last character typed. The answer is the row of the cursor.
fn assert_cursor_after(app: &App, last: &str) -> String {
    let mut terminal = Terminal::new(TestBackend::new(120, 32)).unwrap();
    let context = Context {
        host: "gitlab.example.com".into(),
        now: OffsetDateTime::now_utc(),
    };
    terminal.draw(|frame| draw(frame, app, &context)).unwrap();
    let cursor = terminal.get_cursor_position().unwrap();
    let buffer = terminal.backend().buffer();
    let cells: Vec<&str> = (0..buffer.area.width)
        .map(|x| buffer[(x, cursor.y)].symbol())
        .collect();
    let row = cells.concat();
    let at = usize::from(cursor.x);
    let marker = |c: &&str| *c == "[" || *c == "]";
    let open = cells[..at].iter().rposition(marker);
    let close = cells[at..].iter().position(marker).map(|i| at + i);
    assert!(
        open.is_some_and(|o| cells[o] == "[") && close.is_some_and(|c| c > at && cells[c] == "]"),
        "cursor at {at}, outside the box: {row}"
    );
    assert_eq!(cells[at - 1], last, "{row}");
    assert_eq!(cells[at], " ", "{row}");
    row
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
    // Given: the pipelines pane, on a pipeline that ran on a branch that is not the default.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('j'));
    let git_ref = app.pipeline().unwrap().git_ref.clone();
    assert_ne!(git_ref, "main");

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
    let seeded = app.form.as_ref().unwrap().git_ref.clone().unwrap();
    for _ in seeded.chars() {
        key(&mut app, KeyCode::Backspace);
    }
    assert_eq!(app.form.as_ref().unwrap().git_ref.as_deref(), Some(""));
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
    let run = || {
        let mut app = loaded();
        key(&mut app, KeyCode::Char('2'));
        key(&mut app, KeyCode::Char('N'));
        app
    };
    let play = || {
        let mut app = on_job("manual");
        key(&mut app, KeyCode::Char('P'));
        app
    };
    for open in [run, play] {
        // Given: a run or play form with a secret typed as a value.
        let mut app = open();
        ctrl(&mut app, 'n');
        type_text(&mut app, "TOKEN");
        key(&mut app, KeyCode::Tab);
        type_text(&mut app, SECRET);

        // Then: the form shows it while it is typed, and a debug print of the form does not.
        assert!(screen(&app).contains(SECRET));
        let form = format!("{:?}", app.form);
        assert!(!form.contains(SECRET), "{form}");

        // When: the form is sent, GitLab answers, and later the action fails.
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
}

#[test]
fn a_paste_types_into_the_field_without_its_newlines_and_the_form_stays_open() {
    // Given: the play form on a manual job, on the value of a variable row.
    let mut app = on_job("manual");
    key(&mut app, KeyCode::Char('P'));
    ctrl(&mut app, 'n');
    type_text(&mut app, "TOKEN");
    key(&mut app, KeyCode::Tab);

    // When: a value with a line break in it and a newline at its end is pasted.
    let commands = app.update(Msg::Paste("abc\r\ndef\n".into()));

    // Then: nothing is sent, the form is still open, and the value holds the text alone.
    assert!(acts(&commands).is_empty());
    assert_eq!(app.overlay, Overlay::Form);
    assert_eq!(
        app.form.as_ref().unwrap().variables,
        [("TOKEN".to_string(), "abcdef".to_string())]
    );
}

#[test]
fn a_paste_is_never_a_command() {
    // Given: the pipelines pane.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));

    // When: text that holds the keys to quit and to run a pipeline is pasted.
    let commands = app.update(Msg::Paste("qN\n".into()));

    // Then: nothing happens.
    assert!(commands.is_empty());
    assert!(!app.quit);
    assert_eq!(app.overlay, Overlay::None);

    // When: the question to cancel opens on a running job, and y is pasted.
    let mut app = on_job("running");
    key(&mut app, KeyCode::Char('C'));
    let commands = app.update(Msg::Paste("y".into()));

    // Then: nothing is sent, and the question is still open.
    assert!(acts(&commands).is_empty());
    assert_eq!(app.overlay, Overlay::Confirm);
}

#[test]
fn a_value_wider_than_its_box_shows_its_end_with_the_cursor_after_it() {
    // Given: the run form, on the value of a variable row.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    ctrl(&mut app, 'n');
    type_text(&mut app, "TOKEN");
    key(&mut app, KeyCode::Tab);

    // When: a value of 40 characters is typed, and the form is drawn.
    type_text(&mut app, "0123456789abcdefghijklmnopqrstuvwxyzWXYZ");

    // Then: the row shows the end of the value after an ellipsis, and the cursor sits after the
    // last character typed, inside the box, before its closing marker.
    let row = assert_cursor_after(&app, "Z");
    assert!(row.contains("…") && row.contains("uvwxyzWXYZ"), "{row}");

    // When: the cursor moves back to the key.
    key(&mut app, KeyCode::BackTab);

    // Then: the value is no longer typed, so it shows from its start.
    let shown = screen(&app);
    assert!(shown.contains("0123456789abcdefghijklmnop…"), "{shown}");
}

#[test]
fn a_key_wider_than_its_box_shows_its_end_with_the_cursor_after_it() {
    // Given: the run form, on the key of a variable row.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    ctrl(&mut app, 'n');

    // When: a key of 30 characters is typed, and the form is drawn.
    type_text(&mut app, "abcdefghijklmnopqrstuvwxyzWXYZ");

    // Then: the row shows the end of the key after an ellipsis, and the cursor sits after the
    // last character typed, inside the box, before its closing marker.
    let row = assert_cursor_after(&app, "Z");
    assert!(row.contains("…") && row.contains("uvwxyzWXYZ"), "{row}");
}

#[test]
fn a_ref_wider_than_its_box_shows_its_end_with_the_cursor_after_it() {
    // Given: the run form, on its ref, cleared.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    let seeded = app.form.as_ref().unwrap().git_ref.clone().unwrap();
    for _ in seeded.chars() {
        key(&mut app, KeyCode::Backspace);
    }

    // When: a ref of 54 characters is typed, and the form is drawn.
    type_text(
        &mut app,
        "release/0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJ",
    );

    // Then: the row shows the end of the ref after an ellipsis, and the cursor sits after the
    // last character typed, inside the box, before its closing marker.
    let row = assert_cursor_after(&app, "J");
    assert!(row.contains("…") && row.contains("wxyzABCDEFGHIJ"), "{row}");
}

#[test]
fn a_paste_into_the_ref_stays_one_line() {
    // Given: the run form, on its ref.
    let mut app = loaded();
    key(&mut app, KeyCode::Char('2'));
    key(&mut app, KeyCode::Char('N'));
    let seeded = app.form.as_ref().unwrap().git_ref.clone().unwrap();

    // When: text with a line break is pasted.
    app.update(Msg::Paste("a\r\nb".into()));

    // Then: the ref holds the text without the break.
    let git_ref = app.form.as_ref().unwrap().git_ref.clone().unwrap();
    assert_eq!(git_ref, format!("{seeded}ab"));
}
