use lazylab::gitlab::{Bridge, Job, Status};
use lazylab::tui::app::{App, Cmd, Focus, Msg, Pane};
use lazylab::tui::demo;
use lazylab::tui::jobs::JobRow;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use serde_json::json;
use time::OffsetDateTime;

const PIPELINE_PROJECT: u64 = 101;

fn job(id: u64, stage: &str, name: &str, status: &str) -> Job {
    serde_json::from_value(json!({"id": id, "name": name, "stage": stage, "status": status}))
        .unwrap()
}

fn bridge(id: u64, stage: &str, downstream: Option<(u64, Option<u64>, &str)>) -> Bridge {
    let downstream = downstream
        .map(|(id, project, status)| json!({"id": id, "project_id": project, "status": status}));
    serde_json::from_value(json!({
        "id": id, "name": "docs", "stage": stage, "status": "success", "ref": "main",
        "downstream_pipeline": downstream,
    }))
    .unwrap()
}

/// The demo app on the jobs pane, with its newest pipeline's jobs replaced by `jobs` and
/// `bridges`.
fn showing(jobs: Vec<Job>, bridges: Vec<Bridge>) -> App {
    let mut app = App::new(30);
    let mut commands = app.start();
    while let Some(command) = commands.pop() {
        if let Some(msg) = demo::answer(command, OffsetDateTime::now_utc()) {
            commands.extend(app.update(msg));
        }
    }
    assert_eq!(app.project().unwrap().id, PIPELINE_PROJECT);
    let pipeline = app.pipeline().unwrap().id;
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs,
        bridges,
    });
    press(&mut app, KeyCode::Char('3'));
    app
}

fn press(app: &mut App, code: KeyCode) -> Vec<Cmd> {
    app.update(Msg::Key(KeyEvent::new(code, KeyModifiers::NONE)))
}

/// Each row as a short label: a job by its name, a group by its name and count, a bridge by
/// its id, and a child pipeline that is still loading by its id.
fn labels(app: &App) -> Vec<String> {
    app.job_rows()
        .iter()
        .map(|row| match row {
            JobRow::Job { job, depth, .. } => {
                format!("{}{}", "  ".repeat(*depth as usize), job.name)
            }
            JobRow::Group { name, count, .. } => format!("{name} [{count}]"),
            JobRow::Bridge { bridge, .. } => format!("bridge {}", bridge.id),
            JobRow::Waiting { pipeline } => format!("  waiting {pipeline}"),
        })
        .collect()
}

fn child_requests(commands: &[Cmd]) -> Vec<(u64, u64)> {
    commands
        .iter()
        .filter_map(|c| match c {
            Cmd::LoadChildJobs { project, pipeline } => Some((*project, *pipeline)),
            _ => None,
        })
        .collect()
}

fn go_to(app: &mut App, label: &str) {
    let row = labels(app)
        .iter()
        .position(|l| l == label)
        .expect("the row");
    press(app, KeyCode::Char('g'));
    for _ in 0..row {
        press(app, KeyCode::Char('j'));
    }
}

#[test]
fn jobs_and_bridges_follow_the_order_of_their_stages() {
    // Given: jobs and a bridge whose stages first appear in the order build, test, deploy.
    let jobs = vec![
        job(1, "build", "compile", "success"),
        job(2, "test", "unit", "success"),
        job(3, "build", "image", "success"),
        job(5, "deploy", "staging", "manual"),
    ];
    let bridges = vec![bridge(4, "test", None)];

    // When: they are shown.
    let app = showing(jobs, bridges);

    // Then: each stage's jobs and bridges sit together, in stage order.
    assert_eq!(
        labels(&app),
        ["compile", "image", "unit", "bridge 4", "staging"]
    );
}

#[test]
fn matrix_jobs_fold_into_one_row_with_their_most_urgent_status() {
    // Given: two matrix jobs of one test, one of them failed.
    let jobs = vec![
        job(1, "test", "lint", "success"),
        job(2, "test", "unit: [go, 1.21]", "success"),
        job(3, "test", "unit: [go, 1.22]", "failed"),
    ];
    let mut app = showing(jobs, Vec::new());

    // Then: they show as one folded row, which carries the failure.
    assert_eq!(labels(&app), ["lint", "unit [2]"]);
    let JobRow::Group { status, .. } = &app.job_rows()[1] else {
        panic!("a group row");
    };
    assert_eq!(*status, Status::Failed);

    // When: enter is pressed on it.
    go_to(&mut app, "unit [2]");
    press(&mut app, KeyCode::Enter);

    // Then: it opens on each job, and the focus stays on the list.
    assert_eq!(
        labels(&app),
        [
            "lint",
            "unit [2]",
            "  unit: [go, 1.21]",
            "  unit: [go, 1.22]"
        ]
    );
    assert_eq!(app.focus, Focus::Sidebar);

    // When: enter is pressed on it again.
    press(&mut app, KeyCode::Enter);

    // Then: it folds.
    assert_eq!(labels(&app), ["lint", "unit [2]"]);
}

#[test]
fn enter_on_a_bridge_loads_its_child_pipeline_once_from_the_downstream_project() {
    // Given: a bridge whose child pipeline 900 runs in project 77.
    let jobs = vec![job(1, "build", "compile", "success")];
    let mut app = showing(
        jobs,
        vec![bridge(4, "deploy", Some((900, Some(77), "running")))],
    );
    go_to(&mut app, "bridge 4");

    // When: enter is pressed on it.
    let opened = press(&mut app, KeyCode::Enter);

    // Then: the child pipeline's jobs are asked for in project 77, and a row waits for them.
    assert_eq!(child_requests(&opened), [(77, 900)]);
    assert_eq!(labels(&app), ["compile", "bridge 4", "  waiting 900"]);

    // When: they arrive.
    app.update(Msg::ChildJobsLoaded {
        pipeline: 900,
        jobs: vec![job(901, "test", "e2e", "running")],
    });

    // Then: they show under the bridge, in their own project.
    assert_eq!(labels(&app), ["compile", "bridge 4", "  e2e"]);
    let JobRow::Job { project, .. } = &app.job_rows()[2] else {
        panic!("a job row");
    };
    assert_eq!(*project, 77);

    // When: the bridge is folded and opened again.
    press(&mut app, KeyCode::Enter);
    let again = press(&mut app, KeyCode::Enter);

    // Then: the jobs it has are shown at once, and not asked for again.
    assert!(child_requests(&again).is_empty());
    assert_eq!(labels(&app), ["compile", "bridge 4", "  e2e"]);
}

#[test]
fn a_child_pipeline_without_a_project_runs_in_the_bridge_s_project() {
    // Given: a bridge whose child pipeline names no project.
    let mut app = showing(
        Vec::new(),
        vec![bridge(4, "deploy", Some((900, None, "running")))],
    );

    // When: enter is pressed on it.
    let opened = press(&mut app, KeyCode::Enter);

    // Then: the child pipeline is asked for in the pipeline's own project.
    assert_eq!(child_requests(&opened), [(PIPELINE_PROJECT, 900)]);
}

#[test]
fn enter_on_a_job_or_on_a_bridge_that_triggered_nothing_opens_the_detail_pane() {
    for (jobs, bridges) in [
        (vec![job(1, "build", "compile", "success")], Vec::new()),
        (Vec::new(), vec![bridge(4, "deploy", None)]),
    ] {
        // Given: the jobs pane on that row.
        let mut app = showing(jobs, bridges);

        // When: enter is pressed.
        let commands = press(&mut app, KeyCode::Enter);

        // Then: the detail pane opens, and nothing is asked for.
        assert_eq!(app.focus, Focus::Detail);
        assert!(child_requests(&commands).is_empty());
    }
}

#[test]
fn a_refresh_keeps_the_cursor_on_its_row_and_reloads_open_child_pipelines() {
    // Given: an open bridge with its child pipeline shown, and the cursor on its child job.
    let mut app = showing(
        vec![job(1, "build", "compile", "success")],
        vec![bridge(4, "deploy", Some((900, Some(77), "running")))],
    );
    go_to(&mut app, "bridge 4");
    press(&mut app, KeyCode::Enter);
    app.update(Msg::ChildJobsLoaded {
        pipeline: 900,
        jobs: vec![job(901, "test", "e2e", "running")],
    });
    go_to(&mut app, "  e2e");

    // When: five seconds pass.
    let refresh: Vec<Cmd> = (0..50).flat_map(|_| app.update(Msg::Tick)).collect();

    // Then: the open child pipeline is asked for again.
    assert_eq!(child_requests(&refresh), [(77, 900)]);

    // When: the jobs come back with a retried job on top.
    let pipeline = app.pipeline().unwrap().id;
    app.update(Msg::JobsLoaded {
        pipeline,
        jobs: vec![
            job(1, "build", "compile", "success"),
            job(2, "build", "compile", "running"),
        ],
        bridges: vec![bridge(4, "deploy", Some((900, Some(77), "running")))],
    });

    // Then: the bridge stays open, and the cursor is still on the child job.
    assert_eq!(labels(&app), ["compile", "compile", "bridge 4", "  e2e"]);
    assert_eq!(app.cursor(Pane::Jobs), 3);
    assert_eq!(app.job().unwrap().id, 901);
}
