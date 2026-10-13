//! Pipelines, their stages, jobs and bridges, and job logs.

use serde::{Deserialize, Deserializer};
use time::OffsetDateTime;

use super::{Client, Error, ErrorKind, Page};

/// A larger log stops the read, so a runaway job cannot exhaust memory.
pub const MAX_JOB_LOG_BYTES: usize = 10 * 1024 * 1024;
const UNKNOWN_STAGE: &str = "(unknown stage)";

impl Client {
    pub async fn list_pipelines(
        &self,
        project: u64,
        page: u32,
        per_page: u8,
    ) -> Result<Page<Pipeline>, Error> {
        let query = [
            ("order_by", "updated_at".to_string()),
            ("sort", "desc".into()),
            ("page", page.to_string()),
            ("per_page", per_page.to_string()),
        ];
        let path = format!("projects/{project}/pipelines");
        self.get_page("list pipelines", &path, &query, page).await
    }

    /// One pipeline in full, with the start time, duration and user that a list leaves out.
    pub async fn get_pipeline(&self, project: u64, pipeline: u64) -> Result<Pipeline, Error> {
        let path = format!("projects/{project}/pipelines/{pipeline}");
        self.get_json("get pipeline", &path, &[]).await
    }

    pub async fn latest_pipeline(
        &self,
        project: u64,
        git_ref: Option<&str>,
    ) -> Result<Option<LatestPipeline>, Error> {
        let mut query = vec![
            ("order_by", "updated_at".to_string()),
            ("sort", "desc".into()),
            ("page", "1".into()),
            ("per_page", "1".into()),
        ];
        if let Some(git_ref) = git_ref {
            query.push(("ref", git_ref.into()));
        }
        let path = format!("projects/{project}/pipelines");
        let page: Page<Pipeline> = self.get_page("list pipelines", &path, &query, 1).await?;
        let Some(pipeline) = page.items.into_iter().next() else {
            return Ok(None);
        };
        let stages = self.pipeline_stages(project, pipeline.id).await?;
        Ok(Some(LatestPipeline { pipeline, stages }))
    }

    /// The stages in the order their first job was created, each with its most urgent status.
    pub async fn pipeline_stages(&self, project: u64, pipeline: u64) -> Result<Vec<Stage>, Error> {
        let jobs = self.list_pipeline_jobs(project, pipeline).await?;
        let mut stages: Vec<Stage> = Vec::new();
        for job in jobs {
            let name = match job.stage.trim() {
                "" => UNKNOWN_STAGE,
                name => name,
            };
            match stages.iter_mut().find(|stage| stage.name == name) {
                Some(stage) if job.status.rank() < stage.status.rank() => stage.status = job.status,
                Some(_) => {}
                None => stages.push(Stage {
                    name: name.to_string(),
                    status: job.status,
                }),
            }
        }
        Ok(stages)
    }

    /// GitLab lists bridges apart from jobs, so `list_pipeline_bridges` reads them.
    pub async fn list_pipeline_jobs(&self, project: u64, pipeline: u64) -> Result<Vec<Job>, Error> {
        let path = format!("projects/{project}/pipelines/{pipeline}/jobs");
        let mut jobs: Vec<Job> = self.get_all("list pipeline jobs", &path).await?;
        jobs.sort_by_key(|job| job.id);
        Ok(jobs)
    }

    pub async fn list_pipeline_bridges(
        &self,
        project: u64,
        pipeline: u64,
    ) -> Result<Vec<Bridge>, Error> {
        let path = format!("projects/{project}/pipelines/{pipeline}/bridges");
        let mut bridges: Vec<Bridge> = self.get_all("list pipeline bridges", &path).await?;
        bridges.sort_by_key(|bridge| bridge.id);
        Ok(bridges)
    }

    /// The log a job has written so far.
    pub async fn job_log(&self, project: u64, job: u64) -> Result<String, Error> {
        const OPERATION: &str = "get job log";
        let path = format!("projects/{project}/jobs/{job}/trace");
        let mut response = self.get(OPERATION, &path, &[]).await?;
        let mut log = Vec::new();
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|e| Error::transport(OPERATION, e))?
        {
            if log.len() + chunk.len() > MAX_JOB_LOG_BYTES {
                return Err(Error {
                    kind: ErrorKind::TooLarge,
                    operation: OPERATION,
                    detail: format!("the log is larger than {MAX_JOB_LOG_BYTES} bytes"),
                });
            }
            log.extend_from_slice(&chunk);
        }
        Ok(String::from_utf8_lossy(&log).into_owned())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Status {
    Created,
    WaitingForResource,
    WaitingForCallback,
    Preparing,
    Pending,
    Scheduled,
    Running,
    Success,
    Failed,
    Canceled,
    Canceling,
    Skipped,
    Manual,
    Blocked,
    #[serde(other)]
    Unknown,
}

impl Status {
    /// Lower is more urgent. A group of jobs shows the most urgent status among them.
    pub fn rank(self) -> u8 {
        match self {
            Status::Failed => 0,
            Status::Canceled => 1,
            Status::Manual | Status::Blocked => 2,
            Status::Running | Status::Preparing => 3,
            Status::Pending
            | Status::WaitingForResource
            | Status::WaitingForCallback
            | Status::Scheduled => 4,
            Status::Created => 5,
            Status::Success => 6,
            Status::Skipped => 7,
            // The Go release ranked these last, as a status it did not know.
            Status::Canceling | Status::Unknown => 8,
        }
    }
}

/// GitLab fills `started_at`, `duration` and `user` only on a single read.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Pipeline {
    pub id: u64,
    pub status: Status,
    #[serde(rename = "ref")]
    pub git_ref: String,
    #[serde(default)]
    pub sha: String,
    #[serde(default)]
    pub web_url: String,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub created_at: Option<OffsetDateTime>,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub updated_at: Option<OffsetDateTime>,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub started_at: Option<OffsetDateTime>,
    /// Seconds.
    #[serde(default)]
    pub duration: Option<f64>,
    #[serde(default, deserialize_with = "coverage")]
    pub coverage: Option<f64>,
    #[serde(default)]
    pub user: Option<User>,
}

impl Pipeline {
    /// The last change, which is the creation until GitLab first updates the pipeline.
    pub fn updated(&self) -> Option<OffsetDateTime> {
        self.updated_at.or(self.created_at)
    }
}

/// GitLab sends the coverage percentage as text.
fn coverage<'de, D: Deserializer<'de>>(deserializer: D) -> Result<Option<f64>, D::Error> {
    let text: Option<String> = Option::deserialize(deserializer)?;
    Ok(text.and_then(|t| t.parse().ok()))
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct User {
    pub name: String,
}

#[derive(Debug, Clone, PartialEq)]
pub struct LatestPipeline {
    pub pipeline: Pipeline,
    pub stages: Vec<Stage>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Stage {
    pub name: String,
    pub status: Status,
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Job {
    pub id: u64,
    pub name: String,
    pub stage: String,
    pub status: Status,
    #[serde(default)]
    pub web_url: String,
    /// Seconds.
    #[serde(default)]
    pub duration: Option<f64>,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub started_at: Option<OffsetDateTime>,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub finished_at: Option<OffsetDateTime>,
    #[serde(default)]
    pub failure_reason: Option<String>,
    #[serde(default)]
    pub artifacts: Vec<Artifact>,
    #[serde(default, with = "time::serde::rfc3339::option")]
    pub artifacts_expire_at: Option<OffsetDateTime>,
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Artifact {
    pub file_type: String,
    pub filename: String,
    #[serde(default)]
    pub size: u64,
}

/// A job that triggers a downstream pipeline.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Bridge {
    pub id: u64,
    pub name: String,
    pub stage: String,
    pub status: Status,
    #[serde(rename = "ref")]
    pub git_ref: String,
    /// Seconds.
    #[serde(default)]
    pub duration: Option<f64>,
    /// `None` until the bridge triggers, or when its project is not visible to the token.
    #[serde(default)]
    pub downstream_pipeline: Option<Downstream>,
}

#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Downstream {
    pub id: u64,
    /// The project the downstream pipeline runs in, which can differ from the bridge's own.
    /// `None` when GitLab leaves it out, and then the bridge's project applies.
    #[serde(default)]
    pub project_id: Option<u64>,
    pub status: Status,
    #[serde(default)]
    pub web_url: String,
}
