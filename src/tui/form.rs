//! The run and play forms: a ref for a new pipeline, and CI variables as rows of a key and a value.
//! The comment form: the text of a comment on a merge request, in one or more lines.

use std::fmt;

use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};

use super::app::{Action, Cmd};
use crate::gitlab::Variable;

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
    },
}

#[derive(Clone, PartialEq, Eq)]
pub struct Form {
    pub purpose: Purpose,
    /// `None` for a play, which runs where its job already is, and for a comment.
    pub git_ref: Option<String>,
    /// `None` for a run or a play, which take variables instead.
    pub body: Option<String>,
    pub variables: Vec<(String, String)>,
    /// The field being typed: the ref or the comment first when there is one, then each row's key
    /// and value.
    pub field: usize,
}

/// A value is often a secret, so a debug print shows the keys alone.
impl fmt::Debug for Form {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let keys: Vec<&str> = self.variables.iter().map(|(key, _)| key.as_str()).collect();
        f.debug_struct("Form")
            .field("purpose", &self.purpose)
            .field("git_ref", &self.git_ref)
            .field("keys", &keys)
            .field("field", &self.field)
            .finish_non_exhaustive()
    }
}

pub enum Outcome {
    Open,
    Send(Cmd),
    Closed,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Field {
    Ref,
    Body,
    Key(usize),
    Value(usize),
}

impl Form {
    pub fn run(project: u64, git_ref: String) -> Form {
        Form {
            purpose: Purpose::Run { project },
            git_ref: Some(git_ref),
            body: None,
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn play(project: u64, job: u64, name: String) -> Form {
        Form {
            purpose: Purpose::Play { project, job, name },
            git_ref: None,
            body: None,
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn comment(project: u64, merge_request: u64, discussion: Option<String>) -> Form {
        Form {
            purpose: Purpose::Comment {
                project,
                merge_request,
                discussion,
            },
            git_ref: None,
            body: Some(String::new()),
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
        }
    }

    pub fn takes_variables(&self) -> bool {
        !matches!(self.purpose, Purpose::Comment { .. })
    }

    fn first_row_field(&self) -> usize {
        usize::from(self.git_ref.is_some() || self.body.is_some())
    }

    fn fields(&self) -> usize {
        self.first_row_field() + 2 * self.variables.len()
    }

    pub fn current(&self) -> Option<Field> {
        if self.field >= self.fields() {
            return None;
        }
        let Some(row_field) = self.field.checked_sub(self.first_row_field()) else {
            return Some(if self.body.is_some() {
                Field::Body
            } else {
                Field::Ref
            });
        };
        let row = row_field / 2;
        Some(if row_field % 2 == 0 {
            Field::Key(row)
        } else {
            Field::Value(row)
        })
    }

    fn text(&mut self) -> Option<&mut String> {
        match self.current()? {
            Field::Ref => self.git_ref.as_mut(),
            Field::Body => self.body.as_mut(),
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
            } => {
                let body = self.body.as_deref().unwrap_or_default().trim();
                if body.is_empty() {
                    return Outcome::Open;
                }
                return Outcome::Send(Cmd::Comment {
                    project: *project,
                    merge_request: *merge_request,
                    discussion: discussion.clone(),
                    body: body.to_string(),
                });
            }
        };
        Outcome::Send(Cmd::Act(action))
    }
}
