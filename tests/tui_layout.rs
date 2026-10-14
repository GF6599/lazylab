use lazylab::gitlab::Status;
use lazylab::tui::app::{App, Cmd, Focus, Msg, Pane};
use lazylab::tui::demo;
use lazylab::tui::theme::ROSE_PINE as T;
use lazylab::tui::view::{Context, draw, sidebar};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::buffer::Buffer;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const PAGE_SIZE: u8 = 30;

fn now() -> OffsetDateTime {
    OffsetDateTime::parse("2026-01-15T12:00:00Z", &Rfc3339).unwrap()
}

fn context() -> Context {
    Context {
        host: "gitlab.example.com".into(),
        now: now(),
    }
}

fn loaded() -> App {
    let mut app = App::new(PAGE_SIZE);
    let mut commands: Vec<Cmd> = app.start();
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, now()) {
            commands.extend(app.update(msg));
        }
    }
    app
}

fn press(app: &mut App, c: char) {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::NONE,
    )));
}

fn render(app: &App, width: u16, height: u16) -> Buffer {
    let mut terminal = Terminal::new(TestBackend::new(width, height)).unwrap();
    terminal.draw(|frame| draw(frame, app, &context())).unwrap();
    terminal.backend().buffer().clone()
}

fn row_text(buffer: &Buffer, y: u16) -> String {
    (0..buffer.area.width)
        .map(|x| buffer[(x, y)].symbol())
        .collect()
}

fn screen(buffer: &Buffer) -> String {
    (0..buffer.area.height)
        .map(|y| row_text(buffer, y).trim_end().to_string())
        .collect::<Vec<_>>()
        .join("\n")
}

#[test]
fn a_wide_terminal_shows_the_sidebar_beside_the_detail_pane() {
    // Given: the demo data on a wide terminal.
    let app = loaded();

    // When: the screen is drawn at 120 by 32.
    let buffer = render(&app, 120, 32);

    // Then: the prompt, the pane strip, the sidebar beside the detail pane, and the status bar.
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn a_narrow_terminal_shows_the_sidebar_alone_until_the_detail_pane_is_opened() {
    // Given: the demo data on the jobs pane of a narrow terminal.
    let mut app = loaded();
    press(&mut app, '3');

    // When: the screen is drawn, and drawn again with the detail pane opened.
    let sidebar = render(&app, 80, 24);
    press(&mut app, 'l');
    let detail = render(&app, 80, 24);

    // Then: the sidebar fills the body, and then the detail pane does.
    insta::assert_snapshot!("narrow_sidebar", screen(&sidebar));
    insta::assert_snapshot!("narrow_detail", screen(&detail));
}

#[test]
fn the_full_screen_mode_folds_every_other_pane_to_one_rule() {
    // Given: the pipelines pane in the full-screen mode.
    let mut app = loaded();
    press(&mut app, '2');
    press(&mut app, 'z');

    // When: the screen is drawn.
    let buffer = render(&app, 100, 30);

    // Then: the other panes are one rule each, and the pipelines take the rest.
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn help_shows_the_mark_and_every_key() {
    // Given: help open on a terminal tall enough for the mark.
    let mut app = loaded();
    press(&mut app, '?');

    // When: the screen is drawn.
    let buffer = render(&app, 120, 40);

    // Then: the mark and the key list sit in a frame over the screen.
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn a_terminal_below_the_minimum_says_how_large_it_has_to_be() {
    // When: the screen is drawn at 30 by 8.
    let buffer = render(&loaded(), 30, 8);

    // Then: it names its own size and the size lazylab needs, and draws nothing else.
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn every_state_draws_at_every_size() {
    // Given: an app before any data arrives, and one with the demo data, in every pane and mode.
    let mut states = Vec::new();
    for data in [false, true] {
        for pane in ['1', '2', '3', '4'] {
            for keys in ["", "z", "l", "?"] {
                let mut app = if data { loaded() } else { App::new(PAGE_SIZE) };
                press(&mut app, pane);
                keys.chars().for_each(|key| press(&mut app, key));
                states.push(app);
            }
        }
    }

    // When: each state is drawn at sizes from one cell up, including sizes no snapshot picks.
    for width in (1..=180).step_by(7) {
        for height in [1, 2, 4, 9, 10, 11, 13, 16, 20, 24, 31, 40, 60] {
            for app in &states {
                // Then: it draws without a panic. A pane handed less than its own frame is the
                // Go release's stage table panic.
                render(app, width, height);
            }
        }
    }
}

#[test]
fn the_current_project_wears_a_gold_marker_around_an_iris_label() {
    // Given: the demo data, with the first project current.
    let app = loaded();
    let path = &app.project().unwrap().path_with_namespace;

    // When: the screen is drawn.
    let buffer = render(&app, 120, 32);

    // Then: on the real cells, the bracket before the status glyph is gold and the path is iris.
    let (y, row) = sidebar_row(&buffer, path);
    let at = column_of(&row, path);
    assert_eq!(buffer[(at - 3, y)].symbol(), "[");
    assert_eq!(buffer[(at - 3, y)].fg, T.gold);
    assert_eq!(buffer[(at, y)].fg, T.iris);
}

#[test]
fn a_short_sidebar_folds_every_pane_but_the_one_in_view() {
    // Given: a sidebar 12 rows tall, with the jobs pane in view and every pane wanting 9 rows.
    // When: its heights are planned.
    let heights = sidebar::plan(12, Pane::Jobs, false, |_| 9);

    // Then: each other pane folds to one row, the jobs pane takes the rest, and nothing is lost.
    for pane in Pane::ALL {
        let expected = if pane == Pane::Jobs { 12 - 3 } else { 1 };
        assert_eq!(heights[pane.index()], expected, "{pane:?}");
    }
}

#[test]
fn a_tall_sidebar_lets_the_other_panes_peek() {
    // Given: a sidebar 40 rows tall, with the projects pane in view.
    // When: its heights are planned, outside the full-screen mode and then inside it.
    let peeking = sidebar::plan(40, Pane::Projects, false, |_| 9);
    let zoomed = sidebar::plan(40, Pane::Projects, true, |_| 9);

    // Then: the other panes keep a few rows each, and the full-screen mode folds them instead.
    for pane in [Pane::Pipelines, Pane::Jobs, Pane::MergeRequests] {
        assert!(peeking[pane.index()] > 1, "{pane:?} peeks");
        assert_eq!(zoomed[pane.index()], 1, "{pane:?} folds");
    }
    assert_eq!(peeking.iter().sum::<u16>(), 40);
    assert_eq!(zoomed.iter().sum::<u16>(), 40);
}

#[test]
fn the_focused_side_lights_its_corners() {
    // Given: the demo data on a wide terminal, first with the sidebar focused, then the detail.
    let mut app = loaded();
    let sidebar_lit = render(&app, 120, 32);
    press(&mut app, 'l');
    let detail_lit = render(&app, 120, 32);

    // Then: the top left corner of the sidebar is gold only while the sidebar has the focus.
    let corner = (0, 2);
    assert_eq!(sidebar_lit[corner].fg, T.gold);
    assert_ne!(detail_lit[corner].fg, T.gold);
    assert_eq!(app.focus, Focus::Detail);
}

/// The first list row that holds `text`. A list row opens with the side rule, which tells it from
/// the prompt and from a frame title, because both of those can name the current project too.
fn sidebar_row(buffer: &Buffer, text: &str) -> (u16, String) {
    (0..buffer.area.height)
        .map(|y| (y, row_text(buffer, y)))
        .find(|(_, row)| row.starts_with('│') && row.contains(text))
        .expect("a list row holding the text")
}

fn column_of(row: &str, text: &str) -> u16 {
    let byte = row.find(text).expect("the text on the row");
    row[..byte].chars().count() as u16
}

#[test]
fn a_failure_takes_the_status_bar_until_a_key_is_pressed() {
    // Given: the demo data, and a pipeline list for the current project that failed.
    let mut app = loaded();
    let project = app.project().unwrap().id;
    app.update(Msg::Failed {
        cmd: Cmd::LoadPipelines { project, page: 1 },
        error: "list pipelines: connection refused".into(),
    });

    // When: the screen is drawn, and drawn again after a key.
    let failed = render(&app, 120, 32);
    app.update(Msg::Key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE)));
    let after = render(&app, 120, 32);

    // Then: the bottom row names the failure in the error colour, until the key.
    let bottom = row_text(&failed, 31);
    let x = column_of(&bottom, "list pipelines: connection refused");
    assert_eq!(failed[(x, 31)].fg, T.love);
    assert!(!row_text(&after, 31).contains("list pipelines"));
}

#[test]
fn a_load_in_progress_counts_the_projects_that_have_arrived() {
    // Given: a cache with two projects of five, while the rest load.
    let mut app = App::new(PAGE_SIZE);
    app.start();
    let Some(Msg::CacheLoaded(Some(mut cached))) = demo::answer(Cmd::LoadCache, now()) else {
        panic!("the demo answers with a cache");
    };
    cached.projects.truncate(2);
    cached.total = 5;
    app.update(Msg::CacheLoaded(Some(cached)));

    // When: the screen is drawn.
    let buffer = render(&app, 120, 32);

    // Then: the end of the bottom row counts them.
    let bottom = row_text(&buffer, 31);
    assert!(
        bottom.trim_end().ends_with("loading projects 2/5"),
        "{bottom}"
    );
}

#[test]
fn a_favorite_wears_a_gold_star_before_its_path() {
    // Given: the demo data, with the second project a favorite.
    let mut app = loaded();
    let path = app.projects[1].path_with_namespace.clone();
    app.update(Msg::FavoritesLoaded(vec![app.projects[1].id]));

    // When: the screen is drawn.
    let buffer = render(&app, 120, 32);

    // Then: a gold star sits before the favorite's status glyph, and on no other row.
    let (y, row) = sidebar_row(&buffer, &path);
    let star = column_of(&row, &path) - 4;
    assert_eq!(buffer[(star, y)].symbol(), "★");
    assert_eq!(buffer[(star, y)].fg, T.gold);
    let starred = (0..32).filter(|&y| row_text(&buffer, y).contains('★'));
    assert_eq!(starred.count(), 1);
}

#[test]
fn the_projects_pane_names_its_search_and_its_favorites_view() {
    // Given: the demo data, with the first project a favorite.
    let mut app = loaded();
    app.update(Msg::FavoritesLoaded(vec![app.projects[0].id]));

    // When: a search is typed, and drawn; then the favorites show alone, and drawn.
    press(&mut app, '/');
    "plat".chars().for_each(|c| press(&mut app, c));
    let searching = screen(&render(&app, 120, 32));
    app.update(Msg::Key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE)));
    press(&mut app, 't');
    let favorites = screen(&render(&app, 120, 32));

    // Then: the pane shows the search as it is typed, and then names the favorites view.
    assert!(searching.contains("/plat_"), "{searching}");
    assert!(favorites.contains("★ favorites"), "{favorites}");
}

#[test]
fn each_project_shows_its_latest_pipeline_before_its_path() {
    // Given: the demo data, with known statuses for three projects and one still loading.
    let mut app = loaded();
    let ids: Vec<u64> = app.projects.iter().map(|p| p.id).collect();
    let paths: Vec<String> = (app.projects.iter())
        .map(|p| p.path_with_namespace.clone())
        .collect();
    app.update(Msg::StatusLoaded {
        project: ids[0],
        status: Some(Status::Failed),
    });
    app.update(Msg::StatusLoaded {
        project: ids[1],
        status: None,
    });
    app.update(Msg::Failed {
        cmd: Cmd::LoadStatus { project: ids[2] },
        error: "list pipelines: timed out".into(),
    });

    // When: the screen is drawn.
    let buffer = render(&app, 120, 32);

    // Then: a red cross, the empty set and a question mark sit before those paths.
    let glyph_before = |path: &str| {
        let (y, row) = sidebar_row(&buffer, path);
        let x = column_of(&row, path) - 2;
        (buffer[(x, y)].symbol().to_string(), buffer[(x, y)].fg)
    };
    assert_eq!(glyph_before(&paths[0]), ("✗".into(), T.love));
    assert_eq!(glyph_before(&paths[1]).0, "∅");
    assert_eq!(glyph_before(&paths[2]).0, "?");
}
