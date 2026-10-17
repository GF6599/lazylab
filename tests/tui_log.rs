use lazylab::gitlab::Job;
use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::demo;
use lazylab::tui::view::{Context, draw};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::buffer::Buffer;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::style::Color;
use serde_json::json;
use time::OffsetDateTime;

const PROJECT: u64 = 101;
/// Three ticks of 100 ms, the wait before a moved cursor loads.
const SETTLE_TICKS: usize = 3;
/// Fifty ticks of 100 ms, the five-second refresh.
const REFRESH_TICKS: usize = 50;

fn job(id: u64, status: &str) -> Job {
    serde_json::from_value(
        json!({"id": id, "name": format!("job-{id}"), "stage": "test", "status": status}),
    )
    .unwrap()
}

/// The demo app with the newest pipeline's jobs replaced by `jobs`, still on the projects pane.
fn with_jobs(jobs: Vec<Job>) -> App {
    let mut app = App::new(30);
    let mut commands = app.start();
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, OffsetDateTime::now_utc()) {
            commands.extend(app.update(msg));
        }
    }
    let pipeline = app.pipeline().unwrap().id;
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs,
        bridges: Vec::new(),
    });
    app
}

fn press(app: &mut App, code: KeyCode) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(code, KeyModifiers::NONE)))
}

fn ticks(app: &mut App, n: usize) -> Vec<Cmd> {
    (0..n).flat_map(|_| app.update(Msg::Tick)).collect()
}

fn log_requests(commands: &[Cmd]) -> Vec<(u64, u64)> {
    commands
        .iter()
        .filter_map(|c| match c {
            Cmd::LoadLog { project, job } => Some((*project, *job)),
            _ => None,
        })
        .collect()
}

fn lines(n: usize) -> String {
    (1..=n).map(|i| format!("line {i}\n")).collect()
}

#[test]
fn the_jobs_pane_loads_the_log_of_its_job_and_waits_while_the_cursor_moves() {
    // Given: two jobs, with the projects pane in view.
    let mut app = with_jobs(vec![job(1, "success"), job(2, "running")]);

    // When: the jobs pane comes into view.
    let shown = press(&mut app, KeyCode::Char('3'));

    // Then: the first job's log is asked for at once.
    assert_eq!(log_requests(&shown), [(PROJECT, 1)]);

    // When: the cursor moves to the second job, and rests.
    let moving = press(&mut app, KeyCode::Char('j'));
    let settled = ticks(&mut app, SETTLE_TICKS);

    // Then: its log is asked for once the cursor rests, not before.
    assert!(log_requests(&moving).is_empty());
    assert_eq!(log_requests(&settled), [(PROJECT, 2)]);
}

#[test]
fn no_log_is_asked_for_while_the_jobs_pane_is_out_of_view() {
    // Given: jobs, with the projects pane in view.
    let mut app = with_jobs(vec![job(1, "running")]);

    // When: five seconds pass.
    let commands = ticks(&mut app, REFRESH_TICKS);

    // Then: no log is asked for.
    assert!(log_requests(&commands).is_empty());
}

#[test]
fn a_log_follows_its_end_until_the_reader_scrolls_up_and_again_once_back_at_the_end() {
    // Given: the jobs pane, with the detail pane focused and a log of 50 lines.
    let mut app = with_jobs(vec![job(1, "running")]);
    press(&mut app, KeyCode::Char('3'));
    app.update(Msg::LogLoaded {
        job: 1,
        log: lines(50),
    });
    press(&mut app, KeyCode::Char('l'));

    // Then: it follows its end.
    assert!(app.log_following());

    // When: the reader scrolls up three lines.
    for _ in 0..3 {
        press(&mut app, KeyCode::Char('k'));
    }

    // Then: it stops following, three lines from the end.
    assert!(!app.log_following());
    assert_eq!(app.log_scroll(), 3);

    // When: ten new lines arrive.
    app.update(Msg::LogLoaded {
        job: 1,
        log: lines(60),
    });

    // Then: the lines in view stay in view, now thirteen from the end.
    assert!(!app.log_following());
    assert_eq!(app.log_scroll(), 13);

    // When: G is pressed.
    press(&mut app, KeyCode::Char('G'));

    // Then: it follows its end again.
    assert!(app.log_following());

    // When: the reader scrolls up one line, and back down one.
    press(&mut app, KeyCode::Char('k'));
    press(&mut app, KeyCode::Char('j'));

    // Then: back at the end, it follows again.
    assert!(app.log_following());
}

#[test]
fn every_five_seconds_the_log_of_a_running_job_loads_again() {
    for (status, refreshed) in [("running", true), ("success", false)] {
        // Given: the jobs pane, with the job's log shown.
        let mut app = with_jobs(vec![job(1, status)]);
        press(&mut app, KeyCode::Char('3'));
        app.update(Msg::LogLoaded {
            job: 1,
            log: lines(5),
        });

        // When: five seconds pass.
        let commands = ticks(&mut app, REFRESH_TICKS);

        // Then: the log is asked for again only while the job runs.
        let expected: &[(u64, u64)] = if refreshed { &[(PROJECT, 1)] } else { &[] };
        assert_eq!(log_requests(&commands), expected, "{status}");
    }
}

#[test]
fn a_log_for_a_job_no_longer_selected_is_dropped() {
    // Given: the cursor moved from the first job to the second.
    let mut app = with_jobs(vec![job(1, "success"), job(2, "success")]);
    press(&mut app, KeyCode::Char('3'));
    press(&mut app, KeyCode::Char('j'));

    // When: the first job's log arrives late.
    app.update(Msg::LogLoaded {
        job: 1,
        log: lines(5),
    });

    // Then: it is not shown.
    assert!(app.log.is_none());
}

fn failed(job: u64) -> Msg {
    Msg::Failed {
        cmd: Cmd::LoadLog {
            project: PROJECT,
            job,
        },
        error: "403 Forbidden".into(),
    }
}

fn drawn(app: &App) -> Buffer {
    let mut terminal = Terminal::new(TestBackend::new(120, 32)).unwrap();
    let context = Context {
        host: "gitlab.example.com".into(),
        now: OffsetDateTime::now_utc(),
    };
    terminal.draw(|frame| draw(frame, app, &context)).unwrap();
    terminal.backend().buffer().clone()
}

fn row_text(buffer: &Buffer, y: u16) -> String {
    (0..buffer.area.width)
        .map(|x| buffer[(x, y)].symbol())
        .collect()
}

fn shown(app: &App) -> String {
    let buffer = drawn(app);
    (0..buffer.area.height)
        .map(|y| row_text(&buffer, y))
        .collect::<Vec<_>>()
        .join("\n")
}

#[test]
fn a_failed_log_is_not_asked_for_again_until_the_cursor_moves() {
    // Given: two finished jobs, with the first job's log asked for.
    let mut app = with_jobs(vec![job(1, "success"), job(2, "success")]);
    press(&mut app, KeyCode::Char('3'));

    // When: the load fails, and time passes.
    let failing = app.update(failed(1));
    let waited = ticks(&mut app, SETTLE_TICKS * 3);

    // Then: the detail says the log did not load, and it is not asked for again.
    assert_eq!(log_requests(&failing), []);
    assert_eq!(log_requests(&waited), []);
    assert!(shown(&app).contains("the log did not load"));

    // When: the cursor moves to the second job and back, resting on each.
    press(&mut app, KeyCode::Char('j'));
    let second = ticks(&mut app, SETTLE_TICKS);
    press(&mut app, KeyCode::Char('k'));
    let back = ticks(&mut app, SETTLE_TICKS);

    // Then: each log is asked for, the failed one again too, and the detail waits for it.
    assert_eq!(log_requests(&second), [(PROJECT, 2)]);
    assert_eq!(log_requests(&back), [(PROJECT, 1)]);
    assert!(shown(&app).contains("loading the log"));
}

#[test]
fn a_failed_log_of_a_running_job_loads_again_when_the_refresh_comes_due() {
    // Given: a running job, with its log asked for.
    let mut app = with_jobs(vec![job(1, "running")]);
    press(&mut app, KeyCode::Char('3'));

    // When: the load fails, and five seconds pass.
    let failing = app.update(failed(1));
    let refreshed = ticks(&mut app, REFRESH_TICKS);

    // Then: the log is asked for again once, at the refresh, and the detail waits for it.
    assert_eq!(log_requests(&failing), []);
    assert_eq!(log_requests(&refreshed), [(PROJECT, 1)]);
    assert!(shown(&app).contains("loading the log"));
}

#[test]
fn a_failed_log_is_asked_for_again_when_its_pipeline_is_chosen_again() {
    // Given: the first job's log failed to load, on the newest pipeline.
    let mut app = with_jobs(vec![job(1, "success"), job(2, "success")]);
    press(&mut app, KeyCode::Char('3'));
    app.update(failed(1));
    let pipeline = app.pipeline().unwrap().id;

    // When: the next pipeline is chosen, then this one again, and its jobs arrive.
    press(&mut app, KeyCode::Char('2'));
    press(&mut app, KeyCode::Char('j'));
    ticks(&mut app, SETTLE_TICKS);
    press(&mut app, KeyCode::Char('k'));
    ticks(&mut app, SETTLE_TICKS);
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs: vec![job(1, "success"), job(2, "success")],
        bridges: Vec::new(),
    });

    // Then: the jobs pane asks for the first job's log at once.
    assert_eq!(
        log_requests(&press(&mut app, KeyCode::Char('3'))),
        [(PROJECT, 1)]
    );
}

/// The jobs pane, with `log` loaded for its only job.
fn with_log(log: &str) -> App {
    let mut app = with_jobs(vec![job(1, "success")]);
    press(&mut app, KeyCode::Char('3'));
    app.update(Msg::LogLoaded {
        job: 1,
        log: log.into(),
    });
    app
}

/// The first row that holds `text`, and the column where `text` starts. Each cell holds one
/// character, so a character count is a column.
fn find(buffer: &Buffer, text: &str) -> (u16, u16) {
    (0..buffer.area.height)
        .find_map(|y| {
            let row = row_text(buffer, y);
            let byte = row.find(text)?;
            Some((y, row[..byte].chars().count() as u16))
        })
        .unwrap_or_else(|| panic!("no row holds {text:?}"))
}

#[test]
fn a_tab_in_a_job_log_reaches_the_next_stop_of_eight_columns() {
    // Given: the jobs pane, with a line of Go test output.
    let app = with_log("ok  \tpkg\t0.1s\n");

    // When: the screen is drawn.
    let buffer = drawn(&app);

    // Then: each tab reaches the next multiple of eight columns from the start of the line.
    let (y, start) = find(&buffer, "ok      pkg     0.1s");
    assert_eq!(find(&buffer, "pkg"), (y, start + 8));
    assert_eq!(find(&buffer, "0.1s"), (y, start + 16));
}

#[test]
fn a_tab_in_a_coloured_job_log_counts_the_columns_of_earlier_colours_and_keeps_its_own() {
    // Given: the jobs pane, with a tab after a green span, and a tab inside a red span.
    let app = with_log("\u{1b}[32mok\u{1b}[0m\tpkg\n\u{1b}[31mFAIL\tpkg\u{1b}[0m\t0.1s\n");

    // When: the screen is drawn.
    let buffer = drawn(&app);

    // Then: the tabs reach the stops at eight and sixteen columns, and each part keeps its colour.
    let (y, start) = find(&buffer, "ok      pkg");
    assert_eq!(buffer[(start, y)].fg, Color::Green);
    assert_ne!(buffer[(start + 8, y)].fg, Color::Green);
    let (y, start) = find(&buffer, "FAIL    pkg     0.1s");
    for column in [start, start + 4, start + 8] {
        assert_eq!(
            buffer[(column, y)].fg,
            Color::Red,
            "column {}",
            column - start
        );
    }
    assert_ne!(buffer[(start + 16, y)].fg, Color::Red);
}

#[test]
fn a_tab_after_a_wide_character_counts_its_two_columns() {
    // Given: the jobs pane, with a tab after a character two columns wide.
    let app = with_log("界\tpkg\n");

    // When: the screen is drawn.
    let buffer = drawn(&app);

    // Then: the tab reaches the stop at eight columns, six columns after the character.
    let (y, start) = find(&buffer, "界");
    assert_eq!(find(&buffer, "pkg"), (y, start + 8));
}
