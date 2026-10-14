//! The state of the screen, and the one place it changes: `update`, which takes a message and
//! answers with the work the event loop has to start.

use std::collections::{HashMap, HashSet};

use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};

use crate::gitlab::{Job, MergeRequest, Page, Pipeline, Project};
use crate::store::CachedProjects;

const PAGE: isize = 10;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Pane {
    Projects,
    Pipelines,
    Jobs,
    MergeRequests,
}

impl Pane {
    pub const ALL: [Pane; 4] = [
        Pane::Projects,
        Pane::Pipelines,
        Pane::Jobs,
        Pane::MergeRequests,
    ];

    pub fn label(self) -> &'static str {
        match self {
            Pane::Projects => "projects",
            Pane::Pipelines => "pipelines",
            Pane::Jobs => "jobs",
            Pane::MergeRequests => "merge requests",
        }
    }

    /// The label the pane strip falls back to when the full labels do not fit.
    pub fn short(self) -> &'static str {
        match self {
            Pane::Pipelines => "pipes",
            Pane::MergeRequests => "mrs",
            other => other.label(),
        }
    }

    pub fn key(self) -> char {
        char::from(b'1' + self.index() as u8)
    }

    pub fn index(self) -> usize {
        Pane::ALL.iter().position(|&p| p == self).unwrap_or(0)
    }

    fn step(self, delta: usize) -> Pane {
        Pane::ALL[(self.index() + delta) % Pane::ALL.len()]
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Focus {
    Sidebar,
    Detail,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Overlay {
    None,
    Help,
}

#[derive(Debug)]
pub enum Msg {
    Key(KeyEvent),
    Tick,
    CacheLoaded(Option<CachedProjects>),
    FavoritesLoaded(Vec<u64>),
    ProjectPageLoaded {
        load: u64,
        page: Page<Project>,
    },
    PipelinesLoaded {
        project: u64,
        pipelines: Vec<Pipeline>,
    },
    JobsLoaded {
        pipeline: u64,
        jobs: Vec<Job>,
    },
    MergeRequestsLoaded {
        project: u64,
        merge_requests: Vec<MergeRequest>,
    },
    /// The reason is already fit to show, with any credential redacted.
    Failed {
        cmd: Cmd,
        error: String,
    },
}

#[derive(Clone, Debug, PartialEq)]
pub enum Cmd {
    LoadCache,
    LoadFavorites,
    SaveFavorites(Vec<u64>),
    /// `load` marks each page of one load, so a page from a load that a reload replaced is
    /// dropped instead of mixed into the new list.
    LoadProjectPage {
        load: u64,
        page: u32,
        per_page: u8,
    },
    SaveProjects {
        projects: Vec<Project>,
        total: u64,
    },
    LoadPipelines {
        project: u64,
    },
    LoadJobs {
        project: u64,
        pipeline: u64,
    },
    LoadMergeRequests {
        project: u64,
    },
}

#[derive(Debug, Default)]
pub struct Search {
    pub query: String,
    pub typing: bool,
}

#[derive(Debug)]
pub struct App {
    pub pane: Pane,
    pub focus: Focus,
    /// The full-screen mode. It lasts for the session, because nothing saves it.
    pub zoom: bool,
    pub overlay: Overlay,
    pub tick: u64,
    pub quit: bool,
    pub projects: Vec<Project>,
    /// The size of the whole collection, which is more than `projects` holds while pages load.
    pub total_projects: u64,
    pub loading: bool,
    pub error: Option<String>,
    /// In the order the user set, which the favorites view keeps.
    pub favorites: Vec<u64>,
    pub favorites_only: bool,
    pub search: Search,
    page_size: u8,
    load: u64,
    /// The indexes into `projects` of the rows the projects pane shows, in their order.
    shown: Vec<usize>,
    pub pipelines: Vec<Pipeline>,
    pub jobs: Vec<Job>,
    pub merge_requests: Vec<MergeRequest>,
    cursors: [usize; Pane::ALL.len()],
}

impl App {
    pub fn new(page_size: u8) -> App {
        App {
            pane: Pane::Projects,
            focus: Focus::Sidebar,
            zoom: false,
            overlay: Overlay::None,
            tick: 0,
            quit: false,
            projects: Vec::new(),
            total_projects: 0,
            loading: false,
            error: None,
            favorites: Vec::new(),
            favorites_only: false,
            search: Search::default(),
            page_size,
            load: 0,
            shown: Vec::new(),
            pipelines: Vec::new(),
            jobs: Vec::new(),
            merge_requests: Vec::new(),
            cursors: [0; Pane::ALL.len()],
        }
    }

    pub fn start(&mut self) -> Vec<Cmd> {
        vec![Cmd::LoadCache, Cmd::LoadFavorites]
    }

    pub fn cursor(&self, pane: Pane) -> usize {
        self.cursors[pane.index()]
    }

    pub fn project(&self) -> Option<&Project> {
        self.shown_project(self.cursor(Pane::Projects))
    }

    /// The project on row `row` of the projects pane.
    pub fn shown_project(&self, row: usize) -> Option<&Project> {
        self.shown.get(row).map(|&i| &self.projects[i])
    }

    pub fn shown_projects(&self) -> impl Iterator<Item = &Project> {
        self.shown.iter().map(|&i| &self.projects[i])
    }

    pub fn pipeline(&self) -> Option<&Pipeline> {
        self.pipelines.get(self.cursor(Pane::Pipelines))
    }

    pub fn len(&self, pane: Pane) -> usize {
        match pane {
            Pane::Projects => self.shown.len(),
            Pane::Pipelines => self.pipelines.len(),
            Pane::Jobs => self.jobs.len(),
            Pane::MergeRequests => self.merge_requests.len(),
        }
    }

    pub fn update(&mut self, msg: Msg) -> Vec<Cmd> {
        match msg {
            Msg::Key(key) => self.on_key(key),
            Msg::Tick => {
                self.tick += 1;
                Vec::new()
            }
            Msg::CacheLoaded(cached) => self.cache_loaded(cached),
            Msg::FavoritesLoaded(ids) => {
                let before = self.selected();
                self.favorites = ids;
                self.reshow(before)
            }
            Msg::ProjectPageLoaded { load, page } if load == self.load => self.page_loaded(page),
            Msg::ProjectPageLoaded { .. } => Vec::new(),
            Msg::Failed { cmd, error } => {
                if let Cmd::LoadProjectPage { load, .. } = cmd
                    && load == self.load
                {
                    self.loading = false;
                }
                self.error = Some(error);
                Vec::new()
            }
            Msg::PipelinesLoaded { project, pipelines } => {
                if self.project().map(|p| p.id) != Some(project) {
                    return Vec::new();
                }
                self.pipelines = pipelines;
                self.cursors[Pane::Pipelines.index()] = 0;
                self.pipeline_changed()
            }
            Msg::JobsLoaded { pipeline, jobs } => {
                if self.pipeline().map(|p| p.id) == Some(pipeline) {
                    self.jobs = jobs;
                    self.cursors[Pane::Jobs.index()] = 0;
                }
                Vec::new()
            }
            Msg::MergeRequestsLoaded {
                project,
                merge_requests,
            } => {
                if self.project().map(|p| p.id) == Some(project) {
                    self.merge_requests = merge_requests;
                    self.cursors[Pane::MergeRequests.index()] = 0;
                }
                Vec::new()
            }
        }
    }

    fn on_key(&mut self, key: KeyEvent) -> Vec<Cmd> {
        if key.kind != KeyEventKind::Press {
            return Vec::new();
        }
        self.error = None;
        if self.search.typing {
            return self.on_search_key(key);
        }
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            return match key.code {
                KeyCode::Char('c') => {
                    self.quit = true;
                    Vec::new()
                }
                KeyCode::Char('r') if self.overlay == Overlay::None => self.reload(),
                KeyCode::Char('d') if self.overlay == Overlay::None => self.move_by(PAGE),
                KeyCode::Char('u') if self.overlay == Overlay::None => self.move_by(-PAGE),
                _ => Vec::new(),
            };
        }
        if self.overlay == Overlay::Help {
            if matches!(key.code, KeyCode::Esc | KeyCode::Char('?' | 'q')) {
                self.overlay = Overlay::None;
            }
            return Vec::new();
        }
        let before = self.selected();
        let on_projects = self.pane == Pane::Projects && self.focus == Focus::Sidebar;
        match key.code {
            KeyCode::Char('/') if on_projects => {
                self.search = Search {
                    query: String::new(),
                    typing: true,
                };
                return self.reshow(before);
            }
            KeyCode::Char('f') if on_projects => return self.toggle_favorite(),
            KeyCode::Char('t') if on_projects => {
                self.favorites_only = !self.favorites_only;
                return self.reshow(before);
            }
            KeyCode::Char('{') if on_projects => return self.move_favorite(-1),
            KeyCode::Char('}') if on_projects => return self.move_favorite(1),
            KeyCode::Char('q') => self.quit = true,
            KeyCode::Char('?') => self.overlay = Overlay::Help,
            KeyCode::Char('z') => self.zoom = !self.zoom,
            KeyCode::Char(c @ '1'..='9') => {
                if let Some(&pane) = Pane::ALL.get(c as usize - '1' as usize) {
                    self.pane = pane;
                }
            }
            KeyCode::Tab => self.pane = self.pane.step(1),
            KeyCode::BackTab => self.pane = self.pane.step(Pane::ALL.len() - 1),
            KeyCode::Char('l') | KeyCode::Right | KeyCode::Enter => self.focus = Focus::Detail,
            KeyCode::Char('h') | KeyCode::Left | KeyCode::Esc => self.focus = Focus::Sidebar,
            KeyCode::Char('j') | KeyCode::Down => return self.move_by(1),
            KeyCode::Char('k') | KeyCode::Up => return self.move_by(-1),
            KeyCode::Char('g') => return self.move_by(isize::MIN),
            KeyCode::Char('G') => return self.move_by(isize::MAX),
            _ => {}
        }
        Vec::new()
    }

    fn on_search_key(&mut self, key: KeyEvent) -> Vec<Cmd> {
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            self.quit |= key.code == KeyCode::Char('c');
            return Vec::new();
        }
        let before = self.selected();
        match key.code {
            KeyCode::Enter => self.search.typing = false,
            KeyCode::Esc => self.search = Search::default(),
            KeyCode::Backspace => {
                self.search.query.pop();
            }
            KeyCode::Char(c) => self.search.query.push(c),
            _ => return Vec::new(),
        }
        self.reshow(before)
    }

    fn toggle_favorite(&mut self) -> Vec<Cmd> {
        let Some(id) = self.selected() else {
            return Vec::new();
        };
        match self.favorites.iter().position(|&f| f == id) {
            Some(at) => {
                self.favorites.remove(at);
            }
            None => self.favorites.push(id),
        }
        let mut commands = self.reshow(Some(id));
        commands.push(Cmd::SaveFavorites(self.favorites.clone()));
        commands
    }

    /// Swaps the favorite with its neighbour on screen, which a search can make another
    /// favorite than its neighbour in the saved order.
    fn move_favorite(&mut self, step: isize) -> Vec<Cmd> {
        if !self.favorites_only {
            return Vec::new();
        }
        let cursor = self.cursor(Pane::Projects);
        let neighbour = cursor.checked_add_signed(step);
        let (Some(id), Some(other)) = (
            self.selected(),
            neighbour
                .and_then(|row| self.shown_project(row))
                .map(|p| p.id),
        ) else {
            return Vec::new();
        };
        let at = |id: u64| self.favorites.iter().position(|&f| f == id);
        let (Some(from), Some(to)) = (at(id), at(other)) else {
            return Vec::new();
        };
        self.favorites.swap(from, to);
        let mut commands = self.reshow(Some(id));
        commands.push(Cmd::SaveFavorites(self.favorites.clone()));
        commands
    }

    fn selected(&self) -> Option<u64> {
        self.project().map(|p| p.id)
    }

    /// The cursor stays on the project it was on while that project still shows, and the lists
    /// load again only when another project is selected.
    fn reshow(&mut self, before: Option<u64>) -> Vec<Cmd> {
        let row = self.cursor(Pane::Projects);
        self.shown = self.matching();
        let kept = before.and_then(|id| self.shown_projects().position(|p| p.id == id));
        self.cursors[Pane::Projects.index()] =
            kept.unwrap_or_else(|| row.min(self.shown.len().saturating_sub(1)));
        if self.selected() == before {
            Vec::new()
        } else {
            self.project_changed()
        }
    }

    fn matching(&self) -> Vec<usize> {
        let query = &self.search.query;
        let found = |&i: &usize| {
            let project = &self.projects[i];
            fuzzy(&project.path_with_namespace, query) || fuzzy(&project.name, query)
        };
        if self.favorites_only {
            let index: HashMap<u64, usize> = (self.projects.iter().enumerate())
                .map(|(i, p)| (p.id, i))
                .collect();
            (self.favorites.iter())
                .filter_map(|id| index.get(id).copied())
                .filter(found)
                .collect()
        } else {
            (0..self.projects.len()).filter(found).collect()
        }
    }

    fn cache_loaded(&mut self, cached: Option<CachedProjects>) -> Vec<Cmd> {
        let Some(cached) = cached else {
            return self.reload();
        };
        let before = self.selected();
        self.projects = cached.projects;
        self.total_projects = cached.total;
        let mut commands = self.reshow(before);
        let held = self.projects.len() as u64;
        if held < self.total_projects {
            // Integer division rounds down, so the resumed page starts at or before the first
            // project the cache lacks, and a project it already holds is skipped as a duplicate.
            let page = (held / u64::from(self.page_size.max(1))) as u32 + 1;
            commands.push(self.request(page));
        }
        commands
    }

    fn reload(&mut self) -> Vec<Cmd> {
        self.load += 1;
        vec![self.request(1)]
    }

    fn request(&mut self, page: u32) -> Cmd {
        self.loading = true;
        Cmd::LoadProjectPage {
            load: self.load,
            page,
            per_page: self.page_size,
        }
    }

    fn page_loaded(&mut self, page: Page<Project>) -> Vec<Cmd> {
        let before = self.selected();
        if page.page == 1 {
            self.projects = page.items;
        } else {
            let mut held: HashSet<u64> = self.projects.iter().map(|p| p.id).collect();
            let fresh = page.items.into_iter().filter(|p| held.insert(p.id));
            self.projects.extend(fresh);
        }
        let mut commands = self.reshow(before);
        let held = self.projects.len() as u64;
        self.total_projects = page.total_items.unwrap_or(held).max(held);
        commands.push(Cmd::SaveProjects {
            projects: self.projects.clone(),
            total: self.total_projects,
        });
        match page.next_page {
            Some(next) => commands.push(self.request(next)),
            None => self.loading = false,
        }
        commands
    }

    fn move_by(&mut self, delta: isize) -> Vec<Cmd> {
        let len = self.len(self.pane);
        if self.focus == Focus::Detail || len == 0 {
            return Vec::new();
        }
        let from = self.cursor(self.pane);
        let to = from.saturating_add_signed(delta).min(len - 1);
        if to == from {
            return Vec::new();
        }
        self.cursors[self.pane.index()] = to;
        match self.pane {
            Pane::Projects => self.project_changed(),
            Pane::Pipelines => self.pipeline_changed(),
            Pane::Jobs | Pane::MergeRequests => Vec::new(),
        }
    }

    fn project_changed(&mut self) -> Vec<Cmd> {
        self.pipelines.clear();
        self.jobs.clear();
        self.merge_requests.clear();
        match self.project() {
            Some(project) => vec![
                Cmd::LoadPipelines {
                    project: project.id,
                },
                Cmd::LoadMergeRequests {
                    project: project.id,
                },
            ],
            None => Vec::new(),
        }
    }

    fn pipeline_changed(&mut self) -> Vec<Cmd> {
        self.jobs.clear();
        match (self.project(), self.pipeline()) {
            (Some(project), Some(pipeline)) => vec![Cmd::LoadJobs {
                project: project.id,
                pipeline: pipeline.id,
            }],
            _ => Vec::new(),
        }
    }
}

/// Every character of `pattern` appears in `target` in the same order, in any case.
fn fuzzy(target: &str, pattern: &str) -> bool {
    let mut target = target.chars().flat_map(char::to_lowercase);
    (pattern.chars().flat_map(char::to_lowercase)).all(|c| target.any(|t| t == c))
}
