//! The run and play forms: a ref for a new pipeline, and CI variables as rows of a key and a value.
//! The comment form: the text of a comment on a merge request, in one or more lines.
//! The merge request form: a source branch, a target branch and a title.

use std::fmt;

use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};

use super::app::{Action, Cmd};
use crate::gitlab::{DiffLine, Variable};

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Purpose {
    Run {
        project: u64,
    },
    Play {
        project: u64,
        job: u64,
        name: String,
    },
    Comment {
        project: u64,
        merge_request: u64,
        discussion: Option<String>,
        line: Option<DiffLine>,
    },
    MergeRequest {
        project: u64,
    },
}

#[derive(Clone, PartialEq, Eq)]
pub struct Form {
    pub purpose: Purpose,
    /// `None` for a play, which runs where its job already is, for a comment and for a merge
    /// request.
    pub git_ref: Option<String>,
    /// `None` for every form but the comment form.
    pub body: Option<String>,
    /// `None` for every form but the merge request form.
    pub merge_request: Option<MergeRequestFields>,
    pub variables: Vec<(String, String)>,
    /// The field being typed: the ref, the comment or the merge request fields first when there
    /// are any, then each row's key and value.
    pub field: usize,
}

/// A value is often a secret, so a debug print shows the keys alone.
impl fmt::Debug for Form {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let keys: Vec<&str> = self.variables.iter().map(|(key, _)| key.as_str()).collect();
        f.debug_struct("Form")
            .field("purpose", &self.purpose)
            .field("git_ref", &self.git_ref)
            .field("merge_request", &self.merge_request)
            .field("keys", &keys)
            .field("field", &self.field)
            .finish_non_exhaustive()
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct MergeRequestFields {
    pub source: String,
    pub target: String,
    pub title: String,
    /// The branches GitLab matched for the branch field being typed. `None` until it answers.
    pub matches: Option<BranchMatches>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct BranchMatches {
    pub search: String,
    pub branches: Vec<String>,
    /// The picked branch, which enter puts in the field.
    pub row: usize,
}

pub enum Outcome {
    Open,
    Send(Cmd),
    Closed,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Field {
    Ref,
    Body,
    Source,
    Target,
    Title,
    Key(usize),
    Value(usize),
}

impl Form {
    pub fn run(project: u64, git_ref: String) -> Form {
        Form {
            purpose: Purpose::Run { project },
            git_ref: Some(git_ref),
            body: None,
            merge_request: None,
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn play(project: u64, job: u64, name: String) -> Form {
        Form {
            purpose: Purpose::Play { project, job, name },
            git_ref: None,
            body: None,
            merge_request: None,
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn comment(
        project: u64,
        merge_request: u64,
        discussion: Option<String>,
        line: Option<DiffLine>,
        body: String,
    ) -> Form {
        Form {
            purpose: Purpose::Comment {
                project,
                merge_request,
                discussion,
                line,
            },
            git_ref: None,
            body: Some(body),
            merge_request: None,
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn merge_request(project: u64, fields: MergeRequestFields) -> Form {
        Form {
            purpose: Purpose::MergeRequest { project },
            git_ref: None,
            body: None,
            merge_request: Some(fields),
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn title(&self) -> String {
        match &self.purpose {
            Purpose::Run { .. } => "run pipeline".to_string(),
            Purpose::Play { name, .. } => format!("play {name}"),
            Purpose::Comment {
                discussion: None, ..
            } => "new comment".to_string(),
            Purpose::Comment { .. } => "reply".to_string(),
            Purpose::MergeRequest { .. } => "new merge request".to_string(),
        }
    }

    pub fn takes_variables(&self) -> bool {
        matches!(self.purpose, Purpose::Run { .. } | Purpose::Play { .. })
    }

    /// The fields before the variable rows.
    fn leading(&self) -> &'static [Field] {
        if self.merge_request.is_some() {
            &[Field::Source, Field::Target, Field::Title]
        } else if self.body.is_some() {
            &[Field::Body]
        } else if self.git_ref.is_some() {
            &[Field::Ref]
        } else {
            &[]
        }
    }

    fn fields(&self) -> usize {
        self.leading().len() + 2 * self.variables.len()
    }

    pub fn current(&self) -> Option<Field> {
        if self.field >= self.fields() {
            return None;
        }
        let leading = self.leading();
        if let Some(&field) = leading.get(self.field) {
            return Some(field);
        }
        let row_field = self.field - leading.len();
        let row = row_field / 2;
        Some(if row_field.is_multiple_of(2) {
            Field::Key(row)
        } else {
            Field::Value(row)
        })
    }

    /// The project and the term of the branch field being typed.
    pub fn branch_search(&self) -> Option<(u64, &str)> {
        let Purpose::MergeRequest { project } = self.purpose else {
            return None;
        };
        let fields = self.merge_request.as_ref()?;
        match self.current()? {
            Field::Source => Some((project, &fields.source)),
            Field::Target => Some((project, &fields.target)),
            _ => None,
        }
    }

    /// Matches for a term the branch field no longer holds are dropped.
    pub fn show_branches(&mut self, project: u64, search: &str, branches: Vec<String>) {
        if self.branch_search() != Some((project, search)) {
            return;
        }
        if let Some(fields) = self.merge_request.as_mut() {
            fields.matches = Some(BranchMatches {
                search: search.to_string(),
                branches,
                row: 0,
            });
        }
    }

    fn branch_matches(&mut self) -> Option<&mut BranchMatches> {
        self.merge_request.as_mut()?.matches.as_mut()
    }

    fn take_branch(&mut self) {
        let picked = (self.branch_matches()).and_then(|m| m.branches.get(m.row).cloned());
        if let (Some(picked), Some(Field::Source | Field::Target)) = (picked, self.current())
            && let Some(text) = self.text()
        {
            *text = picked;
        }
    }

    fn text(&mut self) -> Option<&mut String> {
        match self.current()? {
            Field::Ref => self.git_ref.as_mut(),
            Field::Body => self.body.as_mut(),
            Field::Source => self.merge_request.as_mut().map(|m| &mut m.source),
            Field::Target => self.merge_request.as_mut().map(|m| &mut m.target),
            Field::Title => self.merge_request.as_mut().map(|m| &mut m.title),
            Field::Key(row) => self.variables.get_mut(row).map(|(key, _)| key),
            Field::Value(row) => self.variables.get_mut(row).map(|(_, value)| value),
        }
    }

    /// A field of one line drops each line break, so a paste cannot send the form.
    pub fn paste(&mut self, text: &str) {
        let text = if self.current() == Some(Field::Body) {
            text.replace("\r\n", "\n").replace('\r', "\n")
        } else {
            text.replace(['\r', '\n'], "")
        };
        if let Some(field) = self.text() {
            field.push_str(&text);
        }
    }

    pub fn key(&mut self, key: KeyEvent) -> Outcome {
        let fields = self.fields();
        let field = self.field;
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            match key.code {
                KeyCode::Char('s') if !self.takes_variables() => return self.send(),
                KeyCode::Char('n') if self.takes_variables() => {
                    self.variables.push((String::new(), String::new()));
                    self.field = self.fields() - 2;
                }
                KeyCode::Char('d') => {
                    if let Some(Field::Key(row) | Field::Value(row)) = self.current() {
                        self.variables.remove(row);
                        self.field = self.field.min(self.fields().saturating_sub(1));
                    }
                }
                _ => {}
            }
            return Outcome::Open;
        }
        match key.code {
            KeyCode::Enter if self.takes_variables() => return self.send(),
            KeyCode::Enter if self.current() == Some(Field::Title) => return self.send(),
            KeyCode::Enter if self.merge_request.is_some() => {
                self.take_branch();
                self.field = (self.field + 1) % fields;
            }
            KeyCode::Down => {
                if let Some(m) = self.branch_matches() {
                    m.row = (m.row + 1).min(m.branches.len().saturating_sub(1));
                }
            }
            KeyCode::Up => {
                if let Some(m) = self.branch_matches() {
                    m.row = m.row.saturating_sub(1);
                }
            }
            KeyCode::Enter => {
                if let Some(text) = self.text() {
                    text.push('\n');
                }
            }
            KeyCode::Esc => return Outcome::Closed,
            KeyCode::Tab if fields > 0 => self.field = (self.field + 1) % fields,
            KeyCode::BackTab if fields > 0 => self.field = (self.field + fields - 1) % fields,
            KeyCode::Backspace => {
                if let Some(text) = self.text() {
                    text.pop();
                }
            }
            KeyCode::Char(c) => {
                if let Some(text) = self.text() {
                    text.push(c);
                }
            }
            _ => {}
        }
        if self.field != field
            && let Some(fields) = self.merge_request.as_mut()
        {
            fields.matches = None;
        }
        Outcome::Open
    }

    /// A row without a key is left out, as GitLab's own form leaves an empty row out.
    fn variables(&self) -> Vec<Variable> {
        (self.variables.iter())
            .filter(|(key, _)| !key.is_empty())
            .map(|(key, value)| Variable {
                key: key.clone(),
                value: value.clone(),
            })
            .collect()
    }

    /// A blank comment stays open, as the Go release refuses to send one.
    fn send(&self) -> Outcome {
        let action = match &self.purpose {
            Purpose::Run { project } => Action::RunPipeline {
                project: *project,
                git_ref: self.git_ref.clone().unwrap_or_default(),
                variables: self.variables(),
            },
            Purpose::Play { project, job, name } => Action::PlayJob {
                project: *project,
                job: *job,
                name: name.clone(),
                variables: self.variables(),
            },
            Purpose::Comment {
                project,
                merge_request,
                discussion,
                line,
            } => {
                let body = self.body.as_deref().unwrap_or_default().trim();
                if body.is_empty() {
                    return Outcome::Open;
                }
                return Outcome::Send(Cmd::Comment {
                    project: *project,
                    merge_request: *merge_request,
                    discussion: discussion.clone(),
                    line: line.clone(),
                    body: body.to_string(),
                });
            }
            Purpose::MergeRequest { project } => {
                let fields = self.merge_request.clone().unwrap_or_default();
                return Outcome::Send(Cmd::CreateMergeRequest {
                    project: *project,
                    source: fields.source.trim().to_string(),
                    target: fields.target.trim().to_string(),
                    title: fields.title.trim().to_string(),
                });
            }
        };
        Outcome::Send(Cmd::Act(action))
    }
}
