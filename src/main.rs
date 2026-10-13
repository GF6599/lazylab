//! Lazylab, a terminal UI for GitLab.

mod logging;
mod redact;

use std::process::ExitCode;

use clap::Parser;
use tracing::Level;

use crate::redact::redact;

/// Scripts that wrap lazylab read the exit code, so each value keeps its meaning across
/// releases. clap exits with 2 on a usage error, which here means a transient failure that is
/// safe to retry, so a usage error is mapped to this one instead.
const EXIT_GENERIC: u8 = 1;

#[derive(Parser)]
#[command(name = "lazylab", version, about)]
struct Cli {}

fn main() -> ExitCode {
    match Cli::try_parse() {
        Ok(Cli {}) => {
            logging::init(Level::ERROR);
            ExitCode::SUCCESS
        }
        Err(e) if e.use_stderr() => {
            eprint!("{}", redact(&e.render().to_string()));
            ExitCode::from(EXIT_GENERIC)
        }
        Err(e) => {
            let _ = e.print();
            ExitCode::SUCCESS
        }
    }
}
