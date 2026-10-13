use std::process::{Command, Output};

fn lazylab(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_lazylab"))
        .args(args)
        .output()
        .expect("the binary runs")
}

#[test]
fn version_prints_the_name_and_the_package_version() {
    // When: the reader asks for the version.
    let out = lazylab(&["--version"]);
    // Then: it prints the binary name and the package version, and succeeds.
    assert!(out.status.success());
    assert_eq!(
        String::from_utf8_lossy(&out.stdout),
        format!("lazylab {}\n", env!("CARGO_PKG_VERSION"))
    );
}

#[test]
fn help_names_the_tool() {
    // When: the reader asks for help.
    let out = lazylab(&["--help"]);
    // Then: the help says what the tool is, and succeeds.
    assert!(out.status.success());
    assert!(String::from_utf8_lossy(&out.stdout).contains("Terminal UI for GitLab"));
}

/// Exit code 2 means a transient failure that is safe to retry, so a typo must not use it.
#[test]
fn a_positional_argument_is_rejected_with_the_generic_exit_code() {
    // When: the reader types a verb lazylab does not have.
    let out = lazylab(&["pipelines"]);
    // Then: it exits with the generic code and names the argument it rejected.
    assert_eq!(out.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&out.stderr).contains("pipelines"));
}

/// clap echoes an unexpected argument, so a token passed by mistake would land in scrollback.
#[test]
fn a_token_passed_as_an_argument_is_not_echoed() {
    // When: a token is passed where no argument belongs.
    let out = lazylab(&["glpat-abcdefghijklmnopqrst"]);
    // Then: the usage error names a placeholder in its place.
    let stderr = String::from_utf8_lossy(&out.stderr);
    assert_eq!(out.status.code(), Some(1));
    assert!(!stderr.contains("glpat-abcdefghijklmnopqrst"), "{stderr}");
    assert!(stderr.contains("[REDACTED-TOKEN]"), "{stderr}");
}
