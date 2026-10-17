use lazylab::tui::app::{App, Cmd, Msg, Pane};
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

fn press(app: &mut App, c: char) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::NONE,
    )))
}

fn ticks(app: &mut App, n: usize) -> Vec<Cmd> {
    (0..n).flat_map(|_| app.update(Msg::Tick)).collect()
}

fn answer(app: &mut App, mut commands: Vec<Cmd>) {
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, now()) {
            commands.extend(app.update(msg));
        }
    }
}

fn threads_asked(commands: &[Cmd]) -> Vec<(u64, u64)> {
    (commands.iter())
        .filter_map(|c| match c {
            Cmd::LoadThreads {
                project,
                merge_request,
            } => Some((*project, *merge_request)),
            _ => None,
        })
        .collect()
}

fn on_threads() -> App {
    let mut app = loaded();
    press(&mut app, '4');
    let commands = press(&mut app, ']');
    answer(&mut app, commands);
    app
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

fn screen(buffer: &Buffer) -> String {
    (rows(buffer).iter())
        .map(|row| row.trim_end())
        .collect::<Vec<_>>()
        .join("\n")
}

fn marked_thread(buffer: &Buffer) -> String {
    let shown = rows(buffer);
    let marked: Vec<_> = ["Dana", "Kofi", "Mei", "Ravi", "Sol"]
        .into_iter()
        .filter(|author| shown.iter().any(|row| row.contains(&format!("[{author}]"))))
        .collect();
    let [author] = marked[..] else {
        panic!("one marked thread, got {marked:?} in\n{}", shown.join("\n"));
    };
    author.to_string()
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

/// Three ticks of 100 ms, the wait before a moved cursor loads.
const SETTLE_TICKS: usize = 3;
const PROJECT: u64 = 101;

#[test]
fn the_threads_tab_loads_the_threads_of_the_merge_request_the_cursor_rests_on() {
    // Given: the demo data on the merge requests pane, on its info tab.
    let mut app = loaded();
    let on_info = press(&mut app, '4');

    // When: the threads tab opens.
    let opened = press(&mut app, ']');

    // Then: the threads of the first merge request are asked for at once, and not before.
    assert_eq!(threads_asked(&on_info), []);
    assert_eq!(threads_asked(&opened), [(PROJECT, 412)]);

    // When: the cursor moves down two merge requests, then rests.
    let moving = [press(&mut app, 'j'), press(&mut app, 'j')].concat();
    let settled = ticks(&mut app, SETTLE_TICKS);

    // Then: only the threads of the merge request it stopped on are asked for, once it rests.
    assert_eq!(threads_asked(&moving), []);
    assert_eq!(threads_asked(&settled), [(PROJECT, 407)]);
}

#[test]
fn no_threads_are_asked_for_while_the_threads_tab_is_out_of_view() {
    // Given: the demo data on the merge requests pane, on its info tab.
    let mut app = loaded();
    press(&mut app, '4');

    // When: the cursor moves to the next merge request, and rests well past the wait.
    let commands = [press(&mut app, 'j'), ticks(&mut app, SETTLE_TICKS * 3)].concat();

    // Then: no threads are asked for.
    assert_eq!(threads_asked(&commands), []);
}

#[test]
fn the_threads_tab_shows_each_thread_with_its_replies() {
    // Given: the threads of the first merge request, loaded from the demo data.
    let app = on_threads();

    // When: the screen is drawn.
    let buffer = render(&app);
    let shown = rows(&buffer);
    let has = |text: &str| shown.iter().any(|row| row.contains(text));

    // Then: a thread shows its author, its age, whether it is resolved, the line it is on, and
    // its notes on a tree, while a discussion of system notes only stays out.
    assert!(has("[Kofi] · 3h ago  resolved"), "{}", screen(&buffer));
    assert!(has(".gitlab-ci.yml:14"));
    assert!(has("│ Does the cache key change"));
    assert!(has("Dana · 2h ago"));
    assert!(has("└ No, the key is the digest"));
    assert!(has(" Mei  · 2h ago  unresolved"));
    assert!(!has("added 1 commit"));

    // And: the unresolved badge is in the palette's love, the resolved one in its pine.
    let (y, row) = (shown.iter().enumerate())
        .find(|(_, row)| row.contains("unresolved"))
        .unwrap();
    let x = row[..row.find("unresolved").unwrap()].chars().count() as u16;
    assert_eq!(buffer[(x, y as u16)].fg, T.love);
    let (y, row) = (shown.iter().enumerate())
        .find(|(_, row)| row.contains("  resolved"))
        .unwrap();
    let x = row[..row.find("  resolved").unwrap()].chars().count() as u16 + 2;
    assert_eq!(buffer[(x, y as u16)].fg, T.pine);
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn j_and_k_in_the_detail_pick_a_thread() {
    // Given: the threads of the first merge request, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');

    // Then: the first thread is picked, with a gold marker around an iris author.
    let buffer = render(&app);
    assert_eq!(marked_thread(&buffer), "Kofi");
    let shown = rows(&buffer);
    let (y, row) = (shown.iter().enumerate())
        .find(|(_, row)| row.contains("[Kofi]"))
        .unwrap();
    let x = row[..row.find("[Kofi]").unwrap()].chars().count() as u16;
    assert_eq!(buffer[(x, y as u16)].fg, T.gold);
    assert_eq!(buffer[(x + 1, y as u16)].fg, T.iris);

    // When: `j` is pressed. Then: the next thread is picked, and the merge request stays.
    press(&mut app, 'j');
    assert_eq!(marked_thread(&render(&app)), "Mei");
    assert_eq!(app.cursor(Pane::MergeRequests), 0);

    // When: `G`, then `j` past the end, then `k`. Then: the last thread, still, then the one
    // before it.
    press(&mut app, 'G');
    assert_eq!(marked_thread(&render(&app)), "Sol");
    press(&mut app, 'j');
    assert_eq!(marked_thread(&render(&app)), "Sol");
    press(&mut app, 'k');
    assert_eq!(marked_thread(&render(&app)), "Ravi");
}

#[test]
fn another_merge_request_opens_on_its_first_thread() {
    // Given: the last thread of the first merge request picked.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'G');

    // When: the cursor moves to the next merge request, and its threads load.
    press(&mut app, 'h');
    press(&mut app, 'j');
    let asked = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, asked);

    // Then: its first thread is picked.
    assert_eq!(marked_thread(&render(&app)), "Kofi");
}

#[test]
fn the_picked_thread_stays_in_view() {
    // Given: the threads of the first merge request, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');

    // When: the last thread is picked.
    press(&mut app, 'G');

    // Then: its header and its last reply show, though the threads before it fill the pane.
    let shown = rows(&render(&app));
    assert!(shown.iter().any(|row| row.contains("[Sol]")));
    assert!(shown.iter().any(|row| row.contains("└ Thanks.")));
}

#[test]
fn threads_for_a_merge_request_the_cursor_left_are_dropped() {
    // Given: the threads tab, with the first merge request's threads asked for.
    let mut app = loaded();
    press(&mut app, '4');
    let asked = press(&mut app, ']');

    // When: the cursor moves on before they arrive, and then they arrive.
    press(&mut app, 'j');
    answer(&mut app, asked);

    // Then: the detail does not show them, and waits for the threads of the new merge request.
    let shown = rows(&render(&app));
    assert!(!shown.iter().any(|row| row.contains("Kofi")));
    assert!(shown.iter().any(|row| row.contains("loading the threads")));
}

#[test]
fn a_late_answer_for_another_merge_request_leaves_the_picked_thread_alone() {
    // Given: the second merge request's threads, with its last thread picked, while the first
    // merge request's threads are still on their way.
    let mut app = loaded();
    press(&mut app, '4');
    let late = press(&mut app, ']');
    press(&mut app, 'j');
    let asked = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, asked);
    press(&mut app, 'l');
    press(&mut app, 'G');

    // When: the first merge request's threads arrive.
    let late = demo::answer(late.into_iter().next().unwrap(), now()).unwrap();
    let commands = app.update(late);

    // Then: the last thread of the second merge request is still picked, and its threads are
    // not asked for again.
    assert_eq!(marked_thread(&render(&app)), "Sol");
    assert_eq!(threads_asked(&commands), []);
}

#[test]
fn a_failed_load_says_so_and_is_not_asked_again_until_the_cursor_moves() {
    // Given: the threads tab, with the first merge request's threads asked for.
    let mut app = loaded();
    press(&mut app, '4');
    let cmd = press(&mut app, ']').pop().unwrap();

    // When: the load fails, and time passes.
    let failed = app.update(Msg::Failed {
        cmd,
        error: "403 Forbidden".into(),
    });
    let waited = ticks(&mut app, SETTLE_TICKS * 3);

    // Then: the detail says the threads did not load, and they are not asked for again.
    assert!(
        rows(&render(&app))
            .iter()
            .any(|row| row.contains("the threads did not load"))
    );
    assert_eq!(threads_asked(&[failed, waited].concat()), []);

    // When: the cursor moves to the next merge request and rests. Then: its threads load.
    press(&mut app, 'j');
    assert_eq!(
        threads_asked(&ticks(&mut app, SETTLE_TICKS)),
        [(PROJECT, 409)]
    );
}

#[test]
fn the_key_bar_offers_j_and_k_to_pick_a_thread() {
    // Given: the threads tab, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');

    // When: the screen is drawn.
    let shown = rows(&render(&app));

    // Then: the key bar offers `j/k` to pick a thread.
    let bar = shown.last().unwrap();
    assert!(bar.contains("j/k") && bar.contains("thread"), "{bar}");
}
