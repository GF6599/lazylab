//! Hands the terminal to a file explorer, yazi by default, the way lazygit hands it to an editor.
//!
//! The explorer runs with the reader's own configuration and no chooser file, so `enter` still
//! opens their editor and quitting the explorer returns to lazylab.

use std::env;
use std::ffi::OsStr;
use std::fs;
use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Command;

use crate::termsafe;

/// The project's web address, so a key binding of the explorer can build a file's GitLab address.
pub const PROJECT_URL: &str = "LAZYLAB_PROJECT_URL";
/// The branch or the commit the checkout holds.
pub const REF: &str = "LAZYLAB_REF";

/// A checkout to browse, and where its files are on GitLab.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Browse {
    pub dir: PathBuf,
    pub project_url: String,
    pub git_ref: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Ended {
    Closed,
    /// `None` when a signal ended it.
    Exited(Option<i32>),
    Missing,
    /// The setting names a path relative to the working folder, which is refused.
    Relative,
    NotStarted(String),
}

/// Blocks until the explorer exits. The caller gives it the terminal before and takes it back
/// after. `search` is the `PATH` to find a bare program name in. The address and the ref come
/// from GitLab and a key binding can paste them into a shell, so they pass the filter of the
/// clipboard.
pub fn browse(program: &str, search: Option<&OsStr>, browse: &Browse) -> Ended {
    let program = match locate(program, search) {
        Ok(program) => program,
        Err(ended) => return ended,
    };
    let status = Command::new(program)
        .arg(&browse.dir)
        .env(PROJECT_URL, termsafe::filter(&browse.project_url))
        .env(REF, termsafe::filter(&browse.git_ref))
        .status();
    match status {
        Ok(status) if status.success() => Ended::Closed,
        Ok(status) => Ended::Exited(status.code()),
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ended::Missing,
        Err(e) => Ended::NotStarted(e.to_string()),
    }
}

/// The program as an absolute path. A relative entry of `PATH` is skipped, because the files
/// under it can be a checkout, and a checkout holds whatever was pushed to the repository.
fn locate(program: &str, search: Option<&OsStr>) -> Result<PathBuf, Ended> {
    let named = Path::new(program);
    if named.is_absolute() {
        return Ok(named.to_path_buf());
    }
    if program.contains('/') {
        return Err(Ended::Relative);
    }
    let entries = search.map(env::split_paths).into_iter().flatten();
    (entries.filter(|dir| dir.is_absolute()))
        .map(|dir| dir.join(named))
        .find(|path| executable(path))
        .ok_or(Ended::Missing)
}

fn executable(path: &Path) -> bool {
    fs::metadata(path).is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
}
