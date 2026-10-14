//! The files the Go release keeps in `<os-cache-dir>/lazylab`, read and written in its format, so
//! either release starts from what the other one left.

use std::ffi::OsString;
use std::fs::{self, DirBuilder};
use std::io::{self, Write};
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use time::{Duration, OffsetDateTime};
use url::Url;

use crate::gitlab::Project;

const PROJECTS_VERSION: u32 = 2;
const FAVORITES_VERSION: u32 = 2;
const PROJECTS_TTL: Duration = Duration::DAY;

/// `os.UserCacheDir()` joined with `lazylab`, the folder the Go release uses.
pub fn cache_dir() -> Option<PathBuf> {
    user_cache_dir(|name| std::env::var_os(name)).map(|base| base.join("lazylab"))
}

fn user_cache_dir(var: impl Fn(&str) -> Option<OsString>) -> Option<PathBuf> {
    let set = |name: &str| {
        var(name)
            .filter(|value| !value.is_empty())
            .map(PathBuf::from)
    };
    if cfg!(target_os = "macos") {
        return set("HOME").map(|home| home.join("Library/Caches"));
    }
    match set("XDG_CACHE_HOME") {
        Some(dir) => dir.is_absolute().then_some(dir),
        None => set("HOME").map(|home| home.join(".cache")),
    }
}

/// Only the user can enter the folder, as with the Go release, because it names their projects.
pub fn create_dir(dir: &Path) -> io::Result<()> {
    DirBuilder::new().recursive(true).mode(0o700).create(dir)
}

/// `sanitizeHost` in the Go release, so both name the files of one host alike.
fn host_key(host: &Url) -> String {
    let mut name = host.host_str().unwrap_or_default().to_string();
    if let Some(port) = host.port() {
        name.push_str(&format!(":{port}"));
    }
    format!(
        "{}_{}",
        host.scheme(),
        name.replace(':', "-").replace('.', "_")
    )
}

#[derive(Debug, Clone)]
pub struct Store {
    dir: PathBuf,
    host: String,
    key: String,
}

#[derive(Debug, PartialEq)]
pub struct CachedProjects {
    pub projects: Vec<Project>,
    /// The size of the whole collection, which is more than `projects` holds while pages are
    /// still to load.
    pub total: u64,
}

impl Store {
    pub fn new(dir: PathBuf, host: &Url) -> Store {
        Store {
            dir,
            host: host.as_str().trim_end_matches('/').to_string(),
            key: host_key(host),
        }
    }

    pub fn load_projects(&self, now: OffsetDateTime) -> io::Result<Option<CachedProjects>> {
        let Some(file) = self.read::<ProjectsFile>("projects")? else {
            return Ok(None);
        };
        let fresh = file.cached_at.is_none_or(|at| now - at <= PROJECTS_TTL);
        if file.version != PROJECTS_VERSION || !fresh || file.projects.is_empty() {
            return Ok(None);
        }
        Ok(Some(CachedProjects {
            projects: file.projects.into_iter().map(Project::from).collect(),
            total: file.total_projects,
        }))
    }

    /// An empty list is not saved, so a failed load never replaces a good cache.
    pub fn save_projects(
        &self,
        projects: &[Project],
        total: u64,
        now: OffsetDateTime,
    ) -> io::Result<()> {
        if projects.is_empty() {
            return Ok(());
        }
        let file = ProjectsFile {
            version: PROJECTS_VERSION,
            host: self.host.clone(),
            cached_at: Some(now),
            total_projects: total.max(projects.len() as u64),
            projects: projects.iter().map(Node::from).collect(),
        };
        self.write("projects", &serde_json::to_vec(&file)?)
    }

    pub fn load_favorites(&self) -> io::Result<Vec<u64>> {
        match self.read::<FavoritesFile>("favorites")? {
            Some(file) if file.version == FAVORITES_VERSION => Ok(file.project_ids),
            _ => Ok(Vec::new()),
        }
    }

    pub fn save_favorites(&self, ids: &[u64]) -> io::Result<()> {
        let file = FavoritesFile {
            version: FAVORITES_VERSION,
            host: self.host.clone(),
            project_ids: ids.to_vec(),
        };
        self.write("favorites", &serde_json::to_vec_pretty(&file)?)
    }

    fn name(&self, kind: &str) -> String {
        format!("{kind}_{}.json", self.key)
    }

    fn read<T: DeserializeOwned>(&self, kind: &str) -> io::Result<Option<T>> {
        match fs::read(self.dir.join(self.name(kind))) {
            Ok(data) => Ok(Some(serde_json::from_slice(&data)?)),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e),
        }
    }

    /// The rename replaces the file in one step, so a reader, in this process or another, sees
    /// one whole file and never part of two.
    fn write(&self, kind: &str, data: &[u8]) -> io::Result<()> {
        create_dir(&self.dir)?;
        let name = self.name(kind);
        let mut file = tempfile::Builder::new()
            .prefix(&format!("{name}."))
            .suffix(".tmp")
            .tempfile_in(&self.dir)?;
        file.write_all(data)?;
        file.persist(self.dir.join(name))?;
        Ok(())
    }
}

/// The Go decoder fills a missing field with its zero value, and so does `default`.
#[derive(Default, Serialize, Deserialize)]
#[serde(default)]
struct ProjectsFile {
    version: u32,
    host: String,
    #[serde(with = "go_time")]
    cached_at: Option<OffsetDateTime>,
    total_projects: u64,
    projects: Vec<Node>,
}

/// The Go project type has no JSON tags, so the Go release writes its field names.
#[derive(Default, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase", default)]
struct Node {
    #[serde(rename = "ID")]
    id: u64,
    name: String,
    path_with_namespace: String,
    description: String,
    #[serde(rename = "WebURL")]
    web_url: String,
    #[serde(rename = "SSHURLToRepo")]
    ssh_url_to_repo: String,
    #[serde(with = "go_time")]
    last_activity_at: Option<OffsetDateTime>,
    star_count: u64,
    visibility: String,
    default_branch: String,
}

#[derive(Default, Serialize, Deserialize)]
#[serde(default)]
struct FavoritesFile {
    version: u32,
    host: String,
    project_ids: Vec<u64>,
}

impl From<&Project> for Node {
    fn from(project: &Project) -> Node {
        Node {
            id: project.id,
            name: project.name.clone(),
            path_with_namespace: project.path_with_namespace.clone(),
            description: project.description.clone().unwrap_or_default(),
            web_url: project.web_url.clone(),
            ssh_url_to_repo: project.ssh_url_to_repo.clone(),
            last_activity_at: project.last_activity_at,
            star_count: project.star_count,
            visibility: project.visibility.clone(),
            default_branch: project.default_branch.clone().unwrap_or_default(),
        }
    }
}

impl From<Node> for Project {
    fn from(node: Node) -> Project {
        let set = |text: String| (!text.is_empty()).then_some(text);
        Project {
            id: node.id,
            name: node.name,
            path_with_namespace: node.path_with_namespace,
            description: set(node.description),
            web_url: node.web_url,
            ssh_url_to_repo: node.ssh_url_to_repo,
            visibility: node.visibility,
            default_branch: set(node.default_branch),
            star_count: node.star_count,
            last_activity_at: node.last_activity_at,
        }
    }
}

/// Go writes a time it never set as its zero time, not as `null`.
mod go_time {
    use serde::de::Error as _;
    use serde::ser::Error as _;
    use serde::{Deserialize, Deserializer, Serializer};
    use time::OffsetDateTime;
    use time::format_description::well_known::Rfc3339;

    const ZERO: &str = "0001-01-01T00:00:00Z";

    pub fn serialize<S: Serializer>(at: &Option<OffsetDateTime>, s: S) -> Result<S::Ok, S::Error> {
        match at {
            Some(at) => s.serialize_str(&at.format(&Rfc3339).map_err(S::Error::custom)?),
            None => s.serialize_str(ZERO),
        }
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(
        d: D,
    ) -> Result<Option<OffsetDateTime>, D::Error> {
        let text = String::deserialize(d)?;
        if text == ZERO {
            return Ok(None);
        }
        OffsetDateTime::parse(&text, &Rfc3339)
            .map(Some)
            .map_err(D::Error::custom)
    }
}

#[cfg(test)]
mod tests {
    use std::ffi::OsString;
    use std::path::PathBuf;

    use super::user_cache_dir;

    fn env(pairs: &'static [(&str, &str)]) -> impl Fn(&str) -> Option<OsString> {
        move |name| {
            pairs
                .iter()
                .find(|(key, _)| *key == name)
                .map(|(_, value)| OsString::from(value))
        }
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_keeps_caches_in_the_library_whatever_xdg_says() {
        let dir = user_cache_dir(env(&[("HOME", "/Users/a"), ("XDG_CACHE_HOME", "/x")]));
        assert_eq!(dir, Some(PathBuf::from("/Users/a/Library/Caches")));
        assert_eq!(user_cache_dir(env(&[])), None);
    }

    #[cfg(not(target_os = "macos"))]
    #[test]
    fn other_systems_take_xdg_when_it_is_absolute_and_home_when_it_is_unset() {
        let home = &[("HOME", "/home/a")];
        assert_eq!(
            user_cache_dir(env(home)),
            Some(PathBuf::from("/home/a/.cache"))
        );
        let xdg = &[("HOME", "/home/a"), ("XDG_CACHE_HOME", "/var/cache/a")];
        assert_eq!(
            user_cache_dir(env(xdg)),
            Some(PathBuf::from("/var/cache/a"))
        );
        let relative = &[("HOME", "/home/a"), ("XDG_CACHE_HOME", "cache")];
        assert_eq!(user_cache_dir(env(relative)), None);
        assert_eq!(user_cache_dir(env(&[])), None);
    }
}
