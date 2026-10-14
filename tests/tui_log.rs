use lazylab::gitlab::Job;
use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
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
