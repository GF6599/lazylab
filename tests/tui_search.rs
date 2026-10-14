use lazylab::gitlab::Project;
use lazylab::store::CachedProjects;
use lazylab::tui::app::{App, Cmd, Msg, Pane};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;

const API: u64 = 101;
const WEB: u64 = 102;
const WORKERS: u64 = 103;
const LAZYLAB: u64 = 109;

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

fn press(app: &mut App, code: KeyCode) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(code, KeyModifiers::NONE)))
}

fn type_text(app: &mut App, text: &str) -> Vec<Cmd> {
    text.chars()
        .flat_map(|c| press(app, KeyCode::Char(c)))
        .collect()
}

fn shown(app: &App) -> Vec<String> {
    app.shown_projects()
        .map(|p| p.path_with_namespace.clone())
        .collect()
}

fn shown_ids(app: &App) -> Vec<u64> {
    app.shown_projects().map(|p| p.id).collect()
}

fn saved_favorites(commands: &[Cmd]) -> Option<Vec<u64>> {
    commands.iter().find_map(|command| match command {
        Cmd::SaveFavorites(ids) => Some(ids.clone()),
        _ => None,
    })
}

#[test]
fn a_start_reads_the_cache_and_the_favorites() {
    // Given: a new app.
    let mut app = App::new(30);

    // When: it starts.
    let commands = app.start();

    // Then: it asks for the cached projects and the favorites.
    assert_eq!(commands, [Cmd::LoadCache, Cmd::LoadFavorites]);
}

#[test]
fn a_search_keeps_the_projects_whose_path_or_name_holds_its_letters_in_order() {
    // Given: the demo projects.
    let mut app = loaded();

    // When: a search for "hlm" is typed.
    press(&mut app, KeyCode::Char('/'));
    let commands = type_text(&mut app, "hlm");

    // Then: only the helm charts are left, and their lists are asked for.
    assert_eq!(shown(&app), ["acme/infra/helm-charts"]);
    let project = app.project().unwrap().id;
    assert!(
        commands.contains(&Cmd::LoadPipelines { project }),
        "{commands:?}"
    );
}

#[test]
fn a_search_finds_a_project_by_a_name_its_path_does_not_hold() {
    // Given: a project named apart from its path, as GitLab allows.
    let mut app = App::new(30);
    app.start();
    let renamed: Project = serde_json::from_value(serde_json::json!({
        "id": 7,
        "name": "Billing Service",
        "path_with_namespace": "acme/svc-7",
    }))
    .unwrap();
    app.update(Msg::CacheLoaded(Some(CachedProjects {
        projects: vec![renamed],
        total: 1,
    })));

    // When: its name is searched for.
    press(&mut app, KeyCode::Char('/'));
    type_text(&mut app, "billing");

    // Then: it is found.
    assert_eq!(shown(&app), ["acme/svc-7"]);
}

#[test]
fn a_search_takes_every_letter_as_text_and_backspace_takes_it_back() {
    // Given: a search being typed.
    let mut app = loaded();
    press(&mut app, KeyCode::Char('/'));

    // When: q, a letter that quits outside a search, is typed.
    type_text(&mut app, "q");

    // Then: it is part of the search, and nothing matches.
    assert!(!app.quit);
    assert_eq!(app.len(Pane::Projects), 0);

    // When: it is taken back.
    press(&mut app, KeyCode::Backspace);

    // Then: every project is back.
    assert_eq!(app.len(Pane::Projects), app.projects.len());
}

#[test]
fn enter_keeps_the_search_and_escape_clears_it() {
    // Given: a search for "plat".
    let mut app = loaded();
    press(&mut app, KeyCode::Char('/'));
    type_text(&mut app, "plat");

    // When: enter is pressed, and j after it.
    press(&mut app, KeyCode::Enter);
    press(&mut app, KeyCode::Char('j'));

    // Then: the search stays, and j moves through what it kept.
    assert_eq!(shown_ids(&app), [API, WEB, WORKERS]);
    assert_eq!(app.project().unwrap().id, WEB);

    // When: a new search starts.
    press(&mut app, KeyCode::Char('/'));

    // Then: it starts empty, so every project shows.
    assert_eq!(app.len(Pane::Projects), app.projects.len());

    // When: escape is pressed.
    press(&mut app, KeyCode::Esc);

    // Then: every project is back, and the cursor is still on the same project.
    assert_eq!(app.len(Pane::Projects), app.projects.len());
    assert_eq!(app.project().unwrap().id, WEB);
}

#[test]
fn escape_while_typing_clears_the_search() {
    // Given: a search for "plat" being typed.
    let mut app = loaded();
    press(&mut app, KeyCode::Char('/'));
    type_text(&mut app, "plat");

    // When: escape is pressed.
    press(&mut app, KeyCode::Esc);

    // Then: the search is gone, and every project shows.
    assert!(app.search.query.is_empty() && !app.search.typing);
    assert_eq!(app.len(Pane::Projects), app.projects.len());
}

#[test]
fn f_favorites_the_project_and_saves_the_favorites_in_order() {
    // Given: the demo projects, none of them a favorite.
    let mut app = loaded();

    // When: the first two projects are favorited, and the second one again.
    let first = press(&mut app, KeyCode::Char('f'));
    press(&mut app, KeyCode::Char('j'));
    let second = press(&mut app, KeyCode::Char('f'));
    let again = press(&mut app, KeyCode::Char('f'));

    // Then: each change is saved, in the order the favorites were made.
    assert_eq!(saved_favorites(&first), Some(vec![API]));
    assert_eq!(saved_favorites(&second), Some(vec![API, WEB]));
    assert_eq!(saved_favorites(&again), Some(vec![API]));
    assert_eq!(app.favorites, [API]);
}

#[test]
fn t_shows_the_favorites_alone_in_their_order_and_keeps_the_cursor_on_its_project() {
    // Given: two favorites, the later one higher in the list of all projects.
    let mut app = loaded();
    app.update(Msg::FavoritesLoaded(vec![LAZYLAB, API]));
    assert_eq!(app.project().unwrap().id, API);

    // When: t is pressed.
    press(&mut app, KeyCode::Char('t'));

    // Then: only the favorites show, in their own order, with the cursor still on its project.
    assert!(app.favorites_only);
    assert_eq!(shown_ids(&app), [LAZYLAB, API]);
    assert_eq!(app.project().unwrap().id, API);

    // When: t is pressed again.
    press(&mut app, KeyCode::Char('t'));

    // Then: every project shows again.
    assert_eq!(app.len(Pane::Projects), app.projects.len());
}

#[test]
fn braces_move_a_favorite_and_the_cursor_moves_with_it() {
    // Given: three favorites shown alone, with the cursor on the first.
    let mut app = loaded();
    app.update(Msg::FavoritesLoaded(vec![API, WEB, WORKERS]));
    press(&mut app, KeyCode::Char('t'));
    assert_eq!(app.project().unwrap().id, API);

    // When: the closing brace is pressed.
    let down = press(&mut app, KeyCode::Char('}'));

    // Then: the favorite moves down one place, the cursor stays on it, and the order is saved.
    assert_eq!(shown_ids(&app), [WEB, API, WORKERS]);
    assert_eq!(app.project().unwrap().id, API);
    assert_eq!(saved_favorites(&down), Some(vec![WEB, API, WORKERS]));

    // When: the opening brace is pressed.
    press(&mut app, KeyCode::Char('{'));

    // Then: it is back where it started.
    assert_eq!(shown_ids(&app), [API, WEB, WORKERS]);
}

#[test]
fn braces_do_nothing_while_every_project_shows() {
    // Given: two favorites, with every project shown.
    let mut app = loaded();
    app.update(Msg::FavoritesLoaded(vec![API, WEB]));

    // When: the closing brace is pressed.
    let commands = press(&mut app, KeyCode::Char('}'));

    // Then: nothing changes.
    assert!(commands.is_empty());
    assert_eq!(app.favorites, [API, WEB]);
}

#[test]
fn a_favorite_not_loaded_yet_is_kept() {
    // Given: favorites that name a project the list does not hold yet.
    let mut app = loaded();
    app.update(Msg::FavoritesLoaded(vec![999, API]));

    // When: the favorites show alone, and the one there is unfavorited.
    press(&mut app, KeyCode::Char('t'));
    assert_eq!(shown_ids(&app), [API]);
    let commands = press(&mut app, KeyCode::Char('f'));

    // Then: the favorite not loaded yet is still saved, and nothing shows.
    assert_eq!(saved_favorites(&commands), Some(vec![999]));
    assert_eq!(app.len(Pane::Projects), 0);
}

#[test]
fn the_project_keys_do_nothing_in_another_pane() {
    // Given: the pipelines pane.
    let mut app = loaded();
    press(&mut app, KeyCode::Char('2'));

    // When: the search, favorite and favorites-only keys are pressed.
    let commands = type_text(&mut app, "/ft");

    // Then: none of them acted.
    assert!(app.search.query.is_empty() && !app.search.typing);
    assert!(app.favorites.is_empty() && !app.favorites_only);
    assert!(saved_favorites(&commands).is_none());
}
