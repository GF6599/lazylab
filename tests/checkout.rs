use std::path::{Component, Path, PathBuf};
use std::process::Command;
use std::{fs, iter};

use base64::prelude::{BASE64_STANDARD, Engine};
use lazylab::checkout::{Checkouts, Revision};
use lazylab::config::Token;
use tempfile::TempDir;
use url::Url;
use wiremock::matchers::any;
use wiremock::{Mock, MockServer, ResponseTemplate};

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

    /// Moves the project away, so every later fetch of it fails.
    fn close(&self) {
        let bare = self
            .host
            .to_file_path()
            .unwrap()
            .join(format!("{PROJECT}.git"));
        fs::rename(&bare, bare.with_extension("gone")).unwrap();
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
fn a_commit_checked_out_before_opens_again_without_a_fetch() {
    // Given: a commit that is checked out, and a project that no fetch can reach any more.
    let remote = Remote::new();
    let commit = Revision::Commit(remote.push("main", "one"));
    let cache = TempDir::new().unwrap();
    let checkouts = remote.checkouts(cache.path());
    let first = checkouts.check_out(PROJECT, &commit).unwrap();
    remote.close();

    // When: the same commit is checked out again.
    let second = checkouts.check_out(PROJECT, &commit);

    // Then: it opens the same folder, which a fetch could not have filled.
    assert_eq!(second.unwrap(), first);
    assert_eq!(readme(&first), "one");
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
fn the_token_reaches_git_only_through_the_environment_of_the_fetch_for_the_configured_origin() {
    // Given: checkouts for a GitLab host on a port of its own.
    let cache = TempDir::new().unwrap();
    let host = Url::parse("https://gitlab.example.com:8443").unwrap();
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
    let remote = "https://gitlab.example.com:8443/acme/platform/api.git";
    assert!(arguments.iter().any(|a| a == remote), "{arguments:?}");

    // And: the fetch sends the header to the configured origin only, follows no redirect, and
    // never prompts.
    let settings = |command: &Command| {
        let mut set: Vec<(String, String)> = (command.get_envs())
            .filter_map(|(key, value)| Some((key.to_str()?, value?.to_str()?)))
            .filter(|(key, _)| key.starts_with("GIT_"))
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect();
        set.sort();
        set
    };
    let fetch = (commands.iter())
        .find(|c| c.get_args().any(|a| a == "fetch"))
        .expect("a fetch");
    let pair = |key: &str, value: &str| (key.to_string(), value.to_string());
    assert_eq!(
        settings(fetch),
        [
            pair("GIT_CONFIG_COUNT", "2"),
            pair(
                "GIT_CONFIG_KEY_0",
                "http.https://gitlab.example.com:8443/.extraHeader"
            ),
            pair("GIT_CONFIG_KEY_1", "http.followRedirects"),
            pair(
                "GIT_CONFIG_VALUE_0",
                &format!("Authorization: Basic {encoded}")
            ),
            pair("GIT_CONFIG_VALUE_1", "false"),
            pair("GIT_TERMINAL_PROMPT", "0"),
        ]
    );
    // And: no other command gets any of git's settings.
    for command in commands.iter().filter(|c| !std::ptr::eq(*c, fetch)) {
        assert_eq!(settings(command), []);
    }
}

#[tokio::test]
async fn a_redirect_from_the_configured_host_never_carries_the_token_elsewhere() {
    // Given: a GitLab that redirects every request to a second server, which records what it
    // receives.
    let elsewhere = MockServer::start().await;
    let gitlab = MockServer::start().await;
    let target = format!("{}/{PROJECT}.git/info/refs", elsewhere.uri());
    Mock::given(any())
        .respond_with(ResponseTemplate::new(302).insert_header("Location", target.as_str()))
        .mount(&gitlab)
        .await;
    let cache = TempDir::new().unwrap();
    let host = Url::parse(&gitlab.uri()).unwrap();
    let checkouts = Checkouts::new(cache.path(), &host, Token::new(TOKEN));

    // When: a checkout runs against it.
    let ended = tokio::task::spawn_blocking(move || checkouts.check_out(PROJECT, &branch("main")))
        .await
        .unwrap();

    // Then: the checkout fails, the configured host got the token, and the second server got
    // no request. The check does not rest on whether curl drops the header on a redirect.
    assert!(ended.is_err());
    let asked = gitlab.received_requests().await.unwrap();
    assert!(
        asked
            .iter()
            .any(|r| r.headers.contains_key("authorization"))
    );
    let redirected = elsewhere.received_requests().await.unwrap();
    assert!(redirected.is_empty(), "{redirected:?}");
}
