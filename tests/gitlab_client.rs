mod common;

use common::{TOKEN, client_for};
use lazylab::gitlab::{ErrorKind, Page};
use wiremock::matchers::{header, method, path, query_param};
use wiremock::{Mock, MockServer, ResponseTemplate};

fn project_page(body: &str) -> ResponseTemplate {
    ResponseTemplate::new(200)
        .insert_header("Content-Type", "application/json")
        .insert_header("X-Page", "2")
        .insert_header("X-Next-Page", "3")
        .insert_header("X-Total-Pages", "6")
        .set_body_string(body)
}

#[tokio::test]
async fn projects_are_listed_with_the_go_query_and_the_token_in_a_header() {
    // Given: GitLab answers the query the Go release sends, only with the token header.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects"))
        .and(header("PRIVATE-TOKEN", TOKEN))
        .and(query_param("membership", "true"))
        .and(query_param("order_by", "last_activity_at"))
        .and(query_param("sort", "desc"))
        .and(query_param("simple", "true"))
        .and(query_param("page", "2"))
        .and(query_param("per_page", "25"))
        .respond_with(
            project_page(
                r#"[{"id":7,"name":"api","path_with_namespace":"acme/api","description":null,
                "web_url":"https://gitlab.example/acme/api","default_branch":"main",
                "star_count":3,"last_activity_at":"2026-10-13T10:00:00.000Z"}]"#,
            )
            .insert_header("X-Total", "137"),
        )
        .expect(1)
        .mount(&server)
        .await;

    // When: the second page of 25 is listed.
    let page = client_for(&server.uri())
        .list_projects(2, 25)
        .await
        .unwrap();

    // Then: the project and the place of the page in the collection come back.
    assert_eq!(page.items.len(), 1);
    let project = &page.items[0];
    assert_eq!(
        (project.id, project.path_with_namespace.as_str()),
        (7, "acme/api")
    );
    assert_eq!(project.default_branch.as_deref(), Some("main"));
    assert_eq!(
        project.last_activity_at.map(|t| t.unix_timestamp()),
        Some(1_791_885_600)
    );
    assert_eq!(
        (
            page.page,
            page.next_page,
            page.total_pages,
            page.total_items
        ),
        (2, Some(3), Some(6), Some(137))
    );
    let requests = server.received_requests().await.unwrap();
    assert!(
        !requests[0].url.as_str().contains(TOKEN),
        "token in {}",
        requests[0].url
    );
}

#[tokio::test]
async fn a_withheld_total_reads_as_unknown_and_keeps_the_page_count() {
    // Given: GitLab answers without X-Total, as it does past ten thousand items.
    let server = MockServer::start().await;
    Mock::given(path("/api/v4/projects"))
        .respond_with(project_page(
            r#"[{"id":1,"name":"a","path_with_namespace":"t/a"}]"#,
        ))
        .mount(&server)
        .await;
    // When: a page is listed.
    let page: Page<_> = client_for(&server.uri())
        .list_projects(2, 25)
        .await
        .unwrap();
    // Then: the total is unknown, not zero, and the page count stays.
    assert_eq!((page.total_items, page.total_pages), (None, Some(6)));
}

#[tokio::test]
async fn the_api_path_follows_a_sub_path_install_and_is_not_doubled() {
    // Given: a GitLab served under /gitlab.
    let server = MockServer::start().await;
    Mock::given(path("/gitlab/api/v4/projects"))
        .respond_with(project_page("[]"))
        .expect(2)
        .mount(&server)
        .await;
    // When: the host is given with a trailing slash, and with the API path already on it.
    for host in [
        format!("{}/gitlab/", server.uri()),
        format!("{}/gitlab/api/v4", server.uri()),
    ] {
        client_for(&host).list_projects(1, 30).await.unwrap();
    }
    // Then: both reach /gitlab/api/v4/projects, which the mock's expect(2) checks on drop.
}

#[tokio::test]
async fn each_refusal_has_its_kind_and_exit_code_and_never_shows_the_token() {
    // Given: GitLab refusing in each way the exit codes tell apart.
    let cases = [
        (401, ErrorKind::Unauthorized, 3),
        (403, ErrorKind::Forbidden, 3),
        (404, ErrorKind::NotFound, 1),
        (429, ErrorKind::RateLimited, 2),
        (503, ErrorKind::Server, 2),
        (422, ErrorKind::Rejected, 1),
    ];
    for (status, kind, exit_code) in cases {
        let server = MockServer::start().await;
        Mock::given(path("/api/v4/projects"))
            .respond_with(
                ResponseTemplate::new(status)
                    .set_body_string(format!(r#"{{"message":"refused {status} for {TOKEN}"}}"#)),
            )
            .mount(&server)
            .await;
        // When: a page is listed.
        let error = client_for(&server.uri())
            .list_projects(1, 30)
            .await
            .unwrap_err();
        // Then: the kind and exit code match, and the text keeps GitLab's message without the token.
        let text = error.to_string();
        assert_eq!(
            (error.kind, error.exit_code()),
            (kind, exit_code),
            "{status}: {text}"
        );
        assert!(text.contains(&format!("refused {status}")), "{text}");
        assert!(!text.contains(TOKEN), "{text}");
    }
}

#[tokio::test]
async fn no_answer_at_all_is_a_network_failure() {
    // Given: a host where nothing listens. A dropped MockServer goes back to wiremock's pool
    // and keeps answering, so the port comes from a listener that is closed again.
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let host = format!("http://{}", listener.local_addr().unwrap());
    drop(listener);
    // When: a page is listed.
    let error = client_for(&host).list_projects(1, 30).await.unwrap_err();
    // Then: it is a network failure with its own exit code.
    assert_eq!(
        (error.kind, error.exit_code()),
        (ErrorKind::Network, 4),
        "{error}"
    );
}

#[tokio::test]
async fn a_body_that_is_not_a_project_list_is_a_decode_failure() {
    let server = MockServer::start().await;
    Mock::given(path("/api/v4/projects"))
        .respond_with(project_page(r#"{"not":"a list"}"#))
        .mount(&server)
        .await;
    let error = client_for(&server.uri())
        .list_projects(1, 30)
        .await
        .unwrap_err();
    assert_eq!(error.kind, ErrorKind::Decode, "{error}");
}

#[tokio::test]
async fn a_redirect_to_another_host_does_not_carry_the_token() {
    // Given: GitLab redirects the request to a different host, which records what it receives.
    let elsewhere = MockServer::start().await;
    Mock::given(path("/api/v4/projects"))
        .respond_with(project_page("[]"))
        .mount(&elsewhere)
        .await;
    let gitlab = MockServer::start().await;
    Mock::given(path("/api/v4/projects"))
        .respond_with(
            ResponseTemplate::new(302)
                .insert_header("Location", format!("{}/api/v4/projects", elsewhere.uri())),
        )
        .mount(&gitlab)
        .await;

    // When: a page is listed.
    let _ = client_for(&gitlab.uri()).list_projects(1, 30).await;

    // Then: the other host never sees the token.
    let received = elsewhere.received_requests().await.unwrap();
    let leaked = received
        .iter()
        .any(|r| r.headers.get("PRIVATE-TOKEN").is_some());
    assert!(
        !leaked,
        "the token followed the redirect to {}",
        elsewhere.uri()
    );
}
