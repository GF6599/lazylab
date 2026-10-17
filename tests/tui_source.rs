mod common;

use std::fs;
use std::time::{Duration, Instant};

use common::client_for;
use lazylab::gitlab::{Project, Status};
use lazylab::store::Store;
use lazylab::tui::Source;
use lazylab::tui::app::{Action, Cmd, Msg};
use url::Url;
use wiremock::matchers::{method, path, query_param};
use wiremock::{Mock, MockServer, ResponseTemplate};

const PIPELINES: &str = include_str!("fixtures/pipelines.json");
const PIPELINE_JOBS: &str = include_str!("fixtures/pipeline_jobs.json");
const MERGE_REQUESTS: &str = include_str!("fixtures/merge_requests.json");
const BRIDGES: &str = include_str!("fixtures/bridges.json");

fn json_body(body: &str) -> ResponseTemplate {
    ResponseTemplate::new(200)
        .insert_header("Content-Type", "application/json")
        .set_body_string(body)
}

fn project(id: u64) -> Project {
    serde_json::from_value(serde_json::json!({
        "id": id,
        "name": "api",
        "path_with_namespace": "acme/api",
    }))
    .unwrap()
}

#[tokio::test]
async fn a_page_command_lists_that_page_at_that_size() {
    // Given: GitLab answers page 2 of 25 with one project.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects"))
        .and(query_param("page", "2"))
        .and(query_param("per_page", "25"))
        .respond_with(
            json_body(r#"[{"id":7,"name":"api","path_with_namespace":"acme/api"}]"#)
                .insert_header("X-Page", "2")
                .insert_header("X-Total", "26"),
        )
        .expect(1)
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);

    // When: the page is asked for, as part of load 4.
    let answer = source
        .answer(Cmd::LoadProjectPage {
            load: 4,
            page: 2,
            per_page: 25,
        })
        .await;

    // Then: the page comes back marked with its load.
    let Some(Msg::ProjectPageLoaded { load, page }) = answer else {
        panic!("a page, got {answer:?}");
    };
    assert_eq!(load, 4);
    assert_eq!(page.items.iter().map(|p| p.id).collect::<Vec<_>>(), [7]);
    assert_eq!(page.total_items, Some(26));
}

#[tokio::test]
async fn a_failed_call_answers_with_its_command_and_the_reason() {
    // Given: GitLab refuses the token.
    let server = MockServer::start().await;
    Mock::given(path("/api/v4/projects"))
        .respond_with(
            ResponseTemplate::new(401).set_body_string(r#"{"message":"401 Unauthorized"}"#),
        )
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);
    let command = Cmd::LoadProjectPage {
        load: 1,
        page: 1,
        per_page: 30,
    };

    // When: a page is asked for.
    let answer = source.answer(command.clone()).await;

    // Then: the answer names the command and the operation that failed.
    let Some(Msg::Failed { cmd, error }) = answer else {
        panic!("a failure, got {answer:?}");
    };
    assert_eq!(cmd, command);
    assert!(error.starts_with("list projects"), "{error}");
}

#[tokio::test]
async fn saved_projects_come_back_from_the_cache() {
    // Given: a live source with a store in an empty folder.
    let dir = tempfile::tempdir().unwrap();
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let store = Store::new(dir.path().to_path_buf(), &host);
    let source = Source::live(client_for("https://gitlab.example.com"), Some(store));

    // When: two projects are saved, then the cache is asked for.
    let saved = source
        .answer(Cmd::SaveProjects {
            projects: vec![project(1), project(2)],
            total: 5,
        })
        .await;
    let answer = source.answer(Cmd::LoadCache).await;

    // Then: the save answers nothing, and the cache holds both projects and the total.
    assert!(saved.is_none());
    let Some(Msg::CacheLoaded(Some(cached))) = answer else {
        panic!("a cache, got {answer:?}");
    };
    assert_eq!(cached.projects, [project(1), project(2)]);
    assert_eq!(cached.total, 5);
}

#[tokio::test]
async fn a_cache_that_cannot_be_read_or_kept_is_a_miss() {
    // Given: one source whose cache file is not JSON, and one with nowhere to keep a cache.
    let dir = tempfile::tempdir().unwrap();
    fs::write(
        dir.path().join("projects_https_gitlab_example_com.json"),
        "not json",
    )
    .unwrap();
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let broken = Source::live(
        client_for(host.as_str()),
        Some(Store::new(dir.path().to_path_buf(), &host)),
    );
    let none = Source::live(client_for(host.as_str()), None);

    for source in [broken, none] {
        // When: the cache is asked for.
        let answer = source.answer(Cmd::LoadCache).await;

        // Then: the answer is a miss, so the projects load from GitLab.
        assert!(matches!(answer, Some(Msg::CacheLoaded(None))), "{answer:?}");
    }
}

#[tokio::test]
async fn pipelines_jobs_and_open_merge_requests_come_from_gitlab() {
    // Given: GitLab with pipelines, jobs, bridges and open merge requests for project 1, and a
    // child pipeline in project 77.
    let server = MockServer::start().await;
    Mock::given(path("/api/v4/projects/1/pipelines"))
        .and(query_param("page", "1"))
        .respond_with(json_body(PIPELINES))
        .mount(&server)
        .await;
    Mock::given(path("/api/v4/projects/1/pipelines/100/jobs"))
        .respond_with(json_body(PIPELINE_JOBS))
        .mount(&server)
        .await;
    Mock::given(path("/api/v4/projects/1/pipelines/100/bridges"))
        .respond_with(json_body(BRIDGES))
        .mount(&server)
        .await;
    Mock::given(path("/api/v4/projects/77/pipelines/900/jobs"))
        .respond_with(json_body(PIPELINE_JOBS))
        .mount(&server)
        .await;
    Mock::given(path("/api/v4/projects/1/merge_requests"))
        .and(query_param("state", "opened"))
        .respond_with(json_body(MERGE_REQUESTS))
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);

    // When: each list is asked for.
    let pipelines = source
        .answer(Cmd::LoadPipelines {
            project: 1,
            page: 1,
        })
        .await;
    let jobs = source
        .answer(Cmd::LoadJobs {
            project: 1,
            pipeline: 100,
        })
        .await;
    let merge_requests = source.answer(Cmd::LoadMergeRequests { project: 1 }).await;
    let child = source
        .answer(Cmd::LoadChildJobs {
            project: 77,
            pipeline: 900,
        })
        .await;

    // Then: each comes back for the project or pipeline it was asked for.
    assert!(
        matches!(&pipelines, Some(Msg::PipelinesLoaded { project: 1, page }) if !page.items.is_empty()),
        "{pipelines:?}"
    );
    assert!(
        matches!(&jobs, Some(Msg::JobsLoaded { pipeline: 100, jobs, bridges }) if !jobs.is_empty() && !bridges.is_empty()),
        "{jobs:?}"
    );
    assert!(
        matches!(&child, Some(Msg::ChildJobsLoaded { pipeline: 900, jobs }) if !jobs.is_empty()),
        "{child:?}"
    );
    assert!(
        matches!(&merge_requests, Some(Msg::MergeRequestsLoaded { project: 1, merge_requests }) if !merge_requests.is_empty()),
        "{merge_requests:?}"
    );
}

#[tokio::test]
async fn saved_favorites_come_back_in_their_order() {
    // Given: a live source with a store in an empty folder, and one with no store.
    let dir = tempfile::tempdir().unwrap();
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let stored = Source::live(
        client_for(host.as_str()),
        Some(Store::new(dir.path().to_path_buf(), &host)),
    );
    let none = Source::live(client_for(host.as_str()), None);

    // When: favorites are saved to each, then asked for.
    let saved = stored.answer(Cmd::SaveFavorites(vec![3, 1])).await;
    none.answer(Cmd::SaveFavorites(vec![3, 1])).await;
    let kept = stored.answer(Cmd::LoadFavorites).await;
    let lost = none.answer(Cmd::LoadFavorites).await;

    // Then: the store keeps them in their order, and without one there are none.
    assert!(saved.is_none());
    assert!(
        matches!(&kept, Some(Msg::FavoritesLoaded(ids)) if ids == &[3, 1]),
        "{kept:?}"
    );
    assert!(
        matches!(&lost, Some(Msg::FavoritesLoaded(ids)) if ids.is_empty()),
        "{lost:?}"
    );
}

#[tokio::test]
async fn favorites_that_cannot_be_read_are_none() {
    // Given: a favorites file that is not JSON.
    let dir = tempfile::tempdir().unwrap();
    fs::write(
        dir.path().join("favorites_https_gitlab_example_com.json"),
        "not json",
    )
    .unwrap();
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let source = Source::live(
        client_for(host.as_str()),
        Some(Store::new(dir.path().to_path_buf(), &host)),
    );

    // When: the favorites are asked for.
    let answer = source.answer(Cmd::LoadFavorites).await;

    // Then: there are none, and nothing fails.
    assert!(
        matches!(&answer, Some(Msg::FavoritesLoaded(ids)) if ids.is_empty()),
        "{answer:?}"
    );
}

#[tokio::test]
async fn a_status_is_the_newest_pipeline_s_or_none_without_one() {
    // Given: project 1 with a running pipeline and project 2 with none.
    let server = MockServer::start().await;
    Mock::given(path("/api/v4/projects/1/pipelines"))
        .and(query_param("per_page", "1"))
        .respond_with(json_body(
            r#"[{"id":5,"status":"running","ref":"main","sha":"abc"}]"#,
        ))
        .mount(&server)
        .await;
    Mock::given(path("/api/v4/projects/2/pipelines"))
        .respond_with(json_body("[]"))
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);

    // When: both statuses are asked for.
    let running = source.answer(Cmd::LoadStatus { project: 1 }).await;
    let none = source.answer(Cmd::LoadStatus { project: 2 }).await;

    // Then: the first is running, and the second has no pipeline.
    assert!(
        matches!(
            running,
            Some(Msg::StatusLoaded {
                project: 1,
                status: Some(Status::Running)
            })
        ),
        "{running:?}"
    );
    assert!(
        matches!(
            none,
            Some(Msg::StatusLoaded {
                project: 2,
                status: None
            })
        ),
        "{none:?}"
    );
}

#[tokio::test]
async fn no_more_than_five_statuses_are_asked_for_at_once() {
    // Given: GitLab answers each status after 200 ms.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .respond_with(json_body("[]").set_delay(Duration::from_millis(200)))
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);

    // When: ten statuses are asked for together.
    let started = Instant::now();
    let answers = join_all((1..=10).map(|project| {
        let source = source.clone();
        async move { source.answer(Cmd::LoadStatus { project }).await }
    }))
    .await;

    // Then: all ten answer, in two rounds of five, never in one round of ten.
    assert_eq!(answers.iter().filter(|a| a.is_some()).count(), 10);
    assert!(
        started.elapsed() >= Duration::from_millis(390),
        "{:?}",
        started.elapsed()
    );
}

async fn join_all<F: std::future::Future + Send + 'static>(
    futures: impl Iterator<Item = F>,
) -> Vec<F::Output>
where
    F::Output: Send + 'static,
{
    let handles: Vec<_> = futures.map(tokio::spawn).collect();
    let mut outputs = Vec::new();
    for handle in handles {
        outputs.push(handle.await.unwrap());
    }
    outputs
}

#[tokio::test]
async fn a_job_log_is_read_from_the_job_trace() {
    // Given: GitLab with a trace for job 5 of project 1.
    let server = MockServer::start().await;
    Mock::given(path("/api/v4/projects/1/jobs/5/trace"))
        .respond_with(ResponseTemplate::new(200).set_body_string("step 1\nstep 2\n"))
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);

    // When: the log is asked for.
    let answer = source.answer(Cmd::LoadLog { project: 1, job: 5 }).await;

    // Then: the trace comes back for that job.
    assert!(
        matches!(&answer, Some(Msg::LogLoaded { job: 5, log }) if log == "step 1\nstep 2\n"),
        "{answer:?}"
    );
}

#[tokio::test]
async fn a_retry_answers_with_the_pipeline_it_started_and_a_cancel_goes_to_its_project() {
    // Given: GitLab retries pipeline 70 of project 1 as pipeline 71, and cancels job 901 of
    // project 77.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipelines/70/retry"))
        .respond_with(json_body(
            r#"{"id":71,"status":"pending","ref":"main","sha":"abc"}"#,
        ))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/77/jobs/901/cancel"))
        .respond_with(json_body(
            r#"{"id":901,"name":"e2e","stage":"test","status":"canceled"}"#,
        ))
        .expect(1)
        .mount(&server)
        .await;
    let source = Source::live(client_for(&server.uri()), None);
    let retry = Action::RetryPipeline {
        project: 1,
        pipeline: 70,
        git_ref: "main".into(),
    };
    let cancel = Action::CancelJob {
        project: 77,
        job: 901,
        name: "e2e".into(),
    };

    // When: both are sent.
    let retried = source.answer(Cmd::Act(retry.clone())).await;
    let canceled = source.answer(Cmd::Act(cancel.clone())).await;

    // Then: the retry names the pipeline it started, and the cancel answers for its job.
    assert_eq!(
        retried,
        Some(Msg::Acted {
            action: retry,
            started: Some(71)
        })
    );
    assert_eq!(
        canceled,
        Some(Msg::Acted {
            action: cancel,
            started: None
        })
    );
}
