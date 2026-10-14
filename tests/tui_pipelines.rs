use lazylab::gitlab::{Job, Page, Pipeline, Status};
use lazylab::tui::app::{App, Cmd, Msg, ProjectStatus};
use lazylab::tui::demo;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use time::OffsetDateTime;

/// Fifty ticks of 100 ms, the Go release's five-second refresh.
const REFRESH_TICKS: usize = 50;

fn now() -> OffsetDateTime {
    OffsetDateTime::now_utc()
}

/// The demo projects, with the first project's pipelines and its newest pipeline's jobs shown,
/// and the pipelines pane in view.
fn on_pipelines() -> App {
    let mut app = App::new(30);
    let mut commands = app.start();
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, now()) {
            commands.extend(app.update(msg));
        }
    }
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

fn page(items: &[Pipeline], number: u32, next_page: Option<u32>) -> Page<Pipeline> {
    Page {
        items: items.to_vec(),
        page: number,
        next_page,
        total_pages: None,
        total_items: None,
    }
}

fn loaded(project: u64, page: Page<Pipeline>) -> Msg {
    Msg::PipelinesLoaded { project, page }
}

fn jobs_of(app: &App) -> Vec<Job> {
    let project = app.project().unwrap().id;
    let pipeline = app.pipeline().unwrap().id;
    match demo::answer(Cmd::LoadJobs { project, pipeline }, now()) {
        Some(Msg::JobsLoaded { jobs, .. }) => jobs,
        other => panic!("the demo answers with jobs, got {other:?}"),
    }
}

fn ids(pipelines: &[Pipeline]) -> Vec<u64> {
    pipelines.iter().map(|p| p.id).collect()
}

fn pipeline_requests(commands: &[Cmd]) -> Vec<(u64, u32)> {
    commands
        .iter()
        .filter_map(|c| match c {
            Cmd::LoadPipelines { project, page } => Some((*project, *page)),
            _ => None,
        })
        .collect()
}

fn job_requests(commands: &[Cmd]) -> Vec<u64> {
    commands
        .iter()
        .filter_map(|c| match c {
            Cmd::LoadJobs { pipeline, .. } => Some(*pipeline),
            _ => None,
        })
        .collect()
}

#[test]
fn closing_bracket_loads_the_next_page_and_opening_bracket_the_one_before() {
    // Given: the first page of a project whose pipelines go on to a second page.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let all = app.pipelines.clone();
    app.update(loaded(project, page(&all[..5], 1, Some(2))));

    // When: the closing bracket is pressed.
    let next = press(&mut app, ']');

    // Then: the second page is asked for, and the first page's pipelines and jobs are gone.
    assert_eq!(pipeline_requests(&next), [(project, 2)]);
    assert!(app.pipelines.is_empty() && app.jobs.is_empty());

    // When: the second page arrives, and the opening bracket is pressed.
    app.update(loaded(project, page(&all[5..10], 2, Some(3))));
    assert_eq!(ids(&app.pipelines), ids(&all[5..10]));
    let back = press(&mut app, '[');

    // Then: the first page is asked for again.
    assert_eq!(pipeline_requests(&back), [(project, 1)]);
}

#[test]
fn brackets_stop_at_the_first_and_last_page() {
    // Given: a project with a single page of pipelines.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let all = app.pipelines.clone();
    app.update(loaded(project, page(&all, 1, None)));

    // When: both brackets are pressed.
    let commands = [press(&mut app, '['), press(&mut app, ']')].concat();

    // Then: nothing is asked for, and the page stays.
    assert_eq!(pipeline_requests(&commands), []);
    assert_eq!(ids(&app.pipelines), ids(&all));
}

#[test]
fn a_page_that_arrives_after_another_was_chosen_is_dropped() {
    // Given: the second page was asked for.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let all = app.pipelines.clone();
    app.update(loaded(project, page(&all[..5], 1, Some(2))));
    press(&mut app, ']');

    // When: a late answer for the first page arrives.
    app.update(loaded(project, page(&all[..5], 1, Some(2))));

    // Then: it is not shown.
    assert!(app.pipelines.is_empty());
}

#[test]
fn every_five_seconds_the_page_and_the_jobs_in_view_are_asked_for_again() {
    // Given: the pipelines pane, with the newest pipeline's jobs shown.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let pipeline = app.pipeline().unwrap().id;

    // When: just under five seconds pass, and then the rest.
    let early = ticks(&mut app, REFRESH_TICKS - 1);
    let due = ticks(&mut app, 1);

    // Then: the page and the jobs are asked for when five seconds are up, and not before.
    assert_eq!(pipeline_requests(&early), []);
    assert_eq!(pipeline_requests(&due), [(project, 1)]);
    assert_eq!(job_requests(&due), [pipeline]);
}

#[test]
fn a_refresh_waits_while_the_one_before_is_unanswered() {
    // Given: a refresh went out, and GitLab has not answered.
    let mut app = on_pipelines();
    let first = ticks(&mut app, REFRESH_TICKS);
    assert_eq!(pipeline_requests(&first).len(), 1);

    // When: two more refresh periods pass.
    let later = ticks(&mut app, REFRESH_TICKS * 2);

    // Then: no second request piles on top of the first.
    assert_eq!(pipeline_requests(&later), []);
    assert_eq!(job_requests(&later), []);
}

#[test]
fn a_refresh_keeps_the_cursor_on_the_pipeline_it_was_on() {
    // Given: the cursor on the second pipeline, with its jobs shown.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let all = app.pipelines.clone();
    press(&mut app, 'j');
    ticks(&mut app, 3);
    let jobs = jobs_of(&app);
    let current = app.pipeline().unwrap().id;
    app.update(Msg::JobsLoaded {
        pipeline: current,
        jobs: jobs.clone(),
    });

    // When: a refresh arrives with a new pipeline on top.
    let mut newest = all[0].clone();
    newest.id = all[0].id + 1_000;
    let refreshed = [vec![newest], all.clone()].concat();
    let commands = app.update(loaded(project, page(&refreshed, 1, None)));

    // Then: the cursor is still on its pipeline, which moved down a row, and its jobs stay.
    assert_eq!(app.pipeline().unwrap().id, current);
    assert_eq!(app.cursor(lazylab::tui::app::Pane::Pipelines), 2);
    assert_eq!(app.jobs, jobs);
    assert_eq!(job_requests(&commands), []);
}

#[test]
fn a_refresh_without_the_selected_pipeline_moves_to_the_same_row_and_loads_its_jobs() {
    // Given: the cursor on the second pipeline.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let all = app.pipelines.clone();
    press(&mut app, 'j');
    ticks(&mut app, 3);

    // When: a refresh arrives without it.
    let without: Vec<Pipeline> = [&all[..1], &all[2..]].concat();
    let commands = app.update(loaded(project, page(&without, 1, None)));

    // Then: the cursor stays on the second row, and that pipeline's jobs are asked for.
    assert_eq!(app.pipeline().unwrap().id, all[2].id);
    assert_eq!(job_requests(&commands), [all[2].id]);
}

#[test]
fn refreshed_jobs_keep_the_cursor_on_the_job_it_was_on() {
    // Given: the jobs pane, with the cursor on the third job.
    let mut app = on_pipelines();
    press(&mut app, '3');
    press(&mut app, 'j');
    press(&mut app, 'j');
    let current = app.jobs[2].id;
    let pipeline = app.pipeline().unwrap().id;

    // When: the jobs come back with the first one gone.
    let jobs = app.jobs[1..].to_vec();
    app.update(Msg::JobsLoaded { pipeline, jobs });

    // Then: the cursor is still on its job, now one row up.
    assert_eq!(
        app.jobs[app.cursor(lazylab::tui::app::Pane::Jobs)].id,
        current
    );
}

#[test]
fn the_first_page_sets_the_project_status_from_its_newest_pipeline() {
    // Given: the pipelines pane of the first project.
    let mut app = on_pipelines();
    let project = app.project().unwrap().id;
    let mut all = app.pipelines.clone();

    // When: a refresh shows its newest pipeline failed.
    all[0].status = Status::Failed;
    app.update(loaded(project, page(&all, 1, None)));

    // Then: the project's glyph follows.
    assert_eq!(
        app.statuses.get(&project),
        Some(&ProjectStatus::Pipeline(Status::Failed))
    );
}
