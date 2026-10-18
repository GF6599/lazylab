//! The state of the screen, and the one place it changes: `update`, which takes a message and
//! answers with the work the event loop has to start.

use std::cell::Cell;
use std::collections::hash_map::Entry;
use std::collections::{HashMap, HashSet};
use std::time::Duration;

use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use url::Url;

use super::diff::{self, CodeLine, Diff};
use super::form::{Form, MergeRequestFields, Outcome};
use super::jobs::{self, JobRow, RowKey};
use super::log::Log;
use crate::gitlab::{
    Bridge, DiffLine, DiffRefs, Discussion, FileDiff, Job, MergeRequest, MergeRequestState, Page,
    Pipeline, Project, Status, TestReport, Variable,
};
use crate::glab_command::{self, GlabCommand, Item};
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
const NO_GLAB_COMMAND: &str = "No glab command for this selection";

/// A selection whose lists load once the cursor has rested on it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Waiting {
    Project(u64),
    Pipeline { project: u64, pipeline: u64 },
    Job { project: u64, job: u64 },
    MergeRequest,
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
pub enum MrTab {
    Info,
    Threads,
    Diff,
}

impl MrTab {
    pub const ALL: [MrTab; 3] = [MrTab::Info, MrTab::Threads, MrTab::Diff];

    pub fn label(self) -> &'static str {
        match self {
            MrTab::Info => "info",
            MrTab::Threads => "threads",
            MrTab::Diff => "diff",
        }
    }

    fn step(self, delta: usize) -> MrTab {
        let index = MrTab::ALL.iter().position(|&tab| tab == self).unwrap_or(0);
        MrTab::ALL[(index + delta) % MrTab::ALL.len()]
    }
}

/// The states the merge request list turns through, in the Go release's order.
const MR_STATES: [MergeRequestState; 3] = [
    MergeRequestState::Opened,
    MergeRequestState::Merged,
    MergeRequestState::Closed,
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Overlay {
    None,
    Help,
    /// A question before the action in `App::confirm` goes out.
    Confirm,
    /// The run or play form in `App::form`, which takes every key until it closes.
    Form,
    /// The glab commands in `App::command_menu`, one of which goes to the clipboard.
    CommandMenu,
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
    /// Text the terminal pasted, as one piece and not as keys.
    Paste(String),
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
        state: MergeRequestState,
        merge_requests: Vec<MergeRequest>,
    },
    ThreadsLoaded {
        project: u64,
        merge_request: u64,
        discussions: Vec<Discussion>,
    },
    ThreadResolved {
        project: u64,
        merge_request: u64,
        resolved: bool,
    },
    /// `reply` is true for a note in a thread, and false for a new thread.
    Commented {
        project: u64,
        merge_request: u64,
        reply: bool,
    },
    DiffLoaded {
        project: u64,
        merge_request: u64,
        files: Vec<FileDiff>,
    },
    /// `None` until GitLab has prepared the diff.
    DiffRefsLoaded {
        project: u64,
        merge_request: u64,
        refs: Option<DiffRefs>,
    },
    TestReportLoaded {
        project: u64,
        pipeline: u64,
        report: TestReport,
    },
    /// `search` is the term the branches match, so an answer for a term typed over is dropped.
    BranchesLoaded {
        project: u64,
        search: String,
        branches: Vec<String>,
    },
    MergeRequestCreated {
        project: u64,
        merge_request: MergeRequest,
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
        state: MergeRequestState,
    },
    /// `merge_request` is the number GitLab shows as `!42`.
    LoadThreads {
        project: u64,
        merge_request: u64,
    },
    LoadDiff {
        project: u64,
        merge_request: u64,
    },
    /// The commits a comment on a line of the diff names.
    LoadDiffRefs {
        project: u64,
        merge_request: u64,
    },
    LoadTestReport {
        project: u64,
        pipeline: u64,
    },
    /// `resolved` is the state the thread goes to: `false` reopens it.
    ResolveThread {
        project: u64,
        merge_request: u64,
        discussion: String,
        resolved: bool,
    },
    /// `discussion` is the thread a reply goes in, and `None` starts a thread. `line` is the line
    /// of the diff a new thread starts on, and `None` starts it on the merge request as a whole.
    Comment {
        project: u64,
        merge_request: u64,
        discussion: Option<String>,
        line: Option<DiffLine>,
        body: String,
    },
    /// The branches whose name holds `search`. An empty search lists the first page of them.
    LoadBranches {
        project: u64,
        search: String,
    },
    CreateMergeRequest {
        project: u64,
        source: String,
        target: String,
        title: String,
    },
    Act(Action),
    /// The log of a job, in the project it runs in.
    LoadLog {
        project: u64,
        job: u64,
    },
    /// Text for the system clipboard, which the event loop writes to the terminal.
    Copy(String),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProjectStatus {
    Loading,
    Pipeline(Status),
    NoPipeline,
    Unknown,
}

#[derive(Debug)]
pub struct Threads {
    pub project: u64,
    pub merge_request: u64,
    /// `None` when the load failed, which stops a retry until the cursor moves.
    pub discussions: Option<Vec<Discussion>>,
}

#[derive(Debug)]
struct MergeRequestDiffRefs {
    project: u64,
    merge_request: u64,
    /// `None` when GitLab has not prepared the diff or the load failed, which stops a retry until
    /// the cursor moves.
    refs: Option<DiffRefs>,
}

#[derive(Debug)]
pub struct PipelineTestReport {
    pub project: u64,
    pub pipeline: u64,
    /// `None` when the load failed, which stops a retry until the pipeline changes or `T` asks.
    pub report: Option<TestReport>,
}

#[derive(Debug)]
pub struct CommandMenu {
    pub project: String,
    pub commands: Vec<GlabCommand>,
    pub row: usize,
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
    /// The tick the branch search of the form goes out on, once the typing has settled.
    branches_due: Option<u64>,
    pub command_menu: Option<CommandMenu>,
    /// In the order the user set, which the favorites view keeps.
    pub favorites: Vec<u64>,
    pub favorites_only: bool,
    pub search: Search,
    pub statuses: HashMap<u64, ProjectStatus>,
    page_size: u8,
    /// The GitLab the copied glab commands name. `None` leaves the choice to glab.
    host: Option<Url>,
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
    /// The job whose last log load failed, until the next load of it goes out. `ask_log` sends
    /// none, so only a cursor move or the refresh tries again.
    log_failed: Option<u64>,
    /// Lines between the end of the log and the last line in view. Zero follows the end.
    log_scroll: usize,
    /// The rows the detail pane gave the log when it last drew, so a scroll stops at the top.
    pub log_height: Cell<usize>,
    pub merge_requests: Vec<MergeRequest>,
    /// The state of the merge requests the list shows.
    pub mr_state: MergeRequestState,
    /// The same tab stays in view from one merge request to the next.
    pub mr_tab: MrTab,
    threads: Option<Threads>,
    threads_asked: Option<(u64, u64)>,
    /// A resolve answered while a reload was out, which that reload may not show.
    threads_reload_due: bool,
    thread_row: usize,
    diff: Option<Diff>,
    diff_asked: Option<(u64, u64)>,
    /// The row the diff cursor is on, counted among the rows it rests on alone.
    diff_cursor: usize,
    diff_refs: Option<MergeRequestDiffRefs>,
    diff_refs_asked: Option<(u64, u64)>,
    /// The pipeline detail shows the test report instead of the stages.
    pub test_report_open: bool,
    test_report: Option<PipelineTestReport>,
    test_report_asked: Option<(u64, u64)>,
    /// Lines between the top of the test report and the first line in view.
    test_report_scroll: usize,
    /// The lines of the test report and the rows the detail pane gave it when it last drew, so a
    /// scroll stops at the end.
    pub test_report_lines: Cell<usize>,
    pub test_report_height: Cell<usize>,
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
            branches_due: None,
            command_menu: None,
            favorites: Vec::new(),
            favorites_only: false,
            search: Search::default(),
            statuses: HashMap::new(),
            page_size,
            host: None,
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
            log_failed: None,
            log_scroll: 0,
            log_height: Cell::new(0),
            merge_requests: Vec::new(),
            mr_state: MergeRequestState::Opened,
            mr_tab: MrTab::Info,
            threads: None,
            threads_asked: None,
            threads_reload_due: false,
            thread_row: 0,
            diff: None,
            diff_asked: None,
            diff_cursor: 0,
            diff_refs: None,
            diff_refs_asked: None,
            test_report_open: false,
            test_report: None,
            test_report_asked: None,
            test_report_scroll: 0,
            test_report_lines: Cell::new(0),
            test_report_height: Cell::new(0),
            cursors: [0; Pane::ALL.len()],
        }
    }

    pub fn with_host(mut self, host: Url) -> App {
        self.host = Some(host);
        self
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
        let mut asks = self.ask_log();
        asks.extend(self.ask_threads());
        asks.extend(self.ask_diff());
        asks.extend(self.ask_diff_refs());
        asks.extend(self.ask_test_report());
        self.note_asked(&asks);
        commands.extend(asks);
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
                Cmd::LoadLog { job, .. } => {
                    self.log_asked = Some(*job);
                    if self.log_failed == Some(*job) {
                        self.log_failed = None;
                    }
                }
                Cmd::LoadThreads {
                    project,
                    merge_request,
                } => self.threads_asked = Some((*project, *merge_request)),
                Cmd::LoadDiff {
                    project,
                    merge_request,
                } => self.diff_asked = Some((*project, *merge_request)),
                Cmd::LoadDiffRefs {
                    project,
                    merge_request,
                } => self.diff_refs_asked = Some((*project, *merge_request)),
                Cmd::LoadTestReport { project, pipeline } => {
                    self.test_report_asked = Some((*project, *pipeline));
                }
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
        let failed = self.log_failed == Some(job);
        if shown || failed || self.waiting.is_some() || self.log_asked == Some(job) {
            return Vec::new();
        }
        vec![Cmd::LoadLog { project, job }]
    }

    pub fn log_failed(&self, job: u64) -> bool {
        self.log_failed == Some(job)
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

    fn wanted(&self, tab: MrTab) -> Option<(u64, u64)> {
        if self.pane != Pane::MergeRequests || self.mr_tab != tab {
            return None;
        }
        let project = self.selected()?;
        let merge_request = self.merge_requests.get(self.cursor(Pane::MergeRequests))?;
        Some((project, merge_request.iid))
    }

    fn wanted_threads(&self) -> Option<(u64, u64)> {
        self.wanted(MrTab::Threads)
    }

    fn wanted_diff(&self) -> Option<(u64, u64)> {
        self.wanted(MrTab::Diff)
    }

    /// The diff of the merge request in view, once it loaded or failed to.
    pub fn shown_diff(&self) -> Option<&Diff> {
        let wanted = self.wanted_diff()?;
        (self.diff.as_ref()).filter(|d| (d.project, d.merge_request) == wanted)
    }

    /// The row of the diff the cursor is on, once the diff has a row it rests on.
    pub fn picked_diff_row(&self) -> Option<usize> {
        let rows = self.shown_diff()?.rows.as_deref()?;
        diff::cursor_rows(rows).nth(self.diff_cursor)
    }

    fn ask_diff(&mut self) -> Vec<Cmd> {
        let Some((project, merge_request)) = self.wanted_diff() else {
            return Vec::new();
        };
        let asked = self.diff_asked == Some((project, merge_request));
        if self.shown_diff().is_some() || self.waiting.is_some() || asked {
            return Vec::new();
        }
        vec![Cmd::LoadDiff {
            project,
            merge_request,
        }]
    }

    fn diff_loaded(&mut self, key: (u64, u64), files: Option<Vec<FileDiff>>) {
        if self.diff_asked == Some(key) {
            self.diff_asked = None;
        }
        if self.wanted_diff() != Some(key) {
            return;
        }
        let (project, merge_request) = key;
        self.diff = Some(Diff {
            project,
            merge_request,
            rows: files.map(|files| diff::rows(&files)),
        });
    }

    fn picked_code_line(&self) -> Option<&CodeLine> {
        let rows = self.shown_diff()?.rows.as_deref()?;
        rows.get(self.picked_diff_row()?)?.code.as_ref()
    }

    fn shown_diff_refs(&self) -> Option<&MergeRequestDiffRefs> {
        let wanted = self.wanted_diff()?;
        (self.diff_refs.as_ref()).filter(|r| (r.project, r.merge_request) == wanted)
    }

    fn ask_diff_refs(&mut self) -> Vec<Cmd> {
        let Some((project, merge_request)) = self.wanted_diff() else {
            return Vec::new();
        };
        let asked = self.diff_refs_asked == Some((project, merge_request));
        if self.shown_diff_refs().is_some() || self.waiting.is_some() || asked {
            return Vec::new();
        }
        vec![Cmd::LoadDiffRefs {
            project,
            merge_request,
        }]
    }

    fn diff_refs_loaded(&mut self, key: (u64, u64), refs: Option<DiffRefs>) {
        if self.diff_refs_asked == Some(key) {
            self.diff_refs_asked = None;
        }
        if self.wanted_diff() != Some(key) {
            return;
        }
        let (project, merge_request) = key;
        self.diff_refs = Some(MergeRequestDiffRefs {
            project,
            merge_request,
            refs,
        });
    }

    fn move_diff_cursor(&mut self, delta: isize) {
        let len = (self.shown_diff())
            .and_then(|d| d.rows.as_deref())
            .map_or(0, |rows| diff::cursor_rows(rows).count());
        if len > 0 {
            self.diff_cursor = self.diff_cursor.saturating_add_signed(delta).min(len - 1);
        }
    }

    fn clear_diff(&mut self) {
        self.diff = None;
        self.diff_cursor = 0;
        self.diff_refs = None;
    }

    fn wanted_test_report(&self) -> Option<(u64, u64)> {
        if self.pane != Pane::Pipelines || !self.test_report_open {
            return None;
        }
        Some((self.selected()?, self.pipeline()?.id))
    }

    /// The test report of the pipeline in view, once it loaded or failed to.
    pub fn shown_test_report(&self) -> Option<&PipelineTestReport> {
        let wanted = self.wanted_test_report()?;
        (self.test_report.as_ref()).filter(|r| (r.project, r.pipeline) == wanted)
    }

    fn ask_test_report(&mut self) -> Vec<Cmd> {
        let Some((project, pipeline)) = self.wanted_test_report() else {
            return Vec::new();
        };
        let asked = self.test_report_asked == Some((project, pipeline));
        if self.shown_test_report().is_some() || self.waiting.is_some() || asked {
            return Vec::new();
        }
        vec![Cmd::LoadTestReport { project, pipeline }]
    }

    fn test_report_loaded(&mut self, key: (u64, u64), report: Option<TestReport>) {
        if self.test_report_asked == Some(key) {
            self.test_report_asked = None;
        }
        if self.wanted_test_report() != Some(key) {
            return;
        }
        let (project, pipeline) = key;
        self.test_report = Some(PipelineTestReport {
            project,
            pipeline,
            report,
        });
    }

    pub fn test_report_scroll(&self) -> usize {
        self.test_report_scroll
    }

    fn scroll_test_report(&mut self, delta: isize) {
        let (len, height) = (self.test_report_lines.get(), self.test_report_height.get());
        self.test_report_scroll = scrolled(self.test_report_scroll, delta, len, height);
    }

    fn clear_test_report(&mut self) {
        self.test_report = None;
        self.test_report_scroll = 0;
    }

    /// Each open loads the report again, so a failed load can be asked for once more.
    fn toggle_test_report(&mut self) {
        self.test_report_open = !self.test_report_open;
        if self.test_report_open {
            self.clear_test_report();
        }
    }

    /// The threads of the merge request in view, once they loaded or failed to.
    pub fn shown_threads(&self) -> Option<&Threads> {
        let wanted = self.wanted_threads()?;
        (self.threads.as_ref()).filter(|t| (t.project, t.merge_request) == wanted)
    }

    pub fn thread_row(&self) -> usize {
        self.thread_row
    }

    fn ask_threads(&mut self) -> Vec<Cmd> {
        let Some((project, merge_request)) = self.wanted_threads() else {
            return Vec::new();
        };
        let asked = self.threads_asked == Some((project, merge_request));
        if self.shown_threads().is_some() || self.waiting.is_some() || asked {
            return Vec::new();
        }
        vec![Cmd::LoadThreads {
            project,
            merge_request,
        }]
    }

    /// A discussion of system notes only, such as "added 1 commit", is not a thread.
    fn threads_loaded(
        &mut self,
        key: (u64, u64),
        discussions: Option<Vec<Discussion>>,
    ) -> Vec<Cmd> {
        if self.threads_asked == Some(key) {
            self.threads_asked = None;
        }
        if self.wanted_threads() != Some(key) {
            return Vec::new();
        }
        let reload = std::mem::take(&mut self.threads_reload_due);
        let discussions = discussions.map(|all| {
            (all.into_iter())
                .filter(|d| d.notes.iter().any(|n| !n.system))
                .collect::<Vec<_>>()
        });
        let before = (self.shown_threads())
            .and_then(|t| t.discussions.as_ref())
            .and_then(|d| d.get(self.thread_row))
            .map(|d| d.id.clone());
        if let Some(list) = &discussions {
            self.thread_row = keep(list, |d| d.id.clone(), before, self.thread_row);
        }
        let (project, merge_request) = key;
        self.threads = Some(Threads {
            project,
            merge_request,
            discussions,
        });
        if !reload {
            return Vec::new();
        }
        vec![Cmd::LoadThreads {
            project,
            merge_request,
        }]
    }

    fn picked_thread(&self) -> Option<&Discussion> {
        let discussions = self.shown_threads()?.discussions.as_ref()?;
        discussions.get(self.thread_row)
    }

    /// GitLab marks a thread resolvable on its first note, and a plain comment is not.
    fn resolve_thread(&mut self) -> Vec<Cmd> {
        let (Some((project, merge_request)), Some(thread)) =
            (self.wanted_threads(), self.picked_thread())
        else {
            return Vec::new();
        };
        match thread.notes.iter().find(|n| !n.system) {
            Some(note) if note.resolvable => vec![Cmd::ResolveThread {
                project,
                merge_request,
                discussion: thread.id.clone(),
                resolved: !note.resolved,
            }],
            _ => {
                self.notice = Some("This thread cannot be resolved".to_string());
                Vec::new()
            }
        }
    }

    /// The threads load again so the badge shows what GitLab holds, not what was asked for.
    fn thread_resolved(&mut self, key: (u64, u64), resolved: bool) -> Vec<Cmd> {
        let done = if resolved { "Resolved" } else { "Reopened" };
        self.notice = Some(format!("{done} the thread"));
        self.reload_threads(key)
    }

    fn commented(&mut self, key: (u64, u64), reply: bool) -> Vec<Cmd> {
        let done = if reply {
            "Replied in the thread"
        } else {
            "Started a thread"
        };
        self.notice = Some(done.to_string());
        self.reload_threads(key)
    }

    /// Threads out of view are dropped instead, so the threads tab loads them when it opens.
    fn reload_threads(&mut self, key: (u64, u64)) -> Vec<Cmd> {
        if self.wanted_threads() != Some(key) {
            if (self.threads.as_ref()).is_some_and(|t| (t.project, t.merge_request) == key) {
                self.clear_threads();
            }
            return Vec::new();
        }
        if self.threads_asked == Some(key) {
            self.threads_reload_due = true;
            return Vec::new();
        }
        let (project, merge_request) = key;
        vec![Cmd::LoadThreads {
            project,
            merge_request,
        }]
    }

    fn move_thread(&mut self, delta: isize) {
        let len = (self.shown_threads())
            .and_then(|t| t.discussions.as_ref())
            .map_or(0, Vec::len);
        if len > 0 {
            self.thread_row = self.thread_row.saturating_add_signed(delta).min(len - 1);
        }
    }

    fn clear_threads(&mut self) {
        self.threads = None;
        self.threads_reload_due = false;
        self.thread_row = 0;
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
            Msg::Paste(text) => self.on_paste(&text),
            Msg::Tick => {
                self.tick += 1;
                let mut commands = self.settled();
                commands.extend(self.branches_settled());
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
                self.log_failed = Some(job);
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd:
                    Cmd::LoadThreads {
                        project,
                        merge_request,
                    },
                error,
            } => {
                self.error = Some(error);
                self.threads_loaded((project, merge_request), None)
            }
            Msg::Failed {
                cmd:
                    Cmd::LoadDiff {
                        project,
                        merge_request,
                    },
                error,
            } => {
                self.diff_loaded((project, merge_request), None);
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd:
                    Cmd::LoadDiffRefs {
                        project,
                        merge_request,
                    },
                error,
            } => {
                self.diff_refs_loaded((project, merge_request), None);
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd: Cmd::LoadTestReport { project, pipeline },
                error,
            } => {
                self.test_report_loaded((project, pipeline), None);
                self.error = Some(error);
                Vec::new()
            }
            Msg::Failed {
                cmd:
                    Cmd::Comment {
                        project,
                        merge_request,
                        discussion,
                        line,
                        body,
                    },
                error,
            } => {
                self.error = Some(error);
                if self.overlay == Overlay::None {
                    let form = Form::comment(project, merge_request, discussion, line, body);
                    self.form = Some(form);
                    self.overlay = Overlay::Form;
                }
                Vec::new()
            }
            Msg::Failed {
                cmd: Cmd::LoadBranches { project, search },
                error,
            } => {
                self.error = Some(error);
                if let Some(form) = self.form.as_mut() {
                    form.show_branches(project, &search, Vec::new());
                }
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
                state,
                merge_requests,
            } => {
                if self.selected() == Some(project) && self.mr_state == state {
                    self.merge_requests = merge_requests;
                    self.cursors[Pane::MergeRequests.index()] = 0;
                    self.clear_threads();
                    self.clear_diff();
                }
                Vec::new()
            }
            Msg::ThreadsLoaded {
                project,
                merge_request,
                discussions,
            } => self.threads_loaded((project, merge_request), Some(discussions)),
            Msg::ThreadResolved {
                project,
                merge_request,
                resolved,
            } => self.thread_resolved((project, merge_request), resolved),
            Msg::Commented {
                project,
                merge_request,
                reply,
            } => self.commented((project, merge_request), reply),
            Msg::DiffLoaded {
                project,
                merge_request,
                files,
            } => {
                self.diff_loaded((project, merge_request), Some(files));
                Vec::new()
            }
            Msg::DiffRefsLoaded {
                project,
                merge_request,
                refs,
            } => {
                self.diff_refs_loaded((project, merge_request), refs);
                Vec::new()
            }
            Msg::TestReportLoaded {
                project,
                pipeline,
                report,
            } => {
                self.test_report_loaded((project, pipeline), Some(report));
                Vec::new()
            }
            Msg::BranchesLoaded {
                project,
                search,
                branches,
            } => {
                if let Some(form) = self.form.as_mut() {
                    form.show_branches(project, &search, branches);
                }
                Vec::new()
            }
            Msg::MergeRequestCreated { .. } => Vec::new(),
        }
    }

    /// A paste goes only into a field being typed, so it can never act as keys.
    fn on_paste(&mut self, text: &str) -> Vec<Cmd> {
        if self.overlay == Overlay::Form {
            let before = self.branch_search();
            if let Some(form) = self.form.as_mut() {
                form.paste(text);
            }
            self.search_branches_later(before);
            return Vec::new();
        }
        if !self.search.typing {
            return Vec::new();
        }
        let before = self.selected();
        (self.search.query).extend(text.chars().filter(|c| !matches!(c, '\r' | '\n')));
        self.reshow(before)
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
        if self.overlay == Overlay::CommandMenu {
            return self.on_menu_key(key);
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
                KeyCode::Char('o') if self.overlay == Overlay::None => self.copy_web_address(),
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
            KeyCode::Char('t') if self.pane == Pane::MergeRequests => {
                return self.switch_mr_state(1);
            }
            KeyCode::Char('T') if self.pane == Pane::MergeRequests => {
                return self.switch_mr_state(MR_STATES.len() - 1);
            }
            KeyCode::Char('{') if on_projects => return self.move_favorite(-1),
            KeyCode::Char('}') if on_projects => return self.move_favorite(1),
            KeyCode::Char(']') if self.pane == Pane::MergeRequests => {
                self.mr_tab = self.mr_tab.step(1);
            }
            KeyCode::Char('[') if self.pane == Pane::MergeRequests => {
                self.mr_tab = self.mr_tab.step(MrTab::ALL.len() - 1);
            }
            KeyCode::Char('r') if self.focus == Focus::Detail => return self.resolve_thread(),
            KeyCode::Char('c') if self.focus == Focus::Detail => return self.open_comment_form(),
            KeyCode::Char(']') if on_pipelines => {
                if let Some(next) = self.next_pipeline_page {
                    return self.turn_pipeline_page(next);
                }
            }
            KeyCode::Char('[') if on_pipelines && self.pipeline_page > 1 => {
                return self.turn_pipeline_page(self.pipeline_page - 1);
            }
            KeyCode::Char('T') if self.pane == Pane::Pipelines => self.toggle_test_report(),
            KeyCode::Char('N') if self.pane == Pane::Pipelines => return self.open_run_form(),
            KeyCode::Char('N') if self.pane == Pane::MergeRequests => {
                return self.open_merge_request_form();
            }
            KeyCode::Char('P') if self.pane == Pane::Jobs => return self.open_play_form(),
            KeyCode::Char('y') => return self.copy_glab_command(),
            KeyCode::Char('Y') => self.open_command_menu(),
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
        let before = self.branch_search();
        let Some(form) = self.form.as_mut() else {
            self.overlay = Overlay::None;
            return Vec::new();
        };
        match form.key(key) {
            Outcome::Open => {
                self.search_branches_later(before);
                Vec::new()
            }
            Outcome::Send(cmd) => {
                self.close_form();
                vec![cmd]
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
        self.branches_due = None;
    }

    /// The field being typed and its branch search, which a key or a paste can change.
    fn branch_search(&self) -> Option<(usize, u64, String)> {
        let form = self.form.as_ref()?;
        let (project, search) = form.branch_search()?;
        Some((form.field, project, search.to_string()))
    }

    /// A search waits until the typing settles, as a moving cursor does, so only the term the
    /// user stops on goes to GitLab.
    fn search_branches_later(&mut self, before: Option<(usize, u64, String)>) {
        let after = self.branch_search();
        if after != before {
            self.branches_due = after.map(|_| self.tick + SETTLE_TICKS);
        }
    }

    fn branches_settled(&mut self) -> Vec<Cmd> {
        match self.branches_due {
            Some(due) if self.tick >= due => {
                self.branches_due = None;
                self.search_branches()
            }
            _ => Vec::new(),
        }
    }

    fn search_branches(&self) -> Vec<Cmd> {
        (self.branch_search().into_iter())
            .map(|(_, project, search)| Cmd::LoadBranches { project, search })
            .collect()
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

    /// The target is the project's default branch, as in the Go release. The empty source branch
    /// lists the branches at once.
    fn open_merge_request_form(&mut self) -> Vec<Cmd> {
        let Some(project) = self.project() else {
            return Vec::new();
        };
        let fields = MergeRequestFields {
            target: project.default_branch.clone().unwrap_or_default(),
            ..MergeRequestFields::default()
        };
        self.form = Some(Form::merge_request(project.id, fields));
        self.overlay = Overlay::Form;
        self.search_branches()
    }

    /// A new thread on the info tab, a reply in the picked thread on the threads tab, and a new
    /// thread on the picked line on the diff tab.
    fn open_comment_form(&mut self) -> Vec<Cmd> {
        if let Some(key) = self.wanted_diff() {
            return self.open_line_comment_form(key);
        }
        let picked = self.picked_thread().map(|thread| thread.id.clone());
        let form = match (self.wanted(MrTab::Info), self.wanted_threads(), picked) {
            (Some((project, merge_request)), ..) => {
                Form::comment(project, merge_request, None, None, String::new())
            }
            (_, Some((project, merge_request)), Some(thread)) => {
                Form::comment(project, merge_request, Some(thread), None, String::new())
            }
            _ => return Vec::new(),
        };
        self.form = Some(form);
        self.overlay = Overlay::Form;
        Vec::new()
    }

    /// GitLab places a comment on a line by the commits of the diff, as the Go release does, so
    /// the form waits for the diff refs.
    fn open_line_comment_form(&mut self, (project, merge_request): (u64, u64)) -> Vec<Cmd> {
        let Some(code) = self.picked_code_line().cloned() else {
            self.notice = Some("Pick a line of code to comment on".to_string());
            return Vec::new();
        };
        let Some(diff_refs) = self.shown_diff_refs().and_then(|r| r.refs.clone()) else {
            self.notice = Some("The diff refs are not loaded yet".to_string());
            return Vec::new();
        };
        let line = DiffLine {
            diff_refs,
            old_path: code.old_path,
            new_path: code.new_path,
            old_line: code.old_line,
            new_line: code.new_line,
        };
        let form = Form::comment(project, merge_request, None, Some(line), String::new());
        self.form = Some(form);
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

    /// The glab commands for the item of the pane in view, which its detail shows as well.
    fn glab_commands(&self) -> Vec<GlabCommand> {
        let Some(project) = self.project() else {
            return Vec::new();
        };
        let item = match self.pane {
            Pane::Projects => Some(Item::Project),
            Pane::Pipelines => self.pipeline().map(|p| Item::Pipeline {
                id: p.id,
                git_ref: &p.git_ref,
            }),
            // A child pipeline's job can run in a project known only by its ID, and glab needs
            // the path. A group or a bridge is not one job.
            Pane::Jobs => match self.job_rows.get(self.cursor(Pane::Jobs)) {
                Some(JobRow::Job {
                    job,
                    project: runs_in,
                    ..
                }) if *runs_in == project.id => Some(Item::Job { id: job.id }),
                _ => None,
            },
            Pane::MergeRequests => (self.merge_requests)
                .get(self.cursor(Pane::MergeRequests))
                .map(|mr| Item::MergeRequest { iid: mr.iid }),
        };
        item.map_or_else(Vec::new, |item| {
            glab_command::commands(self.host.as_ref(), &project.path_with_namespace, item)
        })
    }

    fn copy_glab_command(&mut self) -> Vec<Cmd> {
        match self.glab_commands().into_iter().next() {
            Some(command) => self.copy_line(command.line),
            None => {
                self.notice = Some(NO_GLAB_COMMAND.to_string());
                Vec::new()
            }
        }
    }

    fn open_command_menu(&mut self) {
        let commands = self.glab_commands();
        let project = self.project().map(|p| p.path_with_namespace.clone());
        match project.filter(|_| !commands.is_empty()) {
            Some(project) => {
                self.command_menu = Some(CommandMenu {
                    project,
                    commands,
                    row: 0,
                });
                self.overlay = Overlay::CommandMenu;
            }
            None => self.notice = Some(NO_GLAB_COMMAND.to_string()),
        }
    }

    fn on_menu_key(&mut self, key: KeyEvent) -> Vec<Cmd> {
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            self.quit |= key.code == KeyCode::Char('c');
            return Vec::new();
        }
        let Some(menu) = self.command_menu.as_mut() else {
            self.overlay = Overlay::None;
            return Vec::new();
        };
        match key.code {
            KeyCode::Char('j') | KeyCode::Down => {
                menu.row = (menu.row + 1).min(menu.commands.len().saturating_sub(1));
            }
            KeyCode::Char('k') | KeyCode::Up => menu.row = menu.row.saturating_sub(1),
            KeyCode::Enter | KeyCode::Char('y') => {
                let line = menu.commands.get(menu.row).map(|c| c.line.clone());
                self.close_command_menu();
                if let Some(line) = line {
                    return self.copy_line(line);
                }
            }
            KeyCode::Esc | KeyCode::Char('q') => self.close_command_menu(),
            _ => {}
        }
        Vec::new()
    }

    fn close_command_menu(&mut self) {
        self.command_menu = None;
        self.overlay = Overlay::None;
    }

    /// The web address of the item of the pane in view and the notice for its copy, or the reason
    /// there is none.
    fn web_address(&self) -> Result<(String, String), &'static str> {
        let (url, notice, missing) = match self.pane {
            Pane::Projects => {
                let project = self.project().ok_or("No project selected")?;
                let notice = format!("Copied {} URL", project.path_with_namespace);
                (&project.web_url, notice, "Project has no URL")
            }
            Pane::Pipelines => {
                let pipeline = self.pipeline().ok_or("No pipeline selected")?;
                let notice = format!("Copied pipeline #{} URL", pipeline.id);
                (&pipeline.web_url, notice, "Pipeline has no URL")
            }
            Pane::Jobs => {
                let job = self.job().ok_or("No job selected")?;
                (
                    &job.web_url,
                    format!("Copied job {} URL", job.name),
                    "Job has no URL",
                )
            }
            Pane::MergeRequests => {
                let mr = (self.merge_requests)
                    .get(self.cursor(Pane::MergeRequests))
                    .ok_or("No merge request selected")?;
                let notice = format!("Copied !{} URL", mr.iid);
                (&mr.web_url, notice, "Merge request has no URL")
            }
        };
        if url.is_empty() {
            return Err(missing);
        }
        Ok((url.clone(), notice))
    }

    fn copy_web_address(&mut self) -> Vec<Cmd> {
        match self.web_address() {
            Ok((url, notice)) => self.copy(notice, url),
            Err(reason) => {
                self.notice = Some(reason.to_string());
                Vec::new()
            }
        }
    }

    fn copy_line(&mut self, line: String) -> Vec<Cmd> {
        self.copy(format!("Copied: {line}"), line)
    }

    fn copy(&mut self, notice: String, text: String) -> Vec<Cmd> {
        self.notice = Some(notice);
        vec![Cmd::Copy(text)]
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
        if self.focus == Focus::Detail && self.wanted_threads().is_some() {
            self.move_thread(delta);
            return Vec::new();
        }
        if self.focus == Focus::Detail && self.wanted_diff().is_some() {
            self.move_diff_cursor(delta);
            return Vec::new();
        }
        if self.focus == Focus::Detail && self.wanted_test_report().is_some() {
            self.scroll_test_report(delta);
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
            Pane::MergeRequests => {
                self.clear_threads();
                self.clear_diff();
                if self.wanted_threads().is_some() || self.wanted_diff().is_some() {
                    self.wait(Waiting::MergeRequest);
                }
                Vec::new()
            }
        }
    }

    /// The list starts again from its first row on the info tab, as in the Go release. While the
    /// project waits, the load at the end of the wait asks for the new state.
    fn switch_mr_state(&mut self, delta: usize) -> Vec<Cmd> {
        let index = MR_STATES.iter().position(|&s| s == self.mr_state);
        self.mr_state = MR_STATES[(index.unwrap_or(0) + delta) % MR_STATES.len()];
        self.merge_requests.clear();
        self.cursors[Pane::MergeRequests.index()] = 0;
        self.mr_tab = MrTab::Info;
        self.clear_threads();
        self.clear_diff();
        let project_waits = matches!(self.waiting, Some((Waiting::Project(_), _)));
        match self.selected() {
            Some(project) if !project_waits => vec![Cmd::LoadMergeRequests {
                project,
                state: self.mr_state,
            }],
            _ => Vec::new(),
        }
    }

    fn project_changed(&mut self) -> Vec<Cmd> {
        self.pipeline_page = 1;
        self.next_pipeline_page = None;
        self.pipelines.clear();
        self.clear_jobs();
        self.merge_requests.clear();
        self.clear_threads();
        self.clear_diff();
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
                Cmd::LoadMergeRequests {
                    project,
                    state: self.mr_state,
                },
            ],
            Waiting::Pipeline { project, pipeline } => vec![Cmd::LoadJobs { project, pipeline }],
            Waiting::Job { project, job } => vec![Cmd::LoadLog { project, job }],
            // `ask_threads`, `ask_diff` and `ask_diff_refs` ask in the update that ends the wait.
            Waiting::MergeRequest => Vec::new(),
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
        self.log_failed = None;
        self.clear_test_report();
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

/// The scroll after a move of `delta` lines, which stops when the last of `len` lines is at the
/// bottom of `height` rows.
fn scrolled(scroll: usize, delta: isize, len: usize, height: usize) -> usize {
    scroll
        .saturating_add_signed(delta)
        .min(len.saturating_sub(height))
}

fn row_bridge(rows: &[JobRow], row: usize) -> Option<&Bridge> {
    match rows.get(row) {
        Some(JobRow::Bridge { bridge, .. }) => Some(bridge),
        _ => None,
    }
}
