//! A merge request's diff as the rows the detail pane draws: a header for each file, then its
//! lines.

use crate::gitlab::FileDiff;
use crate::tui::text::expand_tabs;

#[derive(Debug)]
pub struct Diff {
    pub project: u64,
    pub merge_request: u64,
    /// `None` when the load failed, which stops a retry until the cursor moves.
    pub rows: Option<Vec<Row>>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    File,
    Rule,
    /// The blank row between two files.
    Gap,
    Added,
    Removed,
    Hunk,
    Context,
    Note,
}

impl Kind {
    pub fn is_code(self) -> bool {
        matches!(self, Kind::Added | Kind::Removed | Kind::Context)
    }

    /// The cursor rests on a file header too, so a file without a line of code can be reached.
    pub fn takes_cursor(self) -> bool {
        self.is_code() || self == Kind::File
    }
}

#[derive(Debug, PartialEq, Eq)]
pub struct Row {
    pub kind: Kind,
    pub text: String,
    /// `None` for a row that is not a line of code.
    pub code: Option<CodeLine>,
}

/// Where a line of code is, which a comment on it names. An added line has only a new line, a
/// removed line only an old one, and an unchanged line both.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CodeLine {
    pub old_path: String,
    pub new_path: String,
    pub old_line: Option<u32>,
    pub new_line: Option<u32>,
}

/// GitLab's diff of a file starts at its first hunk, with no `---` and `+++` lines, so the first
/// character alone says what a line is, even for a removed line whose code starts with dashes.
pub fn rows(files: &[FileDiff]) -> Vec<Row> {
    let row = |kind, text: String| Row {
        kind,
        text,
        code: None,
    };
    let mut rows = Vec::new();
    for (i, file) in files.iter().enumerate() {
        if i > 0 {
            rows.push(row(Kind::Gap, String::new()));
        }
        let change = if file.new_file {
            format!("new file: {}", file.new_path)
        } else if file.deleted_file {
            format!("deleted: {}", file.old_path)
        } else if file.renamed_file {
            format!("renamed: {} → {}", file.old_path, file.new_path)
        } else {
            format!("modified: {}", file.new_path)
        };
        rows.push(row(Kind::File, change));
        rows.push(row(Kind::Rule, String::new()));
        if file.too_large {
            rows.push(row(Kind::Note, "too large to show".to_string()));
        }
        let (mut old, mut new) = (0, 0);
        for line in file.diff.lines() {
            let mut chars = line.chars();
            let mark = chars.next().unwrap_or(' ');
            let (kind, old_line, new_line) = match mark {
                '+' => (Kind::Added, None, Some(new)),
                '-' => (Kind::Removed, Some(old), None),
                '@' => {
                    (old, new) = hunk_start(line);
                    (Kind::Hunk, None, None)
                }
                '\\' => (Kind::Note, None, None),
                _ => (Kind::Context, Some(old), Some(new)),
            };
            old += u32::from(old_line.is_some());
            new += u32::from(new_line.is_some());
            let code = kind.is_code().then(|| CodeLine {
                old_path: file.old_path.clone(),
                new_path: file.new_path.clone(),
                old_line,
                new_line,
            });
            // The stops count from the start of the code, after the column that marks the line.
            let text = format!("{mark}{}", expand_tabs(chars.as_str()));
            rows.push(Row { kind, text, code });
        }
    }
    rows
}

/// The first old and new line of a hunk, from a header such as `@@ -12,6 +12,13 @@`. A side that
/// does not parse starts at 0, as in the Go release.
fn hunk_start(header: &str) -> (u32, u32) {
    let ranges = header.split("@@").nth(1).unwrap_or_default();
    let start = |sign: char| {
        (ranges.split_whitespace())
            .find_map(|range| range.strip_prefix(sign))
            .and_then(|range| range.split(',').next()?.parse().ok())
            .unwrap_or(0)
    };
    (start('-'), start('+'))
}

pub fn cursor_rows(rows: &[Row]) -> impl Iterator<Item = usize> + '_ {
    (rows.iter().enumerate())
        .filter(|(_, row)| row.kind.takes_cursor())
        .map(|(i, _)| i)
}
