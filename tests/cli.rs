use std::process::{Command, Output};

fn lazylab(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_lazylab"))
        .args(args)
        .output()
        .expect("the binary runs")
}

#[test]
fn version_prints_the_name_and_the_package_version() {
    let out = lazylab(&["--version"]);
    assert!(out.status.success());
    assert_eq!(
        String::from_utf8_lossy(&out.stdout),
        format!("lazylab {}\n", env!("CARGO_PKG_VERSION"))
    );
}

#[test]
fn help_names_the_tool() {
    let out = lazylab(&["--help"]);
    assert!(out.status.success());
    assert!(String::from_utf8_lossy(&out.stdout).contains("Terminal UI for GitLab"));
}

/// Exit code 2 means a transient failure that is safe to retry, so a typo must not use it.
#[test]
fn a_positional_argument_is_rejected_with_the_generic_exit_code() {
    let out = lazylab(&["pipelines"]);
    assert_eq!(out.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&out.stderr).contains("pipelines"));
}
