mod common;

use common::client_for;
use lazylab::gitlab::{ErrorKind, Status, Variable};
use serde_json::{Value, json};
use wiremock::matchers::{body_json, body_string, method, path};
use wiremock::{Mock, MockServer, ResponseTemplate};

fn created(body: Value) -> ResponseTemplate {
    ResponseTemplate::new(201).set_body_json(body)
}

fn refused(status: u16, body: Value) -> ResponseTemplate {
    ResponseTemplate::new(status).set_body_json(body)
}

fn pipeline(id: u64, status: &str) -> Value {
    json!({"id": id, "status": status, "ref": "main", "sha": "abc123"})
}

fn job(id: u64, status: &str) -> Value {
    json!({"id": id, "name": "deploy", "stage": "deploy", "status": status})
}

fn variable(key: &str, value: &str) -> Variable {
    Variable {
        key: key.into(),
        value: value.into(),
    }
}

#[tokio::test]
async fn a_new_pipeline_carries_its_ref_and_each_variable_in_order() {
    // Given: GitLab creates pipeline 101 when the ref and both variables arrive as env vars.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .and(body_json(json!({
            "ref": "main",
            "variables": [
                {"key": "DEPLOY_ENV", "value": "staging", "variable_type": "env_var"},
                {"key": "DRY_RUN", "value": "true", "variable_type": "env_var"},
            ],
        })))
        .respond_with(created(pipeline(101, "created")))
        .expect(1)
        .mount(&server)
        .await;

    // When: a pipeline is started on main with the two variables.
    let variables = [
        variable("DEPLOY_ENV", "staging"),
        variable("DRY_RUN", "true"),
    ];
    let started = client_for(&server.uri())
        .create_pipeline(1, "main", &variables)
        .await
        .unwrap();

    // Then: the new pipeline comes back.
    assert_eq!((started.id, started.status), (101, Status::Created));
}

#[tokio::test]
async fn a_new_pipeline_with_no_variables_sends_the_ref_alone() {
    // Given: GitLab creates a pipeline only for a body that names the ref and nothing else.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .and(body_json(json!({"ref": "main"})))
        .respond_with(created(pipeline(101, "created")))
        .expect(1)
        .mount(&server)
        .await;

    // When: a pipeline is started with no variables.
    let started = client_for(&server.uri())
        .create_pipeline(1, "main", &[])
        .await
        .unwrap();

    // Then: GitLab accepted the body.
    assert_eq!(started.id, 101);
}

#[tokio::test]
async fn a_retry_returns_the_retried_pipeline_and_starts_no_other() {
    // Given: GitLab retries pipeline 100.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipelines/100/retry"))
        .respond_with(created(pipeline(100, "running")))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .respond_with(created(pipeline(101, "created")))
        .expect(0)
        .mount(&server)
        .await;

    // When: pipeline 100 is retried.
    let retried = client_for(&server.uri())
        .retry_pipeline(1, 100, "main")
        .await
        .unwrap();

    // Then: the same pipeline runs again.
    assert_eq!((retried.id, retried.status), (100, Status::Running));
}

#[tokio::test]
async fn a_retry_with_nothing_to_retry_starts_a_new_pipeline_on_the_same_ref() {
    // Given: GitLab answers the retry with a 400, and creates pipeline 101 on main.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipelines/100/retry"))
        .respond_with(refused(400, json!({"message": "nothing to retry"})))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .and(body_json(json!({"ref": "release"})))
        .respond_with(created(pipeline(101, "created")))
        .expect(1)
        .mount(&server)
        .await;

    // When: pipeline 100 on release is retried.
    let started = client_for(&server.uri())
        .retry_pipeline(1, 100, "release")
        .await
        .unwrap();

    // Then: the new pipeline comes back in place of the refusal.
    assert_eq!(started.id, 101);
}

#[tokio::test]
async fn a_failed_new_pipeline_after_a_failed_retry_reports_both_reasons() {
    // Given: GitLab refuses the retry with a 400 and the new pipeline with a 403.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipelines/100/retry"))
        .respond_with(refused(400, json!({"message": "nothing to retry"})))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .respond_with(refused(403, json!({"message": "403 Forbidden"})))
        .mount(&server)
        .await;

    // When: pipeline 100 is retried.
    let error = client_for(&server.uri())
        .retry_pipeline(1, 100, "main")
        .await
        .unwrap_err();

    // Then: the error takes the kind of the last refusal and names both reasons.
    assert_eq!(error.kind, ErrorKind::Forbidden);
    let text = error.to_string();
    assert!(text.contains("nothing to retry"), "{text}");
    assert!(text.contains("403 Forbidden"), "{text}");
}

#[tokio::test]
async fn a_retry_refused_for_another_reason_starts_no_new_pipeline() {
    // Given: GitLab refuses the retry with a 403.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipelines/100/retry"))
        .respond_with(refused(403, json!({"message": "403 Forbidden"})))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .respond_with(created(pipeline(101, "created")))
        .expect(0)
        .mount(&server)
        .await;

    // When: pipeline 100 is retried.
    let error = client_for(&server.uri())
        .retry_pipeline(1, 100, "main")
        .await
        .unwrap_err();

    // Then: the refusal comes back as it is.
    assert_eq!(error.kind, ErrorKind::Forbidden);
}

#[tokio::test]
async fn a_pipeline_and_a_job_are_canceled() {
    // Given: GitLab cancels pipeline 100 and job 7.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipelines/100/cancel"))
        .respond_with(created(pipeline(100, "canceling")))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/7/cancel"))
        .respond_with(created(job(7, "canceled")))
        .expect(1)
        .mount(&server)
        .await;
    let client = client_for(&server.uri());

    // When: both are canceled.
    let pipeline = client.cancel_pipeline(1, 100).await;
    let job = client.cancel_job(1, 7).await;

    // Then: both requests succeed.
    assert!(pipeline.is_ok(), "{pipeline:?}");
    assert!(job.is_ok(), "{job:?}");
}

#[tokio::test]
async fn a_canceled_job_that_has_already_finished_reports_the_refusal() {
    // Given: GitLab refuses to cancel a finished job.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/7/cancel"))
        .respond_with(refused(403, json!({"message": "403 Forbidden"})))
        .mount(&server)
        .await;

    // When: job 7 is canceled.
    let error = client_for(&server.uri())
        .cancel_job(1, 7)
        .await
        .unwrap_err();

    // Then: the caller learns it did not happen.
    assert_eq!(error.kind, ErrorKind::Forbidden);
}

#[tokio::test]
async fn a_retried_job_comes_back_as_the_new_job() {
    // Given: GitLab retries job 7 as job 8.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/7/retry"))
        .respond_with(created(job(8, "pending")))
        .expect(1)
        .mount(&server)
        .await;

    // When: job 7 is retried.
    let retried = client_for(&server.uri()).retry_job(1, 7).await.unwrap();

    // Then: the new job is returned.
    assert_eq!((retried.id, retried.status), (8, Status::Pending));
}

#[tokio::test]
async fn a_played_job_carries_each_variable_in_order() {
    // Given: GitLab plays job 7 when both variables arrive as env vars.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/7/play"))
        .and(body_json(json!({
            "job_variables_attributes": [
                {"key": "DEPLOY_ENV", "value": "production", "variable_type": "env_var"},
                {"key": "CONFIRM", "value": "yes", "variable_type": "env_var"},
            ],
        })))
        .respond_with(created(job(7, "pending")))
        .expect(1)
        .mount(&server)
        .await;

    // When: job 7 is played with the two variables.
    let variables = [
        variable("DEPLOY_ENV", "production"),
        variable("CONFIRM", "yes"),
    ];
    let played = client_for(&server.uri())
        .play_job(1, 7, &variables)
        .await
        .unwrap();

    // Then: the job is queued.
    assert_eq!((played.id, played.status), (7, Status::Pending));
}

#[tokio::test]
async fn a_played_job_with_no_variables_sends_no_body() {
    // Given: GitLab plays job 7 only for an empty body.
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/7/play"))
        .and(body_string(""))
        .respond_with(created(job(7, "pending")))
        .expect(1)
        .mount(&server)
        .await;

    // When: job 7 is played with no variables.
    let played = client_for(&server.uri()).play_job(1, 7, &[]).await;

    // Then: GitLab accepted the request.
    assert!(played.is_ok(), "{played:?}");
}

#[tokio::test]
async fn a_job_in_a_child_pipeline_is_retried_in_the_child_project() {
    // Given: a bridge in project 1 triggered pipeline 500 in project 42, whose job 9 failed.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/pipelines/100/bridges"))
        .respond_with(ResponseTemplate::new(200).set_body_json(json!([{
            "id": 3, "name": "trigger", "stage": "deploy", "status": "failed", "ref": "main",
            "downstream_pipeline": {"id": 500, "project_id": 42, "status": "failed"},
        }])))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/42/jobs/9/retry"))
        .respond_with(created(job(10, "pending")))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/9/retry"))
        .respond_with(refused(404, json!({"message": "404 Job Not Found"})))
        .expect(0)
        .mount(&server)
        .await;
    let client = client_for(&server.uri());

    // When: job 9 is retried in the project the bridge names.
    let bridges = client.list_pipeline_bridges(1, 100).await.unwrap();
    let child = bridges[0].downstream_pipeline.as_ref().unwrap();
    let retried = client
        .retry_job(child.project_id.unwrap_or(1), 9)
        .await
        .unwrap();

    // Then: the retry reached project 42.
    assert_eq!(retried.id, 10);
}

#[tokio::test]
async fn a_variable_value_never_appears_in_the_error_text() {
    // Given: GitLab quotes a value in its refusal, as plain text and inside an object.
    // And: a second value is the start of the first.
    let secret = r#"hunter"2"#;
    let server = MockServer::start().await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/pipeline"))
        .respond_with(refused(
            400,
            json!({"message": format!("bad value {secret} end")}),
        ))
        .mount(&server)
        .await;
    Mock::given(method("POST"))
        .and(path("/api/v4/projects/1/jobs/7/play"))
        .respond_with(refused(
            400,
            json!({"message": {"variables": [format!("bad value {secret} end")]}}),
        ))
        .mount(&server)
        .await;
    let client = client_for(&server.uri());
    let variables = [variable("USER", "hunter"), variable("API_KEY", secret)];

    // When: a pipeline is started and a job is played with that value.
    let create = client
        .create_pipeline(1, "main", &variables)
        .await
        .unwrap_err();
    let play = client.play_job(1, 7, &variables).await.unwrap_err();

    // Then: neither error shows any part of the value, in its plain or its quoted form.
    let quoted = serde_json::to_string(secret).unwrap();
    let quoted = quoted.trim_matches('"');
    for error in [create, play] {
        for text in [error.to_string(), format!("{error:?}")] {
            assert!(!text.contains(secret), "{text}");
            assert!(!text.contains(quoted), "{text}");
            assert!(text.contains("bad value [REDACTED] end"), "{text}");
        }
    }
}

#[test]
fn a_variable_debug_line_hides_its_value() {
    // Given: a variable that holds a secret.
    let secret = variable("API_KEY", "hunter2");

    // When: it is formatted for a log line.
    let line = format!("{secret:?}");

    // Then: the key shows and the value does not.
    assert!(line.contains("API_KEY"), "{line}");
    assert!(!line.contains("hunter2"), "{line}");
}
