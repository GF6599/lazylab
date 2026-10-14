use lazylab::tui::app::{App, Cmd, Msg};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;

/// Three ticks of 100 ms, the Go release's 300 ms.
const SETTLE_TICKS: usize = 3;

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

fn press(app: &mut App, c: char) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(
        KeyCode::Char(c),
        KeyModifiers::NONE,
    )))
}

fn ticks(app: &mut App, n: usize) -> Vec<Cmd> {
    (0..n).flat_map(|_| app.update(Msg::Tick)).collect()
}

fn lists(commands: &[Cmd]) -> Vec<Cmd> {
    commands
        .iter()
        .filter(|c| {
            matches!(
                c,
                Cmd::LoadPipelines { .. } | Cmd::LoadMergeRequests { .. } | Cmd::LoadJobs { .. }
            )
        })
        .cloned()
        .collect()
}

#[test]
fn the_first_project_after_a_start_loads_its_lists_at_once() {
    // Given: a new app.
    let mut app = App::new(30);
    app.start();

    // When: the cached projects arrive.
    let commands = app.update(demo::answer(Cmd::LoadCache, OffsetDateTime::now_utc()).unwrap());

    // Then: the first project's lists are asked for with no wait.
    let project = app.project().unwrap().id;
    assert_eq!(
        lists(&commands),
        [
            Cmd::LoadPipelines { project },
            Cmd::LoadMergeRequests { project },
        ]
    );
}

#[test]
fn fast_moves_load_the_lists_of_the_project_the_cursor_stops_on_once() {
    // Given: the demo projects, with the first one's lists loaded.
    let mut app = loaded();

    // When: the cursor moves down three projects, one tick apart.
    let mut asked = Vec::new();
    for _ in 0..3 {
        asked.extend(press(&mut app, 'j'));
        asked.extend(ticks(&mut app, 1));
    }

    // Then: nothing is asked for while it moves.
    assert_eq!(lists(&asked), []);

    // When: it rests one tick short of the wait.
    let early = ticks(&mut app, SETTLE_TICKS - 1);

    // Then: still nothing is asked for.
    assert_eq!(lists(&early), []);

    // When: it rests to the end of the wait, and longer.
    let settled = ticks(&mut app, 6);

    // Then: the lists of the project it stopped on are asked for, once.
    let project = app.project().unwrap().id;
    assert_eq!(
        lists(&settled),
        [
            Cmd::LoadPipelines { project },
            Cmd::LoadMergeRequests { project },
        ]
    );
}

#[test]
fn a_move_clears_the_old_lists_at_once() {
    // Given: the demo projects, with the first one's lists shown.
    let mut app = loaded();
    assert!(!app.pipelines.is_empty());

    // When: the cursor moves to the next project.
    press(&mut app, 'j');

    // Then: the first project's lists are gone before the new ones are asked for.
    assert!(app.pipelines.is_empty() && app.jobs.is_empty() && app.merge_requests.is_empty());
}

#[test]
fn fast_moves_through_pipelines_load_the_jobs_of_the_one_the_cursor_stops_on_once() {
    // Given: the pipelines pane of the first project.
    let mut app = loaded();
    press(&mut app, '2');

    // When: the cursor moves down two pipelines, then rests.
    let moving: Vec<Cmd> = [press(&mut app, 'j'), press(&mut app, 'j')].concat();
    let settled = ticks(&mut app, SETTLE_TICKS + 5);

    // Then: only the jobs of the pipeline it stopped on are asked for, after it rests.
    assert_eq!(lists(&moving), []);
    let project = app.project().unwrap().id;
    let pipeline = app.pipeline().unwrap().id;
    assert_eq!(lists(&settled), [Cmd::LoadJobs { project, pipeline }]);
}

#[test]
fn new_pipelines_load_the_jobs_of_the_first_at_once() {
    // Given: the cursor moved to the second project, and the wait passed.
    let mut app = loaded();
    press(&mut app, 'j');
    ticks(&mut app, SETTLE_TICKS);
    let project = app.project().unwrap().id;

    // When: its pipelines arrive.
    let answer = demo::answer(Cmd::LoadPipelines { project }, OffsetDateTime::now_utc());
    let commands = app.update(answer.unwrap());

    // Then: the newest pipeline's jobs are asked for with no wait, because no cursor moved.
    let pipeline = app.pipeline().unwrap().id;
    assert_eq!(lists(&commands), [Cmd::LoadJobs { project, pipeline }]);
}
