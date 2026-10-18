use ratatui::Frame;
use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::style::{Color, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;

use super::Context;
use crate::gitlab::Status;
use crate::tui::app::{App, Focus, MrTab, Overlay, Pane};
use crate::tui::text::{fit, spans_width, width};
use crate::tui::theme::Theme;

/// U+2590 is the half block with an unambiguous East Asian width, so it is one cell in every
/// terminal, where a powerline chevron is private use and renders as tofu without a Nerd Font.
const PROMPT_SEP: &str = "▐";
const BRAND: &str = "{lazylab}";

type Segment = (String, Color, Color);

/// The separator takes the leaving colour as its background and the arriving one as its
/// foreground, which splits the cell down its middle.
fn powerline(segments: &[Segment]) -> Vec<Span<'static>> {
    let mut spans = Vec::with_capacity(segments.len() * 2);
    for (i, (text, fg, bg)) in segments.iter().enumerate() {
        spans.push(Span::styled(
            format!(" {text} "),
            Style::new().fg(*fg).bg(*bg),
        ));
        if let Some((_, _, next_bg)) = segments.get(i + 1) {
            spans.push(Span::styled(PROMPT_SEP, Style::new().fg(*next_bg).bg(*bg)));
        }
    }
    spans
}

pub(super) fn draw_prompt(frame: &mut Frame, app: &App, context: &Context, area: Rect, t: &Theme) {
    let count = |s: Status| app.pipelines.iter().filter(|p| p.status == s).count();
    let tally = (
        format!(
            "{} running · {} failed",
            count(Status::Running),
            count(Status::Failed)
        ),
        t.subtle,
        t.base,
    );
    let brand = (BRAND.to_string(), t.base, t.iris);
    let host = (context.host.clone(), t.text, t.hl_med);
    let path = app
        .project()
        .map(|p| p.path_with_namespace.clone())
        .unwrap_or_default();
    let project = |name: String| (name, t.text, t.hl_low);

    // Widest first. The brand goes before the host, because it names the app the reader started.
    let candidates = [
        vec![brand, host.clone(), project(path.clone()), tally.clone()],
        vec![host, project(path.clone()), tally.clone()],
        vec![project(path.clone()), tally.clone()],
    ];
    let room = area.width as usize;
    let prompt = candidates
        .iter()
        .map(|c| powerline(c))
        .find(|spans| spans_width(spans) <= room)
        .unwrap_or_else(|| {
            let room = room.saturating_sub(width(&tally.0) + 5);
            powerline(&[project(fit(&path, room)), tally])
        });
    frame.render_widget(Paragraph::new(Line::from(prompt)), area);
}

pub(super) fn draw_strip(frame: &mut Frame, app: &App, area: Rect, t: &Theme) {
    let zoom = if app.zoom { " zoom (z) " } else { "" };
    let room = (area.width as usize).saturating_sub(width(zoom));
    let strip = |short: bool| {
        let mut spans = Vec::new();
        for pane in Pane::ALL {
            let on = pane == app.pane;
            let (open, close) = if on { ("[", "]") } else { (" ", " ") };
            let label = if short { pane.short() } else { pane.label() };
            spans.push(Span::styled(open, t.marker()));
            spans.push(Span::styled(pane.key().to_string(), t.dim()));
            spans.push(Span::styled(
                format!(" {label}"),
                if on { t.marked() } else { t.title() },
            ));
            spans.push(Span::styled(close, t.marker()));
            spans.push(Span::raw(" "));
        }
        spans
    };
    let full = strip(false);
    let spans = if spans_width(&full) <= room {
        full
    } else {
        strip(true)
    };
    let [left, right] =
        Layout::horizontal([Constraint::Min(0), Constraint::Length(width(zoom) as u16)])
            .areas(area);
    frame.render_widget(Paragraph::new(Line::from(spans)), left);
    frame.render_widget(
        Paragraph::new(Line::styled(zoom, t.marker()).right_aligned()),
        right,
    );
}

fn mode(app: &App) -> &'static str {
    match (app.overlay, app.focus) {
        _ if app.search.typing => "SEARCH",
        (Overlay::Help, _) => "HELP",
        (Overlay::Confirm, _) => "CONFIRM",
        (Overlay::Form, _) => "FORM",
        (Overlay::None, Focus::Detail) => "DETAIL",
        (Overlay::None, Focus::Sidebar) => "BROWSE",
    }
}

/// The keys that answer from where the reader is, most useful first, so a narrow bar loses the
/// least useful ones.
fn hints(app: &App) -> &'static [(&'static str, &'static str)] {
    match (app.overlay, app.focus) {
        _ if app.search.typing => &[("enter", "keep"), ("esc", "clear")],
        (Overlay::Help, _) => &[("esc", "close")],
        (Overlay::Confirm, _) => &[("y", "yes"), ("n", "no")],
        (Overlay::Form, _) => &[
            ("enter", "send"),
            ("esc", "close"),
            ("tab", "next field"),
            ("ctrl+n", "add a variable"),
            ("ctrl+d", "remove it"),
        ],
        (Overlay::None, Focus::Detail)
            if app.pane == Pane::MergeRequests && app.mr_tab == MrTab::Threads =>
        {
            &[
                ("j/k", "thread"),
                ("r", "resolve"),
                ("[ ]", "tab"),
                ("h", "back"),
                ("?", "help"),
                ("q", "quit"),
            ]
        }
        (Overlay::None, Focus::Detail)
            if app.pane == Pane::MergeRequests && app.mr_tab == MrTab::Diff =>
        {
            &[
                ("j/k", "scroll"),
                ("[ ]", "tab"),
                ("h", "back"),
                ("?", "help"),
                ("q", "quit"),
            ]
        }
        (Overlay::None, Focus::Detail) if app.pane == Pane::MergeRequests => {
            &[("[ ]", "tab"), ("h", "back"), ("?", "help"), ("q", "quit")]
        }
        (Overlay::None, Focus::Detail) if app.pane == Pane::Pipelines && app.test_report_open => &[
            ("j/k", "scroll"),
            ("T", "tests"),
            ("h", "back"),
            ("?", "help"),
            ("q", "quit"),
        ],
        (Overlay::None, Focus::Detail) if app.pane == Pane::Pipelines => {
            &[("T", "tests"), ("h", "back"), ("?", "help"), ("q", "quit")]
        }
        (Overlay::None, Focus::Detail) => &[("h", "back"), ("?", "help"), ("q", "quit")],
        (Overlay::None, Focus::Sidebar) if app.pane == Pane::Projects => &[
            ("j/k", "move"),
            ("l", "open"),
            ("/", "search"),
            ("1-4", "pane"),
            ("z", "zoom"),
            ("?", "help"),
            ("q", "quit"),
        ],
        (Overlay::None, Focus::Sidebar) if app.pane == Pane::Pipelines => &[
            ("j/k", "move"),
            ("l", "open"),
            ("[ ]", "page"),
            ("T", "tests"),
            ("1-4", "pane"),
            ("z", "zoom"),
            ("?", "help"),
            ("q", "quit"),
        ],
        (Overlay::None, Focus::Sidebar) if app.pane == Pane::MergeRequests => &[
            ("j/k", "move"),
            ("l", "open"),
            ("[ ]", "tab"),
            ("1-4", "pane"),
            ("z", "zoom"),
            ("?", "help"),
            ("q", "quit"),
        ],
        (Overlay::None, Focus::Sidebar) => &[
            ("j/k", "move"),
            ("l", "open"),
            ("1-4", "pane"),
            ("z", "zoom"),
            ("?", "help"),
            ("q", "quit"),
        ],
    }
}

/// The count of a load in progress, which a total of zero leaves out, because the first page has
/// not said how many projects there are.
fn progress(app: &App) -> String {
    match (app.loading, app.total_projects) {
        (false, _) => String::new(),
        (true, 0) => "loading projects ".into(),
        (true, total) => format!("loading projects {}/{total} ", app.projects.len()),
    }
}

pub(super) fn draw_status(frame: &mut Frame, app: &App, area: Rect, t: &Theme) {
    let progress = progress(app);
    let room = (area.width as usize).saturating_sub(width(&progress));
    let mut spans = vec![
        Span::styled(format!(" {} ", mode(app)), t.badge()),
        Span::raw(" "),
    ];
    if let Some(error) = &app.error {
        let error = fit(error, room.saturating_sub(spans_width(&spans)));
        spans.push(Span::styled(error, Style::new().fg(t.love)));
    } else if let Some(notice) = &app.notice {
        let notice = fit(notice, room.saturating_sub(spans_width(&spans)));
        spans.push(Span::styled(notice, Style::new().fg(t.foam)));
    } else {
        for (key, action) in hints(app) {
            let hint = [
                Span::styled(*key, t.body()),
                Span::styled(format!(" {action}  "), t.dim()),
            ];
            if spans_width(&spans) + spans_width(&hint) > room {
                break;
            }
            spans.extend(hint);
        }
    }
    let [left, right] = Layout::horizontal([
        Constraint::Min(0),
        Constraint::Length(width(&progress) as u16),
    ])
    .areas(area);
    frame.render_widget(Paragraph::new(Line::from(spans)), left);
    frame.render_widget(
        Paragraph::new(Line::styled(progress, t.dim()).right_aligned()),
        right,
    );
}
