//! Reads the GitLab credentials the glab CLI has stored, so a glab login needs no
//! `GITLAB_TOKEN`.

use std::time::Duration;

#[derive(Debug, PartialEq)]
pub struct Credentials {
    pub token: String,
    pub host: String,
}

/// Runs glab with these arguments and returns its trimmed stdout and stderr, or `None` when it
/// is missing, fails or times out.
type Run<'a> = &'a dyn Fn(&[&str]) -> Option<(String, String)>;

pub fn resolve(host_hint: Option<&str>) -> Option<Credentials> {
    resolve_with(
        &|args| run_with_timeout("glab", args, Duration::from_secs(3)),
        host_hint,
    )
}

fn resolve_with(_run: Run, _host_hint: Option<&str>) -> Option<Credentials> {
    None
}

fn run_with_timeout(
    _program: &str,
    _args: &[&str],
    _timeout: Duration,
) -> Option<(String, String)> {
    Some((String::new(), String::new()))
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
            token: token.into(),
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

    #[test]
    fn a_command_that_finishes_returns_its_trimmed_output() {
        let result = run_with_timeout(
            "sh",
            &["-c", "echo ' out '; echo ' err ' >&2"],
            Duration::from_secs(5),
        );
        assert_eq!(result, Some(("out".into(), "err".into())));
    }
}
