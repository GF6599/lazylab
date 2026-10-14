//! A job log as the screen shows it: what a terminal leaves on each line, in the log's colours.

use ansi_to_tui::IntoText;
use ratatui::text::Line;

#[derive(Debug)]
pub struct Log {
    pub job: u64,
    pub lines: Vec<Line<'static>>,
}

impl Log {
    pub fn new(job: u64, log: &str) -> Log {
        Log {
            job,
            lines: lines(log),
        }
    }
}

/// A carriage return sends a terminal back to the start of the line, so only the text after the
/// last one stays. GitLab hides its section markers that way, and a progress bar ends on its last
/// state. Escape sequences other than colour cannot reach the terminal, because ratatui writes
/// cells, not the bytes it was given.
fn lines(log: &str) -> Vec<Line<'static>> {
    let shown: Vec<&str> = (log.lines())
        .map(|line| line.rsplit('\r').next().unwrap_or(line))
        .collect();
    let shown = shown.join("\n");
    match shown.as_bytes().into_text() {
        Ok(text) => text.lines,
        Err(_) => shown
            .lines()
            .map(|line| Line::raw(line.to_string()))
            .collect(),
    }
}
