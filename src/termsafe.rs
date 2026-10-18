//! Strips the escape sequences that let GitLab text act on the terminal instead of only colouring
//! it, as the Go `termsafe` package does.
//!
//! Anyone who can push a branch or open a merge request chooses the bytes of a name. Copied to
//! the clipboard and pasted into a shell, an escape sequence in one can set the window title,
//! write the clipboard again, or make the terminal answer on stdin as though the reader typed.
//! SGR colour stays, because it only changes how text looks.

const ESC: char = '\x1b';
const BEL: char = '\x07';
const CSI: char = '\u{9b}';
const ST: char = '\u{9c}';
const OSC: char = '\u{9d}';
/// The C1 forms of the DCS, SOS, PM and APC strings.
const C1_STRINGS: [char; 4] = ['\u{90}', '\u{98}', '\u{9e}', '\u{9f}'];

/// `text` without its escape sequences other than SGR, and without the control characters that
/// move or overwrite output. Newline and tab stay, because they are layout.
pub fn filter(text: &str) -> String {
    if !text.chars().any(control) {
        return text.to_string();
    }
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while !rest.is_empty() {
        let (len, keep) = token(rest);
        if keep {
            out.push_str(&rest[..len]);
        }
        rest = &rest[len..];
    }
    out
}

/// A C1 control can arrive as a character, and a terminal can read U+009B as CSI.
fn control(c: char) -> bool {
    c.is_control() && c != '\n' && c != '\t'
}

/// The byte length of the sequence or character at the start of `text`, and whether it may reach
/// the terminal.
fn token(text: &str) -> (usize, bool) {
    let mut chars = text.chars();
    let first = chars.next().unwrap_or_default();
    let introducer = first.len_utf8();
    match (first, chars.next()) {
        (ESC, Some('[')) => csi(text, introducer + 1),
        (ESC, Some(']' | 'P' | 'X' | '^' | '_')) => (string_end(text, introducer + 1), false),
        (ESC, _) => (escape_end(text, introducer), false),
        (CSI, _) => csi(text, introducer),
        (OSC, _) => (string_end(text, introducer), false),
        (c, _) if C1_STRINGS.contains(&c) => (string_end(text, introducer), false),
        (c, _) => (introducer, !control(c)),
    }
}

/// Parameters and intermediates, then one final character. Only SGR is kept: final `m` with no
/// private marker, because XTMODKEYS (`CSI > 4;2 m`) also ends in `m`.
fn csi(text: &str, start: usize) -> (usize, bool) {
    let body = &text[start..];
    let params = body
        .find(|c| !matches!(c, '\x20'..='\x3f'))
        .unwrap_or(body.len());
    match body[params..].chars().next() {
        Some(last @ '\x40'..='\x7e') => {
            let private = body[..params].contains(['<', '=', '>', '?']);
            (start + params + 1, last == 'm' && !private)
        }
        _ => (start + params, false),
    }
}

/// An OSC, DCS, SOS, PM or APC string runs to BEL or ST. An ESC that does not start ST ends it
/// and starts the next sequence. A string without an end runs to the end of `text`.
fn string_end(text: &str, start: usize) -> usize {
    let body = &text[start..];
    match body.find([BEL, ST, ESC]) {
        None => text.len(),
        Some(at) if body[at..].starts_with("\x1b\\") => start + at + 2,
        Some(at) if body[at..].starts_with(ESC) => start + at,
        Some(at) => start + at + body[at..].chars().next().map_or(0, char::len_utf8),
    }
}

/// ESC, intermediates, then one final character. Without the final character only the ESC and
/// the intermediates go.
fn escape_end(text: &str, start: usize) -> usize {
    let body = &text[start..];
    let intermediates = body
        .find(|c| !matches!(c, '\x20'..='\x2f'))
        .unwrap_or(body.len());
    match body[intermediates..].chars().next() {
        Some('\x30'..='\x7e') => start + intermediates + 1,
        _ => start + intermediates,
    }
}
