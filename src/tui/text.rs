use ratatui::text::Span;
use time::OffsetDateTime;
use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

pub fn width(s: &str) -> usize {
    s.width()
}

pub fn spans_width(spans: &[Span<'_>]) -> usize {
    spans.iter().map(|s| s.content.width()).sum()
}

fn cut(s: &str, max: usize) -> String {
    if max == 0 {
        return String::new();
    }
    let mut out = String::new();
    let mut used = 0;
    for c in s.chars() {
        let w = c.width().unwrap_or(0);
        if used + w > max - 1 {
            break;
        }
        out.push(c);
        used += w;
    }
    out.push('…');
    out
}

pub fn fit(s: &str, max: usize) -> String {
    if s.width() <= max {
        s.to_string()
    } else {
        cut(s, max)
    }
}

/// Keeps the end of `s` after an ellipsis, so the end of the text being typed stays in view.
pub fn fit_end(s: &str, max: usize) -> String {
    if s.width() <= max {
        return s.to_string();
    }
    if max == 0 {
        return String::new();
    }
    let mut kept = Vec::new();
    let mut used = 0;
    for c in s.chars().rev() {
        let w = c.width().unwrap_or(0);
        if used + w > max - 1 {
            break;
        }
        kept.push(c);
        used += w;
    }
    std::iter::once('…').chain(kept.into_iter().rev()).collect()
}

pub fn clamp_spans(spans: Vec<Span<'static>>, max: usize) -> Vec<Span<'static>> {
    if spans_width(&spans) <= max {
        return spans;
    }
    let mut out = Vec::new();
    let mut used = 0;
    for span in spans {
        let w = span.content.width();
        if used + w < max {
            used += w;
            out.push(span);
            continue;
        }
        if max > used {
            out.push(Span::styled(cut(&span.content, max - used), span.style));
        }
        break;
    }
    out
}

const TAB_STOP: usize = 4;

/// Each tab reaches the next stop of `TAB_STOP` columns, counted from the start of `text`,
/// because a terminal cell cannot hold a tab.
pub fn expand_tabs(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut column = 0;
    for c in text.chars() {
        if c == '\t' {
            let spaces = TAB_STOP - column % TAB_STOP;
            out.extend(std::iter::repeat_n(' ', spaces));
            column += spaces;
        } else {
            out.push(c);
            column += c.width().unwrap_or(0);
        }
    }
    out
}

pub fn pad(n: usize) -> String {
    " ".repeat(n)
}

/// How long ago `then` was, in the largest whole unit: `4m`, `3h`, `2d`, `1w` or `5mo`.
pub fn age(now: OffsetDateTime, then: OffsetDateTime) -> String {
    let minutes = (now - then).whole_minutes().max(0);
    match minutes {
        0..60 => format!("{minutes}m"),
        60..1440 => format!("{}h", minutes / 60),
        1440..10080 => format!("{}d", minutes / 1440),
        10080..43200 => format!("{}w", minutes / 10080),
        _ => format!("{}mo", minutes / 43200),
    }
}
