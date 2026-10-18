//! The glab commands that act on an item lazylab shows, as the Go `glabcmd` package maps them.
//!
//! Lazylab browses any project, not the repository of the working directory, so every command
//! names its project with `-R`.

use url::Url;

/// glab's own default host, where a bare project path is enough.
const DEFAULT_HOST: &str = "gitlab.com";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Item<'a> {
    Project,
    Pipeline { id: u64, git_ref: &'a str },
    Job { id: u64 },
    MergeRequest { iid: u64 },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct GlabCommand {
    pub label: &'static str,
    pub line: String,
}

/// The commands for `item` in `project`, the most natural first. None without a project path.
pub fn commands(host: Option<&Url>, project: &str, item: Item) -> Vec<GlabCommand> {
    if project.is_empty() {
        return Vec::new();
    }
    let target = shell_quote(&repo(host, project));
    let command = |label, line| GlabCommand { label, line };
    match item {
        Item::Project => vec![command("View project", format!("glab repo view {target}"))],
        Item::Pipeline { id, git_ref } => {
            let mut commands = vec![command(
                "Get pipeline by ID",
                format!("glab ci get -p {id} -R {target}"),
            )];
            // `ci view` takes a branch or a tag, and a detached ref such as
            // refs/merge-requests/57/head is neither.
            if !git_ref.is_empty() && !git_ref.starts_with("refs/") {
                commands.push(command(
                    "View latest pipeline on ref",
                    format!("glab ci view {} -R {target}", shell_quote(git_ref)),
                ));
            }
            commands.push(command(
                "List pipelines",
                format!("glab ci list -R {target}"),
            ));
            commands.push(command(
                "Cancel pipeline",
                format!("glab ci cancel pipeline {id} -R {target}"),
            ));
            commands
        }
        Item::Job { id } => vec![
            command("Trace job log", format!("glab ci trace {id} -R {target}")),
            command("Retry job", format!("glab ci retry {id} -R {target}")),
            command("Cancel job", format!("glab ci cancel job {id} -R {target}")),
        ],
        Item::MergeRequest { iid } => vec![
            command(
                "View merge request",
                format!("glab mr view {iid} -R {target}"),
            ),
            command(
                "Diff merge request",
                format!("glab mr diff {iid} -R {target}"),
            ),
        ],
    }
}

/// A bare path would resolve against the default host of the reader's glab, so a project on
/// another instance is named by its full address.
fn repo(host: Option<&Url>, project: &str) -> String {
    match host {
        Some(host) if host.host_str() != Some(DEFAULT_HOST) => {
            format!("{}/{project}", host.as_str().trim_end_matches('/'))
        }
        _ => project.to_string(),
    }
}

/// A git ref can hold `;`, `$`, `&` or a backtick, which would run a second command when the
/// copied line is pasted into a shell. Anything but the characters a POSIX shell reads literally
/// goes inside single quotes.
fn shell_quote(word: &str) -> String {
    let literal = |c: char| c.is_ascii_alphanumeric() || "@%_+=:,./-".contains(c);
    if !word.is_empty() && word.chars().all(literal) {
        return word.to_string();
    }
    format!("'{}'", word.replace('\'', r"'\''"))
}
