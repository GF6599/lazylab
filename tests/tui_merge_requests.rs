use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::demo;
use lazylab::tui::theme::ROSE_PINE as T;
use lazylab::tui::view::{Context, draw};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::buffer::Buffer;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

fn now() -> OffsetDateTime {
    OffsetDateTime::parse("2026-01-15T12:00:00Z", &Rfc3339).unwrap()
}

fn loaded() -> App {
    let mut app = App::new(30);
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

fn render(app: &App) -> Buffer {
    let mut terminal = Terminal::new(TestBackend::new(120, 32)).unwrap();
    let context = Context {
        host: "gitlab.example.com".into(),
        now: now(),
    };
    terminal.draw(|frame| draw(frame, app, &context)).unwrap();
    terminal.backend().buffer().clone()
}

fn rows(buffer: &Buffer) -> Vec<String> {
    (0..buffer.area.height)
        .map(|y| {
            (0..buffer.area.width)
                .map(|x| buffer[(x, y)].symbol())
                .collect()
        })
        .collect()
}

fn marked_tab(buffer: &Buffer) -> String {
    let rows = rows(buffer);
    let strip = rows
        .iter()
        .find(|row| row.contains("info") && row.contains("threads") && row.contains("diff"))
        .unwrap_or_else(|| panic!("no tab strip in\n{}", rows.join("\n")));
    let marked: Vec<_> = ["info", "threads", "diff"]
        .into_iter()
        .filter(|tab| strip.contains(&format!("[{tab}]")))
        .collect();
    let [tab] = marked[..] else {
        panic!("one marked tab, got {marked:?} in {strip}");
    };
    tab.to_string()
}

#[test]
fn the_brackets_turn_the_merge_request_detail_through_its_tabs() {
    // Given: the demo data on the merge requests pane.
    let mut app = loaded();
    press(&mut app, '4');
    let source_branch = app.merge_requests[0].source_branch.clone();
    let shows_branch = |buffer: &Buffer| {
        (rows(buffer).iter()).any(|row| row.contains("from") && row.contains(&source_branch))
    };

    // Then: the detail opens on the info tab, marked with a gold bracket around an iris label,
    // and shows the merge request's branches.
    let buffer = render(&app);
    assert_eq!(marked_tab(&buffer), "info");
    let shown = rows(&buffer);
    let (y, strip) = (shown.iter().enumerate())
        .find(|(_, row)| row.contains("[info]"))
        .unwrap();
    let at = strip[..strip.find("[info]").unwrap()].chars().count() as u16;
    assert_eq!(buffer[(at, y as u16)].fg, T.gold);
    assert_eq!(buffer[(at + 1, y as u16)].fg, T.iris);
    assert!(shows_branch(&buffer));

    // When: `]` is pressed. Then: the threads tab is marked, and the info is gone.
    press(&mut app, ']');
    let buffer = render(&app);
    assert_eq!(marked_tab(&buffer), "threads");
    assert!(!shows_branch(&buffer));

    // When: `]` is pressed twice more. Then: the diff tab, and then the info tab again.
    press(&mut app, ']');
    assert_eq!(marked_tab(&render(&app)), "diff");
    press(&mut app, ']');
    assert_eq!(marked_tab(&render(&app)), "info");

    // When: `[` is pressed in the detail pane. Then: the tab before info, which is the diff.
    press(&mut app, 'l');
    press(&mut app, '[');
    assert_eq!(marked_tab(&render(&app)), "diff");
}

#[test]
fn the_brackets_on_another_pane_leave_the_merge_request_tab_alone() {
    // Given: the demo data on the projects pane, with the merge request detail on its info tab.
    let mut app = loaded();

    // When: `]` is pressed there, and the merge requests pane is chosen.
    press(&mut app, ']');
    press(&mut app, '4');

    // Then: the merge request detail is still on its info tab.
    assert_eq!(marked_tab(&render(&app)), "info");
}

#[test]
fn the_key_bar_offers_the_brackets_on_the_merge_requests_pane() {
    // Given: the demo data on the merge requests pane.
    let mut app = loaded();
    press(&mut app, '4');

    // When: the screen is drawn.
    let shown = rows(&render(&app));

    // Then: the key bar at the bottom offers `[ ]` to turn the tab.
    let bar = shown.last().unwrap();
    assert!(bar.contains("[ ]") && bar.contains("tab"), "{bar}");
}
