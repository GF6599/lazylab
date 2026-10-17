use lazylab::gitlab::TestReport;
use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::demo;
use lazylab::tui::theme::ROSE_PINE as T;
use lazylab::tui::view::{Context, draw};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::buffer::Buffer;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const PROJECT: u64 = 101;
/// Three ticks of 100 ms, the wait before a moved cursor loads.
const SETTLE_TICKS: usize = 3;
/// Fifty ticks of 100 ms, the five-second refresh.
const REFRESH_TICKS: usize = 50;
/// The first column of the detail pane on a terminal 120 columns wide.
const DETAIL_X: u16 = 48;
/// A terminal height that leaves the detail pane nine lines, fewer than the demo report.
const SHORT: u16 = 14;

fn now() -> OffsetDateTime {
    OffsetDateTime::parse("2026-01-15T12:00:00Z", &Rfc3339).unwrap()
}

fn answer(app: &mut App, mut commands: Vec<Cmd>) {
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, now()) {
            commands.extend(app.update(msg));
        }
    }
}

/// The demo data on the pipelines pane.
fn on_pipelines() -> App {
    let mut app = App::new(30);
    let commands = app.start();
    answer(&mut app, commands);
    press(&mut app, '2');
    app
}

fn press(app: &mut App, c: char) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::NONE,
    )))
}

fn ticks(app: &mut App, n: usize) -> Vec<Cmd> {
    (0..n).flat_map(|_| app.update(Msg::Tick)).collect()
}

fn reports_asked(commands: &[Cmd]) -> Vec<(u64, u64)> {
    (commands.iter())
        .filter_map(|c| match c {
            Cmd::LoadTestReport { project, pipeline } => Some((*project, *pipeline)),
            _ => None,
        })
        .collect()
}

fn render_at(app: &App, height: u16) -> Buffer {
    let mut terminal = Terminal::new(TestBackend::new(120, height)).unwrap();
    let context = Context {
        host: "gitlab.example.com".into(),
        now: now(),
    };
    terminal.draw(|frame| draw(frame, app, &context)).unwrap();
    terminal.backend().buffer().clone()
}

/// The detail pane, without the chrome above and below it.
fn detail(buffer: &Buffer) -> String {
    (2..buffer.area.height - 1)
        .map(|y| {
            let row: String = (DETAIL_X..buffer.area.width)
                .map(|x| buffer[(x, y)].symbol())
                .collect();
            row.trim_end().to_string()
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn colour_of(buffer: &Buffer, text: &str) -> ratatui::style::Color {
    let (x, y) = (0..buffer.area.height)
        .find_map(|y| {
            let row: String = (0..buffer.area.width)
                .map(|x| buffer[(x, y)].symbol())
                .collect();
            let at = row.find(text)?;
            Some((row[..at].chars().count() as u16, y))
        })
        .unwrap_or_else(|| panic!("no {text:?} on the screen"));
    buffer[(x, y)].fg
}

fn empty_report() -> TestReport {
    serde_json::from_value(json!({"total_count": 0, "test_suites": []})).unwrap()
}

#[test]
fn shift_t_on_the_pipelines_pane_loads_the_test_report_of_the_pipeline_in_view() {
    // Given: the demo data on the pipelines pane.
    let mut app = on_pipelines();
    let pipeline = app.pipeline().unwrap().id;

    // When: the refresh comes round. Then: no test report is asked for.
    assert_eq!(reports_asked(&ticks(&mut app, REFRESH_TICKS)), []);

    // When: `T` is pressed. Then: the test report of the pipeline in view is asked for.
    let asked = press(&mut app, 'T');
    assert_eq!(reports_asked(&asked), [(PROJECT, pipeline)]);

    // And: while it is out, the detail says so.
    insta::assert_snapshot!(detail(&render_at(&app, 10)));

    // When: `T` is pressed again. Then: the detail shows the pipeline and its stages again.
    press(&mut app, 'T');
    assert!(detail(&render_at(&app, 30)).contains("stages"));
}

/// The pipelines pane on the demo's failed pipeline, two rows down, with its test report loaded.
fn on_failed_report() -> App {
    let mut app = on_pipelines();
    press(&mut app, 'j');
    press(&mut app, 'j');
    let settled = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, settled);
    let asked = press(&mut app, 'T');
    answer(&mut app, asked);
    app
}

/// The first line inside the detail pane.
fn top_line(app: &App) -> String {
    let shown = detail(&render_at(app, SHORT));
    shown.lines().nth(1).unwrap_or_default().to_string()
}

#[test]
fn the_test_report_shows_each_suite_and_each_failed_case_with_its_output() {
    // When: the test report of the demo's failed pipeline loads.
    let buffer = render_at(&on_failed_report(), 24);

    // Then: the passed counts are pine and the failed counts love.
    assert_eq!(colour_of(&buffer, "17 passed"), T.pine);
    assert_eq!(colour_of(&buffer, "1 failed"), T.love);

    // And: the failed case shows its class and the first five lines of its output, with each tab
    // at the next stop of four columns.
    insta::assert_snapshot!(detail(&buffer));
}

#[test]
fn a_pipeline_without_tests_says_it_has_no_test_report() {
    // Given: the test report of the pipeline in view asked for.
    let mut app = on_pipelines();
    let pipeline = app.pipeline().unwrap().id;
    press(&mut app, 'T');

    // When: GitLab answers an empty report, with no suites.
    app.update(Msg::TestReportLoaded {
        project: PROJECT,
        pipeline,
        report: empty_report(),
    });

    // Then: the detail says there is no test report.
    insta::assert_snapshot!(detail(&render_at(&app, 10)));
}

#[test]
fn a_late_test_report_for_a_pipeline_the_cursor_left_is_dropped() {
    // Given: the test report of the first pipeline asked for.
    let mut app = on_pipelines();
    let late = press(&mut app, 'T');

    // When: the cursor moves to the next pipeline, whose report loads, and then the first
    // report arrives.
    press(&mut app, 'j');
    let settled = ticks(&mut app, SETTLE_TICKS);
    let next = app.pipeline().unwrap().id;
    assert_eq!(reports_asked(&settled), [(PROJECT, next)]);
    answer(&mut app, settled);
    let late = demo::answer(late.into_iter().next().unwrap(), now()).unwrap();
    let commands = app.update(late);

    // Then: the next pipeline's report stays in view, and is not asked for again.
    assert!(detail(&render_at(&app, 24)).contains("suites"));
    assert_eq!(reports_asked(&commands), []);
}

#[test]
fn a_failed_test_report_is_not_asked_again_until_the_pipeline_changes_or_t_asks() {
    // Given: the test report of the first pipeline asked for.
    let mut app = on_pipelines();
    let first = app.pipeline().unwrap().id;
    let cmd = press(&mut app, 'T').pop().unwrap();

    // When: the load fails, and the refresh comes round twice.
    let failed = app.update(Msg::Failed {
        cmd,
        error: "500 Internal Server Error".into(),
    });
    let waited = ticks(&mut app, REFRESH_TICKS * 2);

    // Then: the detail says the report did not load, and it is not asked for again.
    insta::assert_snapshot!(detail(&render_at(&app, 10)));
    assert_eq!(reports_asked(&[failed, waited].concat()), []);

    // When: `T` closes the report and opens it again. Then: it is asked for again.
    press(&mut app, 'T');
    assert_eq!(reports_asked(&press(&mut app, 'T')), [(PROJECT, first)]);

    // When: that load fails too, and the cursor moves to the next pipeline and back, and rests.
    app.update(Msg::Failed {
        cmd: Cmd::LoadTestReport {
            project: PROJECT,
            pipeline: first,
        },
        error: "500 Internal Server Error".into(),
    });
    press(&mut app, 'j');
    press(&mut app, 'k');
    let asked = ticks(&mut app, SETTLE_TICKS);

    // Then: the report is asked for again.
    assert_eq!(reports_asked(&asked), [(PROJECT, first)]);
}

#[test]
fn the_key_bar_offers_shift_t_on_the_pipelines_pane() {
    // Given: the demo data on the pipelines pane.
    let mut app = on_pipelines();

    for focus in ['h', 'l'] {
        // When: the sidebar or the detail is focused, and the screen is drawn.
        press(&mut app, focus);
        let buffer = render_at(&app, 30);

        // Then: the key bar offers `T` for the test report.
        let bar: String = (0..buffer.area.width)
            .map(|x| buffer[(x, buffer.area.height - 1)].symbol())
            .collect();
        assert!(bar.contains("T tests"), "{bar}");
    }
}

#[test]
fn j_and_k_in_the_detail_scroll_the_test_report_between_its_top_and_its_end() {
    // Given: the demo's failed report, longer than the detail pane, with the detail focused.
    let mut app = on_failed_report();
    press(&mut app, 'l');
    assert!(top_line(&app).contains("total"), "{}", top_line(&app));

    // When: `G` scrolls to the end, and `j` tries to go further.
    press(&mut app, 'G');
    let end = detail(&render_at(&app, SHORT));
    press(&mut app, 'j');

    // Then: the failed case's output ends at the bottom of the pane, and stays there.
    insta::assert_snapshot!(end);
    assert_eq!(detail(&render_at(&app, SHORT)), end);

    // When: `k` scrolls up one line. Then: the line above comes into view.
    press(&mut app, 'k');
    assert!(top_line(&app).contains("passed"), "{}", top_line(&app));

    // When: `g` scrolls to the top, and `k` tries to go further. Then: the totals are on top.
    press(&mut app, 'g');
    press(&mut app, 'k');
    assert!(top_line(&app).contains("total"), "{}", top_line(&app));
}

#[test]
fn another_pipeline_opens_its_test_report_at_the_top() {
    // Given: the demo's failed report, scrolled to its end.
    let mut app = on_failed_report();
    let failed = app.pipeline().unwrap().id;
    press(&mut app, 'l');
    render_at(&app, SHORT);
    press(&mut app, 'G');

    // When: the sidebar moves the cursor up one pipeline, and its report loads.
    press(&mut app, 'h');
    press(&mut app, 'k');
    let settled = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, settled);

    // Then: j and k in the sidebar still move the cursor, and the report shows from its top.
    assert_ne!(app.pipeline().unwrap().id, failed);
    assert!(top_line(&app).contains("total"), "{}", top_line(&app));
}

#[test]
fn shift_t_opens_the_test_report_again_at_the_top() {
    // Given: the demo's failed report, scrolled to its end.
    let mut app = on_failed_report();
    press(&mut app, 'l');
    render_at(&app, SHORT);
    press(&mut app, 'G');

    // When: `T` closes the report and opens it again, and it loads.
    press(&mut app, 'T');
    let asked = press(&mut app, 'T');
    answer(&mut app, asked);

    // Then: the report shows from its top.
    assert!(top_line(&app).contains("total"), "{}", top_line(&app));
}

#[test]
fn the_key_bar_offers_j_and_k_to_scroll_the_test_report() {
    // Given: the demo's failed report, with the detail focused.
    let mut app = on_failed_report();
    press(&mut app, 'l');

    // When: the screen is drawn.
    let buffer = render_at(&app, 30);

    // Then: the key bar offers `j/k` to scroll, before `T` for the test report.
    let bar: String = (0..buffer.area.width)
        .map(|x| buffer[(x, buffer.area.height - 1)].symbol())
        .collect();
    assert!(bar.contains("j/k scroll  T tests"), "{bar}");
}
