use std::io;
use std::process::ExitCode;
use std::sync::Mutex;

use clap::Parser;
use lazylab::redact::redact;
use lazylab::store;
use lazylab::tui::{self, Source};
use lazylab::{config, exit, glab, logging};

#[derive(Parser)]
#[command(name = "lazylab", version, about)]
struct Cli {
    #[command(flatten)]
    flags: config::Flags,
}

fn main() -> ExitCode {
    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(e) if e.use_stderr() => {
            eprint!("{}", redact(&e.render().to_string()));
            return ExitCode::from(exit::GENERIC);
        }
        Err(e) => {
            let _ = e.print();
            return ExitCode::SUCCESS;
        }
    };
    let config = match config::load(&cli.flags, &glab::resolve) {
        Ok(config) => config,
        Err(e) => {
            eprintln!("error: {}", redact(&format!("config error: {e}")));
            return ExitCode::from(exit::GENERIC);
        }
    };
    let dir = store::cache_dir();
    match dir.as_deref().map(logging::open) {
        Some(Ok(file)) => logging::init(config.log_level, Mutex::new(file)),
        Some(Err(e)) => {
            eprintln!("warning: {}", redact(&format!("no log file: {e}")));
            logging::init(config.log_level, io::sink);
        }
        None => logging::init(config.log_level, io::sink),
    }
    if let Some(path) = &config.config_file {
        tracing::debug!(path = %path.display(), "using config file");
    }
    let source = match Source::new(&config.host, &config.mode, dir) {
        Ok(source) => source,
        Err(e) => {
            eprintln!("error: {e}");
            return ExitCode::from(e.exit_code());
        }
    };
    let launched = tui::launch(
        &config.host,
        config.projects_per_page,
        &config.explorer,
        source,
    );
    if let Err(e) = launched {
        eprintln!("error: {}", redact(&format!("terminal error: {e}")));
        return ExitCode::from(exit::GENERIC);
    }
    ExitCode::SUCCESS
}
