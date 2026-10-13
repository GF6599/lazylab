//! Resolves the settings from flags, `GITLAB_*` environment variables, a config file and
//! defaults, in that order of precedence.

use std::collections::BTreeMap;
use std::env;
use std::fmt;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::LazyLock;

use clap::Args;
use figment::error::Kind;
use figment::providers::{Env, Format, Json, Serialized, Toml, Yaml};
use figment::{Figment, Provider};
use regex::Regex;
use serde::{Deserialize, Serialize};
use tracing::Level;
use url::Url;

const DEFAULT_HOST: &str = "https://gitlab.com";
const DEFAULT_PROJECTS_PER_PAGE: u8 = 30;
/// The GitLab API caps a page here, and above it returns fewer rows with no error.
const MAX_PROJECTS_PER_PAGE: i64 = 100;
const ENV_PREFIX: &str = "GITLAB_";
/// figment's `Env` parses each value, so a token of digits would arrive as a number. These keys
/// are taken as the text that was set.
const ENV_TEXT_KEYS: [&str; 3] = ["host", "token", "log_level"];
const ENV_NUMBER_KEYS: [&str; 1] = ["projects_per_page"];

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

/// figment's messages quote the rejected value, and that value can be the token, so an error
/// names the setting and its source instead.
impl From<figment::Error> for Error {
    fn from(errors: figment::Error) -> Self {
        let described: Vec<String> = errors
            .into_iter()
            .map(|e| {
                let source = e
                    .metadata
                    .map(|m| match m.source {
                        Some(source) => format!(" in {source} {}", m.name),
                        None => format!(" in {}", m.name),
                    })
                    .unwrap_or_default();
                let expected = match &e.kind {
                    Kind::InvalidType(_, expected) | Kind::InvalidValue(_, expected) => {
                        format!(", expected {expected}")
                    }
                    _ => String::new(),
                };
                format!("invalid {}{source}{expected}", e.path.join("."))
            })
            .collect();
        Error(described.join("; "))
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
    let named = |path: Option<PathBuf>| path.filter(|p| !p.as_os_str().is_empty());
    let config_file = named(flags.config.clone())
        .or_else(|| named(env::var_os("LAZYLAB_CONFIG").map(PathBuf::from)))
        .or_else(|| named(env::var_os("GITLAB_TUI_CONFIG").map(PathBuf::from)));
    let mut sources = Figment::new();
    if let Some(path) = &config_file {
        sources = with_file(sources, path)?;
    }
    let merged: Merged = sources
        .merge(
            Env::prefixed(ENV_PREFIX)
                .only(&ENV_NUMBER_KEYS)
                .map(|key| key.as_str().replace('_', "-").into()),
        )
        .merge(Serialized::defaults(env_text()))
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

fn env_text() -> BTreeMap<String, String> {
    Env::prefixed(ENV_PREFIX)
        .only(&ENV_TEXT_KEYS)
        .iter()
        .map(|(key, value)| (key.as_str().replace('_', "-"), value))
        .collect()
}

/// Reads the file up front, so an unreadable or malformed file is reported with its path rather
/// than as a bare I/O error.
fn with_file(sources: Figment, path: &Path) -> Result<Figment, Error> {
    fs::read(path).map_err(|e| Error(format!("load config {}: {e}", path.display())))?;
    let extension = path
        .extension()
        .and_then(|e| e.to_str())
        .map(str::to_lowercase);
    match extension.as_deref() {
        Some("toml") => merge_file::<Toml>(sources, path),
        Some("yaml" | "yml") => merge_file::<Yaml>(sources, path),
        Some("json") => merge_file::<Json>(sources, path),
        _ => Err(Error(format!(
            "load config {}: unsupported file type, use .toml, .yaml, .yml or .json",
            path.display()
        ))),
    }
}

fn merge_file<F: Format>(sources: Figment, path: &Path) -> Result<Figment, Error> {
    let file = F::file_exact(path);
    file.data().map_err(|e| {
        Error(format!(
            "load config {}: not valid {}{}",
            path.display(),
            F::NAME,
            position(&e.to_string())
        ))
    })?;
    Ok(sources.merge(file))
}

/// A parser message quotes the source line, which can hold the token, so only its position is
/// kept.
fn position(message: &str) -> String {
    static POSITION: LazyLock<Regex> =
        LazyLock::new(|| Regex::new(r"line (\d+),? column (\d+)").expect("a valid pattern"));
    POSITION
        .captures(message)
        .map(|c| format!(" at line {}, column {}", &c[1], &c[2]))
        .unwrap_or_default()
}

fn parse_host(host: &str) -> Result<Url, Error> {
    let url = Url::parse(host).map_err(|e| match e {
        url::ParseError::RelativeUrlWithoutBase => {
            Error("host URL must include a scheme, for example https://".into())
        }
        url::ParseError::EmptyHost => Error("host URL must include a hostname".into()),
        e => Error(format!("invalid host URL: {e}")),
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
    fn an_empty_config_source_falls_through_to_the_next() {
        Jail::expect_with(|jail| {
            // Given: an empty --config and an empty LAZYLAB_CONFIG, and a legacy variable naming a file.
            jail.clear_env();
            jail.create_file("legacy.toml", "host = \"https://legacy.example\"")?;
            jail.set_env("LAZYLAB_CONFIG", "");
            jail.set_env("GITLAB_TUI_CONFIG", "legacy.toml");
            // When: the settings load.
            let config = load(&Flags {
                config: Some(PathBuf::new()),
                ..flags("t")
            })
            .unwrap();
            // Then: the legacy file is read.
            assert_eq!(config.host.as_str(), "https://legacy.example/");
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
    fn a_rejected_setting_is_named_without_quoting_its_value() {
        Jail::expect_with(|jail| {
            // Given: a credential in each place the loader can reject it.
            jail.clear_env();
            jail.create_file("number.toml", "token = 123456789012")?;
            jail.create_file("syntax.toml", "token = \"abcdefghijkl")?;
            jail.create_file("number.yaml", "token: 123456789012\n")?;
            jail.create_file("number.json", r#"{"token": 123456789012}"#)?;
            let from_file = |file: &str| {
                error(&Flags {
                    config: Some(file.into()),
                    ..Flags::default()
                })
            };
            let bad_host = |host: &str| {
                error(&Flags {
                    host: Some(host.into()),
                    ..flags("t")
                })
            };
            // When: each is loaded.
            let rejected = [
                (from_file("number.toml"), "123456789012", "number.toml"),
                (from_file("syntax.toml"), "abcdefghijkl", "syntax.toml"),
                (from_file("number.yaml"), "123456789012", "number.yaml"),
                (from_file("number.json"), "123456789012", "number.json"),
                (
                    bad_host("https://oauth2:s3cretvalue@gitlab .com"),
                    "s3cretvalue",
                    "host",
                ),
                (bad_host("s3cretvalue@gitlab.com"), "s3cretvalue", "scheme"),
            ];
            // Then: each error says where the problem is, and none quotes the credential.
            for (message, secret, names) in rejected {
                assert!(!message.contains(secret), "{secret} quoted in: {message}");
                assert!(message.contains(names), "{names} missing from: {message}");
            }
            Ok(())
        });
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
