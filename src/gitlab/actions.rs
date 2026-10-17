//! Starting, retrying, canceling and playing pipelines and jobs.

use std::cmp::Reverse;
use std::fmt;

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde::ser::{SerializeStruct, Serializer};

use super::{Client, Error, Job, Pipeline, StatusCode, decode, refusal, rejection};

const NO_BODY: Option<&()> = None;
const MASK: &str = "[REDACTED]";

#[derive(Clone, PartialEq, Eq)]
pub struct Variable {
    pub key: String,
    pub value: String,
}

/// A value is often a secret, so a log line shows the key alone.
impl fmt::Debug for Variable {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Variable")
            .field("key", &self.key)
            .finish_non_exhaustive()
    }
}

impl Serialize for Variable {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        let mut variable = serializer.serialize_struct("Variable", 3)?;
        variable.serialize_field("key", &self.key)?;
        variable.serialize_field("value", &self.value)?;
        // A file variable writes its value to a file on the runner, which a terminal UI has no
        // way to offer.
        variable.serialize_field("variable_type", "env_var")?;
        variable.end()
    }
}

#[derive(Serialize)]
struct NewPipeline<'a> {
    #[serde(rename = "ref")]
    git_ref: &'a str,
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    variables: &'a [Variable],
}

#[derive(Serialize)]
struct Play<'a> {
    job_variables_attributes: &'a [Variable],
}

impl Client {
    pub async fn create_pipeline(
        &self,
        project: u64,
        git_ref: &str,
        variables: &[Variable],
    ) -> Result<Pipeline, Error> {
        let body = NewPipeline { git_ref, variables };
        let path = format!("projects/{project}/pipeline");
        self.act("create pipeline", &path, Some(&body))
            .await
            .map_err(|e| e.hiding(variables))
    }

    /// Retries the failed jobs, or starts a new pipeline on `git_ref` when none can be retried.
    pub async fn retry_pipeline(
        &self,
        project: u64,
        pipeline: u64,
        git_ref: &str,
    ) -> Result<Pipeline, Error> {
        const OPERATION: &str = "retry pipeline";
        let path = format!("projects/{project}/pipelines/{pipeline}/retry");
        let response = self.post(OPERATION, &path, NO_BODY).await?;
        // GitLab answers 400 when no job can be retried, as for a pipeline canceled before it ran.
        if response.status() != StatusCode::BAD_REQUEST {
            return decode(OPERATION, refusal(OPERATION, response).await?).await;
        }
        let retry = rejection(OPERATION, response).await;
        self.create_pipeline(project, git_ref, &[])
            .await
            .map_err(|create| Error {
                kind: create.kind,
                operation: OPERATION,
                detail: format!(
                    "{}, and {}: {}",
                    retry.detail, create.operation, create.detail
                ),
            })
    }

    pub async fn cancel_pipeline(&self, project: u64, pipeline: u64) -> Result<(), Error> {
        let path = format!("projects/{project}/pipelines/{pipeline}/cancel");
        self.act_unread("cancel pipeline", &path).await
    }

    /// The retry runs as a new job, which this returns.
    pub async fn retry_job(&self, project: u64, job: u64) -> Result<Job, Error> {
        let path = format!("projects/{project}/jobs/{job}/retry");
        self.act("retry job", &path, NO_BODY).await
    }

    pub async fn cancel_job(&self, project: u64, job: u64) -> Result<(), Error> {
        let path = format!("projects/{project}/jobs/{job}/cancel");
        self.act_unread("cancel job", &path).await
    }

    pub async fn play_job(
        &self,
        project: u64,
        job: u64,
        variables: &[Variable],
    ) -> Result<Job, Error> {
        let body = (!variables.is_empty()).then_some(Play {
            job_variables_attributes: variables,
        });
        let path = format!("projects/{project}/jobs/{job}/play");
        self.act("play job", &path, body.as_ref())
            .await
            .map_err(|e| e.hiding(variables))
    }

    async fn act<T: DeserializeOwned>(
        &self,
        operation: &'static str,
        path: &str,
        body: Option<&impl Serialize>,
    ) -> Result<T, Error> {
        let response = self.post(operation, path, body).await?;
        decode(operation, refusal(operation, response).await?).await
    }

    async fn act_unread(&self, operation: &'static str, path: &str) -> Result<(), Error> {
        let response = self.post(operation, path, NO_BODY).await?;
        refusal(operation, response).await.map(drop)
    }
}

impl Error {
    /// GitLab can quote a value back in its reason, as text or escaped inside a JSON object.
    fn hiding(mut self, variables: &[Variable]) -> Error {
        let mut values: Vec<String> = variables
            .iter()
            .filter(|variable| !variable.value.is_empty())
            .flat_map(|variable| [variable.value.clone(), escaped(&variable.value)])
            .collect();
        // A value that holds another is masked whole before the shorter one can split it.
        values.sort_by_key(|value| Reverse(value.len()));
        for value in values {
            self.detail = self.detail.replace(&value, MASK);
        }
        self
    }
}

fn escaped(value: &str) -> String {
    let quoted = serde_json::to_string(value).expect("a string always serializes");
    quoted[1..quoted.len() - 1].to_string()
}
