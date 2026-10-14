use lazylab::gitlab::{Page, Project};
use lazylab::store::CachedProjects;
use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;

fn projects() -> Vec<Project> {
    match demo::answer(Cmd::LoadCache, OffsetDateTime::now_utc()) {
        Some(Msg::CacheLoaded(Some(cached))) => cached.projects,
        other => panic!("the demo answers with a cache, got {other:?}"),
    }
}

fn cache(projects: &[Project], total: u64) -> Msg {
    Msg::CacheLoaded(Some(CachedProjects {
        projects: projects.to_vec(),
        total,
    }))
}

fn page(items: &[Project], page: u32, next_page: Option<u32>, total: Option<u64>) -> Page<Project> {
    Page {
        items: items.to_vec(),
        page,
        next_page,
        total_pages: None,
        total_items: total,
    }
}

fn ctrl_r() -> Msg {
    Msg::Key(KeyEvent::new(KeyCode::Char('r'), KeyModifiers::CONTROL))
}

fn page_request(commands: &[Cmd]) -> Option<(u64, u32, u8)> {
    commands.iter().find_map(|command| match command {
        Cmd::LoadProjectPage {
            load,
            page,
            per_page,
        } => Some((*load, *page, *per_page)),
        _ => None,
    })
}

fn saved(commands: &[Cmd]) -> Option<(Vec<u64>, u64)> {
    commands.iter().find_map(|command| match command {
        Cmd::SaveProjects { projects, total } => {
            Some((projects.iter().map(|p| p.id).collect(), *total))
        }
        _ => None,
    })
}

fn ids(projects: &[Project]) -> Vec<u64> {
    projects.iter().map(|p| p.id).collect()
}

#[test]
fn without_a_cache_the_first_page_loads_at_the_configured_size() {
    // Given: an app set to 25 projects a page.
    let mut app = App::new(25);
    app.start();

    // When: there is no cache.
    let commands = app.update(Msg::CacheLoaded(None));

    // Then: the first page of 25 is asked for, and the projects show as loading.
    let (_, page, per_page) = page_request(&commands).expect("a page request");
    assert_eq!((page, per_page), (1, 25));
    assert!(app.loading);
}

#[test]
fn a_complete_cache_shows_at_once_and_asks_for_no_page() {
    // Given: a cache that holds all three projects of the collection.
    let mut app = App::new(25);
    app.start();
    let all = &projects()[..3];

    // When: it arrives.
    let commands = app.update(cache(all, 3));

    // Then: the projects show, and no page is asked for.
    assert_eq!(ids(&app.projects), ids(all));
    assert_eq!(page_request(&commands), None);
    assert!(!app.loading);
}

#[test]
fn a_partial_cache_resumes_at_the_page_after_those_it_holds() {
    // Given: pages of two, and a cache that holds five projects of nine.
    let mut app = App::new(2);
    app.start();

    // When: the cache arrives.
    let commands = app.update(cache(&projects()[..5], 9));

    // Then: the five show at once, and loading resumes at page three, which holds the fifth.
    assert_eq!(app.projects.len(), 5);
    let (_, page, per_page) = page_request(&commands).expect("a page request");
    assert_eq!((page, per_page), (3, 2));
    assert!(app.loading);
}

#[test]
fn each_page_is_saved_and_the_next_asked_for_until_the_last() {
    // Given: pages of two, no cache, and three projects on GitLab.
    let all = projects();
    let mut app = App::new(2);
    app.start();
    let (load, ..) = page_request(&app.update(Msg::CacheLoaded(None))).unwrap();

    // When: the first page arrives.
    let commands = app.update(Msg::ProjectPageLoaded {
        load,
        page: page(&all[..2], 1, Some(2), Some(3)),
    });

    // Then: it is shown and saved with the total, and the second page is asked for.
    assert_eq!(ids(&app.projects), ids(&all[..2]));
    assert_eq!(saved(&commands), Some((ids(&all[..2]), 3)));
    assert_eq!(page_request(&commands), Some((load, 2, 2)));

    // When: the last page arrives, holding a project the first page already had.
    let commands = app.update(Msg::ProjectPageLoaded {
        load,
        page: page(&[all[1].clone(), all[2].clone()], 2, None, Some(3)),
    });

    // Then: each project shows once, all three are saved, and loading ends.
    assert_eq!(ids(&app.projects), ids(&all[..3]));
    assert_eq!(saved(&commands), Some((ids(&all[..3]), 3)));
    assert_eq!(page_request(&commands), None);
    assert!(!app.loading);
}

#[test]
fn a_withheld_total_counts_the_projects_loaded_so_far() {
    // Given: no cache.
    let all = projects();
    let mut app = App::new(2);
    app.start();
    let (load, ..) = page_request(&app.update(Msg::CacheLoaded(None))).unwrap();

    // When: a page arrives without a total, as past ten thousand projects, with a next page.
    let commands = app.update(Msg::ProjectPageLoaded {
        load,
        page: page(&all[..2], 1, Some(2), None),
    });

    // Then: the saved total is the projects loaded, and the next page is still asked for.
    assert_eq!(saved(&commands), Some((ids(&all[..2]), 2)));
    assert_eq!(page_request(&commands), Some((load, 2, 2)));
}

#[test]
fn control_r_reloads_from_page_one_and_drops_the_pages_of_the_load_before() {
    // Given: a load that has shown its first page and waits for its second.
    let all = projects();
    let mut app = App::new(2);
    app.start();
    let (before, ..) = page_request(&app.update(Msg::CacheLoaded(None))).unwrap();
    app.update(Msg::ProjectPageLoaded {
        load: before,
        page: page(&all[..2], 1, Some(2), Some(4)),
    });

    // When: control r is pressed.
    let commands = app.update(ctrl_r());

    // Then: page one of a new load is asked for.
    let (load, page_number, _) = page_request(&commands).expect("a page request");
    assert_ne!(load, before);
    assert_eq!(page_number, 1);
    assert!(app.loading);

    // When: the second page of the load before arrives late.
    let late = app.update(Msg::ProjectPageLoaded {
        load: before,
        page: page(&all[2..4], 2, None, Some(4)),
    });

    // Then: it is dropped, and nothing more is asked for.
    assert!(late.is_empty());
    assert_eq!(ids(&app.projects), ids(&all[..2]));
}

#[test]
fn a_reload_keeps_the_cursor_on_the_project_it_was_on() {
    // Given: four projects from the cache, with the cursor on the third.
    let all = projects();
    let mut app = App::new(30);
    app.start();
    app.update(cache(&all[..4], 4));
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char('j'),
        KeyModifiers::NONE,
    )));
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char('j'),
        KeyModifiers::NONE,
    )));
    let current = all[2].id;
    assert_eq!(app.project().unwrap().id, current);
    let (load, ..) = page_request(&app.update(ctrl_r())).unwrap();

    // When: page one arrives with that project now first, because it saw the latest activity.
    let order = [&all[2], &all[0], &all[1], &all[3]].map(Clone::clone);
    let commands = app.update(Msg::ProjectPageLoaded {
        load,
        page: page(&order, 1, None, Some(4)),
    });

    // Then: the cursor follows the project, and its lists are not asked for again.
    assert_eq!(app.project().unwrap().id, current);
    assert!(
        !commands
            .iter()
            .any(|c| matches!(c, Cmd::LoadPipelines { .. })),
        "{commands:?}"
    );
}

#[test]
fn a_failed_page_ends_the_loading_and_shows_the_reason_until_a_key() {
    // Given: a first page in flight.
    let mut app = App::new(2);
    app.start();
    let commands = app.update(Msg::CacheLoaded(None));
    let request = commands
        .into_iter()
        .find(|c| matches!(c, Cmd::LoadProjectPage { .. }))
        .unwrap();

    // When: it fails.
    let commands = app.update(Msg::Failed {
        cmd: request,
        error: "list projects: connection refused".into(),
    });

    // Then: loading ends, and the reason is kept to show.
    assert!(commands.is_empty());
    assert!(!app.loading);
    assert_eq!(
        app.error.as_deref(),
        Some("list projects: connection refused")
    );

    // When: a key is pressed.
    app.update(Msg::Key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE)));

    // Then: the reason is gone.
    assert_eq!(app.error, None);
}
