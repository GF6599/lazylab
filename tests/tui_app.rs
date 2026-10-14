use std::time::Duration;

use lazylab::tui::app::{App, Cmd, Focus, Msg, Overlay, Pane};
use lazylab::tui::{Source, demo, run};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use tokio::sync::mpsc;

const PAGE_SIZE: u8 = 30;

fn now() -> OffsetDateTime {
    OffsetDateTime::parse("2026-01-15T12:00:00Z", &Rfc3339).unwrap()
}

fn key(code: KeyCode) -> Msg {
    Msg::Key(KeyEvent::new(code, KeyModifiers::NONE))
}

fn char(c: char) -> Msg {
    key(KeyCode::Char(c))
}

fn ctrl(c: char) -> Msg {
    Msg::Key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::CONTROL))
}

/// Answers every command from the demo source until none is left, as the event loop does.
fn settle(app: &mut App, mut commands: Vec<Cmd>) {
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, now()) {
            commands.extend(app.update(msg));
        }
    }
}

fn answer(command: Cmd) -> Msg {
    demo::answer(command, now()).expect("the demo answers every load")
}

fn started() -> App {
    let mut app = App::new(PAGE_SIZE);
    let commands = app.start();
    settle(&mut app, commands);
    app
}

fn press(app: &mut App, msg: Msg) {
    let commands = app.update(msg);
    settle(app, commands);
}

#[test]
fn a_start_loads_the_first_project_its_pipelines_jobs_and_merge_requests() {
    // Given: a new app.
    let mut app = App::new(PAGE_SIZE);

    // When: it starts.
    let first = app.start();

    // Then: it reads the project cache and the favorites first.
    assert_eq!(first, [Cmd::LoadCache, Cmd::LoadFavorites]);

    // When: the cached projects arrive.
    let projects = answer(Cmd::LoadCache);
    let next = app.update(projects);

    // Then: it asks for the pipelines and merge requests of the first project.
    let project = app.project().unwrap().id;
    let lists: Vec<_> = (next.into_iter())
        .filter(|c| !matches!(c, Cmd::LoadStatus { .. }))
        .collect();
    assert_eq!(
        lists,
        [
            Cmd::LoadPipelines { project },
            Cmd::LoadMergeRequests { project },
        ]
    );

    // When: the pipelines arrive.
    let pipelines = app.update(answer(Cmd::LoadPipelines { project }));

    // Then: it asks for the jobs of the newest pipeline.
    let pipeline = app.pipeline().unwrap().id;
    assert_eq!(pipelines, [Cmd::LoadJobs { project, pipeline }]);
}

#[test]
fn moving_to_another_project_clears_its_lists_and_asks_for_the_new_ones() {
    // Given: an app showing the first project with everything loaded.
    let mut app = started();
    assert!(!app.pipelines.is_empty() && !app.jobs.is_empty());

    // When: the cursor moves down to the second project.
    let commands = app.update(char('j'));

    // Then: nothing of the first project is left, and the second project's lists are asked for.
    let project = app.project().unwrap().id;
    assert_eq!(app.project().unwrap().id, app.projects[1].id);
    assert!(app.pipelines.is_empty() && app.jobs.is_empty() && app.merge_requests.is_empty());
    assert_eq!(
        commands,
        [
            Cmd::LoadPipelines { project },
            Cmd::LoadMergeRequests { project },
        ]
    );
}

#[test]
fn moving_to_another_pipeline_asks_for_its_jobs() {
    // Given: an app on the pipelines pane.
    let mut app = started();
    press(&mut app, char('2'));
    let project = app.project().unwrap().id;

    // When: the cursor moves down to the second pipeline.
    let commands = app.update(char('j'));

    // Then: the old jobs are gone, and the second pipeline's jobs are asked for.
    let pipeline = app.pipelines[1].id;
    assert_eq!(app.pipeline().unwrap().id, pipeline);
    assert!(app.jobs.is_empty());
    assert_eq!(commands, [Cmd::LoadJobs { project, pipeline }]);
}

#[test]
fn an_answer_for_a_project_no_longer_selected_is_dropped() {
    // Given: the reader moved from the first project to the second before its lists arrived.
    let mut app = started();
    let first = app.project().unwrap().id;
    app.update(char('j'));
    let second = app.project().unwrap().id;

    // When: the first project's pipelines and merge requests arrive late.
    let late = [
        answer(Cmd::LoadPipelines { project: first }),
        answer(Cmd::LoadMergeRequests { project: first }),
    ];
    for msg in late {
        assert!(app.update(msg).is_empty());
    }

    // Then: the second project does not show them.
    assert!(app.pipelines.is_empty() && app.merge_requests.is_empty());

    // When: the second project's own pipelines arrive.
    app.update(answer(Cmd::LoadPipelines { project: second }));

    // Then: they are shown.
    assert!(!app.pipelines.is_empty());
}

#[test]
fn jobs_for_a_pipeline_no_longer_selected_are_dropped() {
    // Given: the reader moved from the newest pipeline to the next one before its jobs arrived.
    let mut app = started();
    press(&mut app, char('2'));
    let project = app.project().unwrap().id;
    let newest = app.pipeline().unwrap().id;
    app.update(char('j'));

    // When: the newest pipeline's jobs arrive late.
    let late = answer(Cmd::LoadJobs {
        project,
        pipeline: newest,
    });
    app.update(late);

    // Then: the selected pipeline does not show them.
    assert!(app.jobs.is_empty());
}

#[test]
fn the_cursor_stays_inside_its_list() {
    // Given: the projects list.
    let mut app = started();
    let last = app.projects.len() - 1;

    // When and then: up from the top stays at the top, and the end keys reach each end.
    press(&mut app, char('k'));
    assert_eq!(app.cursor(Pane::Projects), 0);
    press(&mut app, char('G'));
    assert_eq!(app.cursor(Pane::Projects), last);
    press(&mut app, key(KeyCode::Down));
    assert_eq!(app.cursor(Pane::Projects), last);
    press(&mut app, char('g'));
    assert_eq!(app.cursor(Pane::Projects), 0);
    // And: a page moves ten rows, and stops at the end.
    press(&mut app, ctrl('d'));
    assert_eq!(app.cursor(Pane::Projects), 10.min(last));
    press(&mut app, ctrl('d'));
    assert_eq!(app.cursor(Pane::Projects), last);
    press(&mut app, ctrl('u'));
    assert_eq!(app.cursor(Pane::Projects), last.saturating_sub(10));
}

#[test]
fn number_keys_and_tab_choose_the_pane() {
    // Given: an app on the projects pane.
    let mut app = started();

    // When and then: a digit names a pane, and tab and back tab walk the panes in a ring.
    press(&mut app, char('3'));
    assert_eq!(app.pane, Pane::Jobs);
    press(&mut app, key(KeyCode::Tab));
    assert_eq!(app.pane, Pane::MergeRequests);
    press(&mut app, key(KeyCode::Tab));
    assert_eq!(app.pane, Pane::Projects);
    press(&mut app, key(KeyCode::BackTab));
    assert_eq!(app.pane, Pane::MergeRequests);
    // And: a digit with no pane behind it changes nothing.
    press(&mut app, char('9'));
    assert_eq!(app.pane, Pane::MergeRequests);
}

#[test]
fn focus_moves_to_the_detail_pane_and_back() {
    // Given: an app with the sidebar focused.
    let mut app = started();
    assert_eq!(app.focus, Focus::Sidebar);

    // When and then: l and enter focus the detail pane, and h and escape return.
    for (enter, leave) in [
        (char('l'), char('h')),
        (key(KeyCode::Enter), key(KeyCode::Esc)),
    ] {
        press(&mut app, enter);
        assert_eq!(app.focus, Focus::Detail);
        // And: the list cursor does not move while the detail pane has the focus.
        press(&mut app, char('j'));
        assert_eq!(app.cursor(Pane::Projects), 0);
        press(&mut app, leave);
        assert_eq!(app.focus, Focus::Sidebar);
    }
}

#[test]
fn help_holds_the_keys_until_it_closes() {
    // Given: an app with help open.
    let mut app = started();
    press(&mut app, char('?'));
    assert_eq!(app.overlay, Overlay::Help);

    // When: the reader presses a list key and then q.
    press(&mut app, char('j'));
    press(&mut app, char('q'));

    // Then: the list did not move, and q closed help without quitting.
    assert_eq!(app.cursor(Pane::Projects), 0);
    assert_eq!(app.overlay, Overlay::None);
    assert!(!app.quit);
    // And: escape closes help too.
    press(&mut app, char('?'));
    press(&mut app, key(KeyCode::Esc));
    assert_eq!(app.overlay, Overlay::None);
}

#[test]
fn q_quits_and_control_c_quits_from_anywhere() {
    // Given: one app on the main screen, and one with help open.
    let mut plain = started();
    let mut helping = started();
    press(&mut helping, char('?'));

    // When: q reaches the first, and control c the second.
    press(&mut plain, char('q'));
    press(&mut helping, ctrl('c'));

    // Then: both quit.
    assert!(plain.quit && helping.quit);
}

#[test]
fn z_switches_the_full_screen_mode_on_and_off() {
    // Given: a new app, which always starts with the full-screen mode off.
    let mut app = started();
    assert!(!app.zoom);

    // When and then: z switches it on, and z again switches it off.
    press(&mut app, char('z'));
    assert!(app.zoom);
    press(&mut app, char('z'));
    assert!(!app.zoom);
}

#[test]
fn a_key_release_is_not_a_second_press() {
    // Given: a terminal that reports the release of a key as well as its press.
    let mut app = started();
    let mut release = KeyEvent::new(KeyCode::Char('j'), KeyModifiers::NONE);
    release.kind = KeyEventKind::Release;

    // When: j is pressed and released.
    press(&mut app, char('j'));
    press(&mut app, Msg::Key(release));

    // Then: the cursor moved one row.
    assert_eq!(app.cursor(Pane::Projects), 1);
}

#[test]
fn each_tick_advances_the_clock_that_animates_running_jobs() {
    // Given: a started app.
    let mut app = started();
    let before = app.tick;

    // When: two ticks pass.
    press(&mut app, Msg::Tick);
    press(&mut app, Msg::Tick);

    // Then: the clock moved two steps.
    assert_eq!(app.tick, before + 2);
}

#[tokio::test]
async fn the_loop_runs_each_command_and_feeds_its_answer_back_until_quit() {
    // Given: a terminal, and a screen that presses q once the jobs of the first pipeline are shown.
    let mut terminal = Terminal::new(TestBackend::new(80, 24)).unwrap();
    let (outbox, inbox) = mpsc::unbounded_channel();
    let keys = outbox.clone();
    let mut pressed = false;
    let draw = move |_: &mut ratatui::Frame, app: &App| {
        if !app.jobs.is_empty() && !pressed {
            keys.send(char('q')).unwrap();
            pressed = true;
        }
    };

    // When: the loop runs, for at most five seconds.
    let running = run(
        &mut terminal,
        App::new(PAGE_SIZE),
        Source::Demo,
        inbox,
        outbox,
        draw,
    );
    let app = tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("the loop ends once q arrives")
        .unwrap();

    // Then: it loaded the projects, the first project's pipelines and the newest pipeline's jobs.
    assert!(app.quit);
    assert!(!app.projects.is_empty() && !app.pipelines.is_empty() && !app.jobs.is_empty());
}
