//! Rendering. Layout: prompt (1) | pane strip (1) | body (rest) | status bar (1).
//!
//! From `WIDE` columns up the body puts the sidebar beside the detail pane. Below it the body
//! shows one of the two, and `l` / `h` move between them.

mod chrome;
mod detail;
pub mod sidebar;

use ratatui::Frame;
use ratatui::layout::{Constraint, Layout, Position, Rect};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Clear, Paragraph};
use time::OffsetDateTime;

use super::app::{App, CommandMenu, Focus, Overlay};
use super::form::{Field, Form};
use super::mark;
use super::panel::block;
use super::text::{expand_tabs, fit, fit_end, pad, width};
use super::theme::{ROSE_PINE, Theme};

const MIN_WIDTH: u16 = 40;
const MIN_HEIGHT: u16 = 10;
const WIDE: u16 = 100;
const SIDEBAR_SHARE: u16 = 40;
const SIDEBAR_MIN: u16 = 38;
const SIDEBAR_MAX: u16 = 64;
const HELP_KEY_COLUMN: usize = 12;
const HELP: &[(&str, &str)] = &[
    ("j k ↑ ↓", "move"),
    ("g G", "first, last"),
    ("ctrl+d u", "page down, up"),
    ("1-4 tab", "choose a pane"),
    ("l enter", "open the detail pane"),
    ("h esc", "back"),
    ("/", "search the projects"),
    ("f", "favorite the project, or not"),
    ("t", "favorites only, or every project"),
    ("{ }", "move a favorite up, down"),
    ("ctrl+r", "reload the projects"),
    ("[ ]", "the page of pipelines before, after"),
    ("T", "the test report of the pipeline, or its stages"),
    ("t T", "the merge request state after, before"),
    ("[ ]", "the merge request tab before, after"),
    ("r", "resolve or reopen the thread"),
    ("c", "start a thread, reply, or comment on the line"),
    ("R", "retry the pipeline or job, after a question"),
    ("C", "cancel the pipeline or job, after a question"),
    ("N", "run a pipeline, with variables"),
    ("P", "play a manual job, with variables"),
    ("y", "copy the glab command of the item"),
    ("Y", "choose a glab command of the item to copy"),
    ("ctrl+o", "copy the web address of the item"),
    ("z", "full screen for the pane in view"),
    ("?", "open or close this help"),
    ("q ctrl+c", "quit"),
];

/// What the screen shows that is not app state: the host it talks to, and the time ages count
/// from.
pub struct Context {
    pub host: String,
    pub now: OffsetDateTime,
}

pub fn draw(frame: &mut Frame, app: &App, context: &Context) {
    let t: &Theme = &ROSE_PINE;
    let area = frame.area();
    frame.render_widget(Block::new().style(t.surface()), area);
    if area.width < MIN_WIDTH || area.height < MIN_HEIGHT {
        let message = vec![
            Line::styled(format!("terminal {}x{}", area.width, area.height), t.body()),
            Line::styled(format!("lazylab needs {MIN_WIDTH}x{MIN_HEIGHT}"), t.dim()),
        ];
        frame.render_widget(Paragraph::new(message), area);
        return;
    }

    let [prompt, strip, body, status] = Layout::vertical([
        Constraint::Length(1),
        Constraint::Length(1),
        Constraint::Fill(1),
        Constraint::Length(1),
    ])
    .areas(area);
    chrome::draw_prompt(frame, app, context, prompt, t);
    chrome::draw_strip(frame, app, strip, t);
    if body.width >= WIDE {
        let sidebar_width = (body.width * SIDEBAR_SHARE / 100).clamp(SIDEBAR_MIN, SIDEBAR_MAX);
        let [left, right] =
            Layout::horizontal([Constraint::Length(sidebar_width), Constraint::Fill(1)])
                .areas(body);
        sidebar::draw(frame, app, context, left, t);
        detail::draw(frame, app, context, right, t);
    } else if app.focus == Focus::Detail {
        detail::draw(frame, app, context, body, t);
    } else {
        sidebar::draw(frame, app, context, body, t);
    }
    chrome::draw_status(frame, app, status, t);
    if app.overlay == Overlay::Help {
        draw_help(frame, area, t);
    }
    if let (Overlay::Confirm, Some(action)) = (app.overlay, &app.confirm) {
        draw_confirm(frame, area, &action.question(), action.verb(), t);
    }
    if let (Overlay::Form, Some(form)) = (app.overlay, &app.form) {
        draw_form(frame, area, form, t);
    }
    if let (Overlay::CommandMenu, Some(menu)) = (app.overlay, &app.command_menu) {
        draw_command_menu(frame, area, menu, t);
    }
}

const FORM_WIDTH: usize = 62;
const FORM_LABEL: usize = 10;
const FORM_KEY_BOX: usize = 18;
const COMMENT_ROWS: usize = 8;
const EQUALS: &str = " = ";

/// One input: a filled box between the marker cells, which show only on the field being typed.
/// That field shows the end of its text and keeps a free cell for the cursor, and the second
/// value is how far into the box the cursor goes.
fn input(value: &str, box_width: usize, active: bool, t: &Theme) -> (Vec<Span<'static>>, usize) {
    let (open, close) = if active { ("[", "]") } else { (" ", " ") };
    let text = if active {
        fit_end(value, box_width.saturating_sub(1))
    } else {
        fit(value, box_width)
    };
    let style = if active { t.marked() } else { t.body() };
    let typed = width(&text);
    let spans = vec![
        Span::styled(open, t.marker()),
        Span::styled(text, style.bg(t.hl_low)),
        Span::styled(pad(box_width - typed), t.selected_row()),
        Span::styled(close, t.marker()),
    ];
    (spans, typed)
}

/// The terminal's cursor sits after the text of the field being typed.
fn draw_form(frame: &mut Frame, area: Rect, form: &Form, t: &Theme) {
    let inner_width = FORM_WIDTH.min((area.width as usize).saturating_sub(4));
    let ref_box = inner_width.saturating_sub(FORM_LABEL + 2);
    let value_box = inner_width.saturating_sub(FORM_LABEL + FORM_KEY_BOX + 4 + EQUALS.len());
    let current = form.current();
    let mut lines = Vec::new();
    let mut cursor = None;
    if let Some(body) = &form.body {
        cursor = Some(comment_lines(body, ref_box, &mut lines, t));
    } else if let Some(git_ref) = &form.git_ref {
        let active = current == Some(Field::Ref);
        let (spans, typed) = input(git_ref, ref_box, active, t);
        let mut line = vec![Span::styled(format!("{:<FORM_LABEL$}", "ref"), t.dim())];
        line.extend(spans);
        if active {
            cursor = Some((FORM_LABEL + 1 + typed, lines.len()));
        }
        lines.push(Line::from(line));
    }
    if form.takes_variables() {
        if !lines.is_empty() {
            lines.push(Line::raw(""));
        }
        lines.push(Line::styled("variables", t.title()));
    }
    for (row, (key, value)) in form.variables.iter().enumerate() {
        let (key_spans, key_typed) = input(key, FORM_KEY_BOX, current == Some(Field::Key(row)), t);
        let (value_spans, value_typed) =
            input(value, value_box, current == Some(Field::Value(row)), t);
        let mut line = vec![Span::raw(pad(FORM_LABEL))];
        line.extend(key_spans);
        line.push(Span::styled(EQUALS, t.dim()));
        line.extend(value_spans);
        if current == Some(Field::Key(row)) {
            cursor = Some((FORM_LABEL + 1 + key_typed, lines.len()));
        } else if current == Some(Field::Value(row)) {
            let start = FORM_LABEL + FORM_KEY_BOX + 2 + EQUALS.len() + 1;
            cursor = Some((start + value_typed, lines.len()));
        }
        lines.push(Line::from(line));
    }
    if form.variables.is_empty() && form.takes_variables() {
        lines.push(Line::styled(
            format!("{}none, ctrl+n adds one", pad(FORM_LABEL)),
            t.dim(),
        ));
    }
    let rect = centered(area, inner_width + 4, lines.len() + 2);
    let frame_block = block(&form.title(), true, t);
    let inner = frame_block.inner(rect);
    frame.render_widget(Clear, rect);
    frame.render_widget(frame_block, rect);
    frame.render_widget(Paragraph::new(lines), inner);
    if let Some((x, y)) = cursor {
        let (x, y) = (inner.x + x as u16, inner.y + y as u16);
        if x < inner.right() && y < inner.bottom() {
            frame.set_cursor_position(Position { x, y });
        }
    }
}

/// The box grows with the comment up to `COMMENT_ROWS` lines, and then shows the last ones. The
/// answer is the cursor, after the last line, because typing only adds at the end.
fn comment_lines(
    body: &str,
    box_width: usize,
    lines: &mut Vec<Line<'static>>,
    t: &Theme,
) -> (usize, usize) {
    let all: Vec<&str> = body.split('\n').collect();
    let shown = &all[all.len().saturating_sub(COMMENT_ROWS)..];
    let mut cursor = (0, 0);
    for (row, text) in shown.iter().enumerate() {
        let label = if row == 0 { "comment" } else { "" };
        let mut text = expand_tabs(text);
        if row + 1 < shown.len() {
            text = fit(&text, box_width.saturating_sub(1));
        }
        let (spans, typed) = input(&text, box_width, true, t);
        cursor = (FORM_LABEL + 1 + typed, lines.len());
        let mut line = vec![Span::styled(format!("{label:<FORM_LABEL$}"), t.dim())];
        line.extend(spans);
        lines.push(Line::from(line));
    }
    cursor
}

fn draw_confirm(frame: &mut Frame, area: Rect, question: &str, verb: &str, t: &Theme) {
    let lines = vec![
        Line::styled(question.to_string(), t.body()),
        Line::raw(""),
        Line::from(vec![
            Span::styled("y", t.body()),
            Span::styled(format!(" {verb}   "), t.dim()),
            Span::styled("n", t.body()),
            Span::styled(" cancel", t.dim()),
        ]),
    ];
    let inner_width = lines.iter().map(Line::width).max().unwrap_or(0);
    let rect = centered(area, inner_width + 4, lines.len() + 2);
    let frame_block = block("confirm", true, t);
    let inner = frame_block.inner(rect);
    frame.render_widget(Clear, rect);
    frame.render_widget(frame_block, rect);
    frame.render_widget(Paragraph::new(lines), inner);
}

/// Each command under its label, the chosen label between the marker cells.
fn draw_command_menu(frame: &mut Frame, area: Rect, menu: &CommandMenu, t: &Theme) {
    let room = (area.width as usize).saturating_sub(6);
    let mut lines = Vec::new();
    for (row, command) in menu.commands.iter().enumerate() {
        let chosen = row == menu.row;
        let (open, close) = if chosen { ("[", "]") } else { (" ", " ") };
        let label = if chosen { t.marked() } else { t.body() };
        lines.push(Line::from(vec![
            Span::styled(open, t.marker()),
            Span::styled(fit(command.label, room), label),
            Span::styled(close, t.marker()),
        ]));
        lines.push(Line::styled(
            format!(" {}", fit(&command.line, room)),
            t.dim(),
        ));
    }
    let inner_width = lines.iter().map(Line::width).max().unwrap_or(0);
    let rect = centered(area, inner_width + 4, lines.len() + 2);
    let title = format!("glab · {}", menu.project);
    let frame_block = block(&fit(&title, room), true, t);
    let inner = frame_block.inner(rect);
    frame.render_widget(Clear, rect);
    frame.render_widget(frame_block, rect);
    frame.render_widget(Paragraph::new(lines), inner);
}

fn draw_help(frame: &mut Frame, area: Rect, t: &Theme) {
    let art = area.width as usize >= mark::WIDTH + 6 && area.height as usize >= HELP.len() + 11;
    let mut lines = if art {
        mark::lines(t)
    } else {
        vec![Line::styled("{lazylab}", t.marked())]
    };
    lines.push(Line::raw(""));
    for (keys, action) in HELP {
        lines.push(Line::from(vec![
            Span::styled(format!("{keys:<HELP_KEY_COLUMN$}"), t.body()),
            Span::styled(*action, t.dim()),
        ]));
    }
    let inner_width = lines
        .iter()
        .map(|l| l.spans.iter().map(|s| width(&s.content)).sum::<usize>())
        .max()
        .unwrap_or(0);
    let rect = centered(area, inner_width + 4, lines.len() + 2);
    let frame_block = block("help", true, t);
    let inner = frame_block.inner(rect);
    frame.render_widget(Clear, rect);
    frame.render_widget(frame_block, rect);
    frame.render_widget(Paragraph::new(lines), inner);
}

fn centered(area: Rect, w: usize, h: usize) -> Rect {
    let w = (w.min(area.width as usize)) as u16;
    let h = (h.min(area.height as usize)) as u16;
    Rect {
        x: area.x + (area.width - w) / 2,
        y: area.y + (area.height - h) / 2,
        width: w,
        height: h,
    }
}
