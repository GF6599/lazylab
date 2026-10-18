use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};

use lazylab::explorer::{self, Browse, Ended};
use tempfile::TempDir;

/// An executable shell script named `name` with `body` after the shebang.
fn script(dir: &Path, name: &str, body: &str) -> PathBuf {
    let path = dir.join(name);
    fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
    fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
    path
}

fn browse(dir: &Path, git_ref: &str) -> Browse {
    Browse {
        dir: dir.to_path_buf(),
        project_url: "https://gitlab.example.com/acme/platform/api".into(),
        git_ref: git_ref.into(),
    }
}

#[test]
fn the_explorer_opens_the_checkout_with_the_project_address_and_ref_in_its_environment() {
    // Given: an explorer that writes its folder, its arguments and its environment to a file.
    let temp = TempDir::new().unwrap();
    let checkout = temp.path().join("checkout");
    fs::create_dir(&checkout).unwrap();
    let seen = temp.path().join("seen");
    let program = script(
        temp.path(),
        "explorer",
        &format!(
            "{{ pwd -P; printf '%s\\n' \"$@\"; env; }} > '{}'",
            seen.display()
        ),
    );

    // When: it browses a checkout whose ref carries an escape sequence.
    let ended = explorer::browse(
        program.to_str().unwrap(),
        &browse(&checkout, "\x1b]0;title\x07main"),
    );

    // Then: it ran in the checkout, was given the checkout, and ended well.
    assert_eq!(ended, Ended::Closed);
    let seen = fs::read_to_string(seen).unwrap();
    let lines: Vec<&str> = seen.lines().collect();
    let checkout = checkout.canonicalize().unwrap();
    assert_eq!(Path::new(lines[0]), checkout);
    assert_eq!(fs::canonicalize(lines[1]).unwrap(), checkout);
    // And: its environment holds the address and the filtered ref, and none of git's settings.
    let env = &lines[2..];
    assert!(env.contains(&"LAZYLAB_PROJECT_URL=https://gitlab.example.com/acme/platform/api"));
    assert!(env.contains(&"LAZYLAB_REF=main"), "{env:?}");
    assert!(
        !env.iter().any(|line| line.starts_with("GIT_CONFIG_")),
        "{env:?}"
    );
}

#[test]
fn the_end_of_the_explorer_says_whether_it_ran_and_how_it_exited() {
    // Given: an explorer that succeeds, one that fails, one that cannot run, and one not there.
    let temp = TempDir::new().unwrap();
    let closed = script(temp.path(), "closed", "exit 0");
    let failed = script(temp.path(), "failed", "exit 3");
    let blocked = script(temp.path(), "blocked", "exit 0");
    fs::set_permissions(&blocked, fs::Permissions::from_mode(0o644)).unwrap();
    let missing = temp.path().join("missing");
    let run =
        |program: &Path| explorer::browse(program.to_str().unwrap(), &browse(temp.path(), "main"));

    // When: each browses a folder.
    let ends = [run(&closed), run(&failed), run(&blocked), run(&missing)];

    // Then: each end is told apart.
    assert_eq!(ends[0], Ended::Closed);
    assert_eq!(ends[1], Ended::Exited(Some(3)));
    assert!(matches!(&ends[2], Ended::NotStarted(_)), "{:?}", ends[2]);
    assert_eq!(ends[3], Ended::Missing);
}
