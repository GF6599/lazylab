use ratatui::style::{Color, Style};

use crate::gitlab::Status;

/// Rose Pine main. The values are the house palette, held here once so every surface reads the
/// same slot instead of picking a hex of its own.
pub struct Theme {
    pub base: Color,
    pub muted: Color,
    pub subtle: Color,
    pub text: Color,
    pub love: Color,
    pub gold: Color,
    pub rose: Color,
    pub pine: Color,
    pub foam: Color,
    pub iris: Color,
    pub hl_low: Color,
    pub hl_med: Color,
}

pub const ROSE_PINE: Theme = Theme {
    base: Color::from_u32(0x191724),
    muted: Color::from_u32(0x6e6a86),
    subtle: Color::from_u32(0x908caa),
    text: Color::from_u32(0xe0def4),
    love: Color::from_u32(0xeb6f92),
    gold: Color::from_u32(0xf6c177),
    rose: Color::from_u32(0xebbcba),
    pine: Color::from_u32(0x31748f),
    foam: Color::from_u32(0x9ccfd8),
    iris: Color::from_u32(0xc4a7e7),
    hl_low: Color::from_u32(0x21202e),
    hl_med: Color::from_u32(0x403d52),
};

const SPINNER: [&str; 10] = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

impl Theme {
    pub fn surface(&self) -> Style {
        Style::new().fg(self.text).bg(self.base)
    }

    pub fn border(&self) -> Style {
        Style::new().fg(self.muted)
    }

    pub fn title(&self) -> Style {
        Style::new().fg(self.subtle)
    }

    pub fn dim(&self) -> Style {
        Style::new().fg(self.muted)
    }

    pub fn body(&self) -> Style {
        Style::new().fg(self.text)
    }

    /// Chrome saying "this one". Distinct from `marked`, which is the item itself.
    pub fn marker(&self) -> Style {
        Style::new().fg(self.gold)
    }

    pub fn marked(&self) -> Style {
        Style::new().fg(self.iris)
    }

    pub fn selected_row(&self) -> Style {
        Style::new().bg(self.hl_low)
    }

    pub fn status(&self, status: Status, tick: u64) -> (&'static str, Style) {
        let (glyph, color) = match status {
            Status::Success => ("✓", self.pine),
            Status::Failed => ("✗", self.love),
            Status::Running => (SPINNER[(tick as usize) % SPINNER.len()], self.foam),
            Status::Created
            | Status::WaitingForResource
            | Status::WaitingForCallback
            | Status::Preparing
            | Status::Pending
            | Status::Scheduled => ("◦", self.subtle),
            // A cancel in progress ends where a finished one already is.
            Status::Canceled | Status::Canceling => ("⊘", self.muted),
            Status::Skipped => ("−", self.muted),
            Status::Manual => ("►", self.rose),
            Status::Blocked => ("◧", self.rose),
            Status::Unknown => ("?", self.muted),
        };
        (glyph, Style::new().fg(color))
    }
}
