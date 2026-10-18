use std::io;
use std::os::unix::process::CommandExt;
use std::process::{Command, Output, Stdio};

fn lazylab(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_lazylab"))
        .args(args)
        .output()
        .expect("the binary runs")
}

/// Runs with no environment, so a token, a config file or a glab login on the developer's machine
/// cannot leak in: with no `PATH` and no `HOME`, glab is not found or finds no login.
fn lazylab_without_env(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_lazylab"))
        .args(args)
        .env_clear()
        .output()
        .expect("the binary runs")
}

/// Runs with no environment in a session of its own. The session has no controlling terminal, so
/// the binary cannot take the terminal of the person who runs the tests, and it stops where it
/// opens the screen.
fn lazylab_without_env_or_terminal(args: &[&str]) -> Output {
    let mut command = Command::new(env!("CARGO_BIN_EXE_lazylab"));
    command.args(args).env_clear().stdin(Stdio::null());
    // SAFETY: setsid is async-signal-safe, so it can run between fork and exec.
    unsafe {
        command.pre_exec(|| match libc::setsid() {
            -1 => Err(io::Error::last_os_error()),
            _ => Ok(()),
        });
    }
    command.output().expect("the binary runs")
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

#[test]
fn no_token_from_any_source_exits_with_the_reason() {
    // When: lazylab starts with no token anywhere.
    let out = lazylab_without_env(&[]);
    // Then: one line names the missing token, and the exit code is the generic one.
    assert_eq!(out.status.code(), Some(1));
    assert_eq!(
        String::from_utf8_lossy(&out.stderr),
        "error: config error: gitlab token is required: set --token, $GITLAB_TOKEN, or run `glab auth login`\n"
    );
}

#[test]
fn demo_starts_with_no_token_from_any_source() {
    // When: lazylab starts with --demo, no token anywhere and no terminal.
    let out = lazylab_without_env_or_terminal(&["--demo"]);
    // Then: the settings load, and the only failure is the screen that cannot open.
    let stderr = String::from_utf8_lossy(&out.stderr);
    assert_eq!(out.status.code(), Some(1));
    assert!(stderr.starts_with("error: terminal error: "), "{stderr}");
    assert!(!stderr.contains("token"), "{stderr}");
}
