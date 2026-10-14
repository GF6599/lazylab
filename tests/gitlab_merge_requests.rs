mod common;

use common::client_for;
use lazylab::gitlab::{DiffRefs, MergeRequestState};
use serde_json::{Value, json};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use wiremock::matchers::{body_json, method, path, query_param, query_param_is_missing};
use wiremock::{Mock, MockServer, ResponseTemplate};

const MERGE_REQUESTS: &str = include_str!("fixtures/merge_requests.json");
const DISCUSSIONS: &str = include_str!("fixtures/mr_discussions.json");
const DIFFS: &str = include_str!("fixtures/mr_diffs.json");
const DIFF_REFS: &str = include_str!("fixtures/merge_request_diff_refs.json");

fn json_body(body: impl Into<String>) -> ResponseTemplate {
    ResponseTemplate::new(200)
        .insert_header("Content-Type", "application/json")
        .set_body_string(body.into())
}

fn json_value(body: Value) -> ResponseTemplate {
    ResponseTemplate::new(200).set_body_json(body)
}

#[tokio::test]
async fn open_merge_requests_are_listed_with_their_display_fields() {
    // Given: GitLab answers the first page of open merge requests.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests"))
        .and(query_param("state", "opened"))
        .and(query_param("page", "1"))
        .and(query_param("per_page", "25"))
        .respond_with(json_body(MERGE_REQUESTS).insert_header("X-Next-Page", ""))
        .expect(1)
        .mount(&server)
        .await;

    // When: the first page of 25 open merge requests is listed.
    let page = client_for(&server.uri())
        .list_merge_requests(1, Some(MergeRequestState::Opened), 1, 25)
        .await
        .unwrap();

    // Then: both merge requests keep the fields the merge request pane shows.
    let [newest, older] = &page.items[..] else {
        panic!("two merge requests, got {:?}", page.items);
    };
    assert_eq!(
        (newest.iid, newest.title.as_str(), newest.state),
        (42, "Add feature X", MergeRequestState::Opened)
    );
    assert_eq!(
        (newest.source_branch.as_str(), newest.target_branch.as_str()),
        ("feature-x", "main")
    );
    assert_eq!(
        newest.author.as_ref().map(|a| a.name.as_str()),
        Some("Jane Dev")
    );
    assert_eq!(
        newest.web_url,
        "https://gitlab.com/team/app/-/merge_requests/42"
    );
    assert_eq!(
        newest.updated_at,
        Some(OffsetDateTime::parse("2025-01-15T12:00:00Z", &Rfc3339).unwrap())
    );
    assert_eq!((older.iid, older.state), (41, MergeRequestState::Merged));
    assert_eq!(page.next_page, None);
}

#[tokio::test]
async fn a_list_with_no_state_asks_for_every_state() {
    // Given: GitLab answers only a request that names no state.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests"))
        .and(query_param_is_missing("state"))
        .respond_with(json_body(MERGE_REQUESTS))
        .expect(1)
        .mount(&server)
        .await;

    // When: merge requests are listed with no state.
    let page = client_for(&server.uri())
        .list_merge_requests(1, None, 1, 25)
        .await
        .unwrap();

    // Then: the open and the merged one both come back.
    let states: Vec<_> = page.items.iter().map(|mr| mr.state).collect();
    assert_eq!(
        states,
        [MergeRequestState::Opened, MergeRequestState::Merged]
    );
}

#[tokio::test]
async fn a_thread_keeps_its_notes_in_order_with_who_wrote_them() {
    // Given: GitLab answers with one resolvable thread of two notes.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests/42/discussions"))
        .respond_with(json_body(DISCUSSIONS))
        .expect(1)
        .mount(&server)
        .await;

    // When: the threads of merge request 42 are read.
    let threads = client_for(&server.uri())
        .list_merge_request_discussions(1, 42)
        .await
        .unwrap();

    // Then: the thread keeps its id and both notes, first note first.
    let [thread] = &threads[..] else {
        panic!("one thread, got {threads:?}");
    };
    assert_eq!(thread.id, "disc-1");
    let [first, reply] = &thread.notes[..] else {
        panic!("two notes, got {:?}", thread.notes);
    };
    assert_eq!(
        (first.id, first.author.as_ref().map(|a| a.name.as_str())),
        (501, Some("Reviewer"))
    );
    assert_eq!(
        (reply.id, reply.author.as_ref().map(|a| a.name.as_str())),
        (502, Some("Author"))
    );
    assert!(first.resolvable && !first.resolved && !first.system);
    assert!(!first.body.is_empty());
    assert!(first.created_at.is_some());
    assert_eq!(first.position, None);
}

#[tokio::test]
async fn a_note_on_a_diff_line_names_its_file_and_line() {
    // Given: notes on a changed line, a deleted line, a renamed file, and no line at all.
    let note = |id: u64, position: Value| {
        json!({"id": id, "body": "b", "author": {"name": "R"}, "system": false,
               "resolvable": true, "resolved": false, "position": position})
    };
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests/42/discussions"))
        .respond_with(json_value(json!([{"id": "d", "notes": [
            note(1, json!({"old_path": "src/main.rs", "new_path": "src/main.rs", "old_line": 40, "new_line": 42})),
            note(2, json!({"old_path": "src/main.rs", "new_path": "src/main.rs", "old_line": 7, "new_line": null})),
            note(3, json!({"old_path": "old.rs", "new_path": "new.rs", "old_line": null, "new_line": 3})),
            note(4, Value::Null),
        ]}])))
        .mount(&server)
        .await;

    // When: the threads are read.
    let threads = client_for(&server.uri())
        .list_merge_request_discussions(1, 42)
        .await
        .unwrap();

    // Then: each note names the line the reader looks for, in the file as it is now.
    let located: Vec<_> = threads[0]
        .notes
        .iter()
        .map(|note| {
            note.position.as_ref().map(|p| {
                (
                    p.path().map(str::to_string),
                    p.line(),
                    p.old_line,
                    p.new_line,
                )
            })
        })
        .collect();
    assert_eq!(
        located,
        [
            Some((Some("src/main.rs".into()), Some(42), Some(40), Some(42))),
            Some((Some("src/main.rs".into()), Some(7), Some(7), None)),
            Some((Some("new.rs".into()), Some(3), None, Some(3))),
            None,
        ]
    );
}

#[tokio::test]
async fn a_thread_is_resolved_and_then_reopened() {
    // Given: GitLab accepts a resolve and a reopen of thread disc-1.
    let server = MockServer::start().await;
    for resolved in [true, false] {
        Mock::given(method("PUT"))
            .and(path(
                "/api/v4/projects/1/merge_requests/42/discussions/disc-1",
            ))
            .and(body_json(json!({"resolved": resolved})))
            .respond_with(json_value(json!({"id": "disc-1", "notes": []})))
            .expect(1)
            .mount(&server)
            .await;
    }
    let client = client_for(&server.uri());

    // When: the thread is resolved, then reopened.
    let resolve = client
        .resolve_merge_request_discussion(1, 42, "disc-1", true)
        .await;
    let reopen = client
        .resolve_merge_request_discussion(1, 42, "disc-1", false)
        .await;

    // Then: both requests succeed.
    assert!(resolve.is_ok(), "{resolve:?}");
    assert!(reopen.is_ok(), "{reopen:?}");
}

#[tokio::test]
async fn a_thread_id_stays_one_part_of_the_path() {
    // Given: a thread id that holds a slash, and an endpoint that id would otherwise reach.
    let server = MockServer::start().await;
    Mock::given(method("PUT"))
        .and(path(
            "/api/v4/projects/1/merge_requests/42/discussions/x%2F..%2F..%2F7",
        ))
        .respond_with(json_value(json!({"id": "x", "notes": []})))
        .expect(1)
        .mount(&server)
        .await;
    Mock::given(method("PUT"))
        .and(path("/api/v4/projects/1/merge_requests/7"))
        .respond_with(json_value(json!({})))
        .expect(0)
        .mount(&server)
        .await;

    // When: that thread is resolved.
    let resolve = client_for(&server.uri())
        .resolve_merge_request_discussion(1, 42, "x/../../7", true)
        .await;

    // Then: the request went to the thread, not to merge request 7.
    assert!(resolve.is_ok(), "{resolve:?}");
}

#[tokio::test]
async fn changed_files_keep_their_paths_diffs_and_change_kind() {
    // Given: GitLab answers with an edited file and a new file.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests/42/diffs"))
        .respond_with(json_body(DIFFS))
        .expect(1)
        .mount(&server)
        .await;

    // When: the changed files of merge request 42 are read.
    let files = client_for(&server.uri())
        .list_merge_request_diffs(1, 42)
        .await
        .unwrap();

    // Then: each file keeps its paths, its diff text and what kind of change it is.
    let [edited, added] = &files[..] else {
        panic!("two files, got {files:?}");
    };
    assert_eq!(
        (edited.old_path.as_str(), edited.new_path.as_str()),
        ("main.go", "main.go")
    );
    assert!(edited.diff.starts_with("@@"), "{}", edited.diff);
    assert!(!edited.new_file && !edited.renamed_file && !edited.deleted_file);
    assert_eq!(added.new_path, "new_file.go");
    assert!(added.new_file);
    assert!(!edited.too_large && !added.too_large);
}

#[tokio::test]
async fn a_file_too_large_to_diff_says_so() {
    // Given: GitLab withholds the diff of one file because it is too large.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests/42/diffs"))
        .respond_with(json_value(json!([{
            "old_path": "dump.sql", "new_path": "dump.sql", "diff": "",
            "new_file": false, "renamed_file": false, "deleted_file": false, "too_large": true,
        }])))
        .mount(&server)
        .await;

    // When: the changed files are read.
    let files = client_for(&server.uri())
        .list_merge_request_diffs(1, 42)
        .await
        .unwrap();

    // Then: the file is marked too large, so an empty diff is not read as no change.
    assert!(files[0].too_large);
}

#[tokio::test]
async fn the_diff_refs_name_the_three_commits_a_line_comment_needs() {
    // Given: GitLab answers merge request 42 with its diff refs.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests/42"))
        .respond_with(json_body(DIFF_REFS))
        .expect(1)
        .mount(&server)
        .await;

    // When: the diff refs are read.
    let refs = client_for(&server.uri())
        .merge_request_diff_refs(1, 42)
        .await
        .unwrap();

    // Then: the base, head and start commits come back.
    assert_eq!(
        refs,
        Some(DiffRefs {
            base_sha: "base123".into(),
            head_sha: "head456".into(),
            start_sha: "start789".into(),
        })
    );
}

#[tokio::test]
async fn a_merge_request_gitlab_has_not_prepared_has_no_diff_refs() {
    // Given: one merge request with no diff refs, and three that each lack one commit.
    let server = MockServer::start().await;
    Mock::given(method("GET"))
        .and(path("/api/v4/projects/1/merge_requests/42"))
        .respond_with(json_value(json!({"iid": 42, "diff_refs": null})))
        .mount(&server)
        .await;
    let shas = ["base_sha", "head_sha", "start_sha"];
    for (iid, missing) in (43..).zip(shas) {
        let mut refs =
            json!({"base_sha": "base123", "head_sha": "head456", "start_sha": "start789"});
        refs[missing] = Value::Null;
        Mock::given(method("GET"))
            .and(path(format!("/api/v4/projects/1/merge_requests/{iid}")))
            .respond_with(json_value(json!({"iid": iid, "diff_refs": refs})))
            .mount(&server)
            .await;
    }
    let client = client_for(&server.uri());

    // When: the diff refs of all four are read.
    let mut found = Vec::new();
    for iid in 42..=45 {
        found.push(client.merge_request_diff_refs(1, iid).await.unwrap());
    }

    // Then: none has diff refs, because a line comment needs all three commits.
    assert_eq!(found, [None, None, None, None]);
}
