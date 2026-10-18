use std::path::{Component, Path, PathBuf};
use std::process::Command;
use std::{fs, iter};

use base64::prelude::{BASE64_STANDARD, Engine};
use lazylab::checkout::{Checkouts, Revision};
use lazylab::config::Token;
use tempfile::TempDir;
use url::Url;

const TOKEN: &str = "glpat-abcdefghijklmnopqrst";
const PROJECT: &str = "acme/platform/api";

/// Runs git for the test's own repositories, away from the reader's git configuration, and
/// returns its trimmed output.
fn git(dir: &Path, args: &[&str]) -> String {
    let output = Command::new("git")
        .args(["-c", "user.name=t", "-c", "user.email=t@example.com"])
        .args(["-c", "commit.gpgsign=false"])
        .args(args)
        .current_dir(dir)
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .output()
        .unwrap();
    assert!(output.status.success(), "git {args:?}: {output:?}");
    String::from_utf8(output.stdout).unwrap().trim().to_string()
}

/// A bare repository at `<remote>/acme/platform/api.git`, as a GitLab at the address `remote`
/// serves the project, and a working copy that pushes to it.
struct Remote {
    _dir: TempDir,
    host: Url,
    work: PathBuf,
}

impl Remote {
    fn new() -> Remote {
        let dir = TempDir::new().unwrap();
        let bare = dir.path().join("remote").join(format!("{PROJECT}.git"));
        fs::create_dir_all(&bare).unwrap();
        git(&bare, &["init", "--quiet", "--bare"]);
        let work = dir.path().join("work");
        fs::create_dir(&work).unwrap();
        git(&work, &["init", "--quiet"]);
        let host = Url::from_directory_path(dir.path().join("remote")).unwrap();
        Remote {
            _dir: dir,
            host,
            work,
        }
    }

    /// Commits `text` as the file `README` and pushes it to `branch`, and returns the commit.
    fn push(&self, branch: &str, text: &str) -> String {
        fs::write(self.work.join("README"), text).unwrap();
        git(&self.work, &["add", "README"]);
        git(&self.work, &["commit", "--quiet", "-m", text]);
        let bare = format!("{}{PROJECT}.git", self.host);
        git(
            &self.work,
            &["push", "--quiet", &bare, &format!("HEAD:{branch}")],
        );
        git(&self.work, &["rev-parse", "HEAD"])
    }

    fn checkouts(&self, cache: &Path) -> Checkouts {
        Checkouts::new(cache, &self.host, Token::new(TOKEN))
    }
}

fn readme(dir: &Path) -> String {
    fs::read_to_string(dir.join("README")).unwrap()
}

fn branch(name: &str) -> Revision {
    Revision::Branch(name.to_string())
}

#[test]
fn a_branch_is_checked_out_with_its_last_commit_only_and_the_folder_is_reused() {
    // Given: a project whose main branch has two commits.
    let remote = Remote::new();
    remote.push("main", "one");
    remote.push("main", "two");
    let cache = TempDir::new().unwrap();
    let checkouts = remote.checkouts(cache.path());

    // When: main is checked out, gains a commit, and is checked out again.
    let first = checkouts.check_out(PROJECT, &branch("main")).unwrap();
    let first_text = readme(&first);
    remote.push("main", "three");
    let second = checkouts.check_out(PROJECT, &branch("main")).unwrap();

    // Then: each checkout holds the branch as it stood, from one commit, in the same folder.
    assert_eq!(first_text, "two");
    assert_eq!(second, first);
    assert_eq!(readme(&second), "three");
    assert_eq!(
        git(&second, &["rev-parse", "--is-shallow-repository"]),
        "true"
    );
    assert_eq!(git(&second, &["rev-list", "--count", "HEAD"]), "1");
}

#[test]
fn a_pipeline_commit_is_checked_out_by_its_sha_when_no_branch_ends_there() {
    // Given: a commit that main has moved past.
    let remote = Remote::new();
    let older = remote.push("main", "one");
    remote.push("main", "two");
    let cache = TempDir::new().unwrap();

    // When: the older commit is checked out.
    let dir = (remote.checkouts(cache.path()))
        .check_out(PROJECT, &Revision::Commit(older.clone()))
        .unwrap();

    // Then: the checkout holds that commit alone.
    assert_eq!(readme(&dir), "one");
    assert_eq!(git(&dir, &["rev-parse", "HEAD"]), older);
    assert_eq!(git(&dir, &["rev-list", "--count", "HEAD"]), "1");
}

#[test]
fn a_ref_that_looks_like_an_option_reaches_git_as_a_ref() {
    // Given: a project, and refs that would run a program if git read them as an option.
    let remote = Remote::new();
    remote.push("main", "one");
    let cache = TempDir::new().unwrap();
    let marker = cache.path().join("ran");
    let option = format!("--upload-pack=touch {}", marker.display());
    let checkouts = remote.checkouts(cache.path());

    // When: each is checked out.
    let as_branch = checkouts.check_out(PROJECT, &branch(&option));
    let as_commit = checkouts.check_out(PROJECT, &Revision::Commit(option.clone()));

    // Then: both fail, and git ran nothing the ref named.
    assert!(as_branch.is_err() && as_commit.is_err());
    assert!(!marker.exists(), "git read the ref as an option");
}

#[test]
fn the_folder_is_one_name_for_the_project_and_one_for_the_ref_under_the_host() {
    // Given: checkouts for a GitLab host.
    let cache = PathBuf::from("/cache/lazylab");
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let checkouts = Checkouts::new(&cache, &host, Token::new(TOKEN));
    let root = cache.join("checkouts").join("https_gitlab_example_com");
    let hostile = [
        ("../../x", "main"),
        (PROJECT, "../../x"),
        (PROJECT, ".."),
        (".", "."),
        (PROJECT, "-x"),
        (PROJECT, "feature/a"),
        (PROJECT, "a\0b\\c%2F"),
    ];

    // When: each project and ref is given a folder.
    for (project, git_ref) in hostile {
        let folder = checkouts.folder(project, &branch(git_ref));

        // Then: the folder sits two names below the root, and no name climbs out of it.
        let below: Vec<Component> = folder.strip_prefix(&root).unwrap().components().collect();
        assert_eq!(below.len(), 2, "{project} at {git_ref}: {folder:?}");
        assert!(
            below.iter().all(|c| matches!(c, Component::Normal(_))),
            "{project} at {git_ref}: {folder:?}"
        );
    }
    // And: names that differ only in a character the folder name replaces stay apart.
    let folder = |project: &str, git_ref: &str| checkouts.folder(project, &branch(git_ref));
    assert_ne!(folder(PROJECT, "a/b"), folder(PROJECT, "a_b"));
    assert_ne!(folder(PROJECT, "a/b"), folder(PROJECT, "a%2Fb"));
    assert_ne!(folder("acme/a", "main"), folder("acme_a", "main"));
}

#[test]
fn the_token_reaches_git_only_through_the_environment_of_the_fetch() {
    // Given: checkouts for a GitLab host.
    let cache = TempDir::new().unwrap();
    let host = Url::parse("https://gitlab.example.com").unwrap();
    let checkouts = Checkouts::new(cache.path(), &host, Token::new(TOKEN));

    // When: the git commands of a checkout are built.
    let commands = checkouts.commands(PROJECT, &branch("main")).unwrap();

    // Then: no argument holds the token in any form, and the fetch names the project's address.
    let encoded = BASE64_STANDARD.encode(format!("oauth2:{TOKEN}"));
    let arguments: Vec<String> = (commands.iter())
        .flat_map(|c| iter::once(c.get_program()).chain(c.get_args()))
        .map(|a| a.to_string_lossy().into_owned())
        .collect();
    for argument in &arguments {
        assert!(
            !argument.contains(TOKEN) && !argument.contains(&encoded),
            "{argument}"
        );
    }
    let remote = "https://gitlab.example.com/acme/platform/api.git";
    assert!(arguments.iter().any(|a| a == remote), "{arguments:?}");

    // And: the fetch carries the header in git's configuration variables, and never prompts.
    let env = |command: &Command, name: &str| {
        (command.get_envs())
            .find(|(key, _)| *key == name)
            .and_then(|(_, value)| value)
            .map(|value| value.to_string_lossy().into_owned())
    };
    let fetch = (commands.iter())
        .find(|c| c.get_args().any(|a| a == "fetch"))
        .expect("a fetch");
    assert_eq!(env(fetch, "GIT_CONFIG_COUNT").as_deref(), Some("1"));
    assert_eq!(
        env(fetch, "GIT_CONFIG_KEY_0").as_deref(),
        Some("http.extraHeader")
    );
    assert_eq!(
        env(fetch, "GIT_CONFIG_VALUE_0"),
        Some(format!("Authorization: Basic {encoded}"))
    );
    assert_eq!(env(fetch, "GIT_TERMINAL_PROMPT").as_deref(), Some("0"));
    // And: no other command gets the header.
    for command in commands.iter().filter(|c| !std::ptr::eq(*c, fetch)) {
        assert_eq!(env(command, "GIT_CONFIG_VALUE_0"), None);
    }
}
