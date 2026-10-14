//! The `{lazylab}` lockup. The art is `figlet -f ANSI_Shadow lazylab`. The braces are drawn from
//! box runes, because a terminal cannot scale one glyph to the art's height.

use ratatui::text::{Line, Span};

use super::theme::Theme;

const ART_WIDTH: usize = 57;
const ART: [&str; 6] = [
    "██╗      █████╗ ███████╗██╗   ██╗██╗      █████╗ ██████╗",
    "██║     ██╔══██╗╚══███╔╝╚██╗ ██╔╝██║     ██╔══██╗██╔══██╗",
    "██║     ███████║  ███╔╝  ╚████╔╝ ██║     ███████║██████╔╝",
    "██║     ██╔══██║ ███╔╝    ╚██╔╝  ██║     ██╔══██║██╔══██╗",
    "███████╗██║  ██║███████╗   ██║   ███████╗██║  ██║██████╔╝",
    "╚══════╝╚═╝  ╚═╝╚══════╝   ╚═╝   ╚══════╝╚═╝  ╚═╝╚═════╝",
];
const LEFT_BRACE: [&str; 6] = ["╭", "│", "┘", "┐", "│", "╰"];
const RIGHT_BRACE: [&str; 6] = ["╮", "│", "└", "┌", "│", "╯"];

pub const WIDTH: usize = ART_WIDTH + 4;

pub fn lines(t: &Theme) -> Vec<Line<'static>> {
    (0..ART.len())
        .map(|i| {
            Line::from(vec![
                Span::styled(LEFT_BRACE[i], t.title()),
                Span::raw(" "),
                Span::styled(format!("{:<ART_WIDTH$}", ART[i]), t.marked()),
                Span::raw(" "),
                Span::styled(RIGHT_BRACE[i], t.title()),
            ])
        })
        .collect()
}
