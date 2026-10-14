//! Merge requests, their threads and changed files, and resolving a thread.

use reqwest::Method;
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use url::Url;

use super::{Client, Error, Page, User, decode, refusal};

impl Client {
    /// `None` lists merge requests in every state.
    pub async fn list_merge_requests(
        &self,
        project: u64,
        state: Option<MergeRequestState>,
        page: u32,
        per_page: u8,
    ) -> Result<Page<MergeRequest>, Error> {
        let mut query = vec![
            ("page", page.to_string()),
            ("per_page", per_page.to_string()),
        ];
        if let Some(state) = state {
            query.push(("state", state.as_str().into()));
        }
        let path = format!("projects/{project}/merge_requests");
        self.get_page("list merge requests", &path, &query, page)
            .await
    }

    /// Every thread, system notes included.
    pub async fn list_merge_request_discussions(
        &self,
        project: u64,
        merge_request: u64,
    ) -> Result<Vec<Discussion>, Error> {
        let path = format!("projects/{project}/merge_requests/{merge_request}/discussions");
        self.get_all("list merge request threads", &path).await
    }

    pub async fn resolve_merge_request_discussion(
        &self,
        project: u64,
        merge_request: u64,
        discussion: &str,
        resolved: bool,
    ) -> Result<(), Error> {
        const OPERATION: &str = "resolve merge request thread";
        let url = self.discussion_url(project, merge_request, discussion);
        let response = self
            .write(Method::PUT, OPERATION, url, Some(&Resolve { resolved }))
            .await?;
        refusal(OPERATION, response).await.map(drop)
    }

    pub async fn add_merge_request_discussion_note(
        &self,
        project: u64,
        merge_request: u64,
        discussion: &str,
        body: &str,
    ) -> Result<(), Error> {
        const OPERATION: &str = "reply in merge request thread";
        let mut url = self.discussion_url(project, merge_request, discussion);
        url.path_segments_mut()
            .expect("an API URL has a path")
            .push("notes");
        let response = self
            .write(Method::POST, OPERATION, url, Some(&Reply { body }))
            .await?;
        refusal(OPERATION, response).await.map(drop)
    }

    /// `None` comments on the merge request as a whole.
    pub async fn create_merge_request_discussion(
        &self,
        project: u64,
        merge_request: u64,
        body: &str,
        line: Option<&DiffLine>,
    ) -> Result<(), Error> {
        const OPERATION: &str = "start merge request thread";
        let thread = NewDiscussion {
            body,
            position: line.map(|line| TextPosition {
                position_type: "text",
                line,
            }),
        };
        let path = format!("projects/{project}/merge_requests/{merge_request}/discussions");
        let response = self.post(OPERATION, &path, Some(&thread)).await?;
        refusal(OPERATION, response).await.map(drop)
    }

    pub async fn create_merge_request(
        &self,
        project: u64,
        merge_request: &NewMergeRequest<'_>,
    ) -> Result<MergeRequest, Error> {
        const OPERATION: &str = "open merge request";
        let path = format!("projects/{project}/merge_requests");
        let response = self.post(OPERATION, &path, Some(merge_request)).await?;
        decode(OPERATION, refusal(OPERATION, response).await?).await
    }

    fn discussion_url(&self, project: u64, merge_request: u64, discussion: &str) -> Url {
        let mut url = self.url(&format!(
            "projects/{project}/merge_requests/{merge_request}/discussions"
        ));
        // The id comes from GitLab, and a slash in it would send the request to another endpoint.
        url.path_segments_mut()
            .expect("an API URL has a path")
            .push(discussion);
        url
    }

    pub async fn list_merge_request_diffs(
        &self,
        project: u64,
        merge_request: u64,
    ) -> Result<Vec<FileDiff>, Error> {
        let path = format!("projects/{project}/merge_requests/{merge_request}/diffs");
        self.get_all("list merge request diffs", &path).await
    }

    /// `None` until GitLab has prepared the diff, as for a merge request that was just opened.
    pub async fn merge_request_diff_refs(
        &self,
        project: u64,
        merge_request: u64,
    ) -> Result<Option<DiffRefs>, Error> {
        let path = format!("projects/{project}/merge_requests/{merge_request}");
        let found: WithDiffRefs = self
            .get_json("get merge request diff refs", &path, &[])
            .await?;
        Ok(found.diff_refs.and_then(PartialDiffRefs::complete))
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum MergeRequestState {
    Opened,
    Closed,
    Locked,
    Merged,
    #[serde(other)]
    Unknown,
}

impl MergeRequestState {
    fn as_str(self) -> &'static str {
        match self {
            MergeRequestState::Opened => "opened",
            MergeRequestState::Closed => "closed",
            MergeRequestState::Locked => "locked",
            MergeRequestState::Merged => "merged",
            MergeRequestState::Unknown => "unknown",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct MergeRequest {
    /// The number GitLab shows as `!42`, unique within its project.
    pub iid: u64,
    pub title: String,
    pub state: MergeRequestState,
    #[serde(default)]
    pub author: Option<User>,
    pub source_branch: String,
    pub target_branch: String,
    #[serde(default)]
    pub web_url: String,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub updated_at: Option<OffsetDateTime>,
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Discussion {
    pub id: String,
    pub notes: Vec<Note>,
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Note {
    pub id: u64,
    #[serde(default)]
    pub author: Option<User>,
    #[serde(default)]
    pub body: String,
    /// Written by GitLab, as for "added 1 commit".
    #[serde(default)]
    pub system: bool,
    #[serde(default)]
    pub resolvable: bool,
    #[serde(default)]
    pub resolved: bool,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub created_at: Option<OffsetDateTime>,
    #[serde(default)]
    pub position: Option<Position>,
}

/// A deleted line has only an old line, an added line only a new one, and an unchanged line both.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Position {
    #[serde(default)]
    pub old_path: Option<String>,
    #[serde(default)]
    pub new_path: Option<String>,
    #[serde(default)]
    pub old_line: Option<u32>,
    #[serde(default)]
    pub new_line: Option<u32>,
}

impl Position {
    /// The file as it is after the change, so a renamed file shows its new name.
    pub fn path(&self) -> Option<&str> {
        self.new_path.as_deref().or(self.old_path.as_deref())
    }

    pub fn line(&self) -> Option<u32> {
        self.new_line.or(self.old_line)
    }
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct FileDiff {
    pub old_path: String,
    pub new_path: String,
    /// Empty when `too_large` is set.
    #[serde(default)]
    pub diff: String,
    #[serde(default)]
    pub new_file: bool,
    #[serde(default)]
    pub renamed_file: bool,
    #[serde(default)]
    pub deleted_file: bool,
    #[serde(default)]
    pub too_large: bool,
}

/// The commits a comment on a diff line has to name.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct DiffRefs {
    pub base_sha: String,
    pub head_sha: String,
    pub start_sha: String,
}

/// An added line has only a new line, a deleted line only an old one, and an unchanged line both.
#[derive(Debug, Serialize)]
pub struct DiffLine {
    #[serde(flatten)]
    pub diff_refs: DiffRefs,
    pub old_path: String,
    pub new_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub old_line: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub new_line: Option<u32>,
}

#[derive(Debug, Serialize)]
pub struct NewMergeRequest<'a> {
    pub title: &'a str,
    pub source_branch: &'a str,
    pub target_branch: &'a str,
    pub description: &'a str,
}

#[derive(Deserialize)]
struct WithDiffRefs {
    #[serde(default)]
    diff_refs: Option<PartialDiffRefs>,
}

#[derive(Deserialize)]
struct PartialDiffRefs {
    base_sha: Option<String>,
    head_sha: Option<String>,
    start_sha: Option<String>,
}

impl PartialDiffRefs {
    fn complete(self) -> Option<DiffRefs> {
        Some(DiffRefs {
            base_sha: self.base_sha?,
            head_sha: self.head_sha?,
            start_sha: self.start_sha?,
        })
    }
}

#[derive(Serialize)]
struct Resolve {
    resolved: bool,
}

#[derive(Serialize)]
struct Reply<'a> {
    body: &'a str,
}

#[derive(Serialize)]
struct NewDiscussion<'a> {
    body: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    position: Option<TextPosition<'a>>,
}

#[derive(Serialize)]
struct TextPosition<'a> {
    position_type: &'static str,
    #[serde(flatten)]
    line: &'a DiffLine,
}
