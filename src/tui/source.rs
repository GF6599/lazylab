//! Where the answers to the screen's commands come from: GitLab and the cache, or the sample data.

use std::sync::Arc;

use time::OffsetDateTime;

use super::app::{Cmd, Msg};
use super::demo;
use crate::gitlab::{Client, Error, MergeRequestState};
use crate::store::Store;

/// Pipelines and merge requests load one page of this size until their panes page through more.
const FIRST_PAGE: u8 = 20;

#[derive(Clone)]
pub enum Source {
    Demo,
    Live(Arc<Live>),
}

pub struct Live {
    client: Client,
    store: Option<Store>,
}

impl Source {
    /// Without a store there is no cache, so every start loads the projects from GitLab.
    pub fn live(client: Client, store: Option<Store>) -> Source {
        Source::Live(Arc::new(Live { client, store }))
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
            Cmd::LoadPipelines { project } => self
                .client
                .list_pipelines(project, 1, FIRST_PAGE)
                .await
                .map(|page| Msg::PipelinesLoaded {
                    project,
                    pipelines: page.items,
                }),
            Cmd::LoadJobs { project, pipeline } => self
                .client
                .list_pipeline_jobs(project, pipeline)
                .await
                .map(|jobs| Msg::JobsLoaded { pipeline, jobs }),
            Cmd::LoadMergeRequests { project } => self
                .client
                .list_merge_requests(project, Some(MergeRequestState::Opened), 1, FIRST_PAGE)
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
