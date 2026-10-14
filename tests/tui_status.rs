use lazylab::gitlab::{Project, Status};
use lazylab::store::CachedProjects;
use lazylab::tui::app::{App, Cmd, Msg, ProjectStatus};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;

fn projects() -> Vec<Project> {
    match demo::answer(Cmd::LoadCache, OffsetDateTime::now_utc()) {
        Some(Msg::CacheLoaded(Some(cached))) => cached.projects,
        other => panic!("the demo answers with a cache, got {other:?}"),
    }
}

/// An app with pages of two, showing every demo project from the cache, and the commands the
/// cache answer started.
fn showing_all() -> (App, Vec<Project>, Vec<Cmd>) {
    let all = projects();
    let mut app = App::new(2);
    app.start();
    let commands = app.update(Msg::CacheLoaded(Some(CachedProjects {
        total: all.len() as u64,
        projects: all.clone(),
    })));
    (app, all, commands)
}

fn asked(commands: &[Cmd]) -> Vec<u64> {
    commands
        .iter()
        .filter_map(|command| match command {
            Cmd::LoadStatus { project } => Some(*project),
            _ => None,
        })
        .collect()
}

fn press(app: &mut App, code: KeyCode, modifiers: KeyModifiers) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(code, modifiers)))
}

#[test]
fn the_status_of_each_project_within_a_page_of_the_cursor_is_asked_for_once() {
    // Given: pages of two, and the cursor on the first of the demo projects.
    let (mut app, all, commands) = showing_all();

    // Then: the statuses of the rows within two of the cursor are asked for, and they load.
    assert_eq!(asked(&commands), [all[0].id, all[1].id, all[2].id]);
    assert_eq!(app.statuses.get(&all[0].id), Some(&ProjectStatus::Loading));

    // When: the cursor moves down two rows.
    press(&mut app, KeyCode::Char('j'), KeyModifiers::NONE);
    let commands = press(&mut app, KeyCode::Char('j'), KeyModifiers::NONE);

    // Then: only the rows it newly comes near are asked for.
    assert_eq!(asked(&commands), [all[4].id]);
}

#[test]
fn an_answer_records_the_latest_status_or_that_there_is_no_pipeline() {
    // Given: the statuses of the first rows were asked for.
    let (mut app, all, _) = showing_all();

    // When: one project answers with a failed pipeline, and another with none.
    app.update(Msg::StatusLoaded {
        project: all[0].id,
        status: Some(Status::Failed),
    });
    app.update(Msg::StatusLoaded {
        project: all[1].id,
        status: None,
    });

    // Then: each is recorded.
    assert_eq!(
        app.statuses.get(&all[0].id),
        Some(&ProjectStatus::Pipeline(Status::Failed))
    );
    assert_eq!(
        app.statuses.get(&all[1].id),
        Some(&ProjectStatus::NoPipeline)
    );
}

#[test]
fn a_failed_status_is_unknown_and_takes_no_status_bar() {
    // Given: the status of the first project was asked for.
    let (mut app, all, _) = showing_all();

    // When: the call fails.
    app.update(Msg::Failed {
        cmd: Cmd::LoadStatus { project: all[0].id },
        error: "list pipelines: connection refused".into(),
    });

    // Then: the status is unknown, and the status bar keeps its hints.
    assert_eq!(app.statuses.get(&all[0].id), Some(&ProjectStatus::Unknown));
    assert_eq!(app.error, None);
}

#[test]
fn control_r_asks_for_the_statuses_again() {
    // Given: the first project's status is known.
    let (mut app, all, _) = showing_all();
    app.update(Msg::StatusLoaded {
        project: all[0].id,
        status: Some(Status::Success),
    });
    let (load, ..) = match &press(&mut app, KeyCode::Char('r'), KeyModifiers::CONTROL)[..] {
        [Cmd::LoadProjectPage { load, page, .. }] => (*load, *page),
        other => panic!("one page request, got {other:?}"),
    };

    // When: the first page of the reload arrives.
    let commands = app.update(Msg::ProjectPageLoaded {
        load,
        page: lazylab::gitlab::Page {
            items: all[..3].to_vec(),
            page: 1,
            next_page: None,
            total_pages: Some(1),
            total_items: Some(3),
        },
    });

    // Then: the statuses near the cursor are asked for again.
    assert_eq!(asked(&commands), [all[0].id, all[1].id, all[2].id]);
}
