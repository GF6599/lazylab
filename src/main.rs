use std::process::ExitCode;

use clap::Parser;
use lazylab::redact::redact;
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
    logging::init(config.log_level);
    if let Some(path) = &config.config_file {
        tracing::debug!(path = %path.display(), "using config file");
    }
    ExitCode::SUCCESS
}
