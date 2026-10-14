use ratatui::Frame;
use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use time::OffsetDateTime;

use super::Context;
use crate::tui::app::{App, Focus, Pane};
use crate::tui::panel::{
    Row, block, bottom_label, folded_rule, label_style, light_corners, marked_line, window_start,
};
use crate::tui::text::age;
use crate::tui::theme::Theme;

/// Rows an unfocused pane keeps when the sidebar has room for it to peek.
const PEEK_ROWS: u16 = 4;
const FOCUS_MIN_ROWS: u16 = 3;
const BORDERS: u16 = 2;

/// Unfocused panes peek at a few rows while there is room, and fold to a one-row rule when there
/// is not or when the full-screen mode is on. Every height is a whole row count taken from what
/// is left, so no pane can be handed less than nothing.
pub fn plan(
    height: u16,
    in_view: Pane,
    zoom: bool,
    wants: impl Fn(Pane) -> u16,
) -> [u16; Pane::ALL.len()] {
    let mut heights = [0u16; Pane::ALL.len()];
    let others: Vec<Pane> = Pane::ALL.into_iter().filter(|&p| p != in_view).collect();
    let peeks: Vec<u16> = others
        .iter()
        .map(|&p| wants(p).clamp(1, PEEK_ROWS) + BORDERS)
        .collect();
    let peek_total: u16 = peeks.iter().sum();
    let folded_total = others.len() as u16;

    if !zoom && height >= peek_total + FOCUS_MIN_ROWS + BORDERS {
        for (pane, h) in others.iter().zip(peeks) {
            heights[pane.index()] = h;
        }
        heights[in_view.index()] = height - peek_total;
    } else if height > folded_total {
        for pane in &others {
            heights[pane.index()] = 1;
        }
        heights[in_view.index()] = height - folded_total;
    } else {
        heights[in_view.index()] = height;
    }
    heights
}

pub(super) fn draw(frame: &mut Frame, app: &App, context: &Context, area: Rect, t: &Theme) {
    let heights = plan(area.height, app.pane, app.zoom, |p| app.len(p) as u16);
    let rects = Layout::vertical(heights.map(Constraint::Length)).split(area);
    for pane in Pane::ALL {
        draw_pane(frame, app, context, pane, rects[pane.index()], t);
    }
}

fn draw_pane(frame: &mut Frame, app: &App, context: &Context, pane: Pane, area: Rect, t: &Theme) {
    if area.height == 0 || area.width == 0 {
        return;
    }
    let lit = pane == app.pane && app.focus == Focus::Sidebar;
    if area.height < 3 {
        let rule = folded_rule(pane.label(), summary(app, pane, t), area.width, t);
        let row = Rect { height: 1, ..area };
        frame.render_widget(Paragraph::new(rule), row);
        if lit {
            frame.buffer_mut()[(row.x, row.y)].set_style(t.marker());
        }
        return;
    }

    let len = app.len(pane);
    let position = if len == 0 {
        "empty".to_string()
    } else {
        format!("{}/{len}", app.cursor(pane) + 1)
    };
    let frame_block = block(pane.label(), false, t).title_bottom(bottom_label(position, t));
    let list = frame_block.inner(area);
    frame.render_widget(frame_block, area);

    let cursor = app.cursor(pane);
    let lines: Vec<Line> = (0..len)
        .map(|i| {
            marked_line(
                row(app, context, pane, i, t),
                list.width,
                i == cursor,
                lit,
                t,
            )
        })
        .collect();
    let start = window_start(lines.len(), cursor, list.height as usize);
    let visible: Vec<Line> = lines
        .into_iter()
        .skip(start)
        .take(list.height as usize)
        .collect();
    frame.render_widget(Paragraph::new(visible), list);
    if lit {
        light_corners(frame.buffer_mut(), area, t);
    }
}

/// What a folded rule names, so the selection stays readable when its pane has no rows.
fn summary(app: &App, pane: Pane, t: &Theme) -> Vec<Span<'static>> {
    let cursor = app.cursor(pane);
    match pane {
        Pane::Projects => app
            .projects
            .get(cursor)
            .map(|p| vec![Span::styled(p.path_with_namespace.clone(), t.body())]),
        Pane::Pipelines => app.pipelines.get(cursor).map(|p| {
            let (glyph, style) = t.status(p.status, app.tick);
            vec![
                Span::styled(glyph, style),
                Span::styled(format!(" #{} {}", p.id, p.git_ref), t.body()),
            ]
        }),
        Pane::Jobs => app.jobs.get(cursor).map(|j| {
            let (glyph, style) = t.status(j.status, app.tick);
            vec![
                Span::styled(glyph, style),
                Span::styled(format!(" {}:{}", j.stage, j.name), t.body()),
            ]
        }),
        Pane::MergeRequests => app
            .merge_requests
            .get(cursor)
            .map(|mr| vec![Span::styled(format!("!{} {}", mr.iid, mr.title), t.body())]),
    }
    .unwrap_or_default()
}

fn row(app: &App, context: &Context, pane: Pane, i: usize, t: &Theme) -> Row {
    let current = i == app.cursor(pane);
    let label = label_style(current, t);
    let when = |at: Option<OffsetDateTime>| {
        vec![Span::styled(
            at.map(|at| age(context.now, at)).unwrap_or_default(),
            t.dim(),
        )]
    };
    match pane {
        Pane::Projects => {
            let p = &app.projects[i];
            Row {
                left: vec![Span::styled(p.path_with_namespace.clone(), label)],
                right: when(p.last_activity_at),
            }
        }
        Pane::Pipelines => {
            let p = &app.pipelines[i];
            let (glyph, style) = t.status(p.status, app.tick);
            Row {
                left: vec![
                    Span::styled(glyph, style),
                    Span::styled(format!(" #{}", p.id), label),
                    Span::styled(format!(" {}", p.git_ref), t.title()),
                ],
                right: when(p.updated()),
            }
        }
        Pane::Jobs => {
            let j = &app.jobs[i];
            let (glyph, style) = t.status(j.status, app.tick);
            Row {
                left: vec![
                    Span::styled(glyph, style),
                    Span::styled(format!(" {}", j.name), label),
                ],
                right: vec![Span::styled(j.stage.clone(), t.dim())],
            }
        }
        Pane::MergeRequests => {
            let mr = &app.merge_requests[i];
            Row {
                left: vec![
                    Span::styled(format!("!{} ", mr.iid), t.dim()),
                    Span::styled(mr.title.clone(), label),
                ],
                right: when(mr.updated_at),
            }
        }
    }
}
