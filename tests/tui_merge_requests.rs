use lazylab::gitlab::Discussion;
use lazylab::tui::app::{App, Cmd, Msg, Overlay, Pane};
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
    render_at(app, 32)
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

fn resolves(commands: &[Cmd]) -> Vec<(String, bool)> {
    (commands.iter())
        .filter_map(|c| match c {
            Cmd::ResolveThread {
                discussion,
                resolved,
                ..
            } => Some((discussion.clone(), *resolved)),
            _ => None,
        })
        .collect()
}

#[test]
fn r_resolves_or_reopens_the_picked_thread_and_the_threads_load_again() {
    // Given: the threads of the first merge request, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');

    // When: `r` is pressed on the resolved first thread, then on the unresolved second one.
    let reopen = press(&mut app, 'r');
    press(&mut app, 'j');
    let resolve = press(&mut app, 'r');

    // Then: the first is reopened and the second resolved, each in the merge request it is in.
    assert_eq!(resolves(&reopen), [("d1".to_string(), false)]);
    assert_eq!(resolves(&resolve), [("d3".to_string(), true)]);
    assert!(matches!(
        resolve[..],
        [Cmd::ResolveThread {
            project: PROJECT,
            merge_request: 412,
            ..
        }]
    ));

    // When: GitLab answers that the second thread is resolved.
    let reload = app.update(Msg::ThreadResolved {
        project: PROJECT,
        merge_request: 412,
        resolved: true,
    });

    // Then: the status bar says so, and the threads load again with the same thread picked.
    let bar = rows(&render(&app)).last().unwrap().clone();
    assert!(bar.contains("Resolved the thread"), "{bar}");
    assert_eq!(threads_asked(&reload), [(PROJECT, 412)]);
    answer(&mut app, reload);
    assert_eq!(marked_thread(&render(&app)), "Mei");
}

#[test]
fn a_thread_that_cannot_be_resolved_says_so() {
    // Given: the plain comment picked, which GitLab does not let anyone resolve.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'G');
    press(&mut app, 'k');

    // When: `r` is pressed.
    let commands = press(&mut app, 'r');

    // Then: nothing goes to GitLab, and the status bar says why.
    assert_eq!(resolves(&commands), []);
    let bar = rows(&render(&app)).last().unwrap().clone();
    assert!(bar.contains("This thread cannot be resolved"), "{bar}");
}

#[test]
fn r_resolves_only_in_the_detail_of_the_threads_tab() {
    // Given: the merge request info in the detail pane, and the threads tab with the sidebar
    // focused.
    let mut info = loaded();
    press(&mut info, '4');
    press(&mut info, 'l');
    let mut threads = on_threads();

    // When: `r` is pressed in each.
    let commands = [press(&mut info, 'r'), press(&mut threads, 'r')].concat();

    // Then: nothing goes to GitLab.
    assert_eq!(resolves(&commands), []);
}

#[test]
fn a_resolve_answered_after_the_cursor_left_loads_no_threads() {
    // Given: the first thread reopened, and the cursor moved to the next merge request.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'r');
    press(&mut app, 'h');
    press(&mut app, 'j');

    // When: GitLab answers that the thread is reopened.
    let commands = app.update(Msg::ThreadResolved {
        project: PROJECT,
        merge_request: 412,
        resolved: false,
    });

    // Then: the status bar says so, and the first merge request's threads are not asked for.
    let bar = rows(&render(&app)).last().unwrap().clone();
    assert!(bar.contains("Reopened the thread"), "{bar}");
    assert_eq!(threads_asked(&commands), []);
}

#[test]
fn the_key_bar_offers_r_to_resolve_a_thread() {
    // Given: the threads tab, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');

    // When: the screen is drawn.
    let shown = rows(&render(&app));

    // Then: the key bar offers `r` to resolve the thread.
    let bar = shown.last().unwrap();
    assert!(bar.contains("r resolve"), "{bar}");
}

fn diffs_asked(commands: &[Cmd]) -> Vec<(u64, u64)> {
    (commands.iter())
        .filter_map(|c| match c {
            Cmd::LoadDiff {
                project,
                merge_request,
            } => Some((*project, *merge_request)),
            _ => None,
        })
        .collect()
}

fn on_diff() -> App {
    let mut app = loaded();
    press(&mut app, '4');
    let commands = press(&mut app, '[');
    answer(&mut app, commands);
    app
}

fn shows(buffer: &Buffer, text: &str) -> bool {
    rows(buffer).iter().any(|row| row.contains(text))
}

fn colour_of(buffer: &Buffer, text: &str) -> ratatui::style::Color {
    let shown = rows(buffer);
    let (y, row) = (shown.iter().enumerate())
        .find(|(_, row)| row.contains(text))
        .unwrap_or_else(|| panic!("no {text:?} in\n{}", shown.join("\n")));
    let x = row[..row.find(text).unwrap()].chars().count() as u16;
    buffer[(x, y as u16)].fg
}

#[test]
fn the_diff_tab_loads_the_diff_of_the_merge_request_the_cursor_rests_on() {
    // Given: the demo data on the merge requests pane, on its info tab.
    let mut app = loaded();
    press(&mut app, '4');

    // When: the diff tab opens.
    let opened = press(&mut app, '[');

    // Then: the diff of the first merge request is asked for at once, and no threads.
    assert_eq!(diffs_asked(&opened), [(PROJECT, 412)]);
    assert_eq!(threads_asked(&opened), []);

    // When: the cursor moves down two merge requests, then rests.
    let moving = [press(&mut app, 'j'), press(&mut app, 'j')].concat();
    let settled = ticks(&mut app, SETTLE_TICKS);

    // Then: only the diff of the merge request it stopped on is asked for, once it rests.
    assert_eq!(diffs_asked(&moving), []);
    assert_eq!(diffs_asked(&settled), [(PROJECT, 407)]);
}

#[test]
fn no_diff_is_asked_for_while_the_diff_tab_is_out_of_view() {
    // Given: the demo data on the threads tab of the merge requests pane.
    let mut app = on_threads();

    // When: the cursor moves to the next merge request, and rests well past the wait.
    let commands = [press(&mut app, 'j'), ticks(&mut app, SETTLE_TICKS * 3)].concat();

    // Then: no diff is asked for.
    assert_eq!(diffs_asked(&commands), []);
}

#[test]
fn the_diff_tab_shows_each_file_with_its_lines_in_the_palette_colours() {
    // Given: the diff of the first merge request, loaded from the demo data.
    let app = on_diff();

    // When: the screen is drawn tall enough for the whole diff.
    let buffer = render_at(&app, 60);

    // Then: each file says how it changed, and a file too large for GitLab says so.
    assert!(
        shows(&buffer, "modified: .gitlab-ci.yml"),
        "{}",
        screen(&buffer)
    );
    assert!(shows(&buffer, "renamed: ci/cache.sh → ci/layers.sh"));
    assert!(shows(&buffer, "new file: Makefile"));
    assert!(shows(&buffer, "deleted: db/old-cache.sql"));
    assert!(shows(&buffer, "modified: vendor/bundle.js"));
    assert!(shows(&buffer, "too large to show"));

    // And: a tab in the code reaches the next stop of four columns.
    assert!(shows(&buffer, "+ci: build"));
    assert!(shows(&buffer, "+    just ci-compile"));

    // And: added lines are pine, removed lines love, hunk headers foam, the rest plain text,
    // and a removed line that starts with dashes is still a removed line.
    assert_eq!(colour_of(&buffer, "+  cache:"), T.pine);
    assert_eq!(colour_of(&buffer, "-compile:"), T.love);
    assert_eq!(colour_of(&buffer, "--- old"), T.love);
    assert_eq!(colour_of(&buffer, "@@ -12,6 +12,13 @@"), T.foam);
    assert_eq!(colour_of(&buffer, "   interruptible: true"), T.text);
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn j_and_k_in_the_detail_scroll_the_diff() {
    // Given: the diff of the first merge request, taller than the detail pane, focused.
    let mut app = on_diff();
    press(&mut app, 'l');

    // Then: the diff shows from its first file.
    let top = render(&app);
    assert!(shows(&top, "modified: .gitlab-ci.yml"));
    assert!(!shows(&top, "too large to show"));

    // When: `j` is pressed. Then: the diff moves up a line, and the merge request stays.
    press(&mut app, 'j');
    assert!(!shows(&render(&app), "modified: .gitlab-ci.yml"));
    assert_eq!(app.cursor(Pane::MergeRequests), 0);

    // When: `G`, then `j` past the end. Then: the last line shows at the bottom, still.
    press(&mut app, 'G');
    let bottom = render(&app);
    assert!(shows(&bottom, "too large to show"));
    press(&mut app, 'j');
    assert_eq!(screen(&render(&app)), screen(&bottom));

    // When: `g`, then `k` past the top. Then: the diff shows from its first file again.
    press(&mut app, 'g');
    press(&mut app, 'k');
    assert_eq!(screen(&render(&app)), screen(&top));
}

#[test]
fn another_merge_request_opens_at_the_top_of_its_diff() {
    // Given: the end of the first merge request's diff in view.
    let mut app = on_diff();
    press(&mut app, 'l');
    press(&mut app, 'G');

    // When: the cursor moves to the next merge request, and its diff loads.
    press(&mut app, 'h');
    press(&mut app, 'j');
    let asked = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, asked);

    // Then: its diff shows from its first file.
    assert!(shows(&render(&app), "modified: .gitlab-ci.yml"));
}

#[test]
fn a_late_diff_for_a_merge_request_the_cursor_left_is_dropped() {
    // Given: the second merge request's diff in view, while the first merge request's diff is
    // still on its way.
    let mut app = loaded();
    press(&mut app, '4');
    let late = press(&mut app, '[');
    press(&mut app, 'j');
    let asked = ticks(&mut app, SETTLE_TICKS);
    answer(&mut app, asked);

    // When: the first merge request's diff arrives.
    let late = demo::answer(late.into_iter().next().unwrap(), now()).unwrap();
    let commands = app.update(late);

    // Then: the second merge request's diff stays in view, and is not asked for again.
    assert!(shows(&render(&app), "modified: .gitlab-ci.yml"));
    assert_eq!(diffs_asked(&commands), []);
}

#[test]
fn a_failed_diff_load_says_so_and_is_not_asked_again_until_the_cursor_moves() {
    // Given: the diff tab, with the first merge request's diff asked for.
    let mut app = loaded();
    press(&mut app, '4');
    let cmd = press(&mut app, '[').pop().unwrap();

    // When: the load fails, and time passes.
    let failed = app.update(Msg::Failed {
        cmd,
        error: "403 Forbidden".into(),
    });
    let waited = ticks(&mut app, SETTLE_TICKS * 3);

    // Then: the detail says the diff did not load, and it is not asked for again.
    assert!(shows(&render(&app), "the diff did not load"));
    assert_eq!(diffs_asked(&[failed, waited].concat()), []);

    // When: the cursor moves to the next merge request and rests. Then: its diff loads.
    press(&mut app, 'j');
    assert_eq!(
        diffs_asked(&ticks(&mut app, SETTLE_TICKS)),
        [(PROJECT, 409)]
    );
}

#[test]
fn the_key_bar_offers_j_and_k_to_scroll_the_diff() {
    // Given: the diff tab, with the detail pane focused.
    let mut app = on_diff();
    press(&mut app, 'l');

    // When: the screen is drawn.
    let shown = rows(&render(&app));

    // Then: the key bar offers `j/k` to scroll.
    let bar = shown.last().unwrap();
    assert!(bar.contains("j/k scroll"), "{bar}");
}

#[test]
fn the_info_tab_shows_the_merge_request() {
    // Given: the demo data on the merge requests pane, on its info tab.
    let mut app = loaded();
    press(&mut app, '4');

    // When: the screen is drawn.
    let buffer = render(&app);

    // Then: the detail shows the title, the branches, the author and the age.
    insta::assert_snapshot!(screen(&buffer));
}

#[test]
fn a_tab_in_a_thread_reaches_the_next_stop_of_four_columns() {
    // Given: the threads tab of the first merge request.
    let mut app = on_threads();

    // When: its threads load again with a note whose code is indented with a tab.
    let thread: Discussion = serde_json::from_value(serde_json::json!({"id": "d9", "notes": [
        {"id": 9, "author": {"name": "Kofi"}, "body": "if ok {\n\treturn x\n}\nkey:\tvalue"},
    ]}))
    .unwrap();
    app.update(Msg::ThreadsLoaded {
        project: PROJECT,
        merge_request: 412,
        discussions: vec![thread],
    });

    // Then: the indented line keeps its indent under the tree, and a tab after text reaches the
    // next stop.
    let buffer = render(&app);
    assert!(shows(&buffer, "        return x"), "{}", screen(&buffer));
    assert!(shows(&buffer, "key:    value"));
}

#[test]
fn a_second_resolve_during_a_reload_loads_the_threads_once_more() {
    // Given: the first thread reopened, and the reload that follows still on its way.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'r');
    let first = app.update(Msg::ThreadResolved {
        project: PROJECT,
        merge_request: 412,
        resolved: false,
    });

    // When: the second thread is resolved before that reload answers, and then it answers.
    press(&mut app, 'j');
    press(&mut app, 'r');
    let second = app.update(Msg::ThreadResolved {
        project: PROJECT,
        merge_request: 412,
        resolved: true,
    });
    let reload = demo::answer(first.into_iter().next().unwrap(), now()).unwrap();
    let after = app.update(reload);

    // Then: one load is out at a time, and the threads load once more after the first, so the
    // second change shows too.
    assert_eq!(threads_asked(&second), []);
    assert_eq!(threads_asked(&after), [(PROJECT, 412)]);
}

#[test]
fn a_reload_due_on_one_merge_request_is_not_spent_on_the_next() {
    // Given: two resolves on the first merge request, the second while the first's reload was
    // still on its way.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'r');
    let resolved = |resolved| Msg::ThreadResolved {
        project: PROJECT,
        merge_request: 412,
        resolved,
    };
    app.update(resolved(false));
    press(&mut app, 'j');
    press(&mut app, 'r');
    app.update(resolved(true));

    // When: the cursor moves to the next merge request, and its threads arrive.
    press(&mut app, 'h');
    press(&mut app, 'j');
    let asked = ticks(&mut app, SETTLE_TICKS);
    let threads = demo::answer(asked.into_iter().next().unwrap(), now()).unwrap();
    let commands = app.update(threads);

    // Then: the next merge request's threads are not asked for again.
    assert_eq!(threads_asked(&commands), []);
}

#[test]
fn a_reload_due_goes_out_once_after_a_failed_reload() {
    // Given: two resolves on the first merge request, the second while the first's reload was
    // still on its way.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'r');
    let resolved = |resolved| Msg::ThreadResolved {
        project: PROJECT,
        merge_request: 412,
        resolved,
    };
    let first = app.update(resolved(false));
    press(&mut app, 'j');
    press(&mut app, 'r');
    app.update(resolved(true));

    // When: the first reload fails, and the one it left due fails too.
    let failed = |cmd: Cmd| Msg::Failed {
        cmd,
        error: "502 Bad Gateway".into(),
    };
    let due = app.update(failed(first.into_iter().next().unwrap()));
    let after = app.update(failed(due[0].clone()));

    // Then: the due reload goes out once, and no more after it fails.
    assert_eq!(threads_asked(&due), [(PROJECT, 412)]);
    assert_eq!(threads_asked(&after), []);
}

fn comments(commands: &[Cmd]) -> Vec<Cmd> {
    (commands.iter())
        .filter(|c| matches!(c, Cmd::Comment { .. }))
        .cloned()
        .collect()
}

/// Presses `c`, types the text, and presses enter.
fn comment(app: &mut App, text: &str) -> Vec<Cmd> {
    let mut commands = press(app, 'c');
    for c in text.chars() {
        commands.extend(press(app, c));
    }
    commands.extend(app.update(Msg::Key(KeyEvent::new(
        KeyCode::Enter,
        KeyModifiers::NONE,
    ))));
    commands
}

fn commented(reply: bool) -> Msg {
    Msg::Commented {
        project: PROJECT,
        merge_request: 412,
        reply,
    }
}

#[test]
fn c_on_the_info_tab_starts_a_thread_on_the_merge_request_in_view() {
    // Given: the info of the second merge request, with the detail pane focused.
    let mut app = loaded();
    press(&mut app, '4');
    press(&mut app, 'j');
    press(&mut app, 'l');

    // When: `c` opens the form, and a comment is typed and sent.
    let sent = comment(&mut app, "Ship it");

    // Then: a new thread goes to that merge request, and the form closes.
    assert_eq!(
        comments(&sent),
        [Cmd::Comment {
            project: PROJECT,
            merge_request: 409,
            discussion: None,
            body: "Ship it".into(),
        }]
    );
    assert_eq!(app.overlay, Overlay::None);
    assert!(app.form.is_none());
}

#[test]
fn c_on_the_threads_tab_replies_in_the_picked_thread() {
    // Given: the second thread picked, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');
    press(&mut app, 'j');

    // When: `c` opens the form, and a reply is typed and sent.
    let sent = comment(&mut app, "Done in the docs");

    // Then: the reply goes to that thread, in the merge request it is in.
    assert_eq!(
        comments(&sent),
        [Cmd::Comment {
            project: PROJECT,
            merge_request: 412,
            discussion: Some("d3".into()),
            body: "Done in the docs".into(),
        }]
    );
}

#[test]
fn c_opens_no_form_on_the_diff_tab_or_from_the_sidebar() {
    // Given: the diff tab with the detail pane focused, and the threads tab with the sidebar
    // focused.
    let mut diff = on_diff();
    press(&mut diff, 'l');
    let mut threads = on_threads();

    for app in [&mut diff, &mut threads] {
        // When: `c` is pressed.
        let commands = press(app, 'c');

        // Then: no form opens, and nothing goes to GitLab.
        assert_eq!(app.overlay, Overlay::None);
        assert_eq!(comments(&commands), []);
    }
}

#[test]
fn a_blank_comment_is_not_sent_and_the_form_stays_open() {
    // Given: the info tab, with the detail pane focused.
    let mut app = loaded();
    press(&mut app, '4');
    press(&mut app, 'l');

    // When: a comment of spaces alone is sent.
    let sent = comment(&mut app, "   ");

    // Then: nothing goes to GitLab, and the form is still open.
    assert_eq!(comments(&sent), []);
    assert_eq!(app.overlay, Overlay::Form);
}

#[test]
fn a_reply_answered_says_so_and_the_threads_load_again() {
    // Given: a reply sent in the first thread.
    let mut app = on_threads();
    press(&mut app, 'l');
    comment(&mut app, "Thanks");

    // When: GitLab answers that the reply is in.
    let reload = app.update(commented(true));

    // Then: the status bar says so, and the threads load again.
    let bar = rows(&render(&app)).last().unwrap().clone();
    assert!(bar.contains("Replied in the thread"), "{bar}");
    assert_eq!(threads_asked(&reload), [(PROJECT, 412)]);
}

#[test]
fn a_comment_answered_during_a_reload_loads_the_threads_once_more() {
    // Given: a reply answered, and the reload that follows still on its way.
    let mut app = on_threads();
    press(&mut app, 'l');
    let first = app.update(commented(true));

    // When: a second reply is answered before that reload answers, and then it answers.
    let second = app.update(commented(true));
    let reload = demo::answer(first.into_iter().next().unwrap(), now()).unwrap();
    let after = app.update(reload);

    // Then: one load is out at a time, and the threads load once more after the first.
    assert_eq!(threads_asked(&second), []);
    assert_eq!(threads_asked(&after), [(PROJECT, 412)]);
}

#[test]
fn a_thread_started_from_the_info_tab_shows_once_the_threads_tab_opens() {
    // Given: the threads of the first merge request loaded, and then the info tab in the detail.
    let mut app = on_threads();
    press(&mut app, '[');
    press(&mut app, 'l');

    // When: a new thread is answered, and the threads tab opens.
    let answered = app.update(commented(false));
    let opened = press(&mut app, ']');

    // Then: no threads load while out of view, and they load on the threads tab.
    assert_eq!(threads_asked(&answered), []);
    assert_eq!(threads_asked(&opened), [(PROJECT, 412)]);
}

#[test]
fn a_new_thread_answered_says_so() {
    // Given: the info tab, with the detail pane focused.
    let mut app = loaded();
    press(&mut app, '4');
    press(&mut app, 'l');

    // When: GitLab answers that the new thread is in.
    app.update(commented(false));

    // Then: the status bar says so.
    let bar = rows(&render(&app)).last().unwrap().clone();
    assert!(bar.contains("Started a thread"), "{bar}");
}

#[test]
fn a_failed_comment_shows_the_error_and_loads_nothing() {
    // Given: a reply sent in the first thread.
    let mut app = on_threads();
    press(&mut app, 'l');
    let sent = comment(&mut app, "Thanks");

    // When: GitLab refuses it.
    let commands = app.update(Msg::Failed {
        cmd: comments(&sent).remove(0),
        error: "reply in merge request thread: 403 Forbidden".into(),
    });

    // Then: the status bar shows the error, no form is open, and nothing loads again.
    let bar = rows(&render(&app)).last().unwrap().clone();
    assert!(bar.contains("403 Forbidden"), "{bar}");
    assert_eq!(app.overlay, Overlay::None);
    assert_eq!(commands, []);
}

#[test]
fn the_comment_form_starts_a_thread() {
    // Given: the info tab, with the detail pane focused.
    let mut app = loaded();
    press(&mut app, '4');
    press(&mut app, 'l');

    // When: `c` opens the form, and a comment is typed.
    press(&mut app, 'c');
    for c in "Ship it".chars() {
        press(&mut app, c);
    }

    // Then: a framed form shows the comment in a marked box, and the key bar offers to send it.
    insta::assert_snapshot!(screen(&render_at(&app, 12)));
}

#[test]
fn the_comment_form_replies_in_the_picked_thread() {
    // Given: the threads tab, with the detail pane focused.
    let mut app = on_threads();
    press(&mut app, 'l');

    // When: `c` opens the form.
    press(&mut app, 'c');

    // Then: the form says it replies, with an empty box.
    insta::assert_snapshot!(screen(&render_at(&app, 12)));
}

#[test]
fn the_key_bar_offers_c_on_the_info_and_threads_tabs() {
    // Given: the info tab and the threads tab, each with the detail pane focused.
    let mut info = loaded();
    press(&mut info, '4');
    press(&mut info, 'l');
    let mut threads = on_threads();
    press(&mut threads, 'l');

    // When: the screens are drawn.
    let bar = |app: &App| rows(&render(app)).last().unwrap().clone();

    // Then: the info tab offers a comment, and the threads tab a reply.
    assert!(bar(&info).contains("c comment"), "{}", bar(&info));
    assert!(bar(&threads).contains("c reply"), "{}", bar(&threads));
}
