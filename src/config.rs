//! Resolves the settings from flags, `GITLAB_*` environment variables, a config file and
//! defaults, in that order of precedence.

use std::env;
use std::fmt;
use std::path::{Path, PathBuf};

use clap::Args;
use figment::Figment;
use figment::providers::{Env, Format, Json, Serialized, Toml, Yaml};
use serde::{Deserialize, Serialize};
use tracing::Level;
use url::Url;

const DEFAULT_HOST: &str = "https://gitlab.com";
const DEFAULT_PROJECTS_PER_PAGE: u8 = 30;
/// The GitLab API caps a page here, and above it returns fewer rows with no error.
const MAX_PROJECTS_PER_PAGE: i64 = 100;
const ENV_PREFIX: &str = "GITLAB_";
const ENV_KEYS: [&str; 4] = ["host", "token", "projects_per_page", "log_level"];

#[derive(Args, Serialize, Default)]
#[serde(rename_all = "kebab-case")]
pub struct Flags {
    /// GitLab host, defaults to https://gitlab.com
    #[arg(long)]
    #[serde(skip_serializing_if = "Option::is_none")]
    pub host: Option<String>,
    /// GitLab personal access token (api scope). Prefer $GITLAB_TOKEN, because a flag value is
    /// visible in the process list
    #[arg(long)]
    #[serde(skip_serializing_if = "Option::is_none")]
    pub token: Option<String>,
    /// Number of projects to request per page
    #[arg(long)]
    #[serde(skip_serializing_if = "Option::is_none")]
    pub projects_per_page: Option<i64>,
    /// Log level: debug, info, warn, error
    #[arg(long)]
    #[serde(skip_serializing_if = "Option::is_none")]
    pub log_level: Option<String>,
    /// Optional config file (YAML, TOML, JSON)
    #[arg(long)]
    #[serde(skip)]
    pub config: Option<PathBuf>,
}

/// Prints as a placeholder, so no `{:?}` of a config can put the token in a log line.
#[derive(Clone, PartialEq)]
pub struct Token(String);

impl fmt::Debug for Token {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("[REDACTED]")
    }
}

#[derive(Debug, PartialEq)]
pub struct Config {
    pub host: Url,
    pub token: Token,
    pub projects_per_page: u8,
    pub log_level: Level,
    pub config_file: Option<PathBuf>,
}

#[derive(Debug)]
pub struct Error(String);

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl From<figment::Error> for Error {
    fn from(e: figment::Error) -> Self {
        Error(e.to_string())
    }
}

/// Every source after merging, before defaults and validation.
#[derive(Deserialize)]
#[serde(rename_all = "kebab-case")]
struct Merged {
    host: Option<String>,
    token: Option<String>,
    projects_per_page: Option<i64>,
    log_level: Option<String>,
}

pub fn load(flags: &Flags) -> Result<Config, Error> {
    let config_file = flags
        .config
        .clone()
        .or_else(|| env::var_os("LAZYLAB_CONFIG").map(PathBuf::from))
        .or_else(|| env::var_os("GITLAB_TUI_CONFIG").map(PathBuf::from))
        .filter(|path| !path.as_os_str().is_empty());
    let mut sources = Figment::new();
    if let Some(path) = &config_file {
        sources = with_file(sources, path)?;
    }
    let merged: Merged = sources
        .merge(
            Env::prefixed(ENV_PREFIX)
                .only(&ENV_KEYS)
                .map(|key| key.as_str().replace('_', "-").into()),
        )
        .merge(Serialized::defaults(flags))
        .extract()?;

    let present = |value: Option<String>| value.filter(|v| !v.is_empty());
    let host = parse_host(&present(merged.host).unwrap_or_else(|| DEFAULT_HOST.into()))?;
    let projects_per_page = match merged.projects_per_page {
        None | Some(..=0) => DEFAULT_PROJECTS_PER_PAGE,
        Some(n @ 1..=MAX_PROJECTS_PER_PAGE) => n as u8,
        Some(n) => {
            return Err(Error(format!(
                "projects-per-page must be between 1 and {MAX_PROJECTS_PER_PAGE}, got {n}"
            )));
        }
    };
    let log_level = match present(merged.log_level)
        .map(|l| l.to_lowercase())
        .as_deref()
    {
        None | Some("error") => Level::ERROR,
        Some("warn") => Level::WARN,
        Some("info") => Level::INFO,
        Some("debug") => Level::DEBUG,
        Some(other) => {
            return Err(Error(format!(
                "invalid log level {other:?}, must be one of: debug, info, warn, error"
            )));
        }
    };
    let token = present(merged.token)
        .map(Token)
        .ok_or_else(|| Error("gitlab token is required: set --token or $GITLAB_TOKEN".into()))?;

    Ok(Config {
        host,
        token,
        projects_per_page,
        log_level,
        config_file,
    })
}

/// Reads the file once up front, so an unreadable or malformed file is reported with its path
/// rather than as a bare I/O error.
fn with_file(sources: Figment, path: &Path) -> Result<Figment, Error> {
    let unreadable = |e: figment::Error| Error(format!("load config {}: {e}", path.display()));
    let extension = path
        .extension()
        .and_then(|e| e.to_str())
        .map(str::to_lowercase);
    match extension.as_deref() {
        Some("toml") => {
            let file = Toml::file_exact(path);
            figment::Provider::data(&file).map_err(unreadable)?;
            Ok(sources.merge(file))
        }
        Some("yaml" | "yml") => {
            let file = Yaml::file_exact(path);
            figment::Provider::data(&file).map_err(unreadable)?;
            Ok(sources.merge(file))
        }
        Some("json") => {
            let file = Json::file_exact(path);
            figment::Provider::data(&file).map_err(unreadable)?;
            Ok(sources.merge(file))
        }
        _ => Err(Error(format!(
            "load config {}: unsupported file type, use .toml, .yaml, .yml or .json",
            path.display()
        ))),
    }
}

fn parse_host(host: &str) -> Result<Url, Error> {
    let url = Url::parse(host).map_err(|e| match e {
        url::ParseError::RelativeUrlWithoutBase => Error(format!(
            "host URL must include a scheme (e.g., https://{host})"
        )),
        url::ParseError::EmptyHost => Error("host URL must include a hostname".into()),
        e => Error(format!("invalid host URL {host:?}: {e}")),
    })?;
    if !matches!(url.scheme(), "http" | "https") {
        return Err(Error(format!(
            "host URL must use http or https scheme, got {:?}",
            url.scheme()
        )));
    }
    if url.host_str().is_none_or(str::is_empty) {
        return Err(Error("host URL must include a hostname".into()));
    }
    Ok(url)
}

#[cfg(test)]
mod tests {
    use figment::Jail;

    use super::*;

    fn flags(token: &str) -> Flags {
        Flags {
            token: Some(token.into()),
            ..Flags::default()
        }
    }

    fn error(flags: &Flags) -> String {
        load(flags)
            .expect_err("the settings are rejected")
            .to_string()
    }

    #[test]
    fn a_token_alone_takes_the_default_for_every_other_setting() {
        Jail::expect_with(|jail| {
            // Given: no environment and no config file.
            jail.clear_env();
            // When: only a token is passed.
            let config = load(&flags("t0ken")).unwrap();
            // Then: everything else is the documented default.
            assert_eq!(
                config,
                Config {
                    host: Url::parse("https://gitlab.com").unwrap(),
                    token: Token("t0ken".into()),
                    projects_per_page: 30,
                    log_level: Level::ERROR,
                    config_file: None,
                }
            );
            Ok(())
        });
    }

    #[test]
    fn a_flag_beats_the_environment_which_beats_the_file() {
        Jail::expect_with(|jail| {
            // Given: a host and a page size in the file, the environment and the flags.
            jail.clear_env();
            jail.create_file(
                "lazylab.toml",
                "host = \"https://file.example\"\nprojects-per-page = 10\ntoken = \"from-file\"",
            )?;
            jail.set_env("LAZYLAB_CONFIG", "lazylab.toml");
            let file_only = load(&Flags::default()).unwrap();

            jail.set_env("GITLAB_HOST", "https://env.example");
            jail.set_env("GITLAB_PROJECTS_PER_PAGE", "20");
            let env_over_file = load(&Flags::default()).unwrap();

            let flags_over_env = load(&Flags {
                host: Some("https://flag.example".into()),
                projects_per_page: Some(40),
                ..Flags::default()
            })
            .unwrap();

            // Then: each layer wins over the ones below it, and the file still supplies the token.
            assert_eq!(file_only.host.as_str(), "https://file.example/");
            assert_eq!(file_only.projects_per_page, 10);
            assert_eq!(env_over_file.host.as_str(), "https://env.example/");
            assert_eq!(env_over_file.projects_per_page, 20);
            assert_eq!(flags_over_env.host.as_str(), "https://flag.example/");
            assert_eq!(flags_over_env.projects_per_page, 40);
            assert_eq!(flags_over_env.token, Token("from-file".into()));
            Ok(())
        });
    }

    #[test]
    fn the_config_flag_beats_lazylab_config_which_beats_gitlab_tui_config() {
        Jail::expect_with(|jail| {
            // Given: three files, each named by one of the three sources.
            jail.clear_env();
            for name in ["flag", "lazylab", "legacy"] {
                jail.create_file(
                    format!("{name}.toml"),
                    &format!("host = \"https://{name}.example\""),
                )?;
            }
            jail.set_env("GITLAB_TUI_CONFIG", "legacy.toml");
            let legacy = load(&flags("t")).unwrap();
            jail.set_env("LAZYLAB_CONFIG", "lazylab.toml");
            let lazylab = load(&flags("t")).unwrap();
            let flag = load(&Flags {
                config: Some("flag.toml".into()),
                ..flags("t")
            })
            .unwrap();

            // Then: the most specific source names the file that is read.
            assert_eq!(legacy.host.as_str(), "https://legacy.example/");
            assert_eq!(lazylab.host.as_str(), "https://lazylab.example/");
            assert_eq!(flag.host.as_str(), "https://flag.example/");
            assert_eq!(flag.config_file, Some(PathBuf::from("flag.toml")));
            Ok(())
        });
    }

    #[test]
    fn yaml_and_json_files_are_read_by_their_extension() {
        Jail::expect_with(|jail| {
            // Given: the same settings as YAML and as JSON.
            jail.clear_env();
            jail.create_file("a.yaml", "projects-per-page: 50\nlog-level: info\n")?;
            jail.create_file(
                "b.json",
                r#"{"projects-per-page": 60, "log-level": "warn"}"#,
            )?;
            let read = |file: &str| {
                load(&Flags {
                    config: Some(file.into()),
                    ..flags("t")
                })
                .unwrap()
            };
            // Then: both are read.
            assert_eq!(
                (read("a.yaml").projects_per_page, read("a.yaml").log_level),
                (50, Level::INFO)
            );
            assert_eq!(
                (read("b.json").projects_per_page, read("b.json").log_level),
                (60, Level::WARN)
            );
            Ok(())
        });
    }

    #[test]
    fn a_config_file_that_cannot_be_read_is_an_error_that_names_it() {
        Jail::expect_with(|jail| {
            jail.clear_env();
            jail.create_file("settings.ini", "host=x")?;
            let named = |file: &str| Flags {
                config: Some(file.into()),
                ..flags("t")
            };
            assert!(error(&named("missing.toml")).contains("missing.toml"));
            assert!(error(&named("settings.ini")).contains("settings.ini"));
            Ok(())
        });
    }

    #[test]
    fn invalid_settings_are_rejected_with_the_reason() {
        Jail::expect_with(|jail| {
            jail.clear_env();
            let with = |change: fn(&mut Flags)| {
                let mut f = flags("t");
                change(&mut f);
                error(&f)
            };
            assert!(error(&Flags::default()).contains("gitlab token is required"));
            assert!(with(|f| f.host = Some("gitlab.com".into())).contains("scheme"));
            assert!(with(|f| f.host = Some("ftp://gitlab.com".into())).contains("http or https"));
            assert!(with(|f| f.host = Some("https://".into())).contains("hostname"));
            assert!(with(|f| f.projects_per_page = Some(101)).contains("between 1 and 100"));
            assert!(with(|f| f.log_level = Some("trace".into())).contains("invalid log level"));
            Ok(())
        });
    }

    #[test]
    fn a_token_from_the_environment_is_read_as_text_whatever_it_looks_like() {
        for token in ["12345678901234567890", "true", "[abc]"] {
            Jail::expect_with(|jail| {
                // Given: a token that would parse as a number, a bool or a list.
                jail.clear_env();
                jail.set_env("GITLAB_TOKEN", token);
                // When: the settings load.
                let config = load(&Flags::default()).unwrap();
                // Then: the token is the text as written.
                assert_eq!(config.token, Token(token.into()));
                Ok(())
            });
        }
    }

    #[test]
    fn a_bad_number_in_the_environment_is_an_error_not_the_default() {
        Jail::expect_with(|jail| {
            jail.clear_env();
            jail.set_env("GITLAB_PROJECTS_PER_PAGE", "abc");
            assert!(error(&flags("t")).to_lowercase().contains("projects"));
            Ok(())
        });
    }

    #[test]
    fn a_zero_page_size_takes_the_default_and_the_level_ignores_case() {
        Jail::expect_with(|jail| {
            jail.clear_env();
            let config = load(&Flags {
                projects_per_page: Some(0),
                log_level: Some("DEBUG".into()),
                ..flags("t")
            })
            .unwrap();
            assert_eq!(
                (config.projects_per_page, config.log_level),
                (30, Level::DEBUG)
            );
            Ok(())
        });
    }

    #[test]
    fn a_debug_print_of_the_config_does_not_show_the_token() {
        Jail::expect_with(|jail| {
            jail.clear_env();
            let config = load(&flags("abcdefghijklmnopqrst")).unwrap();
            assert!(!format!("{config:?}").contains("abcdefghijklmnopqrst"));
            Ok(())
        });
    }
}
