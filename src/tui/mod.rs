//! The terminal interface, drawn with ratatui in the house terminal idiom.

pub mod app;
pub mod demo;
pub mod mark;
pub mod panel;
pub mod text;
pub mod theme;

use ratatui::backend::Backend;
use ratatui::{Frame, Terminal};
use time::OffsetDateTime;
use tokio::sync::mpsc::{UnboundedReceiver, UnboundedSender};

use app::{App, Cmd, Msg};

/// Keys, ticks and finished work all arrive through `inbox`, so they are handled one at a time.
pub async fn run<B: Backend>(
    terminal: &mut Terminal<B>,
    mut inbox: UnboundedReceiver<Msg>,
    outbox: UnboundedSender<Msg>,
    mut draw: impl FnMut(&mut Frame, &App),
) -> Result<App, B::Error> {
    let mut app = App::new();
    dispatch(app.start(), &outbox);
    while !app.quit {
        terminal.draw(|frame| draw(frame, &app))?;
        match inbox.recv().await {
            Some(msg) => dispatch(app.update(msg), &outbox),
            None => break,
        }
    }
    Ok(app)
}

fn dispatch(commands: Vec<Cmd>, outbox: &UnboundedSender<Msg>) {
    for command in commands {
        let outbox = outbox.clone();
        tokio::spawn(async move {
            // A send fails only once the loop has ended, when no one needs the answer.
            let _ = outbox.send(demo::answer(command, OffsetDateTime::now_utc()));
        });
    }
}
