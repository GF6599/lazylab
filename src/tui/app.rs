//! The state of the screen, and the one place it changes: `update`, which takes a message and
//! answers with the work the event loop has to start.

use std::collections::HashSet;

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
    page_size: u8,
    load: u64,
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
            page_size,
            load: 0,
            pipelines: Vec::new(),
            jobs: Vec::new(),
            merge_requests: Vec::new(),
            cursors: [0; Pane::ALL.len()],
        }
    }

    pub fn start(&mut self) -> Vec<Cmd> {
        vec![Cmd::LoadCache]
    }

    pub fn cursor(&self, pane: Pane) -> usize {
        self.cursors[pane.index()]
    }

    pub fn project(&self) -> Option<&Project> {
        self.projects.get(self.cursor(Pane::Projects))
    }

    pub fn pipeline(&self) -> Option<&Pipeline> {
        self.pipelines.get(self.cursor(Pane::Pipelines))
    }

    pub fn len(&self, pane: Pane) -> usize {
        match pane {
            Pane::Projects => self.projects.len(),
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
        match key.code {
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

    fn cache_loaded(&mut self, cached: Option<CachedProjects>) -> Vec<Cmd> {
        let Some(cached) = cached else {
            return self.reload();
        };
        self.projects = cached.projects;
        self.total_projects = cached.total;
        self.cursors[Pane::Projects.index()] = 0;
        let mut commands = self.project_changed();
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
        let mut commands = if page.page == 1 {
            self.replace_projects(page.items)
        } else {
            let mut held: HashSet<u64> = self.projects.iter().map(|p| p.id).collect();
            let fresh = page.items.into_iter().filter(|p| held.insert(p.id));
            self.projects.extend(fresh);
            Vec::new()
        };
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

    fn replace_projects(&mut self, projects: Vec<Project>) -> Vec<Cmd> {
        let selected = self.project().map(|p| p.id);
        self.projects = projects;
        let kept = selected.and_then(|id| self.projects.iter().position(|p| p.id == id));
        self.cursors[Pane::Projects.index()] = kept.unwrap_or(0);
        if kept.is_some() {
            Vec::new()
        } else {
            self.project_changed()
        }
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
