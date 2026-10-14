//! A typed client for the parts of the GitLab REST API that lazylab uses.

use std::fmt;
use std::str::FromStr;
use std::time::Duration;

use reqwest::header::{HeaderMap, HeaderValue};
use reqwest::{Method, RequestBuilder, Response, StatusCode, redirect};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use url::Url;

use crate::config::Token;
use crate::exit;
use crate::redact::redact;

mod actions;
mod merge_requests;
mod pipelines;
mod repository;

pub use actions::Variable;
pub use merge_requests::{
    DiffLine, DiffRefs, Discussion, FileDiff, MergeRequest, MergeRequestState, NewMergeRequest,
    Note, Position,
};
pub use pipelines::{
    Artifact, Bridge, Downstream, Job, LatestPipeline, MAX_JOB_LOG_BYTES, Pipeline, Stage, Status,
    TestCase, TestCaseStatus, TestReport, TestSuite, User,
};
pub use repository::Commit;

/// The Go release gives every call 15 seconds.
const TIMEOUT: Duration = Duration::from_secs(15);
const API_PATH: &str = "api/v4/";
const MAX_REDIRECTS: usize = 10;
/// The largest page GitLab serves.
const ALL_PAGES_SIZE: u8 = 100;

pub struct Client {
    http: reqwest::Client,
    api: Url,
}

impl Client {
    pub fn new(host: &Url, token: &Token) -> Result<Client, Error> {
        let setup = |detail: String| Error {
            kind: ErrorKind::Setup,
            operation: "create the GitLab client",
            detail,
        };
        let mut value = HeaderValue::from_str(token.expose())
            .map_err(|_| setup("the token holds characters a header cannot carry".into()))?;
        value.set_sensitive(true);
        let mut headers = HeaderMap::new();
        headers.insert("PRIVATE-TOKEN", value);
        let http = reqwest::Client::builder()
            .default_headers(headers)
            .redirect(redirect::Policy::custom(same_origin_only))
            .timeout(TIMEOUT)
            .user_agent(concat!("lazylab/", env!("CARGO_PKG_VERSION")))
            .build()
            .map_err(|e| setup(chain(&e)))?;
        Ok(Client {
            http,
            api: api_base(host),
        })
    }

    pub async fn list_projects(&self, page: u32, per_page: u8) -> Result<Page<Project>, Error> {
        let query = [
            ("membership", "true".to_string()),
            ("order_by", "last_activity_at".into()),
            ("sort", "desc".into()),
            ("simple", "true".into()),
            ("page", page.to_string()),
            ("per_page", per_page.to_string()),
        ];
        self.get_page("list projects", "projects", &query, page)
            .await
    }

    async fn get(
        &self,
        operation: &'static str,
        path: &str,
        query: &[(&str, String)],
    ) -> Result<Response, Error> {
        let request = self.http.get(self.url(path)).query(query);
        let response = send(operation, request).await?;
        refusal(operation, response).await
    }

    /// Unlike `get`, this leaves a refusal to the caller, because a retry treats a 400 as a cue.
    async fn post(
        &self,
        operation: &'static str,
        path: &str,
        body: Option<&impl Serialize>,
    ) -> Result<Response, Error> {
        self.write(Method::POST, operation, self.url(path), body)
            .await
    }

    async fn write(
        &self,
        method: Method,
        operation: &'static str,
        url: Url,
        body: Option<&impl Serialize>,
    ) -> Result<Response, Error> {
        let mut request = self.http.request(method, url);
        if let Some(body) = body {
            request = request.json(body);
        }
        send(operation, request).await
    }

    fn url(&self, path: &str) -> Url {
        self.api.join(path).expect("an API path is relative")
    }

    async fn get_json<T: DeserializeOwned>(
        &self,
        operation: &'static str,
        path: &str,
        query: &[(&str, String)],
    ) -> Result<T, Error> {
        let response = self.get(operation, path, query).await?;
        decode(operation, response).await
    }

    async fn get_page<T: DeserializeOwned>(
        &self,
        operation: &'static str,
        path: &str,
        query: &[(&str, String)],
        requested: u32,
    ) -> Result<Page<T>, Error> {
        let response = self.get(operation, path, query).await?;
        let headers = response.headers().clone();
        let items = decode(operation, response).await?;
        Ok(Page {
            items,
            page: header_number(&headers, "x-page").unwrap_or(requested),
            next_page: header_number(&headers, "x-next-page"),
            total_pages: header_number(&headers, "x-total-pages"),
            total_items: header_number(&headers, "x-total"),
        })
    }

    async fn get_all<T: DeserializeOwned>(
        &self,
        operation: &'static str,
        path: &str,
    ) -> Result<Vec<T>, Error> {
        let mut items = Vec::new();
        let mut page = 1;
        loop {
            let query = [
                ("page", page.to_string()),
                ("per_page", ALL_PAGES_SIZE.to_string()),
            ];
            let next = self.get_page(operation, path, &query, page).await?;
            items.extend(next.items);
            match next.next_page {
                Some(number) if number > page => page = number,
                // A next page that does not advance would repeat the request forever.
                Some(number) => {
                    return Err(Error {
                        kind: ErrorKind::Decode,
                        operation,
                        detail: format!("GitLab named page {number} as the page after {page}"),
                    });
                }
                None => return Ok(items),
            }
        }
    }
}

/// reqwest strips only the standard credential headers when a redirect leaves the origin, so
/// `PRIVATE-TOKEN` would travel to whatever host the redirect names. A redirect that leaves the
/// origin stops instead, and its 3xx answer is reported as a refusal.
fn same_origin_only(attempt: redirect::Attempt) -> redirect::Action {
    let first = &attempt.previous()[0];
    let next = attempt.url();
    let same = first.scheme() == next.scheme()
        && first.host_str() == next.host_str()
        && first.port_or_known_default() == next.port_or_known_default();
    if same && attempt.previous().len() < MAX_REDIRECTS {
        attempt.follow()
    } else {
        attempt.stop()
    }
}

/// The API root under the host, keeping a sub-path install and not doubling `/api/v4`.
fn api_base(host: &Url) -> Url {
    let mut base = host.clone();
    let path = base
        .path()
        .trim_end_matches('/')
        .trim_end_matches("/api/v4")
        .to_string();
    base.set_path(&format!("{path}/{API_PATH}"));
    base
}

/// GitLab leaves a header empty rather than out when it has no value, as for the next page
/// on the last page.
fn header_number<T: FromStr>(headers: &HeaderMap, name: &str) -> Option<T> {
    headers.get(name)?.to_str().ok()?.trim().parse().ok()
}

async fn send(operation: &'static str, request: RequestBuilder) -> Result<Response, Error> {
    request
        .send()
        .await
        .map_err(|e| Error::transport(operation, e))
}

async fn decode<T: DeserializeOwned>(
    operation: &'static str,
    response: Response,
) -> Result<T, Error> {
    response
        .json()
        .await
        .map_err(|e| Error::transport(operation, e))
}

async fn refusal(operation: &'static str, response: Response) -> Result<Response, Error> {
    if response.status().is_success() {
        return Ok(response);
    }
    Err(rejection(operation, response).await)
}

async fn rejection(operation: &'static str, response: Response) -> Error {
    let status = response.status();
    let kind = match status {
        StatusCode::UNAUTHORIZED => ErrorKind::Unauthorized,
        StatusCode::FORBIDDEN => ErrorKind::Forbidden,
        StatusCode::NOT_FOUND => ErrorKind::NotFound,
        StatusCode::TOO_MANY_REQUESTS => ErrorKind::RateLimited,
        s if s.is_server_error() => ErrorKind::Server,
        _ => ErrorKind::Rejected,
    };
    let body = response.text().await.unwrap_or_default();
    Error {
        kind,
        operation,
        detail: format!("GitLab answered {status}{}", reason(&body)),
    }
}

/// GitLab puts its reason in `message`, or in `error` for an OAuth refusal.
fn reason(body: &str) -> String {
    let Ok(value) = serde_json::from_str::<serde_json::Value>(body) else {
        return String::new();
    };
    match value.get("message").or_else(|| value.get("error")) {
        Some(serde_json::Value::String(text)) => format!(": {text}"),
        Some(other) => format!(": {other}"),
        None => String::new(),
    }
}

fn chain(error: &dyn std::error::Error) -> String {
    let mut text = error.to_string();
    let mut source = error.source();
    while let Some(cause) = source {
        text.push_str(&format!(": {cause}"));
        source = cause.source();
    }
    text
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
    pub ssh_url_to_repo: String,
    #[serde(default)]
    pub visibility: String,
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
    /// The answer is larger than lazylab reads, as for a job log past `MAX_JOB_LOG_BYTES`.
    TooLarge,
}

#[derive(Debug)]
pub struct Error {
    pub kind: ErrorKind,
    operation: &'static str,
    detail: String,
}

impl Error {
    fn transport(operation: &'static str, e: reqwest::Error) -> Error {
        let kind = if e.is_decode() {
            ErrorKind::Decode
        } else {
            ErrorKind::Network
        };
        let e = e.without_url();
        Error {
            kind,
            operation,
            detail: chain(&e),
        }
    }

    pub fn exit_code(&self) -> u8 {
        match self.kind {
            ErrorKind::Unauthorized | ErrorKind::Forbidden => exit::UNAUTHORIZED,
            ErrorKind::RateLimited | ErrorKind::Server => exit::TRANSIENT,
            ErrorKind::Network => exit::NETWORK,
            ErrorKind::NotFound
            | ErrorKind::Rejected
            | ErrorKind::Decode
            | ErrorKind::Setup
            | ErrorKind::TooLarge => exit::GENERIC,
        }
    }
}

/// GitLab's reason can quote the request, so the text goes through `redact`.
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.operation, redact(&self.detail))
    }
}
