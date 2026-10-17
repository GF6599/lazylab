//! A job log as the screen shows it: what a terminal leaves on each line, in the log's colours.

use ansi_to_tui::IntoText;
use ratatui::text::Line;

use crate::tui::text::expand_tabs_at;

/// The tab stop of a terminal, which a job log was written for.
const TAB_STOP: usize = 8;

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
    let lines = match shown.as_bytes().into_text() {
        Ok(text) => text.lines,
        Err(_) => shown
            .lines()
            .map(|line| Line::raw(line.to_string()))
            .collect(),
    };
    lines.into_iter().map(expand_tabs_in_line).collect()
}

/// A tab in one span reaches its stop from the start of the line, not of the span.
fn expand_tabs_in_line(mut line: Line<'static>) -> Line<'static> {
    let mut column = 0;
    for span in &mut line.spans {
        span.content = expand_tabs_at(&span.content, TAB_STOP, &mut column).into();
    }
    line
}
