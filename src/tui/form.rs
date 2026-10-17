//! The run and play forms: a ref for a new pipeline, and CI variables as rows of a key and a value.

use std::fmt;

use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};

use super::app::Action;
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
}

#[derive(Clone, PartialEq, Eq)]
pub struct Form {
    pub purpose: Purpose,
    /// `None` for a play, which runs where its job already is.
    pub git_ref: Option<String>,
    pub variables: Vec<(String, String)>,
    /// The field being typed: the ref first when there is one, then each row's key and value.
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
    Send(Action),
    Closed,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Field {
    Ref,
    Key(usize),
    Value(usize),
}

impl Form {
    pub fn run(project: u64, git_ref: String) -> Form {
        Form {
            purpose: Purpose::Run { project },
            git_ref: Some(git_ref),
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn play(project: u64, job: u64, name: String) -> Form {
        Form {
            purpose: Purpose::Play { project, job, name },
            git_ref: None,
            variables: Vec::new(),
            field: 0,
        }
    }

    pub fn title(&self) -> String {
        match &self.purpose {
            Purpose::Run { .. } => "run pipeline".to_string(),
            Purpose::Play { name, .. } => format!("play {name}"),
        }
    }

    fn first_row_field(&self) -> usize {
        usize::from(self.git_ref.is_some())
    }

    fn fields(&self) -> usize {
        self.first_row_field() + 2 * self.variables.len()
    }

    pub fn current(&self) -> Option<Field> {
        if self.field >= self.fields() {
            return None;
        }
        let Some(row_field) = self.field.checked_sub(self.first_row_field()) else {
            return Some(Field::Ref);
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
            Field::Key(row) => self.variables.get_mut(row).map(|(key, _)| key),
            Field::Value(row) => self.variables.get_mut(row).map(|(_, value)| value),
        }
    }

    pub fn paste(&mut self, text: &str) {
        if let Some(field) = self.text() {
            field.push_str(text);
        }
    }

    pub fn key(&mut self, key: KeyEvent) -> Outcome {
        let fields = self.fields();
        if key.modifiers.contains(KeyModifiers::CONTROL) {
            match key.code {
                KeyCode::Char('n') => {
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
            KeyCode::Enter => return Outcome::Send(self.action()),
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
    fn action(&self) -> Action {
        let variables: Vec<Variable> = (self.variables.iter())
            .filter(|(key, _)| !key.is_empty())
            .map(|(key, value)| Variable {
                key: key.clone(),
                value: value.clone(),
            })
            .collect();
        match &self.purpose {
            Purpose::Run { project } => Action::RunPipeline {
                project: *project,
                git_ref: self.git_ref.clone().unwrap_or_default(),
                variables,
            },
            Purpose::Play { project, job, name } => Action::PlayJob {
                project: *project,
                job: *job,
                name: name.clone(),
                variables,
            },
        }
    }
}
