use ratatui::Frame;
use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;

use super::Context;
use super::sidebar::row_name;
use super::sidebar::row_status;
use crate::gitlab::{Discussion, Status, TestCaseStatus};
use crate::tui::app::{App, Focus, MrTab, Pane};
use crate::tui::diff::{self, Kind};
use crate::tui::jobs::JobRow;
use crate::tui::panel::{
    Row, block, bottom_label, kv, light_corners, marked_line, tab_spans, window_start,
};
use crate::tui::text::{age, clamp_spans, expand_tabs, fit, pad, width as text_width};
use crate::tui::theme::Theme;

pub(super) fn draw(frame: &mut Frame, app: &App, context: &Context, area: Rect, t: &Theme) {
    let inner = block("", true, t).inner(area);
    let tabs =
        (app.pane == Pane::MergeRequests && inner.height > 2).then(|| mr_tabs(app.mr_tab, t));
    let mut body = inner;
    if tabs.is_some() {
        body.y += 2;
        body.height -= 2;
    }
    let (title, lines, label) = match job_view(app, body.height as usize, t) {
        Some(job) => job,
        None => {
            let (title, lines) = view(app, context, body, t);
            (title, lines, None)
        }
    };
    let mut frame_block = block(&title, true, t);
    if let Some(label) = label {
        frame_block = frame_block.title_bottom(bottom_label(label, t));
    }
    frame.render_widget(frame_block, area);
    if let Some(tabs) = tabs {
        frame.render_widget(Paragraph::new(tabs), Rect { height: 1, ..inner });
    }
    frame.render_widget(Paragraph::new(lines), body);
    if app.focus == Focus::Detail {
        light_corners(frame.buffer_mut(), area, t);
    }
}

/// A job shows its status and stage above as much of its log as fits, and the frame says
/// whether the log follows its end or where in the log the reader is.
fn job_view(
    app: &App,
    height: usize,
    t: &Theme,
) -> Option<(String, Vec<Line<'static>>, Option<String>)> {
    if app.pane != Pane::Jobs {
        return None;
    }
    let job = app.job()?;
    let (glyph, style) = t.status(job.status, app.tick);
    let mut lines = vec![
        kv(
            "status",
            vec![Span::styled(
                format!("{glyph} {}", status_label(job.status)),
                style,
            )],
            t,
        ),
        kv("stage", vec![Span::styled(job.stage.clone(), t.body())], t),
        Line::raw(""),
    ];
    let title = format!("job {}", job.name);
    let room = height.saturating_sub(lines.len());
    app.log_height.set(room);
    let Some(log) = app.log.as_ref().filter(|log| log.job == job.id) else {
        let waiting = if app.log_failed(job.id) {
            "the log did not load"
        } else {
            "loading the log"
        };
        lines.push(Line::styled(waiting, t.dim()));
        return Some((title, lines, None));
    };
    let len = log.lines.len();
    let end = len.saturating_sub(app.log_scroll()).max(room.min(len));
    let start = end.saturating_sub(room);
    lines.extend(log.lines[start..end].iter().cloned());
    let label = if app.log_following() {
        "following".to_string()
    } else {
        format!("{end}/{len}")
    };
    Some((title, lines, Some(label)))
}

fn view(app: &App, context: &Context, body: Rect, t: &Theme) -> (String, Vec<Line<'static>>) {
    let width = body.width as usize;
    let text = |s: String| vec![Span::styled(s, t.body())];
    let ago = |at: Option<time::OffsetDateTime>| {
        text(
            at.map(|at| format!("{} ago", age(context.now, at)))
                .unwrap_or_default(),
        )
    };
    let cursor = app.cursor(app.pane);
    let status = |status, tick| {
        let (glyph, style) = t.status(status, tick);
        vec![Span::styled(
            format!("{glyph} {}", status_label(status)),
            style,
        )]
    };
    match app.pane {
        Pane::Projects => app.project().map(|p| {
            let lines = vec![
                kv("about", text(p.description.clone().unwrap_or_default()), t),
                kv(
                    "branch",
                    text(p.default_branch.clone().unwrap_or_default()),
                    t,
                ),
                kv("active", ago(p.last_activity_at), t),
                kv("web", text(p.web_url.clone()), t),
            ];
            (p.path_with_namespace.clone(), lines)
        }),
        Pane::Pipelines if app.test_report_open => (app.pipelines.get(cursor)).map(|p| {
            let lines = test_report(app, width, t);
            app.test_report_lines.set(lines.len());
            app.test_report_height.set(body.height as usize);
            let lines = lines.into_iter().skip(app.test_report_scroll()).collect();
            (format!("test report #{}", p.id), lines)
        }),
        Pane::Pipelines => app.pipelines.get(cursor).map(|p| {
            let mut lines = vec![
                kv("status", status(p.status, app.tick), t),
                kv("ref", text(p.git_ref.clone()), t),
                kv("commit", text(p.sha.clone()), t),
                kv(
                    "by",
                    text(p.user.as_ref().map(|u| u.name.clone()).unwrap_or_default()),
                    t,
                ),
                kv("updated", ago(p.updated()), t),
            ];
            let graph = stage_graph(app, width, t);
            if !graph.is_empty() {
                lines.push(Line::raw(""));
                lines.push(Line::styled("stages", t.title()));
                lines.extend(graph);
            }
            (format!("pipeline #{}", p.id), lines)
        }),
        Pane::Jobs => app.job_rows().get(cursor).and_then(|row| match row {
            JobRow::Job { .. } => None,
            JobRow::Group {
                name,
                stage,
                status: worst,
                count,
                ..
            } => Some((
                format!("jobs {name}"),
                vec![
                    kv("status", status(*worst, app.tick), t),
                    kv("stage", text(stage.clone()), t),
                    kv("jobs", text(count.to_string()), t),
                ],
            )),
            JobRow::Bridge { bridge, .. } => Some((
                format!("bridge {}", bridge.name),
                vec![
                    kv("status", status(row_status(row), app.tick), t),
                    kv("stage", text(bridge.stage.clone()), t),
                    kv(
                        "child",
                        text(bridge.downstream_pipeline.as_ref().map_or_else(
                            || "none yet".to_string(),
                            |child| format!("pipeline #{}", child.id),
                        )),
                        t,
                    ),
                ],
            )),
            JobRow::Waiting { .. } => None,
        }),
        Pane::MergeRequests => app.merge_requests.get(cursor).map(|mr| {
            let lines = match app.mr_tab {
                MrTab::Info => vec![
                    kv("title", text(mr.title.clone()), t),
                    kv("from", text(mr.source_branch.clone()), t),
                    kv("into", text(mr.target_branch.clone()), t),
                    kv(
                        "by",
                        text(
                            mr.author
                                .as_ref()
                                .map(|a| a.name.clone())
                                .unwrap_or_default(),
                        ),
                        t,
                    ),
                    kv("updated", ago(mr.updated_at), t),
                ],
                MrTab::Threads => threads(app, context, width, body.height as usize, t),
                MrTab::Diff => diff(app, width, body.height as usize, t),
            };
            (format!("merge request !{}", mr.iid), lines)
        }),
    }
    .unwrap_or_else(|| (app.pane.label().to_string(), Vec::new()))
}

const RULE: usize = 40;

/// The picked thread shows whole when it fits, and from its first line when it does not.
fn threads(
    app: &App,
    context: &Context,
    width: usize,
    height: usize,
    t: &Theme,
) -> Vec<Line<'static>> {
    let Some(threads) = app.shown_threads() else {
        return vec![Line::styled("loading the threads", t.dim())];
    };
    let Some(discussions) = &threads.discussions else {
        return vec![Line::styled("the threads did not load", t.dim())];
    };
    if discussions.is_empty() {
        return vec![Line::styled("no threads", t.dim())];
    }
    let code = app.threads_diff();
    let mut lines = Vec::new();
    let mut picked = 0..0;
    for (row, discussion) in discussions.iter().enumerate() {
        if row > 0 {
            lines.push(Line::styled("─".repeat(RULE.min(width)), t.border()));
            lines.push(Line::raw(""));
        }
        let start = lines.len();
        let on = row == app.thread_row();
        lines.extend(thread(discussion, on, code, context, width, t));
        if on {
            picked = start..lines.len();
        }
    }
    let top = picked.end.saturating_sub(height).min(picked.start);
    lines.split_off(top)
}

/// The view keeps the picked line in its middle, as the lists in the sidebar do.
fn diff(app: &App, width: usize, height: usize, t: &Theme) -> Vec<Line<'static>> {
    let Some(diff) = app.shown_diff() else {
        return vec![Line::styled("loading the diff", t.dim())];
    };
    let Some(rows) = &diff.rows else {
        return vec![Line::styled("the diff did not load", t.dim())];
    };
    if rows.is_empty() {
        return vec![Line::styled("no changes", t.dim())];
    }
    let picked = app.picked_diff_row();
    let start = window_start(rows.len(), picked.unwrap_or(0), height);
    (rows.iter().enumerate().skip(start).take(height))
        .map(|(i, row)| {
            let on = picked == Some(i);
            let spans = Row {
                left: diff_spans(row, on, width, t),
                right: Vec::new(),
            };
            marked_line(spans, width as u16, on, false, t)
        })
        .collect()
}

/// The sign of the picked line keeps the colour of its change, so the reader still sees whether
/// the line was added or removed.
fn diff_spans(row: &diff::Row, on: bool, width: usize, t: &Theme) -> Vec<Span<'static>> {
    let colour = |c| Style::new().fg(c);
    let style = match row.kind {
        Kind::File => t.title(),
        Kind::Rule => {
            let rule = "─".repeat(RULE.min(width.saturating_sub(2)));
            return vec![Span::styled(rule, t.border())];
        }
        Kind::Added => colour(t.pine),
        Kind::Removed => colour(t.love),
        Kind::Hunk => colour(t.foam),
        Kind::Context | Kind::Gap => t.body(),
        Kind::Note => t.dim(),
    };
    if !on {
        return vec![Span::styled(row.text.clone(), style)];
    }
    if row.kind == Kind::File {
        return vec![Span::styled(row.text.clone(), t.marked())];
    }
    let sign = row.text.chars().next().map_or(0, char::len_utf8);
    let (sign, code) = row.text.split_at(sign);
    vec![
        Span::styled(sign.to_string(), style),
        Span::styled(code.to_string(), t.marked()),
    ]
}

/// The Go release shows this many lines of a failed case's output.
const OUTPUT_LINES: usize = 5;

/// The totals, then each suite with its counts, and under it each case that failed.
fn test_report(app: &App, width: usize, t: &Theme) -> Vec<Line<'static>> {
    let Some(shown) = app.shown_test_report() else {
        return vec![Line::styled("loading the test report", t.dim())];
    };
    let Some(report) = &shown.report else {
        return vec![Line::styled("the test report did not load", t.dim())];
    };
    if report.test_suites.is_empty() {
        return vec![Line::styled("no test report", t.dim())];
    }
    let count = |n: u32, style: Style| vec![Span::styled(n.to_string(), style)];
    let (passed, failed) = (Style::new().fg(t.pine), Style::new().fg(t.love));
    let mut lines = vec![
        kv("total", count(report.total_count, t.body()), t),
        kv("passed", count(report.success_count, passed), t),
        kv("failed", count(report.failed_count, failed), t),
        kv("skipped", count(report.skipped_count, t.body()), t),
        kv("errors", count(report.error_count, failed), t),
        kv(
            "time",
            vec![Span::styled(format!("{:.2}s", report.total_time), t.body())],
            t,
        ),
        Line::raw(""),
        Line::styled("suites", t.title()),
    ];
    let room = width.saturating_sub(4);
    let names = (report.test_suites.iter()).map(|s| text_width(&s.name));
    let name_width = names.max().unwrap_or(0);
    for suite in &report.test_suites {
        let counts = vec![
            Span::styled(suite.name.clone(), t.body()),
            Span::raw(pad(name_width - text_width(&suite.name) + 2)),
            Span::styled(format!("{} passed", suite.success_count), passed),
            Span::raw(pad(2)),
            Span::styled(format!("{} failed", suite.failed_count), failed),
        ];
        lines.push(Line::from(clamp_spans(counts, width)));
        let cases = (suite.test_cases.iter())
            .filter(|c| matches!(c.status, TestCaseStatus::Failed | TestCaseStatus::Error));
        for case in cases {
            let (glyph, style) = t.status(Status::Failed, app.tick);
            lines.push(Line::from(clamp_spans(
                vec![
                    Span::raw(pad(2)),
                    Span::styled(glyph, style),
                    Span::styled(format!(" {}", case.name), t.body()),
                ],
                width,
            )));
            if !case.classname.is_empty() {
                let class = format!("    class: {}", case.classname);
                lines.push(Line::styled(fit(&class, width), t.dim()));
            }
            let output = case.system_output.as_deref().unwrap_or_default();
            for text in output.lines().take(OUTPUT_LINES) {
                let text = fit(&expand_tabs(text), room);
                lines.push(Line::styled(format!("{}{text}", pad(4)), t.body()));
            }
            if output.lines().nth(OUTPUT_LINES).is_some() {
                lines.push(Line::styled(format!("{}…", pad(4)), t.dim()));
            }
        }
    }
    lines
}

/// The Go release shows this many lines of the diff on each side of the line a thread is on.
const CONTEXT_LINES: usize = 10;

/// A note's first body line hangs from the tree, and its other lines continue the tree.
fn thread(
    discussion: &Discussion,
    on: bool,
    code: Option<&[diff::Row]>,
    context: &Context,
    width: usize,
    t: &Theme,
) -> Vec<Line<'static>> {
    let notes: Vec<_> = discussion.notes.iter().filter(|n| !n.system).collect();
    let mut lines = Vec::new();
    for (i, note) in notes.iter().enumerate() {
        let (first, last) = (i == 0, i + 1 == notes.len());
        let author = note.author.as_ref().map_or("", |a| a.name.as_str());
        let mut header = if first {
            let (open, close) = if on { ("[", "]") } else { (" ", " ") };
            vec![
                Span::styled(open, t.marker()),
                Span::styled(author.to_string(), if on { t.marked() } else { t.title() }),
                Span::styled(close, t.marker()),
            ]
        } else {
            vec![
                Span::raw(pad(2)),
                Span::styled(author.to_string(), t.title()),
            ]
        };
        if let Some(at) = note.created_at {
            header.push(Span::styled(
                format!(" · {} ago", age(context.now, at)),
                t.dim(),
            ));
        }
        if first && note.resolvable {
            let (badge, colour) = if note.resolved {
                ("resolved", t.pine)
            } else {
                ("unresolved", t.love)
            };
            header.push(Span::raw(pad(2)));
            header.push(Span::styled(badge, Style::new().fg(colour)));
        }
        lines.push(Line::from(clamp_spans(header, width)));
        if first
            && let Some(position) = &note.position
            && let (Some(path), Some(line)) = (position.path(), position.line())
        {
            lines.push(Line::styled(
                fit(&format!("  {path}:{line}"), width),
                t.dim(),
            ));
            for row in code.map_or(&[][..], |rows| diff::around(rows, position, CONTEXT_LINES)) {
                let mut spans = vec![Span::raw(pad(2))];
                spans.extend(diff_spans(row, false, width, t));
                lines.push(Line::from(clamp_spans(spans, width)));
            }
        }
        let (hang, carry) = match (first, last) {
            (_, true) => ("└ ", "  "),
            (true, false) => ("│ ", "│ "),
            (false, false) => ("├ ", "│ "),
        };
        let room = width.saturating_sub(4);
        for (n, text) in note.body.lines().enumerate() {
            lines.push(Line::from(vec![
                Span::raw(pad(2)),
                Span::styled(if n == 0 { hang } else { carry }, t.border()),
                Span::styled(fit(&expand_tabs(text), room), t.body()),
            ]));
        }
        lines.push(Line::raw(""));
    }
    lines
}

fn mr_tabs(current: MrTab, t: &Theme) -> Line<'static> {
    let tabs: Vec<_> = (MrTab::ALL.into_iter())
        .map(|tab| (tab.label(), tab == current))
        .collect();
    Line::from(tab_spans(&tabs, t))
}

fn status_label(status: Status) -> &'static str {
    match status {
        Status::Created => "created",
        Status::WaitingForResource => "waiting for resource",
        Status::WaitingForCallback => "waiting for callback",
        Status::Preparing => "preparing",
        Status::Pending => "pending",
        Status::Scheduled => "scheduled",
        Status::Running => "running",
        Status::Success => "passed",
        Status::Failed => "failed",
        Status::Canceled => "canceled",
        Status::Canceling => "canceling",
        Status::Skipped => "skipped",
        Status::Manual => "manual",
        Status::Blocked => "blocked",
        Status::Unknown => "unknown",
    }
}

const COLUMN_GAP: usize = 3;
/// A status glyph and the space after it.
const GLYPH_CELLS: usize = 2;

/// The selected pipeline's stages side by side, the way GitLab draws them, or stacked when the
/// columns do not fit. A column holds the jobs pane's top rows, so matrix jobs stay folded.
fn stage_graph(app: &App, width: usize, t: &Theme) -> Vec<Line<'static>> {
    let mut columns: Vec<(&str, Vec<&JobRow>)> = Vec::new();
    for row in app.job_rows() {
        let Some(stage) = row.stage() else {
            continue;
        };
        match columns.iter_mut().find(|(name, _)| *name == stage) {
            Some((_, rows)) => rows.push(row),
            None => columns.push((stage, vec![row])),
        }
    }
    let cell = |row: &JobRow| {
        let (glyph, style) = t.status(row_status(row), app.tick);
        (Span::styled(glyph, style), row_name(row))
    };
    let widths: Vec<usize> = (columns.iter())
        .map(|(stage, rows)| {
            (rows
                .iter()
                .map(|row| GLYPH_CELLS + text_width(&row_name(row))))
            .chain([text_width(stage)])
            .max()
            .unwrap_or(0)
                + COLUMN_GAP
        })
        .collect();

    if widths.iter().sum::<usize>() > width {
        let mut lines = Vec::new();
        for (stage, rows) in &columns {
            lines.push(Line::styled(stage.to_string(), t.title()));
            for row in rows {
                let (glyph, name) = cell(row);
                lines.push(Line::from(vec![
                    Span::raw(pad(GLYPH_CELLS)),
                    glyph,
                    Span::styled(format!(" {name}"), t.body()),
                ]));
            }
        }
        return lines;
    }

    let mut header = Vec::new();
    let mut rule = Vec::new();
    for ((stage, _), w) in columns.iter().zip(&widths) {
        header.push(Span::styled(
            format!("{stage}{}", pad(w - text_width(stage))),
            t.dim(),
        ));
        rule.push(Span::styled("─".repeat(w - COLUMN_GAP), t.border()));
        rule.push(Span::raw(pad(COLUMN_GAP)));
    }
    let mut lines = vec![Line::from(header), Line::from(rule)];
    let depth = columns
        .iter()
        .map(|(_, rows)| rows.len())
        .max()
        .unwrap_or(0);
    for depth_row in 0..depth {
        let mut spans = Vec::new();
        for ((_, rows), w) in columns.iter().zip(&widths) {
            match rows.get(depth_row) {
                Some(row) => {
                    let (glyph, name) = cell(row);
                    spans.push(glyph);
                    let fill = w - GLYPH_CELLS - text_width(&name);
                    spans.push(Span::styled(format!(" {name}{}", pad(fill)), t.body()));
                }
                None => spans.push(Span::raw(pad(*w))),
            }
        }
        lines.push(Line::from(spans));
    }
    lines
}
