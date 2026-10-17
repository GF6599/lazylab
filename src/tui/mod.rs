//! The terminal interface, drawn with ratatui in the house terminal idiom.

pub mod app;
pub mod demo;
pub mod diff;
pub mod form;
pub mod jobs;
pub mod log;
pub mod mark;
pub mod panel;
pub mod source;
pub mod text;
pub mod theme;
pub mod view;

use std::io;
use std::panic;
use std::thread;

use ratatui::backend::Backend;
use ratatui::crossterm::event::{self, DisableBracketedPaste, EnableBracketedPaste, Event};
use ratatui::crossterm::execute;
use ratatui::{DefaultTerminal, Frame, Terminal};
use time::OffsetDateTime;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};
use url::Url;

use app::{App, Cmd, Msg, TICK};
pub use source::Source;
use view::Context;

/// Takes the terminal for the session and gives it back as it was, even when the session fails.
pub fn launch(host: &Url, page_size: u8, source: Source) -> io::Result<()> {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    let mut terminal = ratatui::try_init()?;
    let restore = panic::take_hook();
    panic::set_hook(Box::new(move |info| {
        paste_off();
        restore(info);
    }));
    let ended = execute!(io::stdout(), EnableBracketedPaste)
        .and_then(|()| runtime.block_on(session(&mut terminal, host, App::new(page_size), source)));
    paste_off();
    ratatui::restore();
    ended
}

/// The shell after lazylab would otherwise get every paste wrapped in escape codes.
fn paste_off() {
    let _ = execute!(io::stdout(), DisableBracketedPaste);
}

async fn session(
    terminal: &mut DefaultTerminal,
    host: &Url,
    app: App,
    source: Source,
) -> io::Result<()> {
    let (outbox, inbox) = mpsc::unbounded_channel();
    read_input(outbox.clone());
    tick(outbox.clone());
    let mut context = Context {
        host: host.host_str().unwrap_or_default().to_string(),
        now: OffsetDateTime::now_utc(),
    };
    run(terminal, app, source, inbox, outbox, |frame, app| {
        context.now = OffsetDateTime::now_utc();
        view::draw(frame, app, &context);
    })
    .await?;
    Ok(())
}

/// Reading the terminal blocks, so it runs on a thread of its own. The thread ends with the process.
fn read_input(outbox: UnboundedSender<Msg>) {
    thread::spawn(move || {
        while let Ok(event) = event::read() {
            let msg = match event {
                Event::Key(key) => Msg::Key(key),
                Event::Paste(text) => Msg::Paste(text),
                _ => continue,
            };
            if outbox.send(msg).is_err() {
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
    mut app: App,
    source: Source,
    mut inbox: UnboundedReceiver<Msg>,
    outbox: UnboundedSender<Msg>,
    mut draw: impl FnMut(&mut Frame, &App),
) -> Result<App, B::Error> {
    dispatch(app.start(), &source, &outbox);
    while !app.quit {
        terminal.draw(|frame| draw(frame, &app))?;
        match inbox.recv().await {
            Some(msg) => dispatch(app.update(msg), &source, &outbox),
            None => break,
        }
    }
    Ok(app)
}

fn dispatch(commands: Vec<Cmd>, source: &Source, outbox: &UnboundedSender<Msg>) {
    for command in commands {
        let (source, outbox) = (source.clone(), outbox.clone());
        tokio::spawn(async move {
            if let Some(msg) = source.answer(command).await {
                // A send fails only once the loop has ended, when no one needs the answer.
                let _ = outbox.send(msg);
            }
        });
    }
}
