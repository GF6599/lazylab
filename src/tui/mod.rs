//! The terminal interface, drawn with ratatui in the house terminal idiom.

pub mod app;
pub mod clipboard;
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

use std::env;
use std::io::{self, Write};
use std::panic;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use ratatui::backend::Backend;
use ratatui::crossterm::event::{self, DisableBracketedPaste, EnableBracketedPaste, Event};
use ratatui::crossterm::execute;
use ratatui::crossterm::terminal::{EnterAlternateScreen, enable_raw_mode};
use ratatui::{DefaultTerminal, Frame, Terminal};
use time::OffsetDateTime;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};
use tokio::time::MissedTickBehavior;
use url::Url;

use crate::explorer::{self, Browse};
use app::{App, Cmd, Msg, TICK};
pub use source::Source;
use view::Context;

/// How long the input thread waits for a key before it looks whether it has to stop.
const INPUT_POLL: Duration = Duration::from_millis(50);

/// Takes the terminal for the session and gives it back as it was, even when the session fails.
/// `explorer` is the program that `e` hands the terminal to.
pub fn launch(host: &Url, page_size: u8, explorer: &str, source: Source) -> io::Result<()> {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    let mut terminal = ratatui::try_init()?;
    let restore = panic::take_hook();
    panic::set_hook(Box::new(move |info| {
        paste_off();
        restore(info);
    }));
    let ended = execute!(io::stdout(), EnableBracketedPaste).and_then(|()| {
        runtime.block_on(session(
            &mut terminal,
            host,
            App::new(page_size).with_host(host.clone()),
            source,
            explorer,
        ))
    });
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
    program: &str,
) -> io::Result<()> {
    let (outbox, inbox) = mpsc::unbounded_channel();
    let mut input = Input::start(outbox.clone());
    tick(outbox.clone());
    let restart = outbox.clone();
    let browse = move |request: &Browse| {
        input.stop();
        paste_off();
        ratatui::restore();
        let ended = explorer::browse(program, env::var_os("PATH").as_deref(), request);
        if let Err(e) = take_back() {
            tracing::warn!(error = %e, "take the terminal back from the explorer");
        }
        input = Input::start(restart.clone());
        Msg::Browsed {
            program: program.to_string(),
            ended,
        }
    };
    let mut context = Context {
        host: host.host_str().unwrap_or_default().to_string(),
        now: OffsetDateTime::now_utc(),
    };
    run(
        terminal,
        app,
        source,
        (outbox, inbox),
        &mut io::stdout(),
        |frame, app| {
            context.now = OffsetDateTime::now_utc();
            view::draw(frame, app, &context);
        },
        browse,
    )
    .await?;
    Ok(())
}

/// The terminal as `ratatui::try_init` leaves it, without another panic hook.
fn take_back() -> io::Result<()> {
    enable_raw_mode()?;
    execute!(io::stdout(), EnterAlternateScreen, EnableBracketedPaste)
}

/// Reading the terminal blocks, so it runs on a thread of its own. The thread stops while the
/// explorer has the terminal, so the two never read the same keys.
struct Input {
    stop: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
}

impl Input {
    fn start(outbox: UnboundedSender<Msg>) -> Input {
        let stop = Arc::new(AtomicBool::new(false));
        let stopping = Arc::clone(&stop);
        let thread = thread::spawn(move || {
            while !stopping.load(Ordering::Relaxed) {
                if !event::poll(INPUT_POLL).unwrap_or(false) {
                    continue;
                }
                let msg = match event::read() {
                    Ok(Event::Key(key)) => Msg::Key(key),
                    Ok(Event::Paste(text)) => Msg::Paste(text),
                    Ok(_) => continue,
                    Err(_) => return,
                };
                if outbox.send(msg).is_err() {
                    return;
                }
            }
        });
        Input {
            stop,
            thread: Some(thread),
        }
    }

    /// Returns once the thread has ended, which takes at most one `INPUT_POLL`.
    fn stop(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

fn tick(outbox: UnboundedSender<Msg>) {
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(TICK);
        // The explorer holds the loop for as long as the reader browses, and the ticks it
        // missed would otherwise arrive at once.
        interval.set_missed_tick_behavior(MissedTickBehavior::Delay);
        loop {
            interval.tick().await;
            if outbox.send(Msg::Tick).is_err() {
                return;
            }
        }
    });
}

/// Keys, ticks and finished work all arrive through `inbox`, so they are handled one at a time.
/// A copy goes to `clipboard`, which is the output the terminal draws to. `browse` has the
/// terminal while the explorer runs, and answers how it ended.
pub async fn run<B: Backend>(
    terminal: &mut Terminal<B>,
    mut app: App,
    source: Source,
    (outbox, mut inbox): (UnboundedSender<Msg>, UnboundedReceiver<Msg>),
    clipboard: &mut impl Write,
    mut draw: impl FnMut(&mut Frame, &App),
    mut browse: impl FnMut(&Browse) -> Msg,
) -> Result<App, B::Error> {
    if let Source::Demo = source {
        app = app.demo();
    }
    let mut commands = app.start();
    loop {
        for command in commands.drain(..) {
            match command {
                Cmd::Copy(text) => copy(clipboard, &text),
                Cmd::Browse(request) => {
                    // A send fails only once the loop has ended, when no one needs the answer.
                    let _ = outbox.send(browse(&request));
                    terminal.clear()?;
                }
                command => dispatch(command, &source, &outbox),
            }
        }
        if app.quit {
            break;
        }
        terminal.draw(|frame| draw(frame, &app))?;
        match inbox.recv().await {
            Some(msg) => commands = app.update(msg),
            None => break,
        }
    }
    Ok(app)
}

fn copy(clipboard: &mut impl Write, text: &str) {
    let written = clipboard.write_all(&clipboard::osc52(text));
    if let Err(e) = written.and_then(|()| clipboard.flush()) {
        tracing::warn!(error = %e, "write the clipboard");
    }
}

fn dispatch(command: Cmd, source: &Source, outbox: &UnboundedSender<Msg>) {
    let (source, outbox) = (source.clone(), outbox.clone());
    tokio::spawn(async move {
        if let Some(msg) = source.answer(command).await {
            // A send fails only once the loop has ended, when no one needs the answer.
            let _ = outbox.send(msg);
        }
    });
}
