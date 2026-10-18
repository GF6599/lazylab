//! The system clipboard, written with OSC 52. The terminal on the reader's own machine carries out
//! the write, so a copy works over SSH as well.

use base64::prelude::{BASE64_STANDARD, Engine};

use crate::termsafe;

/// The sequence that sets the clipboard to `text`. Every copy passes through here, and the text is
/// filtered first because it comes from GitLab and is pasted into a shell.
pub fn osc52(text: &str) -> Vec<u8> {
    let encoded = BASE64_STANDARD.encode(termsafe::filter(text));
    format!("\x1b]52;c;{encoded}\x07").into_bytes()
}
