mod common;

use common::client_for;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use wiremock::matchers::{method, path, query_param, query_param_is_missing};
use wiremock::{Mock, MockServer, ResponseTemplate};

const BRANCHES: &str = include_str!("fixtures/branches.json");
const BRANCHES_PAGE_2: &str = include_str!("fixtures/branches_page2.json");
const COMMITS: &str = include_str!("fixtures/commits.json");

fn json_body(body: &str) -> ResponseTemplate {
    ResponseTemplate::new(200)
        .insert_header("Content-Type", "application/json")
        .set_body_string(body)
}

#[tokio::test]
async fn branches_are_listed_by_name_in_order() {
    // Given: GitLab answers a branch list with no search.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/repository/branches"))
        .and(query_param("per_page", "100"))
        .and(query_param_is_missing("search"))
        .respond_with(json_body(BRANCHES))
        .expect(1)
        .mount(&server)
        .await;

    // When: the branches are listed.
    let names = client_for(&server.uri())
        .list_branches(1, None)
        .await
        .unwrap();

    // Then: every name comes back in the order GitLab gave.
    assert_eq!(names, ["main", "develop", "feature/login"]);
}

#[tokio::test]
async fn a_branch_search_reads_one_page_of_matches() {
    // Given: GitLab matches two branches for "release" and names a page after them.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/repository/branches"))
        .and(query_param("search", "release"))
        .and(query_param("per_page", "100"))
        .respond_with(
            json_body(BRANCHES_PAGE_2)
                .insert_header("X-Page", "1")
                .insert_header("X-Next-Page", "2")
                .insert_header("X-Total-Pages", "2"),
        )
        .expect(1)
        .mount(&server)
        .await;

    // When: the branches are searched.
    let names = client_for(&server.uri())
        .list_branches(1, Some("release"))
        .await
        .unwrap();

    // Then: the matches of the first page come back, and no further page is read.
    assert_eq!(names, ["release/v1", "release/v2"]);
}

#[tokio::test]
async fn recent_commits_on_a_ref_come_newest_first_with_who_wrote_them() {
    // Given: GitLab answers the ten latest commits on main.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/repository/commits"))
        .and(query_param("ref_name", "main"))
        .and(query_param("per_page", "10"))
        .respond_with(json_body(COMMITS))
        .expect(1)
        .mount(&server)
        .await;

    // When: ten commits on main are read.
    let commits = client_for(&server.uri())
        .list_commits(1, Some("main"), 10)
        .await
        .unwrap();

    // Then: each commit keeps the fields the detail pane shows, in GitLab's order.
    let ids: Vec<_> = commits.iter().map(|c| c.short_id.as_str()).collect();
    assert_eq!(ids, ["abc123", "def456", "ghi789"]);
    let newest = &commits[0];
    assert_eq!(
        (newest.title.as_str(), newest.author_name.as_str()),
        ("feat: Add initial implementation", "Dev User")
    );
    assert_eq!(
        newest.created_at,
        Some(OffsetDateTime::parse("2025-01-10T10:00:00Z", &Rfc3339).unwrap())
    );
}

#[tokio::test]
async fn commits_with_no_ref_come_from_the_default_branch() {
    // Given: GitLab answers only a commit list that names no ref.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/repository/commits"))
        .and(query_param_is_missing("ref_name"))
        .and(query_param("per_page", "5"))
        .respond_with(json_body(COMMITS))
        .expect(1)
        .mount(&server)
        .await;

    // When: five commits are read with no ref.
    let commits = client_for(&server.uri())
        .list_commits(1, None, 5)
        .await
        .unwrap();

    // Then: GitLab chose the branch, and its commits come back.
    assert!(!commits.is_empty());
}
