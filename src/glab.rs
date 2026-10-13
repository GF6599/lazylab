//! Reads the GitLab credentials the glab CLI has stored, so a glab login needs no
//! `GITLAB_TOKEN`.

use std::io::Read;
use std::process::{Command, Stdio};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use crate::config::Token;

/// A token behind a locked OS keyring makes glab wait on an unlock prompt, and startup must not
/// wait with it.
const TIMEOUT: Duration = Duration::from_secs(3);
const POLL: Duration = Duration::from_millis(10);
const DEFAULT_HOST: &str = "gitlab.com";

#[derive(Debug, PartialEq)]
pub struct Credentials {
    pub token: Token,
    pub host: String,
}

/// Runs glab with these arguments and returns its trimmed stdout and stderr, or `None` when it
/// is missing, fails or times out.
type Run<'a> = &'a dyn Fn(&[&str]) -> Option<(String, String)>;

pub fn resolve(host_hint: Option<&str>) -> Option<Credentials> {
    resolve_with(&|args| run_with_timeout("glab", args, TIMEOUT), host_hint)
}

/// With a hint, only a token stored for that host, so a token never goes to another instance.
/// Without one, glab's default host decides, and its token and host travel together.
fn resolve_with(run: Run, host_hint: Option<&str>) -> Option<Credentials> {
    match host_hint {
        Some(hint) => token_for_host(run, hostname_of(hint)).map(|token| Credentials {
            token,
            host: hint.to_string(),
        }),
        None => {
            let stored_host =
                output(run, &["config", "get", "host"]).unwrap_or_else(|| DEFAULT_HOST.into());
            let token = output(run, &["config", "get", "token"])
                .map(Token)
                .or_else(|| token_for_host(run, hostname_of(&stored_host)))?;
            Some(Credentials {
                token,
                host: host_url(run, &stored_host),
            })
        }
    }
}

fn output(run: Run, args: &[&str]) -> Option<String> {
    run(args).map(|(out, _)| out).filter(|out| !out.is_empty())
}

/// The per-host config entry first, then `glab auth status`, the only way to read a token that
/// lives in the OS keyring rather than in glab's config file. glab prints that report to
/// stderr, and both streams are read in case it moves.
fn token_for_host(run: Run, hostname: &str) -> Option<Token> {
    let stored = output(run, &["config", "get", "token", "--host", hostname]);
    stored
        .or_else(|| {
            let (out, err) = run(&["auth", "status", "--hostname", hostname, "--show-token"])?;
            format!("{out}\n{err}")
                .lines()
                .find_map(|line| line.split_once("Token found:"))
                .map(|(_, token)| token.trim().to_string())
                .filter(|token| !token.is_empty())
        })
        .map(Token)
}

/// glab passes `GITLAB_HOST` through as set, so a stored host can already carry a scheme.
fn host_url(run: Run, stored_host: &str) -> String {
    if stored_host.contains("://") {
        return stored_host.to_string();
    }
    let protocol =
        output(run, &["config", "get", "api_protocol"]).unwrap_or_else(|| "https".into());
    format!("{protocol}://{stored_host}")
}

fn hostname_of(host: &str) -> &str {
    let host = host.split_once("://").map_or(host, |(_, rest)| rest);
    host.split('/').next().unwrap_or(host)
}

fn run_with_timeout(program: &str, args: &[&str], timeout: Duration) -> Option<(String, String)> {
    let mut child = Command::new(program)
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .ok()?;
    let stdout = read_in_background(child.stdout.take()?);
    let stderr = read_in_background(child.stderr.take()?);
    let deadline = Instant::now() + timeout;
    let status = loop {
        if let Some(status) = child.try_wait().ok()? {
            break status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            return None;
        }
        thread::sleep(POLL);
    };
    let (stdout, stderr) = (stdout.join().ok()?, stderr.join().ok()?);
    status
        .success()
        .then(|| (stdout.trim().to_string(), stderr.trim().to_string()))
}

/// Both pipes drain while the command runs, so a full pipe cannot stall it until the deadline.
fn read_in_background(mut pipe: impl Read + Send + 'static) -> JoinHandle<String> {
    thread::spawn(move || {
        let mut text = String::new();
        let _ = pipe.read_to_string(&mut text);
        text
    })
}

#[cfg(test)]
mod tests {
    use std::time::Instant;

    use super::*;

    /// A glab that answers the argument lines it knows, and prints nothing for the rest.
    fn glab(answers: &[(&str, &str, &str)]) -> impl Fn(&[&str]) -> Option<(String, String)> {
        let answers: Vec<(String, String, String)> = answers
            .iter()
            .map(|(args, out, err)| (args.to_string(), out.to_string(), err.to_string()))
            .collect();
        move |args| {
            let line = args.join(" ");
            let found = answers.iter().find(|(known, _, _)| *known == line);
            Some(found.map_or_else(Default::default, |(_, out, err)| (out.clone(), err.clone())))
        }
    }

    fn found(token: &str, host: &str) -> Option<Credentials> {
        Some(Credentials {
            token: Token(token.into()),
            host: host.into(),
        })
    }

    #[test]
    fn without_a_hint_the_token_comes_with_glabs_default_host() {
        // Given: glab answers for its default host in each of the ways it can store a login.
        let stored = glab(&[
            ("config get token", "glpat-stored-token", ""),
            ("config get host", "gitlab.example.com", ""),
            ("config get api_protocol", "https", ""),
        ]);
        let defaults = glab(&[("config get token", "glpat-stored-token", "")]);
        let with_scheme = glab(&[
            ("config get token", "glpat-stored-token", ""),
            ("config get host", "https://gitlab.example.com", ""),
            ("config get api_protocol", "https", ""),
        ]);
        let per_host = glab(&[("config get token --host gitlab.com", "glpat-per-host", "")]);
        let keyring = glab(&[(
            "auth status --hostname gitlab.com --show-token",
            "",
            "gitlab.com\n  ✓ Logged in to gitlab.com as someone (keyring)\n  ✓ Token found: glpat-keyring.01.suffix",
        )]);
        let no_token = glab(&[("config get host", "gitlab.com", "")]);
        let missing = |_: &[&str]| None;

        // When: each resolves without a host hint.
        // Then: the token travels with the host it belongs to, and no token means no credentials.
        assert_eq!(
            resolve_with(&stored, None),
            found("glpat-stored-token", "https://gitlab.example.com")
        );
        assert_eq!(
            resolve_with(&defaults, None),
            found("glpat-stored-token", "https://gitlab.com")
        );
        assert_eq!(
            resolve_with(&with_scheme, None),
            found("glpat-stored-token", "https://gitlab.example.com")
        );
        assert_eq!(
            resolve_with(&per_host, None),
            found("glpat-per-host", "https://gitlab.com")
        );
        assert_eq!(
            resolve_with(&keyring, None),
            found("glpat-keyring.01.suffix", "https://gitlab.com")
        );
        assert_eq!(resolve_with(&no_token, None), None);
        assert_eq!(resolve_with(&missing, None), None);
    }

    #[test]
    fn a_hint_takes_only_the_token_stored_for_that_host() {
        // Given: a default-host token, and per-host or keyring tokens for the hinted host.
        let per_host = glab(&[
            ("config get token", "glpat-default", ""),
            (
                "config get token --host gitlab.internal.example",
                "glpat-internal",
                "",
            ),
        ]);
        let keyring = glab(&[(
            "auth status --hostname gitlab.internal.example --show-token",
            "",
            "  ✓ Token found: glpat-internal-keyring",
        )]);
        let other_host_only = glab(&[("config get token", "glpat-default", "")]);
        let hint = Some("https://gitlab.internal.example");

        // When: each resolves with the hint.
        // Then: the hinted host's token comes back with the hint, and the default token never does.
        assert_eq!(
            resolve_with(&per_host, hint),
            found("glpat-internal", "https://gitlab.internal.example")
        );
        assert_eq!(
            resolve_with(&keyring, hint),
            found("glpat-internal-keyring", "https://gitlab.internal.example")
        );
        assert_eq!(resolve_with(&other_host_only, hint), None);
    }

    #[test]
    fn a_command_that_outlives_the_deadline_is_stopped() {
        // When: a command that sleeps for 5 s runs with a 50 ms deadline.
        let start = Instant::now();
        let result = run_with_timeout("sleep", &["5"], Duration::from_millis(50));
        // Then: it gives up promptly with nothing.
        assert_eq!(result, None);
        assert!(
            start.elapsed() < Duration::from_secs(2),
            "took {:?}",
            start.elapsed()
        );
    }

    /// Runs inside a `Jail`, because a config test's `Jail` moves the working directory of the
    /// whole test process and then deletes it, and the shell reports that on stderr.
    #[test]
    fn a_command_that_finishes_returns_its_trimmed_output() {
        figment::Jail::expect_with(|_| {
            // When: a command prints to both streams with surrounding spaces.
            let result = run_with_timeout(
                "sh",
                &["-c", "echo ' out '; echo ' err ' >&2"],
                Duration::from_secs(5),
            );
            // Then: both come back trimmed.
            assert_eq!(result, Some(("out".into(), "err".into())));
            Ok(())
        });
    }
}
