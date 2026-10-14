use ratatui::Frame;
use ratatui::layout::Rect;
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;

use super::Context;
use super::sidebar::row_status;
use crate::gitlab::Status;
use crate::tui::app::{App, Focus, Pane};
use crate::tui::jobs::JobRow;
use crate::tui::panel::{block, kv, light_corners};
use crate::tui::text::age;
use crate::tui::theme::Theme;

pub(super) fn draw(frame: &mut Frame, app: &App, context: &Context, area: Rect, t: &Theme) {
    let (title, lines) = view(app, context, t);
    let frame_block = block(&title, true, t);
    let body = frame_block.inner(area);
    frame.render_widget(frame_block, area);
    frame.render_widget(Paragraph::new(lines), body);
    if app.focus == Focus::Detail {
        light_corners(frame.buffer_mut(), area, t);
    }
}

fn view(app: &App, context: &Context, t: &Theme) -> (String, Vec<Line<'static>>) {
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
        Pane::Projects => app.projects.get(cursor).map(|p| {
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
        Pane::Pipelines => app.pipelines.get(cursor).map(|p| {
            let lines = vec![
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
            (format!("pipeline #{}", p.id), lines)
        }),
        Pane::Jobs => app.job_rows().get(cursor).and_then(|row| match row {
            JobRow::Job { job, .. } => Some((
                format!("job {}", job.name),
                vec![
                    kv("status", status(job.status, app.tick), t),
                    kv("stage", text(job.stage.clone()), t),
                ],
            )),
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
            let lines = vec![
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
            ];
            (format!("merge request !{}", mr.iid), lines)
        }),
    }
    .unwrap_or_else(|| (app.pane.label().to_string(), Vec::new()))
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
