mod common;

use common::client_for;
use lazylab::gitlab::{ErrorKind, MAX_JOB_LOG_BYTES, Stage, Status};
use serde_json::json;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use wiremock::matchers::{method, path, query_param, query_param_is_missing};
use wiremock::{Mock, MockServer, ResponseTemplate};

const PIPELINES: &str = include_str!("fixtures/pipelines.json");
const PIPELINE_JOBS: &str = include_str!("fixtures/pipeline_jobs.json");
const BRIDGES: &str = include_str!("fixtures/bridges.json");
const BRIDGES_PAGE_2: &str = include_str!("fixtures/bridges_page2.json");

fn json_body(body: impl Into<String>) -> ResponseTemplate {
    ResponseTemplate::new(200)
        .insert_header("Content-Type", "application/json")
        .set_body_string(body.into())
}

fn at(text: &str) -> OffsetDateTime {
    OffsetDateTime::parse(text, &Rfc3339).unwrap()
}

fn stage(name: &str, status: Status) -> Stage {
    Stage {
        name: name.into(),
        status,
    }
}

#[tokio::test]
async fn pipelines_are_listed_newest_first_with_their_display_fields() {
    // Given: GitLab answers the newest-first query with two pipelines.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines"))
        .and(query_param("order_by", "updated_at"))
        .and(query_param("sort", "desc"))
        .and(query_param("page", "1"))
        .and(query_param("per_page", "25"))
        .respond_with(
            json_body(PIPELINES)
                .insert_header("X-Page", "1")
                .insert_header("X-Next-Page", "")
                .insert_header("X-Total-Pages", "1"),
        )
        .expect(1)
        .mount(&server)
        .await;

    // When: the first page of 25 is listed.
    let page = client_for(&server.uri())
        .list_pipelines(1, 1, 25)
        .await
        .unwrap();

    // Then: both pipelines keep the fields the pipelines pane shows.
    assert_eq!((page.page, page.next_page), (1, None));
    let [newest, older] = &page.items[..] else {
        panic!("two pipelines, got {:?}", page.items);
    };
    assert_eq!(
        (newest.id, newest.status, newest.git_ref.as_str()),
        (100, Status::Success, "main")
    );
    assert_eq!(newest.source.as_deref(), Some("push"));
    assert_eq!(newest.sha, "abc123");
    assert_eq!(newest.coverage, Some(85.5));
    assert_eq!(
        newest.user.as_ref().map(|u| u.name.as_str()),
        Some("Test User")
    );
    assert_eq!(newest.updated(), Some(at("2025-01-01T10:05:00Z")));
    assert_eq!((older.id, older.status), (99, Status::Failed));
    assert_eq!((older.coverage, older.user.as_ref()), (None, None));
}

#[tokio::test]
async fn a_project_with_no_pipelines_lists_an_empty_page() {
    // Given: GitLab answers with no pipelines.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines"))
        .respond_with(json_body("[]"))
        .mount(&server)
        .await;

    // When: the first page is listed.
    let page = client_for(&server.uri())
        .list_pipelines(1, 1, 25)
        .await
        .unwrap();

    // Then: the page is empty, and nothing is reported as a failure.
    assert!(page.items.is_empty());
}

#[tokio::test]
async fn one_pipeline_carries_its_start_time_and_falls_back_to_its_creation_time() {
    // Given: a running pipeline that GitLab has not updated since it was created.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100"))
        .respond_with(json_body(
            r#"{"id":100,"status":"running","ref":"main",
            "started_at":"2026-08-09T12:00:00Z","created_at":"2026-08-09T11:59:00Z"}"#,
        ))
        .mount(&server)
        .await;

    // When: the pipeline is read.
    let pipeline = client_for(&server.uri())
        .get_pipeline(1, 100)
        .await
        .unwrap();

    // Then: it carries the start time, and its last change is its creation.
    assert_eq!((pipeline.id, pipeline.status), (100, Status::Running));
    assert_eq!(pipeline.started_at, Some(at("2026-08-09T12:00:00Z")));
    assert_eq!(pipeline.updated(), Some(at("2026-08-09T11:59:00Z")));
}

#[tokio::test]
async fn the_latest_pipeline_on_a_ref_comes_with_its_stages() {
    // Given: GitLab answers the newest pipeline on main, then its jobs.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines"))
        .and(query_param("ref", "main"))
        .and(query_param("order_by", "updated_at"))
        .and(query_param("sort", "desc"))
        .and(query_param("per_page", "1"))
        .respond_with(json_body(PIPELINES))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/jobs"))
        .respond_with(json_body(PIPELINE_JOBS))
        .expect(1)
        .mount(&server)
        .await;

    // When: the latest pipeline on main is read.
    let latest = client_for(&server.uri())
        .latest_pipeline(1, Some("main"))
        .await
        .unwrap()
        .expect("a latest pipeline");

    // Then: it is the newest one, with one stage per job stage in order.
    assert_eq!(latest.pipeline.id, 100);
    assert_eq!(
        latest.stages,
        [
            stage("build", Status::Success),
            stage("test", Status::Success),
            stage("deploy", Status::Failed),
        ]
    );
}

#[tokio::test]
async fn a_project_with_no_pipelines_has_no_latest_pipeline() {
    // Given: GitLab answers with no pipelines on any ref.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines"))
        .and(query_param_is_missing("ref"))
        .respond_with(json_body("[]"))
        .expect(1)
        .mount(&server)
        .await;

    // When: the latest pipeline on any ref is read.
    let latest = client_for(&server.uri())
        .latest_pipeline(1, None)
        .await
        .unwrap();

    // Then: there is none, and no job list was asked for.
    assert_eq!(latest, None);
}

#[tokio::test]
async fn each_stage_shows_the_most_urgent_status_among_its_jobs() {
    // Given: jobs out of id order, where each stage holds a different contest.
    let contests = [
        (13, " ", "created"),
        (9, "failure", "success"),
        (1, "failure", "failed"),
        (2, "cancel", "pending"),
        (3, "cancel", "canceled"),
        (4, "manual", "success"),
        (5, "manual", "manual"),
        (6, "tie", "running"),
        (7, "tie", "preparing"),
        (8, "callback", "success"),
        (10, "callback", "waiting_for_callback"),
        (11, "quiet", "success"),
        (12, "quiet", "skipped"),
        (14, "novel", "a_status_from_a_newer_gitlab"),
    ];
    let jobs: Vec<_> = contests
        .iter()
        .map(|(id, stage, status)| json!({"id": id, "name": "job", "stage": stage, "status": status}))
        .collect();
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/jobs"))
        .respond_with(json_body(json!(jobs).to_string()))
        .mount(&server)
        .await;

    // When: the stages of the pipeline are read.
    let stages = client_for(&server.uri())
        .pipeline_stages(1, 100)
        .await
        .unwrap();

    // Then: each stage, in the order its first job ran, shows its most urgent status.
    assert_eq!(
        stages,
        [
            stage("failure", Status::Failed),
            stage("cancel", Status::Canceled),
            stage("manual", Status::Manual),
            stage("tie", Status::Running),
            stage("callback", Status::WaitingForCallback),
            stage("quiet", Status::Success),
            stage("(unknown stage)", Status::Created),
            stage("novel", Status::Unknown),
        ]
    );
}

#[tokio::test]
async fn jobs_are_listed_with_their_runner_artifacts_and_failure_reason() {
    // Given: GitLab answers the jobs of a pipeline, a hundred to a page.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/jobs"))
        .and(query_param("per_page", "100"))
        .respond_with(json_body(PIPELINE_JOBS))
        .expect(1)
        .mount(&server)
        .await;

    // When: the jobs are listed.
    let jobs = client_for(&server.uri())
        .list_pipeline_jobs(1, 100)
        .await
        .unwrap();

    // Then: each job keeps the details that explain a red pipeline.
    let [build, _, deploy] = &jobs[..] else {
        panic!("three jobs, got {jobs:?}");
    };
    assert_eq!(
        (build.id, build.name.as_str(), build.stage.as_str()),
        (1001, "build", "build")
    );
    assert_eq!(build.duration, Some(30.5));
    assert_eq!(
        build.runner.as_ref().and_then(|r| r.description.as_deref()),
        Some("shared-runner-1")
    );
    let artifacts: Vec<_> = build
        .artifacts
        .iter()
        .map(|a| a.filename.as_str())
        .collect();
    assert_eq!(artifacts, ["artifacts.zip"]);
    assert_eq!(build.artifacts_expire_at, Some(at("2025-02-01T10:00:00Z")));
    assert_eq!(deploy.status, Status::Failed);
    assert_eq!(deploy.failure_reason.as_deref(), Some("script_failure"));
}

#[tokio::test]
async fn a_pipeline_with_no_jobs_yet_has_no_jobs_and_no_stages() {
    // Given: a pipeline that GitLab has not scheduled any job for.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/jobs"))
        .respond_with(json_body("[]"))
        .mount(&server)
        .await;
    let client = client_for(&server.uri());

    // When: its jobs and its stages are read.
    let jobs = client.list_pipeline_jobs(1, 100).await.unwrap();
    let stages = client.pipeline_stages(1, 100).await.unwrap();

    // Then: both are empty, and nothing is reported as a failure.
    assert!(jobs.is_empty());
    assert!(stages.is_empty());
}

#[tokio::test]
async fn bridges_on_every_page_come_back_in_id_order() {
    // Given: GitLab splits three bridges over two pages.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/bridges"))
        .and(query_param("page", "1"))
        .and(query_param("per_page", "100"))
        .respond_with(json_body(BRIDGES).insert_header("X-Next-Page", "2"))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/bridges"))
        .and(query_param("page", "2"))
        .respond_with(json_body(BRIDGES_PAGE_2).insert_header("X-Next-Page", ""))
        .expect(1)
        .mount(&server)
        .await;

    // When: the bridges are listed.
    let bridges = client_for(&server.uri())
        .list_pipeline_bridges(1, 100)
        .await
        .unwrap();

    // Then: all three come back, and only a bridge that triggered has a downstream pipeline.
    let ids: Vec<_> = bridges.iter().map(|b| b.id).collect();
    assert_eq!(ids, [2001, 2002, 2003]);
    let downstream: Vec<_> = bridges
        .iter()
        .map(|b| b.downstream_pipeline.as_ref().map(|d| d.id))
        .collect();
    assert_eq!(downstream, [Some(200), None, Some(201)]);
    assert_eq!(bridges[1].git_ref, "main");
}

#[tokio::test]
async fn a_bridge_into_another_project_names_that_project() {
    // Given: a bridge whose downstream pipeline runs in project 42.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/bridges"))
        .respond_with(json_body(
            r#"[{"id":3001,"name":"trigger","stage":"deploy","status":"running","ref":"main",
            "downstream_pipeline":{"id":300,"project_id":42,"status":"running",
            "web_url":"https://gitlab.example/acme/child/-/pipelines/300"}}]"#,
        ))
        .mount(&server)
        .await;

    // When: the bridges are listed.
    let bridges = client_for(&server.uri())
        .list_pipeline_bridges(1, 100)
        .await
        .unwrap();

    // Then: the downstream pipeline names its own project, so its jobs are read from there.
    let downstream = bridges[0].downstream_pipeline.as_ref().unwrap();
    assert_eq!((downstream.id, downstream.project_id), (300, Some(42)));
    assert_eq!(downstream.status, Status::Running);
}

#[tokio::test]
async fn a_job_log_comes_back_as_written() {
    // Given: a job log that holds a byte which is not UTF-8.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/jobs/1001/trace"))
        .respond_with(
            ResponseTemplate::new(200)
                .insert_header("Content-Type", "text/plain")
                .set_body_bytes(b"Running build...\nBuild succeeded! \xff".to_vec()),
        )
        .mount(&server)
        .await;

    // When: the log is read.
    let log = client_for(&server.uri()).job_log(1, 1001).await.unwrap();

    // Then: the text is unchanged, and the stray byte shows as a replacement character.
    assert_eq!(log, "Running build...\nBuild succeeded! \u{FFFD}");
}

#[tokio::test]
async fn a_job_log_past_the_cap_is_refused_and_one_at_the_cap_is_read() {
    // Given: one log exactly at the cap and one a byte past it.
    let server = MockServer::start().await;
    for (job, size) in [(1, MAX_JOB_LOG_BYTES), (2, MAX_JOB_LOG_BYTES + 1)] {
        Mock::given(method("GET"))
            .and(path(format!("/api/v4/projects/1/jobs/{job}/trace")))
            .respond_with(ResponseTemplate::new(200).set_body_bytes(vec![b'x'; size]))
            .mount(&server)
            .await;
    }
    let client = client_for(&server.uri());

    // When: both logs are read.
    let at_cap = client.job_log(1, 1).await;
    let past_cap = client.job_log(1, 2).await;

    // Then: the log at the cap reads whole, and the larger one is refused as too large.
    assert_eq!(at_cap.unwrap().len(), MAX_JOB_LOG_BYTES);
    assert_eq!(past_cap.unwrap_err().kind, ErrorKind::TooLarge);
}

#[tokio::test]
async fn a_next_page_that_does_not_advance_stops_the_list() {
    // Given: a server that names the page it just served as the next page.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/bridges"))
        .respond_with(json_body(BRIDGES).insert_header("X-Next-Page", "1"))
        .up_to_n_times(1)
        .mount(&server)
        .await;

    // When: the bridges are listed.
    let error = client_for(&server.uri())
        .list_pipeline_bridges(1, 100)
        .await
        .unwrap_err();

    // Then: the list stops after one request and reports the answer as malformed.
    assert_eq!(error.kind, ErrorKind::Decode);
    assert_eq!(server.received_requests().await.unwrap().len(), 1);
}
