//! Sample data that answers the screen's commands without a GitLab. It is written as GitLab's own
//! JSON and decoded into the client types, so the screen meets the same values a real answer would
//! give it.

use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use time::format_description::well_known::Rfc3339;
use time::{Duration, OffsetDateTime};

use super::app::{Cmd, Msg};
use crate::gitlab::{DiffRefs, MergeRequestState, Page};
use crate::store::CachedProjects;

const PROJECTS: [(&str, &str, &str); 13] = [
    (
        "acme/platform/api",
        "Public REST API and webhook fan-out",
        "main",
    ),
    ("acme/platform/web", "Customer dashboard", "main"),
    (
        "acme/platform/workers",
        "Queue consumers and scheduled jobs",
        "main",
    ),
    (
        "acme/infra/terraform",
        "Cloud accounts, networks and DNS",
        "main",
    ),
    (
        "acme/infra/helm-charts",
        "Charts for every deployed service",
        "main",
    ),
    (
        "acme/infra/runner-images",
        "Base images for the CI runners",
        "main",
    ),
    (
        "acme/data/ingest",
        "Event ingestion and schema registry",
        "develop",
    ),
    (
        "acme/data/warehouse-models",
        "Warehouse transformations",
        "main",
    ),
    ("acme/tools/lazylab", "Terminal UI for GitLab", "main"),
    (
        "acme/tools/release-bot",
        "Changelog and tag automation",
        "main",
    ),
    ("acme/mobile/ios-app", "iOS client", "main"),
    ("acme/mobile/android-app", "Android client", "main"),
    ("acme/docs/handbook", "Engineering handbook", "main"),
];
const FIRST_PROJECT: u64 = 101;
const PIPELINES_PER_PROJECT: u64 = 14;
const PIPELINE_ID_STEP: u64 = 7;
const REFS: [&str; 6] = [
    "main",
    "feat/runner-cache",
    "fix/flaky-e2e",
    "release/0.4",
    "feat/sso-login",
    "chore/bump-deps",
];
const AUTHORS: [&str; 5] = ["Dana", "Kofi", "Mei", "Ravi", "Sol"];
const PATTERN: [&str; 10] = [
    "running", "success", "failed", "success", "manual", "success", "canceled", "success",
    "failed", "success",
];
const JOB_PLAN: [(&str, &str); 9] = [
    ("build", "compile"),
    ("build", "image"),
    ("test", "unit"),
    ("test", "integration"),
    ("test", "lint"),
    ("security", "sast"),
    ("security", "dependency-scan"),
    ("deploy", "staging"),
    ("deploy", "production"),
];
const OPEN_MERGE_REQUESTS: [(u64, &str, &str); 5] = [
    (412, "Cache the runner image layers", "feat/runner-cache"),
    (409, "Retry the flaky checkout step", "fix/flaky-e2e"),
    (407, "Add SSO login behind a flag", "feat/sso-login"),
    (401, "Split the e2e suite by shard", "feat/e2e-shards"),
    (398, "Bump the toolchain", "chore/bump-deps"),
];
const MERGED_MERGE_REQUESTS: [(u64, &str, &str); 4] = [
    (396, "Cache the dependency downloads", "feat/dep-cache"),
    (392, "Pin the base image digest", "chore/pin-base"),
    (388, "Fix the nightly time zone", "fix/nightly-tz"),
    (381, "Add worker health checks", "feat/worker-health"),
];
const CLOSED_MERGE_REQUESTS: [(u64, &str, &str); 2] = [
    (394, "Try a second runner pool", "spike/runner-pool"),
    (385, "Move the handbook to a wiki", "docs/wiki"),
];

pub fn answer(command: Cmd, now: OffsetDateTime) -> Option<Msg> {
    let msg = match command {
        Cmd::LoadCache => {
            let projects = projects(now);
            Msg::CacheLoaded(Some(CachedProjects {
                total: projects.len() as u64,
                projects,
            }))
        }
        Cmd::LoadProjectPage { load, page, .. } => {
            let items = if page == 1 { projects(now) } else { Vec::new() };
            Msg::ProjectPageLoaded {
                load,
                page: Page {
                    total_items: Some(PROJECTS.len() as u64),
                    items,
                    page,
                    next_page: None,
                    total_pages: Some(1),
                },
            }
        }
        Cmd::LoadFavorites => Msg::FavoritesLoaded(Vec::new()),
        Cmd::LoadStatus { project } => Msg::StatusLoaded {
            project,
            status: pipelines(project, now).first().map(|p| p.status),
        },
        Cmd::SaveProjects { .. } | Cmd::SaveFavorites(_) => return None,
        Cmd::LoadPipelines { project, page } => {
            let items = if page == 1 {
                pipelines(project, now)
            } else {
                Vec::new()
            };
            Msg::PipelinesLoaded {
                project,
                page: Page {
                    total_items: Some(PIPELINES_PER_PROJECT),
                    items,
                    page,
                    next_page: None,
                    total_pages: Some(1),
                },
            }
        }
        Cmd::LoadJobs { project, pipeline } => Msg::JobsLoaded {
            pipeline,
            jobs: jobs(project, pipeline),
            bridges: Vec::new(),
        },
        Cmd::Act(action) => Msg::Acted {
            action,
            started: None,
        },
        Cmd::LoadLog { job, .. } => Msg::LogLoaded {
            job,
            log: format!("$ ./ci/job {job}\nRunning the job\nJob {job} done\n"),
        },
        Cmd::LoadChildJobs { pipeline, .. } => Msg::ChildJobsLoaded {
            pipeline,
            jobs: Vec::new(),
        },
        Cmd::LoadMergeRequests { project, state } => Msg::MergeRequestsLoaded {
            project,
            state,
            merge_requests: merge_requests(project, state, now),
        },
        Cmd::LoadThreads {
            project,
            merge_request,
        } => Msg::ThreadsLoaded {
            project,
            merge_request,
            discussions: threads(now),
        },
        Cmd::LoadDiff {
            project,
            merge_request,
        } => Msg::DiffLoaded {
            project,
            merge_request,
            files: diffs(),
        },
        Cmd::LoadDiffRefs {
            project,
            merge_request,
        } => Msg::DiffRefsLoaded {
            project,
            merge_request,
            refs: Some(diff_refs(merge_request)),
        },
        Cmd::LoadTestReport { project, pipeline } => Msg::TestReportLoaded {
            project,
            pipeline,
            report: test_report(project, pipeline),
        },
        Cmd::ResolveThread {
            project,
            merge_request,
            resolved,
            ..
        } => Msg::ThreadResolved {
            project,
            merge_request,
            resolved,
        },
        Cmd::Comment {
            project,
            merge_request,
            discussion,
            ..
        } => Msg::Commented {
            project,
            merge_request,
            reply: discussion.is_some(),
        },
    };
    Some(msg)
}

fn projects(now: OffsetDateTime) -> Vec<crate::gitlab::Project> {
    let minutes = [
        4, 12, 60, 180, 300, 1440, 2880, 2900, 4320, 10080, 10100, 20160, 43200,
    ];
    PROJECTS
        .iter()
        .zip(minutes)
        .zip(FIRST_PROJECT..)
        .map(|(((path, description, branch), minutes), id)| {
            json!({
                "id": id,
                "name": path.rsplit('/').next(),
                "path_with_namespace": path,
                "description": description,
                "web_url": format!("https://gitlab.example.com/{path}"),
                "default_branch": branch,
                "last_activity_at": at(now, minutes),
            })
        })
        .map(decode)
        .collect()
}

fn pipelines(project: u64, now: OffsetDateTime) -> Vec<crate::gitlab::Pipeline> {
    let shift = project.saturating_sub(FIRST_PROJECT) as usize;
    (0..PIPELINES_PER_PROJECT as usize)
        .map(|i| {
            let k = i + shift * 3;
            let git_ref = if i % 3 == 0 {
                "main"
            } else {
                REFS[k % REFS.len()]
            };
            let minutes = (i * 37 + shift * 5 + 2) as i64;
            let source = ["push", "merge_request_event", "schedule"][k % 3];
            json!({
                "id": pipeline_id(project, i as u64),
                "status": pipeline_status(project, i as u64),
                "ref": git_ref,
                "sha": format!("{:07x}", 0x3f2a9c1u32.wrapping_mul(k as u32 + 7) & 0x0fff_ffff),
                "source": source,
                "created_at": at(now, minutes),
                "updated_at": at(now, minutes),
                "duration": (140 + (k * 53) % 600) as f64,
                "user": {"name": AUTHORS[k % AUTHORS.len()]},
            })
        })
        .map(decode)
        .collect()
}

fn pipeline_id(project: u64, index: u64) -> u64 {
    48_210 + project * 131 - index * PIPELINE_ID_STEP
}

fn pipeline_status(project: u64, index: u64) -> &'static str {
    let shift = project.saturating_sub(FIRST_PROJECT);
    if index == 0 && shift.is_multiple_of(2) {
        "running"
    } else {
        PATTERN[((index + shift * 3) as usize) % PATTERN.len()]
    }
}

fn status_of(project: u64, pipeline: u64) -> &'static str {
    let index = pipeline_id(project, 0).saturating_sub(pipeline) / PIPELINE_ID_STEP;
    pipeline_status(project, index)
}

fn jobs(project: u64, pipeline: u64) -> Vec<crate::gitlab::Job> {
    let status = status_of(project, pipeline);
    JOB_PLAN
        .iter()
        .zip(0u64..)
        .map(|(&(stage, name), i)| {
            json!({
                "id": pipeline * 10 + i,
                "name": name,
                "stage": stage,
                "status": job_status(status, stage, name),
                "duration": (20 + (i * 41) % 300) as f64,
            })
        })
        .map(decode)
        .collect()
}

fn job_status(pipeline: &str, stage: &str, name: &str) -> &'static str {
    match (pipeline, stage, name) {
        ("success", _, "production") => "manual",
        ("success", _, _) => "success",
        ("manual", "deploy", _) => "manual",
        ("manual", _, _) => "success",
        ("failed", "build", _) => "success",
        ("failed", "test", "integration") => "failed",
        ("failed", "test", _) => "success",
        ("running", "build", _) | ("running", "test", "lint") => "success",
        ("running", "test", _) => "running",
        ("canceled", "build", _) => "success",
        ("canceled", "test", _) => "canceled",
        ("failed" | "canceled", _, _) => "skipped",
        _ => "pending",
    }
}

/// A failed pipeline fails one integration case, which the other pipelines pass.
fn test_report(project: u64, pipeline: u64) -> crate::gitlab::TestReport {
    let failed = u32::from(status_of(project, pipeline) == "failed");
    let case = if failed == 1 { "failed" } else { "success" };
    decode(json!({
        "total_time": 84.2,
        "total_count": 60,
        "success_count": 60 - failed,
        "failed_count": failed,
        "test_suites": [
            {"name": "unit", "total_count": 42, "success_count": 42},
            {"name": "integration", "total_count": 18, "success_count": 18 - failed,
             "failed_count": failed, "test_cases": [{
                "status": case,
                "name": "checkout retries a dropped connection",
                "classname": "e2e.CheckoutTest",
                "system_output": "expected 3 retries, got 1\n\tat CheckoutTest.retry(CheckoutTest.java:88)\n\tat Runner.run(Runner.java:41)\n\tat Runner.main(Runner.java:12)\nretry log:\n  attempt 1 refused\n  attempt 2 never sent",
            }]},
        ],
    }))
}

fn merge_requests(
    project: u64,
    state: MergeRequestState,
    now: OffsetDateTime,
) -> Vec<crate::gitlab::MergeRequest> {
    let shift = project.saturating_sub(FIRST_PROJECT) as usize;
    let listed: &[(u64, &str, &str)] = match state {
        MergeRequestState::Opened => &OPEN_MERGE_REQUESTS,
        MergeRequestState::Merged => &MERGED_MERGE_REQUESTS,
        MergeRequestState::Closed => &CLOSED_MERGE_REQUESTS,
        MergeRequestState::Locked | MergeRequestState::Unknown => &[],
    };
    listed
        .iter()
        .zip([120, 360, 1440, 4320, 10080])
        .enumerate()
        .map(|(i, (&(iid, title, source), minutes))| {
            json!({
                "iid": iid,
                "title": title,
                "state": state.as_str(),
                "author": {"name": AUTHORS[(i + shift) % AUTHORS.len()]},
                "source_branch": source,
                "target_branch": "main",
                "web_url": format!("https://gitlab.example.com/merge_requests/{iid}"),
                "updated_at": at(now, minutes),
            })
        })
        .map(decode)
        .collect()
}

fn threads(now: OffsetDateTime) -> Vec<crate::gitlab::Discussion> {
    let note = |id: u64, author: &str, body: &str, minutes: i64| {
        json!({
            "id": id,
            "author": {"name": author},
            "body": body,
            "created_at": at(now, minutes),
        })
    };
    let on_line = |mut note: Value, path: &str, line: u32, resolved: bool| {
        note["resolvable"] = json!(true);
        note["resolved"] = json!(resolved);
        note["position"] = json!({"old_path": path, "new_path": path, "new_line": line});
        note
    };
    let resolvable = |mut note: Value, resolved: bool| {
        note["resolvable"] = json!(true);
        note["resolved"] = json!(resolved);
        note
    };
    let mut system = note(2, "Dana", "added 1 commit", 170);
    system["system"] = json!(true);
    [
        json!({"id": "d1", "notes": [
            on_line(
                note(1, "Kofi", "Does the cache key change when the base image is retagged but not rebuilt?", 180),
                ".gitlab-ci.yml", 14, true,
            ),
            resolvable(note(3, "Dana", "No, the key is the digest, so a retag reuses the same layers.", 120), true),
        ]}),
        json!({"id": "d2", "notes": [system]}),
        json!({"id": "d3", "notes": [
            resolvable(note(4, "Mei", "Please add the eviction size to the runner config docs.", 150), false),
        ]}),
        json!({"id": "d4", "notes": [note(5, "Ravi", "Looks good to me.\nOne nit below.", 90)]}),
        json!({"id": "d5", "notes": [
            on_line(note(6, "Sol", "Should the cache survive a runner restart?", 70), "ci/runner.toml", 8, false),
            resolvable(note(7, "Dana", "Yes, it lives on the host volume.", 45), false),
            resolvable(note(8, "Sol", "Thanks.", 30), false),
        ]}),
    ]
    .into_iter()
    .map(decode)
    .collect()
}

fn diffs() -> Vec<crate::gitlab::FileDiff> {
    let file =
        |old: &str, new: &str, diff: &str| json!({"old_path": old, "new_path": new, "diff": diff});
    let mut renamed = file(
        "ci/cache.sh",
        "ci/layers.sh",
        "@@ -1,3 +1,3 @@\n #!/bin/sh\n-restore_cache\n+restore_layers\n echo done\n\\ No newline at end of file\n",
    );
    renamed["renamed_file"] = json!(true);
    let mut created = file(
        "Makefile",
        "Makefile",
        "@@ -0,0 +1,3 @@\n+ci:\tbuild\n+build:\n+\tjust ci-compile\n",
    );
    created["new_file"] = json!(true);
    let mut deleted = file(
        "db/old-cache.sql",
        "db/old-cache.sql",
        "@@ -1,2 +0,0 @@\n--- old cache table\n-DROP TABLE cache;\n",
    );
    deleted["deleted_file"] = json!(true);
    let mut large = file("vendor/bundle.js", "vendor/bundle.js", "");
    large["too_large"] = json!(true);
    [
        file(
            ".gitlab-ci.yml",
            ".gitlab-ci.yml",
            "@@ -12,6 +12,13 @@ default:\n   image: registry.example.com/ci/base:2026.10\n   interruptible: true\n+  cache:\n+    key:\n+      files: [ci/base.digest]\n+    paths: [.layers/]\n+    policy: pull-push\n \n stages: [build, test, security, deploy]\n-compile:\n-  script: just ci-compile\n+compile:\n+  script:\n+    - just restore-layers\n+    - just ci-compile\n",
        ),
        renamed,
        created,
        deleted,
        large,
    ]
    .into_iter()
    .map(decode)
    .collect()
}

/// Each merge request names commits of its own, so a comment shows which merge request it is on.
fn diff_refs(merge_request: u64) -> DiffRefs {
    let sha = |side: u64| format!("{:040x}", merge_request * 0x1_0000 + side);
    DiffRefs {
        base_sha: sha(1),
        start_sha: sha(2),
        head_sha: sha(3),
    }
}

fn at(now: OffsetDateTime, minutes_ago: i64) -> String {
    (now - Duration::minutes(minutes_ago))
        .format(&Rfc3339)
        .expect("a demo time formats")
}

fn decode<T: DeserializeOwned>(value: Value) -> T {
    serde_json::from_value(value).expect("the demo data matches the GitLab answer shape")
}
