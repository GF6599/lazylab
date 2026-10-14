use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Padding};

use super::text::{clamp_spans, pad, spans_width};
use super::theme::Theme;

/// Every frame on the surface is built here, so the border style stays one lever.
const BORDER: BorderType = BorderType::Plain;

pub fn block(title: &str, padded: bool, t: &Theme) -> Block<'static> {
    let mut block = Block::bordered()
        .border_type(BORDER)
        .border_style(t.border())
        .style(t.surface())
        .title(Line::from(vec![
            Span::styled("─", t.border()),
            Span::styled(format!(" {title} "), t.title()),
        ]));
    if padded {
        block = block.padding(Padding::horizontal(1));
    }
    block
}

/// The region-scale form of the `[ ]` marker.
pub fn light_corners(buf: &mut Buffer, area: Rect, t: &Theme) {
    if area.width < 2 || area.height < 2 {
        return;
    }
    let (left, top) = (area.x, area.y);
    let (right, bottom) = (area.right() - 1, area.bottom() - 1);
    for (x, y) in [
        (left, top),
        (left + 1, top),
        (left, top + 1),
        (right, bottom),
        (right - 1, bottom),
        (right, bottom - 1),
    ] {
        buf[(x, y)].set_style(t.marker());
    }
}

/// A folded panel: one rule that still names its pane and the selection inside it.
pub fn folded_rule(
    title: &str,
    summary: Vec<Span<'static>>,
    width: u16,
    t: &Theme,
) -> Line<'static> {
    let width = width as usize;
    let lead = vec![
        Span::styled("─ ", t.border()),
        Span::styled(title.to_string(), t.title()),
    ];
    let mut spans = lead;
    if !summary.is_empty() {
        spans.push(Span::styled(" · ", t.dim()));
        spans.extend(summary);
    }
    // The gap after the label is reserved before the fill, so the label never welds to the rule.
    let mut spans = clamp_spans(spans, width.saturating_sub(1));
    spans.push(Span::raw(" "));
    let fill = width.saturating_sub(spans_width(&spans));
    spans.push(Span::styled("─".repeat(fill), t.border()));
    Line::from(spans).style(t.surface())
}

/// The bracket cells are blank on every row that is not current, so the column holds still as
/// the cursor travels, and they replace the frame's horizontal padding rather than adding to it.
pub struct Row {
    pub left: Vec<Span<'static>>,
    pub right: Vec<Span<'static>>,
}

pub fn marked_line(row: Row, width: u16, current: bool, filled: bool, t: &Theme) -> Line<'static> {
    let inner = (width as usize).saturating_sub(2);
    let mut right = row.right;
    let mut right_w = spans_width(&right);
    if right_w + 1 >= inner {
        right.clear();
        right_w = 0;
    }
    let left_budget = inner.saturating_sub(if right_w > 0 { right_w + 1 } else { 0 });
    let left = clamp_spans(row.left, left_budget);
    let gap = inner.saturating_sub(spans_width(&left) + right_w);
    let (open, close) = if current { ("[", "]") } else { (" ", " ") };

    let mut spans = vec![Span::styled(open, t.marker())];
    spans.extend(left);
    spans.push(Span::raw(pad(gap)));
    spans.extend(right);
    spans.push(Span::styled(close, t.marker()));
    let line = Line::from(spans);
    if current && filled {
        line.style(t.selected_row())
    } else {
        line
    }
}

pub fn label_style(current: bool, t: &Theme) -> Style {
    if current { t.marked() } else { t.body() }
}
