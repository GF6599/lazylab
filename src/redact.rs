//! Removes GitLab credentials from text before it reaches a log line, stderr or the screen.

use std::sync::LazyLock;

use regex::Regex;

/// Applied in this order, so `Authorization: Bearer glpat-x` collapses into the header
/// placeholder whole rather than leaving the scheme or the token behind.
///
/// A field ends at `[^\t\n\x0C\r ]`, the ASCII whitespace Go's RE2 means by `\s`. Rust's `\s` is
/// Unicode, so it stops at a no-break space and would print the rest of the credential. No rule
/// crosses a newline, so the error text after a header survives.
static RULES: LazyLock<[(Regex, &str); 5]> = LazyLock::new(|| {
    [
        (
            r"[Aa]uthorization:[ \t]*[^\t\n\x0C\r ]+([ \t]+[^\t\n\x0C\r ]+)*",
            "Authorization: [REDACTED]",
        ),
        (
            r"(?i)private-token:[ \t]*[^\t\n\x0C\r ]+",
            "PRIVATE-TOKEN: [REDACTED]",
        ),
        (
            r"(?i)((?:private|access)_token)=[^&\t\n\x0C\r ]+",
            "${1}=[REDACTED]",
        ),
        (
            r"[Bb]earer[ \t]+[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]+)*",
            "Bearer [REDACTED]",
        ),
        (
            r"gl[a-zA-Z0-9]{1,8}-[a-zA-Z0-9_-]{6,}(?:\.[a-zA-Z0-9_-]+)*",
            "[REDACTED-TOKEN]",
        ),
    ]
    .map(|(pattern, replacement)| (Regex::new(pattern).expect("a valid pattern"), replacement))
});

pub fn redact(text: &str) -> String {
    RULES
        .iter()
        .fold(text.to_string(), |text, (pattern, replacement)| {
            pattern.replace_all(&text, *replacement).into_owned()
        })
}

#[cfg(test)]
mod tests {
    use super::redact;

    /// The cases of the Go `TestRedact` table, so both releases scrub the same shapes.
    #[test]
    fn every_credential_shape_is_replaced_and_plain_text_is_kept() {
        // Given: each credential shape, and plain text with none.
        let cases = [
            (
                "error: glpat-abcdefghijklmnopqrstuvwxyz",
                "error: [REDACTED-TOKEN]",
            ),
            (
                "failed with gloas-1234567890abcdefghij",
                "failed with [REDACTED-TOKEN]",
            ),
            (
                "Authorization: Bearer abc123xyz",
                "Authorization: [REDACTED]",
            ),
            ("token: Bearer abc123xyz456", "token: Bearer [REDACTED]"),
            (
                "Authorization: glpat-secret123456789",
                "Authorization: [REDACTED]",
            ),
            (
                "tokens: glpat-abc123 and gldt-xyz789",
                "tokens: [REDACTED-TOKEN] and [REDACTED-TOKEN]",
            ),
            ("normal log message", "normal log message"),
            (
                "GET https://gitlab.com/api?token=glpat-secret",
                "GET https://gitlab.com/api?token=[REDACTED-TOKEN]",
            ),
            ("found glcbt-abc123def456", "found [REDACTED-TOKEN]"),
            ("key=glx-abc123def", "key=[REDACTED-TOKEN]"),
            ("leak: glPAT-abc123def456", "leak: [REDACTED-TOKEN]"),
            (
                "error: glpat-a1b2c3d4e5f6g7h8i9j0.01.0a1b2c3d4",
                "error: [REDACTED-TOKEN]",
            ),
            (
                "token: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl",
                "token: Bearer [REDACTED]",
            ),
            (
                "PRIVATE-TOKEN: abcdefghijklmnopqrst",
                "PRIVATE-TOKEN: [REDACTED]",
            ),
            (
                "PRIVATE-TOKEN: abcdefghij\u{a0}klmnopqrst",
                "PRIVATE-TOKEN: [REDACTED]",
            ),
            (
                "request failed, Private-Token: abcdefghijklmnopqrst",
                "request failed, PRIVATE-TOKEN: [REDACTED]",
            ),
            (
                "Authorization: Bearer abc123\nPRIVATE-TOKEN: xyz789\nrequest returned 401",
                "Authorization: [REDACTED]\nPRIVATE-TOKEN: [REDACTED]\nrequest returned 401",
            ),
            ("Bearer\nretrying in 5s", "Bearer\nretrying in 5s"),
            (
                "PRIVATE-TOKEN:\nGET /api/v4/projects returned 401",
                "PRIVATE-TOKEN:\nGET /api/v4/projects returned 401",
            ),
            (
                "Authorization:abcdefghijklmnopqrst",
                "Authorization: [REDACTED]",
            ),
            (
                "GET https://gitlab.com/api/v4/projects?private_token=abcdefghijklmnopqrst&per_page=30",
                "GET https://gitlab.com/api/v4/projects?private_token=[REDACTED]&per_page=30",
            ),
            (
                "GET https://gitlab.com/api/v4/projects?access_token=abcdefghijklmnopqrst&per_page=30",
                "GET https://gitlab.com/api/v4/projects?access_token=[REDACTED]&per_page=30",
            ),
            (
                "GET /api?ACCESS_TOKEN=abcdefghijklmnopqrst",
                "GET /api?ACCESS_TOKEN=[REDACTED]",
            ),
        ];
        // When: each input is redacted.
        // Then: every case matches its expected output.
        let failures: Vec<String> = cases
            .iter()
            .filter(|(input, want)| redact(input) != *want)
            .map(|(input, want)| format!("{input:?}\n  got  {:?}\n  want {want:?}", redact(input)))
            .collect();
        assert!(failures.is_empty(), "{}", failures.join("\n"));
    }
}
