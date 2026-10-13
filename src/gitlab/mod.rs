//! A typed client for the parts of the GitLab REST API that lazylab uses.

use std::fmt;

use serde::Deserialize;
use time::OffsetDateTime;
use url::Url;

use crate::config::Token;

pub struct Client;

impl Client {
    pub fn new(_host: &Url, _token: &Token) -> Result<Client, Error> {
        Ok(Client)
    }

    pub async fn list_projects(&self, _page: u32, _per_page: u8) -> Result<Page<Project>, Error> {
        Err(Error {
            kind: ErrorKind::Setup,
            detail: "not implemented".into(),
        })
    }
}

/// One page of a list, with where it sits in the whole collection.
#[derive(Debug, PartialEq)]
pub struct Page<T> {
    pub items: Vec<T>,
    pub page: u32,
    pub next_page: Option<u32>,
    pub total_pages: Option<u32>,
    /// `None` when GitLab withholds the count, which it does past ten thousand items.
    pub total_items: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Project {
    pub id: u64,
    pub name: String,
    pub path_with_namespace: String,
    #[serde(default)]
    pub description: Option<String>,
    #[serde(default)]
    pub web_url: String,
    #[serde(default)]
    pub default_branch: Option<String>,
    #[serde(default)]
    pub star_count: u64,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub last_activity_at: Option<OffsetDateTime>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorKind {
    Unauthorized,
    Forbidden,
    NotFound,
    RateLimited,
    Server,
    Rejected,
    Network,
    Decode,
    Setup,
}

#[derive(Debug)]
pub struct Error {
    pub kind: ErrorKind,
    detail: String,
}

impl Error {
    pub fn exit_code(&self) -> u8 {
        1
    }
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.detail)
    }
}
