//! Rendering. Layout: prompt (1) | pane strip (1) | body (rest) | status bar (1).
//!
//! From `WIDE` columns up the body puts the sidebar beside the detail pane. Below it the body
//! shows one of the two, and `l` / `h` move between them.

mod chrome;
mod detail;
pub mod sidebar;

use ratatui::Frame;
use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Clear, Paragraph};
use time::OffsetDateTime;

use super::app::{App, Focus, Overlay};
use super::mark;
use super::panel::block;
use super::text::width;
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
