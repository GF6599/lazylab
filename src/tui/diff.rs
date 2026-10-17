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
    Added,
    Removed,
    Hunk,
    Context,
    Note,
}

#[derive(Debug, PartialEq, Eq)]
pub struct Row {
    pub kind: Kind,
    pub text: String,
}

/// GitLab's diff of a file starts at its first hunk, with no `---` and `+++` lines, so the first
/// character alone says what a line is, even for a removed line whose code starts with dashes.
pub fn rows(files: &[FileDiff]) -> Vec<Row> {
    let row = |kind, text: String| Row { kind, text };
    let mut rows = Vec::new();
    for (i, file) in files.iter().enumerate() {
        if i > 0 {
            rows.push(row(Kind::Context, String::new()));
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
        for line in file.diff.lines() {
            let mut chars = line.chars();
            let mark = chars.next().unwrap_or(' ');
            let kind = match mark {
                '+' => Kind::Added,
                '-' => Kind::Removed,
                '@' => Kind::Hunk,
                '\\' => Kind::Note,
                _ => Kind::Context,
            };
            // The stops count from the start of the code, after the column that marks the line.
            rows.push(row(kind, format!("{mark}{}", expand_tabs(chars.as_str()))));
        }
    }
    rows
}
