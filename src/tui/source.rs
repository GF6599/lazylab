//! Where the answers to the screen's commands come from: GitLab and the cache, or the sample data.

use std::sync::Arc;

use time::OffsetDateTime;
use tokio::sync::Semaphore;

use super::app::{Action, Cmd, Msg};
use super::demo;
use crate::gitlab::{Client, Error, MergeRequestState};
use crate::store::Store;

/// Pipelines and merge requests load in pages of this size.
const PAGE_SIZE: u8 = 20;
/// The Go release found that more status calls at once tripped GitLab's rate limit.
const STATUS_CALLS: usize = 5;

#[derive(Clone)]
pub enum Source {
    Demo,
    Live(Arc<Live>),
}

pub struct Live {
    client: Client,
    store: Option<Store>,
    status_calls: Semaphore,
}

impl Source {
    /// Without a store there is no cache, so every start loads the projects from GitLab.
    pub fn live(client: Client, store: Option<Store>) -> Source {
        Source::Live(Arc::new(Live {
            client,
            store,
            status_calls: Semaphore::new(STATUS_CALLS),
        }))
    }

    /// `None` for a command that answers nothing, such as a save.
    pub async fn answer(&self, cmd: Cmd) -> Option<Msg> {
        match self {
            Source::Demo => demo::answer(cmd, OffsetDateTime::now_utc()),
            Source::Live(live) => live.answer(cmd).await,
        }
    }
}

impl Live {
    async fn answer(&self, cmd: Cmd) -> Option<Msg> {
        let result = match cmd.clone() {
            Cmd::LoadCache => return Some(Msg::CacheLoaded(self.cached())),
            Cmd::LoadFavorites => return Some(Msg::FavoritesLoaded(self.favorites())),
            Cmd::SaveFavorites(ids) => {
                if let Some(store) = &self.store
                    && let Err(e) = store.save_favorites(&ids)
                {
                    tracing::warn!(error = %e, "save the favorites");
                }
                return None;
            }
            Cmd::SaveProjects { projects, total } => {
                if let Some(store) = &self.store
                    && let Err(e) = store.save_projects(&projects, total, OffsetDateTime::now_utc())
                {
                    tracing::warn!(error = %e, "save the project cache");
                }
                return None;
            }
            Cmd::LoadProjectPage {
                load,
                page,
                per_page,
            } => self
                .client
                .list_projects(page, per_page)
                .await
                .map(|page| Msg::ProjectPageLoaded { load, page }),
            Cmd::LoadPipelines { project, page } => self
                .client
                .list_pipelines(project, page, PAGE_SIZE)
                .await
                .map(|page| Msg::PipelinesLoaded { project, page }),
            Cmd::LoadStatus { project } => {
                // The semaphore is never closed, so the permit always comes.
                let _permit = self.status_calls.acquire().await.ok();
                self.client
                    .list_pipelines(project, 1, 1)
                    .await
                    .map(|page| Msg::StatusLoaded {
                        project,
                        status: page.items.first().map(|p| p.status),
                    })
            }
            Cmd::LoadJobs { project, pipeline } => {
                let jobs = self.client.list_pipeline_jobs(project, pipeline);
                let bridges = self.client.list_pipeline_bridges(project, pipeline);
                tokio::try_join!(jobs, bridges).map(|(jobs, bridges)| Msg::JobsLoaded {
                    pipeline,
                    jobs,
                    bridges,
                })
            }
            Cmd::Act(action) => self.act(action).await,
            Cmd::LoadLog { project, job } => self
                .client
                .job_log(project, job)
                .await
                .map(|log| Msg::LogLoaded { job, log }),
            Cmd::LoadChildJobs { project, pipeline } => self
                .client
                .list_pipeline_jobs(project, pipeline)
                .await
                .map(|jobs| Msg::ChildJobsLoaded { pipeline, jobs }),
            Cmd::LoadMergeRequests { project } => self
                .client
                .list_merge_requests(project, Some(MergeRequestState::Opened), 1, PAGE_SIZE)
                .await
                .map(|page| Msg::MergeRequestsLoaded {
                    project,
                    merge_requests: page.items,
                }),
        };
        Some(result.unwrap_or_else(|e: Error| {
            tracing::warn!(error = %e, "GitLab call failed");
            Msg::Failed {
                cmd,
                error: e.to_string(),
            }
        }))
    }

    async fn act(&self, action: Action) -> Result<Msg, Error> {
        let client = &self.client;
        let started = match &action {
            Action::RetryPipeline {
                project,
                pipeline,
                git_ref,
            } => Some(
                client
                    .retry_pipeline(*project, *pipeline, git_ref)
                    .await?
                    .id,
            ),
            Action::CancelPipeline { project, pipeline } => {
                client.cancel_pipeline(*project, *pipeline).await?;
                None
            }
            Action::RetryJob { project, job, .. } => {
                client.retry_job(*project, *job).await?;
                None
            }
            Action::CancelJob { project, job, .. } => {
                client.cancel_job(*project, *job).await?;
                None
            }
            Action::RunPipeline {
                project,
                git_ref,
                variables,
            } => Some(
                client
                    .create_pipeline(*project, git_ref, variables)
                    .await?
                    .id,
            ),
            Action::PlayJob {
                project,
                job,
                variables,
                ..
            } => {
                client.play_job(*project, *job, variables).await?;
                None
            }
        };
        Ok(Msg::Acted { action, started })
    }

    /// Favorites that cannot be read are none, so the screen still opens on every project.
    fn favorites(&self) -> Vec<u64> {
        let Some(store) = &self.store else {
            return Vec::new();
        };
        store.load_favorites().unwrap_or_else(|e| {
            tracing::warn!(error = %e, "read the favorites");
            Vec::new()
        })
    }

    /// A cache that cannot be read is a miss, so the projects load from GitLab instead.
    fn cached(&self) -> Option<crate::store::CachedProjects> {
        let store = self.store.as_ref()?;
        store
            .load_projects(OffsetDateTime::now_utc())
            .unwrap_or_else(|e| {
                tracing::warn!(error = %e, "read the project cache");
                None
            })
    }
}
