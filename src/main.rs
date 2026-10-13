//! Lazylab, a terminal UI for GitLab.

mod config;
mod glab;
mod logging;
mod redact;

use std::process::ExitCode;

use clap::Parser;

use crate::redact::redact;

/// Scripts that wrap lazylab read the exit code, so each value keeps its meaning across
/// releases. clap exits with 2 on a usage error, which here means a transient failure that is
/// safe to retry, so a usage error is mapped to this one instead.
const EXIT_GENERIC: u8 = 1;

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
            return ExitCode::from(EXIT_GENERIC);
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
            return ExitCode::from(EXIT_GENERIC);
        }
    };
    logging::init(config.log_level);
    if let Some(path) = &config.config_file {
        tracing::debug!(path = %path.display(), "using config file");
    }
    ExitCode::SUCCESS
}
