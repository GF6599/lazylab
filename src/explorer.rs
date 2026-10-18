//! Hands the terminal to a file explorer, yazi by default, the way lazygit hands it to an editor.
//!
//! The explorer runs with the reader's own configuration and no chooser file, so `enter` still
//! opens their editor and quitting the explorer returns to lazylab.

use std::io;
use std::path::PathBuf;
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
    NotStarted(String),
}

/// Blocks until the explorer exits. The caller gives it the terminal before and takes it back
/// after. The address and the ref come from GitLab and a key binding can paste them into a shell,
/// so they pass the filter of the clipboard.
pub fn browse(program: &str, browse: &Browse) -> Ended {
    let status = Command::new(program)
        .arg(&browse.dir)
        .current_dir(&browse.dir)
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
