use std::env;
use std::ffi::OsString;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Component, Path, PathBuf};

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

/// `to` written relative to the working folder of the test, as a relative PATH entry is read.
fn relative(to: &Path) -> PathBuf {
    let here = env::current_dir().unwrap();
    let up = here
        .components()
        .filter(|c| matches!(c, Component::Normal(_)));
    let mut path: PathBuf = up.map(|_| "..").collect();
    path.push(to.strip_prefix("/").unwrap());
    path
}

#[test]
fn the_explorer_opens_the_checkout_with_the_project_address_and_ref_in_its_environment() {
    // Given: an explorer that writes its arguments and its environment to a file.
    let temp = TempDir::new().unwrap();
    let checkout = temp.path().join("checkout");
    fs::create_dir(&checkout).unwrap();
    let seen = temp.path().join("seen");
    let program = script(
        temp.path(),
        "explorer",
        &format!("{{ printf '%s\\n' \"$@\"; env; }} > '{}'", seen.display()),
    );

    // When: it browses a checkout whose ref carries an escape sequence.
    let ended = explorer::browse(
        program.to_str().unwrap(),
        None,
        &browse(&checkout, "\x1b]0;title\x07main"),
    );

    // Then: it was given the checkout, and ended well.
    assert_eq!(ended, Ended::Closed);
    let seen = fs::read_to_string(seen).unwrap();
    let lines: Vec<&str> = seen.lines().collect();
    assert_eq!(Path::new(lines[0]), checkout);
    // And: its environment holds the address and the filtered ref, and none of git's settings.
    let env = &lines[1..];
    assert!(env.contains(&"LAZYLAB_PROJECT_URL=https://gitlab.example.com/acme/platform/api"));
    assert!(env.contains(&"LAZYLAB_REF=main"), "{env:?}");
    assert!(
        !env.iter().any(|line| line.starts_with("GIT_CONFIG_")),
        "{env:?}"
    );
}

#[test]
fn a_bare_name_runs_from_an_absolute_path_entry_and_never_from_the_checkout() {
    // Given: a checkout that holds a program named yazi, a folder that holds another, and a
    // PATH whose relative entries come first, one of them naming the checkout.
    let temp = TempDir::new().unwrap();
    let checkout = temp.path().join("checkout");
    let bin = temp.path().join("bin");
    fs::create_dir(&checkout).unwrap();
    fs::create_dir(&bin).unwrap();
    let marker = |name: &str| temp.path().join(name);
    let touch = |name: &str| format!("touch '{}'", marker(name).display());
    script(&checkout, "yazi", &touch("from-checkout"));
    script(&bin, "yazi", &touch("from-bin"));
    let entries = [PathBuf::from("."), PathBuf::new(), relative(&checkout), bin];
    let search: OsString = env::join_paths(entries).unwrap();

    // When: yazi browses the checkout.
    let ended = explorer::browse("yazi", Some(&search), &browse(&checkout, "main"));

    // Then: the yazi of the folder ran, and the yazi of the checkout did not.
    assert_eq!(ended, Ended::Closed);
    assert!(marker("from-bin").exists());
    assert!(!marker("from-checkout").exists());
}

#[test]
fn the_end_of_the_explorer_says_whether_it_ran_and_how_it_exited() {
    // Given: an explorer that succeeds, one that fails, one that cannot run, one not there,
    // and a setting that names a program relative to the working folder.
    let temp = TempDir::new().unwrap();
    let closed = script(temp.path(), "closed", "exit 0");
    let failed = script(temp.path(), "failed", "exit 3");
    let blocked = script(temp.path(), "blocked", "exit 0");
    fs::set_permissions(&blocked, fs::Permissions::from_mode(0o644)).unwrap();
    let missing = temp.path().join("missing");
    let search = OsString::from(temp.path());
    let run =
        |program: &str| explorer::browse(program, Some(&search), &browse(temp.path(), "main"));
    let path = |program: &Path| program.to_str().unwrap().to_string();

    // When: each browses a folder.
    let ends = [
        run(&path(&closed)),
        run(&path(&failed)),
        run(&path(&blocked)),
        run(&path(&missing)),
        run("missing"),
        run(&path(&relative(&closed))),
    ];

    // Then: each end is told apart.
    assert_eq!(ends[0], Ended::Closed);
    assert_eq!(ends[1], Ended::Exited(Some(3)));
    assert!(matches!(&ends[2], Ended::NotStarted(_)), "{:?}", ends[2]);
    assert_eq!(ends[3], Ended::Missing);
    assert_eq!(ends[4], Ended::Missing);
    assert_eq!(ends[5], Ended::Relative);
}
