//! The state of the screen, and the one place it changes: `update`, which takes a message and
//! answers with the work the event loop has to start.

use std::cell::Cell;
use std::collections::hash_map::Entry;
use std::collections::{HashMap, HashSet};
use std::time::Duration;

use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};

use super::form::{Form, Outcome};
use super::jobs::{self, JobRow, RowKey};
use super::log::Log;
use crate::gitlab::{Bridge, Job, MergeRequest, Page, Pipeline, Project, Status, Variable};
use crate::store::CachedProjects;

const PAGE: isize = 10;
/// The loop sends `Msg::Tick` this often, and a running job's spinner moves one frame on each.
pub const TICK: Duration = Duration::from_millis(100);
/// The Go release waits this long after the cursor's last move before it loads the selection's
/// lists, so holding `j` through a list asks GitLab about the one project it stops on.
const SETTLE: Duration = Duration::from_millis(300);
const SETTLE_TICKS: u64 = (SETTLE.as_millis() / TICK.as_millis()) as u64;
/// The Go release asks for the pipelines and jobs in view this often.
const REFRESH: Duration = Duration::from_secs(5);
const REFRESH_TICKS: u64 = (REFRESH.as_millis() / TICK.as_millis()) as u64;

/// A selection whose lists load once the cursor has rested on it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Waiting {
    Project(u64),
    Pipeline { project: u64, pipeline: u64 },
    Job { project: u64, job: u64 },
}

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
    /// A question before the action in `App::confirm` goes out.
    Confirm,
    /// The run or play form in `App::form`, which takes every key until it closes.
    Form,
}

/// A change to a pipeline or a job. Each names the project it acts in, which for a job of a
/// child pipeline is the child's own project.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Action {
    /// `git_ref` is where a new pipeline starts when GitLab has no job to retry.
    RetryPipeline {
        project: u64,
        pipeline: u64,
        git_ref: String,
    },
    CancelPipeline {
        project: u64,
        pipeline: u64,
    },
    RetryJob {
        project: u64,
        job: u64,
        name: String,
    },
    CancelJob {
        project: u64,
        job: u64,
        name: String,
    },
    RunPipeline {
        project: u64,
        git_ref: String,
        variables: Vec<Variable>,
    },
    PlayJob {
        project: u64,
        job: u64,
        name: String,
        variables: Vec<Variable>,
    },
}

impl Action {
    pub fn question(&self) -> String {
        match self {
            Action::RetryPipeline { pipeline, .. } => format!("Retry pipeline #{pipeline}?"),
            Action::CancelPipeline { pipeline, .. } => format!("Cancel pipeline #{pipeline}?"),
            Action::RetryJob { job, name, .. } => format!("Retry job {name} (#{job})?"),
            Action::CancelJob { job, name, .. } => format!("Cancel job {name} (#{job})?"),
            Action::RunPipeline { git_ref, .. } => format!("Run a pipeline on {git_ref}?"),
            Action::PlayJob { job, name, .. } => format!("Play job {name} (#{job})?"),
        }
    }

    pub fn verb(&self) -> &'static str {
        match self {
            Action::RetryPipeline { .. } | Action::RetryJob { .. } => "retry",
            Action::CancelPipeline { .. } | Action::CancelJob { .. } => "cancel",
            Action::RunPipeline { .. } => "run",
            Action::PlayJob { .. } => "play",
        }
    }

    fn done(&self, started: Option<u64>) -> String {
        match self {
            Action::RetryPipeline {
                pipeline, git_ref, ..
            } => match started {
                Some(new) if new != *pipeline => format!("Started pipeline #{new} on {git_ref}"),
                _ => format!("Retried pipeline #{pipeline}"),
            },
            Action::CancelPipeline { pipeline, .. } => format!("Canceled pipeline #{pipeline}"),
            Action::RetryJob { name, .. } => format!("Retried job {name}"),
            Action::CancelJob { name, .. } => format!("Canceled job {name}"),
            Action::RunPipeline { git_ref, .. } => match started {
                Some(new) => format!("Started pipeline #{new} on {git_ref}"),
                None => format!("Started a pipeline on {git_ref}"),
            },
            Action::PlayJob { name, .. } => format!("Played job {name}"),
        }
    }
}

#[derive(Debug, PartialEq)]
pub enum Msg {
    Key(KeyEvent),
    Tick,
    CacheLoaded(Option<CachedProjects>),
    FavoritesLoaded(Vec<u64>),
    /// `None` for a project without a pipeline.
    StatusLoaded {
        project: u64,
        status: Option<Status>,
    },
    ProjectPageLoaded {
        load: u64,
        page: Page<Project>,
    },
    PipelinesLoaded {
        project: u64,
        page: Page<Pipeline>,
    },
    JobsLoaded {
        pipeline: u64,
        jobs: Vec<Job>,
        bridges: Vec<Bridge>,
    },
    ChildJobsLoaded {
        pipeline: u64,
        jobs: Vec<Job>,
    },
    LogLoaded {
        job: u64,
        log: String,
    },
    /// `started` is the pipeline a retry left running, which is a new one when GitLab had no job
    /// to retry.
    Acted {
        action: Action,
        started: Option<u64>,
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
    /// The status of the project's latest pipeline, for the glyph before its path.
    LoadStatus {
        project: u64,
    },
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
        page: u32,
    },
    /// A pipeline's jobs and bridges, which come together so they describe the same moment.
    LoadJobs {
        project: u64,
        pipeline: u64,
    },
    /// The jobs of a bridge's child pipeline, in the project it runs in.
    LoadChildJobs {
        project: u64,
        pipeline: u64,
    },
    LoadMergeRequests {
        project: u64,
    },
    Act(Action),
    /// The log of a job, in the project it runs in.
    LoadLog {
        project: u64,
        job: u64,
    },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProjectStatus {
    Loading,
    Pipeline(Status),
    NoPipeline,
    Unknown,
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
    /// What the last action did, shown until the next key.
    pub notice: Option<String>,
    /// The action the open question asks about.
    pub confirm: Option<Action>,
    /// Variable values live here while the form is open, and nowhere once it closes.
    pub form: Option<Form>,
    /// In the order the user set, which the favorites view keeps.
    pub favorites: Vec<u64>,
    pub favorites_only: bool,
    pub search: Search,
    pub statuses: HashMap<u64, ProjectStatus>,
    page_size: u8,
    load: u64,
    /// The selection that waits, and the tick its lists load on.
    waiting: Option<(Waiting, u64)>,
    /// The first selection after a start loads at once, because nothing moved the cursor there.
    lists_asked: bool,
    /// The indexes into `projects` of the rows the projects pane shows, in their order.
    shown: Vec<usize>,
    pub pipelines: Vec<Pipeline>,
    /// The page of pipelines in view, counted from 1.
    pub pipeline_page: u32,
    next_pipeline_page: Option<u32>,
    /// A refresh waits while a load of the same list is unanswered, so a slow GitLab never
    /// collects a queue of them.
    pipelines_asked: bool,
    jobs_asked: bool,
    refreshed_at: u64,
    pub jobs: Vec<Job>,
    pub bridges: Vec<Bridge>,
    /// Child pipeline jobs, by the child pipeline's ID.
    children: HashMap<u64, Vec<Job>>,
    children_asked: HashSet<u64>,
    /// The groups and bridges the reader opened, which stay open across a refresh.
    open: HashSet<RowKey>,
    job_rows: Vec<JobRow>,
    pub log: Option<Log>,
    log_asked: Option<u64>,
    /// Lines between the end of the log and the last line in view. Zero follows the end.
    log_scroll: usize,
    /// The rows the detail pane gave the log when it last drew, so a scroll stops at the top.
    pub log_height: Cell<usize>,
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
            notice: None,
            confirm: None,
            form: None,
            favorites: Vec::new(),
            favorites_only: false,
            search: Search::default(),
            statuses: HashMap::new(),
            page_size,
            load: 0,
            waiting: None,
            lists_asked: false,
            shown: Vec::new(),
            pipelines: Vec::new(),
            pipeline_page: 1,
            next_pipeline_page: None,
            pipelines_asked: false,
            jobs_asked: false,
            refreshed_at: 0,
            jobs: Vec::new(),
            bridges: Vec::new(),
            children: HashMap::new(),
            children_asked: HashSet::new(),
            open: HashSet::new(),
            job_rows: Vec::new(),
            log: None,
            log_asked: None,
            log_scroll: 0,
            log_height: Cell::new(0),
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

    /// The project's pipelines fill more than one page.
    pub fn pipelines_paged(&self) -> bool {
        self.pipeline_page > 1 || self.next_pipeline_page.is_some()
    }

    pub fn pipeline(&self) -> Option<&Pipeline> {
        self.pipelines.get(self.cursor(Pane::Pipelines))
    }

    pub fn len(&self, pane: Pane) -> usize {
        match pane {
            Pane::Projects => self.shown.len(),
            Pane::Pipelines => self.pipelines.len(),
            Pane::Jobs => self.job_rows.len(),
            Pane::MergeRequests => self.merge_requests.len(),
        }
    }

    pub fn update(&mut self, msg: Msg) -> Vec<Cmd> {
        let mut commands = self.handle(msg);
        self.note_asked(&commands);
        let log = self.ask_log();
        self.note_asked(&log);
        commands.extend(log);
        commands
    }

    /// Notes each load that went out, so the same one does not go out again before it answers.
    fn note_asked(&mut self, commands: &[Cmd]) {
        for command in commands {
            match command {
                Cmd::LoadPipelines { .. } => self.pipelines_asked = true,
                Cmd::LoadJobs { .. } => self.jobs_asked = true,
                Cmd::LoadChildJobs { pipeline, .. } => {
                    self.children_asked.insert(*pipeline);
                }
                Cmd::LoadLog { job, .. } => self.log_asked = Some(*job),
                _ => {}
            }
        }
    }

    pub fn log_following(&self) -> bool {
        self.log_scroll == 0
    }

    pub fn log_scroll(&self) -> usize {
        self.log_scroll
    }

    /// The job whose log the screen wants: the job on the current row of the jobs pane, while
    /// that pane is in view, and the project it runs in.
    fn wanted_log(&self) -> Option<(u64, u64)> {
        if self.pane != Pane::Jobs {
            return None;
        }
        match self.job_rows.get(self.cursor(Pane::Jobs)) {
            Some(JobRow::Job { job, project, .. }) => Some((*project, job.id)),
            _ => None,
        }
    }

    /// A log loads at once when nothing waits, so a pane switch or a new list shows it, and a
    /// moving cursor waits like any other selection.
    fn ask_log(&mut self) -> Vec<Cmd> {
        let Some((project, job)) = self.wanted_log() else {
            return Vec::new();
        };
        let shown = self.log.as_ref().is_some_and(|log| log.job == job);
        if shown || self.waiting.is_some() || self.log_asked == Some(job) {
            return Vec::new();
        }
        vec![Cmd::LoadLog { project, job }]
    }

    fn log_loaded(&mut self, job: u64, text: &str) {
        if self.log_asked == Some(job) {
            self.log_asked = None;
        }
        if self.wanted_log().map(|(_, wanted)| wanted) != Some(job) {
            return;
        }
        let log = Log::new(job, text);
        match &self.log {
            // New lines arrive below, so the lines in view stay in view while the reader is up.
            Some(old) if old.job == job && self.log_scroll > 0 => {
                self.log_scroll += log.lines.len().saturating_sub(old.lines.len());
            }
            Some(old) if old.job == job => {}
            _ => self.log_scroll = 0,
        }
        self.log = Some(log);
    }

    fn scroll_log(&mut self, delta: isize) {
        let Some(log) = &self.log else {
            return;
        };
        let top = log.lines.len().saturating_sub(self.log_height.get());
        self.log_scroll = if delta < 0 {
            self.log_scroll
                .saturating_add(delta.unsigned_abs())
                .min(top)
        } else {
            self.log_scroll.saturating_sub(delta.unsigned_abs())
        };
    }

    fn handle(&mut self, msg: Msg) -> Vec<Cmd> {
        match msg {
            Msg::Key(key) => self.on_key(key),
            Msg::Tick => {
                self.tick += 1;
                let mut commands = self.settled();
                commands.extend(self.refresh());
                commands
            }
            Msg::CacheLoaded(cached) => self.cache_loaded(cached),
            Msg::FavoritesLoaded(ids) => {
                let before = self.selected();
                self.favorites = ids;
                self.reshow(before)
            }
            Msg::ProjectPageLoaded { load, page } if load == self.load => self.page_loaded(page),
            Msg::ProjectPageLoaded { .. } => Vec::new(),
            Msg::StatusLoaded { project, status } => {
                let status = status.map_or(ProjectStatus::NoPipeline, ProjectStatus::Pipeline);
                self.statuses.insert(project, status);
                Vec::new()
            }
            Msg::Failed {
                cmd: cmd @ (Cmd::LoadPipelines { .. } | Cmd::LoadJobs { .. }),
                error,
            } => {
                match cmd {
                    Cmd::LoadPipelines { .. } => self.pipelines_asked = false,
                    _ => self.jobs_asked = false,
                }
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd: Cmd::LoadChildJobs { pipeline, .. },
                error,
            } => {
                self.children_asked.remove(&pipeline);
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd: Cmd::LoadLog { job, .. },
                error,
            } => {
                if self.log_asked == Some(job) {
                    self.log_asked = None;
                }
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd: Cmd::LoadStatus { project },
                ..
            } => {
                self.statuses.insert(project, ProjectStatus::Unknown);
                Vec::new()
            }
            Msg::Failed {
                cmd: Cmd::LoadProjectPage { load, .. },
                ..
            } if load != self.load => Vec::new(),
            Msg::Failed { cmd, error } => {
                if let Cmd::LoadProjectPage { .. } = cmd {
                    self.loading = false;
                }
                self.error = Some(error);
                Vec::new()
            }
            Msg::PipelinesLoaded { project, page } => {
                self.pipelines_asked = false;
                if self.selected() != Some(project) || page.page != self.pipeline_page {
                    return Vec::new();
                }
                self.pipelines_loaded(project, page)
            }
            Msg::JobsLoaded {
                pipeline,
                jobs,
                bridges,
            } => {
                self.jobs_asked = false;
                if self.pipeline().map(|p| p.id) == Some(pipeline) {
                    self.jobs = jobs;
                    self.bridges = bridges;
                    self.rebuild_job_rows();
                }
                Vec::new()
            }
            Msg::Acted { action, started } => {
                self.notice = Some(action.done(started));
                self.reload_lists()
            }
            Msg::LogLoaded { job, log } => {
                self.log_loaded(job, &log);
                Vec::new()
            }
            Msg::ChildJobsLoaded { pipeline, jobs } => {
                self.children_asked.remove(&pipeline);
                self.children.insert(pipeline, jobs);
                self.rebuild_job_rows();
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
        self.notice = None;
        if self.overlay == Overlay::Confirm {
            return self.on_confirm_key(key);
        }
        if self.overlay == Overlay::Form {
            return self.on_form_key(key);
        }
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
        let on_pipelines = self.pane == Pane::Pipelines && self.focus == Focus::Sidebar;
        let on_jobs = self.pane == Pane::Jobs && self.focus == Focus::Sidebar;
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
            KeyCode::Char(']') if on_pipelines => {
                if let Some(next) = self.next_pipeline_page {
                    return self.turn_pipeline_page(next);
                }
            }
            KeyCode::Char('[') if on_pipelines && self.pipeline_page > 1 => {
                return self.turn_pipeline_page(self.pipeline_page - 1);
            }
            KeyCode::Char('N') if self.pane == Pane::Pipelines => return self.open_run_form(),
            KeyCode::Char('P') if self.pane == Pane::Jobs => return self.open_play_form(),
            KeyCode::Char('R') => return self.propose(true),
            KeyCode::Char('C') => return self.propose(false),
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
            KeyCode::Enter if on_jobs && self.opens() => return self.toggle_row(),
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

    fn on_confirm_key(&mut self, key: KeyEvent) -> Vec<Cmd> {
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            self.quit |= key.code == KeyCode::Char('c');
            return Vec::new();
        }
        match key.code {
            KeyCode::Char('y') => {
                self.overlay = Overlay::None;
                self.confirm.take().map(Cmd::Act).into_iter().collect()
            }
            KeyCode::Char('n') | KeyCode::Esc => {
                self.overlay = Overlay::None;
                self.confirm = None;
                Vec::new()
            }
            _ => Vec::new(),
        }
    }

    fn on_form_key(&mut self, key: KeyEvent) -> Vec<Cmd> {
        if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('c') {
            self.quit = true;
            return Vec::new();
        }
        let Some(form) = self.form.as_mut() else {
            self.overlay = Overlay::None;
            return Vec::new();
        };
        match form.key(key) {
            Outcome::Open => Vec::new(),
            Outcome::Send(action) => {
                self.close_form();
                vec![Cmd::Act(action)]
            }
            Outcome::Closed => {
                self.close_form();
                Vec::new()
            }
        }
    }

    fn close_form(&mut self) {
        self.form = None;
        self.overlay = Overlay::None;
    }

    fn open_run_form(&mut self) -> Vec<Cmd> {
        let Some(project) = self.project() else {
            return Vec::new();
        };
        let git_ref = (self.pipeline().map(|p| p.git_ref.clone()))
            .or_else(|| project.default_branch.clone())
            .unwrap_or_default();
        self.form = Some(Form::run(project.id, git_ref));
        self.overlay = Overlay::Form;
        Vec::new()
    }

    fn open_play_form(&mut self) -> Vec<Cmd> {
        match self.job_rows.get(self.cursor(Pane::Jobs)) {
            Some(JobRow::Job { job, project, .. }) if job.status == Status::Manual => {
                self.form = Some(Form::play(*project, job.id, job.name.clone()));
                self.overlay = Overlay::Form;
            }
            Some(JobRow::Job { .. }) => {
                self.notice = Some("Only a manual job can be played".to_string());
            }
            Some(_) => self.notice = Some("Select a single job to play".to_string()),
            None => {}
        }
        Vec::new()
    }

    /// Asks before a retry or a cancel, or says why there is nothing to do, as the Go release
    /// does for a job that has finished or a row that is not one job.
    fn propose(&mut self, retry: bool) -> Vec<Cmd> {
        let cancelable = |status: Status| status.is_active() && status != Status::Canceling;
        let action = match self.pane {
            Pane::Pipelines => {
                let (Some(project), Some(pipeline)) = (self.selected(), self.pipeline()) else {
                    return Vec::new();
                };
                if retry {
                    Ok(Action::RetryPipeline {
                        project,
                        pipeline: pipeline.id,
                        git_ref: pipeline.git_ref.clone(),
                    })
                } else if cancelable(pipeline.status) {
                    Ok(Action::CancelPipeline {
                        project,
                        pipeline: pipeline.id,
                    })
                } else {
                    Err("This pipeline has nothing left to cancel")
                }
            }
            Pane::Jobs => match self.job_rows.get(self.cursor(Pane::Jobs)) {
                Some(JobRow::Job { job, project, .. }) => {
                    let (project, id, name) = (*project, job.id, job.name.clone());
                    if retry && job.status.is_active() {
                        Err("This job is still running")
                    } else if retry {
                        Ok(Action::RetryJob {
                            project,
                            job: id,
                            name,
                        })
                    } else if cancelable(job.status) {
                        Ok(Action::CancelJob {
                            project,
                            job: id,
                            name,
                        })
                    } else {
                        Err("This job has nothing left to cancel")
                    }
                }
                Some(_) => Err("Select a single job to retry or cancel"),
                None => return Vec::new(),
            },
            Pane::Projects | Pane::MergeRequests => return Vec::new(),
        };
        match action {
            Ok(action) => {
                self.confirm = Some(action);
                self.overlay = Overlay::Confirm;
            }
            Err(reason) => self.notice = Some(reason.to_string()),
        }
        Vec::new()
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
        let mut commands = if self.selected() == before {
            Vec::new()
        } else {
            self.project_changed()
        };
        commands.extend(self.ask_statuses());
        commands
    }

    /// Asks once for each project within a page of the cursor, as the Go release asks for the
    /// page in view, because one call for each of thousands of projects would trip GitLab's
    /// rate limit.
    fn ask_statuses(&mut self) -> Vec<Cmd> {
        let cursor = self.cursor(Pane::Projects);
        let reach = usize::from(self.page_size);
        let rows = cursor.saturating_sub(reach)..(cursor + reach + 1).min(self.shown.len());
        let mut commands = Vec::new();
        for row in rows {
            let project = self.projects[self.shown[row]].id;
            if let Entry::Vacant(entry) = self.statuses.entry(project) {
                entry.insert(ProjectStatus::Loading);
                commands.push(Cmd::LoadStatus { project });
            }
        }
        commands
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
        self.statuses.clear();
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
        if self.focus == Focus::Detail && self.pane == Pane::Jobs {
            self.scroll_log(delta);
            return Vec::new();
        }
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
            Pane::Projects => {
                let mut commands = self.project_changed();
                commands.extend(self.ask_statuses());
                commands
            }
            Pane::Pipelines => {
                self.clear_jobs();
                if let (Some(project), Some(pipeline)) = (self.selected(), self.pipeline()) {
                    let pipeline = pipeline.id;
                    self.wait(Waiting::Pipeline { project, pipeline });
                }
                Vec::new()
            }
            Pane::Jobs => {
                self.log = None;
                self.waiting = None;
                if let Some((project, job)) = self.wanted_log() {
                    self.wait(Waiting::Job { project, job });
                }
                Vec::new()
            }
            Pane::MergeRequests => Vec::new(),
        }
    }

    fn project_changed(&mut self) -> Vec<Cmd> {
        self.pipeline_page = 1;
        self.next_pipeline_page = None;
        self.pipelines.clear();
        self.clear_jobs();
        self.merge_requests.clear();
        self.waiting = None;
        let Some(project) = self.selected() else {
            return Vec::new();
        };
        if !self.lists_asked {
            self.lists_asked = true;
            return self.lists(Waiting::Project(project));
        }
        self.wait(Waiting::Project(project));
        Vec::new()
    }

    fn wait(&mut self, selection: Waiting) {
        self.waiting = Some((selection, self.tick + SETTLE_TICKS));
    }

    fn settled(&mut self) -> Vec<Cmd> {
        match self.waiting {
            Some((selection, due)) if self.tick >= due => {
                self.waiting = None;
                self.lists(selection)
            }
            _ => Vec::new(),
        }
    }

    fn lists(&self, selection: Waiting) -> Vec<Cmd> {
        match selection {
            Waiting::Project(project) => vec![
                Cmd::LoadPipelines {
                    project,
                    page: self.pipeline_page,
                },
                Cmd::LoadMergeRequests { project },
            ],
            Waiting::Pipeline { project, pipeline } => vec![Cmd::LoadJobs { project, pipeline }],
            Waiting::Job { project, job } => vec![Cmd::LoadLog { project, job }],
        }
    }

    fn pipelines_loaded(&mut self, project: u64, page: Page<Pipeline>) -> Vec<Cmd> {
        if page.page == 1 {
            let newest = page.items.first().map(|p| p.status);
            let status = newest.map_or(ProjectStatus::NoPipeline, ProjectStatus::Pipeline);
            self.statuses.insert(project, status);
        }
        let row = self.cursor(Pane::Pipelines);
        let before = self.pipeline().map(|p| p.id);
        self.pipelines = page.items;
        self.next_pipeline_page = page.next_page;
        self.cursors[Pane::Pipelines.index()] = keep(&self.pipelines, |p| p.id, before, row);
        if self.pipeline().map(|p| p.id) == before {
            Vec::new()
        } else {
            self.pipeline_changed()
        }
    }

    fn turn_pipeline_page(&mut self, page: u32) -> Vec<Cmd> {
        let Some(project) = self.selected() else {
            return Vec::new();
        };
        self.pipeline_page = page;
        self.next_pipeline_page = None;
        self.pipelines.clear();
        self.clear_jobs();
        self.cursors[Pane::Pipelines.index()] = 0;
        self.waiting = None;
        vec![Cmd::LoadPipelines { project, page }]
    }

    /// Asks again for the pipelines and the jobs in view, once nothing moves and nothing waits.
    fn refresh(&mut self) -> Vec<Cmd> {
        let due = self.tick.saturating_sub(self.refreshed_at) >= REFRESH_TICKS;
        if !due || self.waiting.is_some() {
            return Vec::new();
        }
        self.refreshed_at = self.tick;
        self.reload_lists()
    }

    /// The pipelines and the jobs in view, the log of a running job, and every open child
    /// pipeline, each unless a load of it is already out.
    fn reload_lists(&mut self) -> Vec<Cmd> {
        let Some(project) = self.selected() else {
            return Vec::new();
        };
        let mut commands = Vec::new();
        if !self.pipelines_asked {
            let page = self.pipeline_page;
            commands.push(Cmd::LoadPipelines { project, page });
        }
        if let Some(pipeline) = self.pipeline().map(|p| p.id)
            && !self.jobs_asked
        {
            commands.push(Cmd::LoadJobs { project, pipeline });
        }
        let running = self.job().is_some_and(|job| job.status.is_active());
        if let Some((project, job)) = self.wanted_log()
            && running
            && self.log_asked.is_none()
        {
            commands.push(Cmd::LoadLog { project, job });
        }
        let open: Vec<(u64, u64)> = self.child_pipelines().collect();
        for (project, pipeline) in open {
            if !self.children_asked.contains(&pipeline) {
                commands.push(Cmd::LoadChildJobs { project, pipeline });
            }
        }
        commands
    }

    /// The project and ID of each child pipeline under an open bridge.
    fn child_pipelines(&self) -> impl Iterator<Item = (u64, u64)> + '_ {
        let project = self.selected().unwrap_or_default();
        self.bridges
            .iter()
            .filter(|b| self.open.contains(&RowKey::Bridge(b.id)))
            .filter_map(move |b| b.downstream_pipeline.as_ref())
            .map(move |d| (d.project_id.unwrap_or(project), d.id))
    }

    pub fn job_rows(&self) -> &[JobRow] {
        &self.job_rows
    }

    /// The job on the jobs pane's current row, when that row is a job.
    pub fn job(&self) -> Option<&Job> {
        match self.job_rows.get(self.cursor(Pane::Jobs)) {
            Some(JobRow::Job { job, .. }) => Some(job),
            _ => None,
        }
    }

    /// The current row is a group, or a bridge that triggered a child pipeline.
    fn opens(&self) -> bool {
        match self.job_rows.get(self.cursor(Pane::Jobs)) {
            Some(JobRow::Group { .. }) => true,
            Some(JobRow::Bridge { bridge, .. }) => bridge.downstream_pipeline.is_some(),
            _ => false,
        }
    }

    fn toggle_row(&mut self) -> Vec<Cmd> {
        let Some(row) = self.job_rows.get(self.cursor(Pane::Jobs)) else {
            return Vec::new();
        };
        let key = row.key();
        if !self.open.remove(&key) {
            self.open.insert(key);
        }
        self.rebuild_job_rows();
        let project = self.selected().unwrap_or_default();
        let wanted = match row_bridge(&self.job_rows, self.cursor(Pane::Jobs)) {
            Some(bridge) if self.open.contains(&RowKey::Bridge(bridge.id)) => bridge
                .downstream_pipeline
                .as_ref()
                .map(|d| (d.project_id.unwrap_or(project), d.id)),
            _ => None,
        };
        match wanted {
            Some((project, pipeline))
                if !self.children.contains_key(&pipeline)
                    && !self.children_asked.contains(&pipeline) =>
            {
                vec![Cmd::LoadChildJobs { project, pipeline }]
            }
            _ => Vec::new(),
        }
    }

    fn rebuild_job_rows(&mut self) {
        let row = self.cursor(Pane::Jobs);
        let before = self.job_rows.get(row).map(JobRow::key);
        let project = self.selected().unwrap_or_default();
        self.job_rows = jobs::rows(
            project,
            &self.jobs,
            &self.bridges,
            &self.children,
            &self.open,
        );
        self.cursors[Pane::Jobs.index()] = keep(&self.job_rows, JobRow::key, before, row);
    }

    fn clear_jobs(&mut self) {
        self.jobs.clear();
        self.bridges.clear();
        self.children.clear();
        self.open.clear();
        self.job_rows.clear();
        self.cursors[Pane::Jobs.index()] = 0;
        self.log = None;
        self.log_asked = None;
    }

    fn pipeline_changed(&mut self) -> Vec<Cmd> {
        self.clear_jobs();
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

/// The row of the item that was current, while the list still holds it, and otherwise the same
/// row, kept inside the list.
fn keep<T, K: PartialEq>(
    items: &[T],
    key: impl Fn(&T) -> K,
    before: Option<K>,
    row: usize,
) -> usize {
    before
        .and_then(|before| items.iter().position(|item| key(item) == before))
        .unwrap_or_else(|| row.min(items.len().saturating_sub(1)))
}

fn row_bridge(rows: &[JobRow], row: usize) -> Option<&Bridge> {
    match rows.get(row) {
        Some(JobRow::Bridge { bridge, .. }) => Some(bridge),
        _ => None,
    }
}
