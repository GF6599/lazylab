//! Shallow checkouts of a project at one ref, kept under `<os-cache-dir>/lazylab/checkouts/<host>`
//! and reused by the next checkout of the same project and ref.
//!
//! The token reaches git only in the environment of the fetch, so no argument, URL or file holds
//! it, and the process list does not show it.

use std::fmt;
use std::io;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

use base64::prelude::{BASE64_STANDARD, Engine};
use url::Url;

use crate::config::Token;
use crate::redact::redact;
use crate::{store, termsafe};

const ROOT: &str = "checkouts";
/// GitLab takes any user name with a token as the password.
const USER: &str = "oauth2";

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Revision {
    Branch(String),
    /// A full commit SHA, which is how a pipeline names the code it built.
    Commit(String),
}

impl Revision {
    pub fn name(&self) -> &str {
        match self {
            Revision::Branch(name) | Revision::Commit(name) => name,
        }
    }

    /// A branch goes as its full ref and a commit as hex digits, so neither can start with `-`.
    fn refspec(&self) -> Result<String, Error> {
        let sha = |s: &str| matches!(s.len(), 40 | 64) && s.bytes().all(|b| b.is_ascii_hexdigit());
        match self {
            Revision::Branch(name) if !name.is_empty() => Ok(format!("refs/heads/{name}")),
            Revision::Commit(commit) if sha(commit) => Ok(commit.clone()),
            other => Err(Error(format!(
                "{:?} is not a branch or a commit",
                other.name()
            ))),
        }
    }
}

#[derive(Debug)]
pub struct Error(String);

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

#[derive(Clone)]
pub struct Checkouts {
    root: PathBuf,
    host: Url,
    token: Token,
}

impl Checkouts {
    /// `cache` is the lazylab cache folder. The host gets a folder of its own, named as the store
    /// names its files.
    pub fn new(cache: &Path, host: &Url, token: Token) -> Checkouts {
        Checkouts {
            root: cache.join(ROOT).join(store::host_key(host)),
            host: host.clone(),
            token,
        }
    }

    /// The project path and the ref come from GitLab, so each becomes one folder name that
    /// cannot leave the root.
    pub fn folder(&self, project: &str, revision: &Revision) -> PathBuf {
        let root = self.root.join(folder_name(project));
        root.join(folder_name(revision.name()))
    }

    /// `git init`, `git fetch` and `git checkout`, each to run in `folder`. An init of a folder
    /// that holds a repository leaves it as it is, so the same three reuse a checkout.
    pub fn commands(&self, project: &str, revision: &Revision) -> Result<[Command; 3], Error> {
        if project.is_empty() {
            return Err(Error("no project to check out".into()));
        }
        let refspec = revision.refspec()?;
        let remote = format!("{}/{project}.git", self.host.as_str().trim_end_matches('/'));
        let dir = self.folder(project, revision);
        let git = |args: &[&str]| {
            let mut command = Command::new("git");
            command.args(args).current_dir(&dir).stdin(Stdio::null());
            command
        };
        let mut fetch = git(&["fetch", "--quiet", "--depth", "1", "--", &remote, &refspec]);
        let credential = BASE64_STANDARD.encode(format!("{USER}:{}", self.token.expose()));
        fetch
            .env("GIT_TERMINAL_PROMPT", "0")
            .env("GIT_CONFIG_COUNT", "1")
            .env("GIT_CONFIG_KEY_0", "http.extraHeader")
            .env(
                "GIT_CONFIG_VALUE_0",
                format!("Authorization: Basic {credential}"),
            );
        let checkout = git(&["checkout", "--quiet", "--detach", "FETCH_HEAD"]);
        Ok([git(&["init", "--quiet"]), fetch, checkout])
    }

    /// Blocks until git has finished, and returns the folder that holds the files.
    pub fn check_out(&self, project: &str, revision: &Revision) -> Result<PathBuf, Error> {
        let commands = self.commands(project, revision)?;
        let dir = self.folder(project, revision);
        store::create_dir(&dir).map_err(|e| Error(format!("create {}: {e}", dir.display())))?;
        for mut command in commands {
            run(&mut command)?;
        }
        Ok(dir)
    }
}

/// The output is read rather than shown, because the screen is drawn while git runs. The remote
/// writes part of it, so it is filtered before it can reach the screen.
fn run(command: &mut Command) -> Result<(), Error> {
    let step = command
        .get_args()
        .next()
        .map(|a| a.to_string_lossy().into_owned());
    let step = format!("git {}", step.unwrap_or_default());
    let output = command.output().map_err(|e| match e.kind() {
        io::ErrorKind::NotFound => Error("git is not installed".into()),
        _ => Error(format!("{step}: {e}")),
    })?;
    if output.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&output.stderr);
    let reason = stderr
        .lines()
        .rfind(|l| !l.trim().is_empty())
        .unwrap_or("no reason given");
    Err(Error(format!(
        "{step} failed: {}",
        termsafe::filter(&redact(reason.trim()))
    )))
}

/// Letters, digits, `_`, `-` and a `.` after the first character stay, and every other byte is
/// written as `%XX`. `%` is one of those bytes, so two names never share a folder.
fn folder_name(text: &str) -> String {
    let mut name = String::with_capacity(text.len());
    for (at, byte) in text.bytes().enumerate() {
        let kept =
            byte.is_ascii_alphanumeric() || b"_-".contains(&byte) || (byte == b'.' && at > 0);
        if kept {
            name.push(char::from(byte));
        } else {
            name.push_str(&format!("%{byte:02X}"));
        }
    }
    name
}
