//! The terminal interface, drawn with ratatui in the house terminal idiom.

pub mod app;
pub mod demo;
pub mod mark;
pub mod panel;
pub mod text;
pub mod theme;
pub mod view;

use std::io;
use std::thread;
use std::time::Duration;

use ratatui::backend::Backend;
use ratatui::crossterm::event::{self, Event};
use ratatui::{DefaultTerminal, Frame, Terminal};
use time::OffsetDateTime;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};
use url::Url;

use app::{App, Cmd, Msg};
use view::Context;

/// A running job's spinner moves on each tick.
const TICK: Duration = Duration::from_millis(100);

/// Takes the terminal for the session and gives it back as it was, even when the session fails.
pub fn launch(host: &Url) -> io::Result<()> {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_time()
        .build()?;
    let mut terminal = ratatui::try_init()?;
    let ended = runtime.block_on(session(&mut terminal, host));
    ratatui::restore();
    ended
}

async fn session(terminal: &mut DefaultTerminal, host: &Url) -> io::Result<()> {
    let (outbox, inbox) = mpsc::unbounded_channel();
    read_keys(outbox.clone());
    tick(outbox.clone());
    let mut context = Context {
        host: host.host_str().unwrap_or_default().to_string(),
        now: OffsetDateTime::now_utc(),
    };
    run(terminal, inbox, outbox, |frame, app| {
        context.now = OffsetDateTime::now_utc();
        view::draw(frame, app, &context);
    })
    .await?;
    Ok(())
}

/// Reading a key blocks, so it runs on a thread of its own. The thread ends with the process.
fn read_keys(outbox: UnboundedSender<Msg>) {
    thread::spawn(move || {
        while let Ok(event) = event::read() {
            if let Event::Key(key) = event
                && outbox.send(Msg::Key(key)).is_err()
            {
                return;
            }
        }
    });
}

fn tick(outbox: UnboundedSender<Msg>) {
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(TICK);
        loop {
            interval.tick().await;
            if outbox.send(Msg::Tick).is_err() {
                return;
            }
        }
    });
}

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
