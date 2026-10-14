//! Branches and commits of a project repository.

use serde::Deserialize;
use time::OffsetDateTime;

use super::{ALL_PAGES_SIZE, Client, Error};

impl Client {
    /// One page only, so a picker narrows a large repository with `search` instead.
    pub async fn list_branches(
        &self,
        project: u64,
        search: Option<&str>,
    ) -> Result<Vec<String>, Error> {
        let mut query = vec![("per_page", ALL_PAGES_SIZE.to_string())];
        if let Some(search) = search {
            query.push(("search", search.into()));
        }
        let path = format!("projects/{project}/repository/branches");
        let branches: Vec<Branch> = self.get_json("list branches", &path, &query).await?;
        Ok(branches.into_iter().map(|b| b.name).collect())
    }

    /// Newest first. `None` reads the default branch.
    pub async fn list_commits(
        &self,
        project: u64,
        git_ref: Option<&str>,
        count: u8,
    ) -> Result<Vec<Commit>, Error> {
        let mut query = vec![("page", "1".into()), ("per_page", count.to_string())];
        if let Some(git_ref) = git_ref {
            query.push(("ref_name", git_ref.into()));
        }
        let path = format!("projects/{project}/repository/commits");
        self.get_json("list commits", &path, &query).await
    }
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Commit {
    pub short_id: String,
    pub title: String,
    #[serde(default)]
    pub author_name: String,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub created_at: Option<OffsetDateTime>,
}

#[derive(Deserialize)]
struct Branch {
    name: String,
}
