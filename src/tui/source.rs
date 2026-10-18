//! Where the answers to the screen's commands come from: GitLab and the cache, or the sample data.

use std::path::PathBuf;
use std::sync::Arc;

use time::OffsetDateTime;
use tokio::sync::Semaphore;
use url::Url;

use super::app::{Action, Cmd, Msg};
use super::demo;
use crate::checkout::Checkouts;
use crate::config::Mode;
use crate::explorer::Browse;
use crate::gitlab::{Client, Error, NewMergeRequest};
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
    checkouts: Option<Checkouts>,
    status_calls: Semaphore,
}

impl Source {
    /// `dir` is the folder that holds the store of each host and the checkouts. Only a live
    /// session checks out, because a checkout needs the token.
    pub fn new(host: &Url, mode: &Mode, dir: Option<PathBuf>) -> Result<Source, Error> {
        match mode {
            Mode::Demo => Ok(Source::Demo),
            Mode::Live(token) => {
                let client = Client::new(host, token)?;
                let checkouts = (dir.as_deref()).map(|d| Checkouts::new(d, host, token.clone()));
                let store = dir.map(|dir| Store::new(dir, host));
                Ok(Source::Live(Arc::new(Live::new(client, store, checkouts))))
            }
        }
    }

    /// Without a store there is no cache, so every start loads the projects from GitLab. Only
    /// `new` gives a source a folder to check a project out into.
    pub fn live(client: Client, store: Option<Store>) -> Source {
        Source::Live(Arc::new(Live::new(client, store, None)))
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
    fn new(client: Client, store: Option<Store>, checkouts: Option<Checkouts>) -> Live {
        Live {
            client,
            store,
            checkouts,
            status_calls: Semaphore::new(STATUS_CALLS),
        }
    }

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
            // The event loop writes a copy to the terminal, so none reaches a source.
            Cmd::Copy(_) => return None,
            Cmd::CheckOut { .. } => return Some(self.check_out(cmd).await),
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
            Cmd::LoadMergeRequests { project, state } => self
                .client
                .list_merge_requests(project, Some(state), 1, PAGE_SIZE)
                .await
                .map(|page| Msg::MergeRequestsLoaded {
                    project,
                    state,
                    merge_requests: page.items,
                }),
            Cmd::LoadThreads {
                project,
                merge_request,
            } => self
                .client
                .list_merge_request_discussions(project, merge_request)
                .await
                .map(|discussions| Msg::ThreadsLoaded {
                    project,
                    merge_request,
                    discussions,
                }),
            Cmd::LoadDiff {
                project,
                merge_request,
            } => self
                .client
                .list_merge_request_diffs(project, merge_request)
                .await
                .map(|files| Msg::DiffLoaded {
                    project,
                    merge_request,
                    files,
                }),
            Cmd::LoadDiffRefs {
                project,
                merge_request,
            } => self
                .client
                .merge_request_diff_refs(project, merge_request)
                .await
                .map(|refs| Msg::DiffRefsLoaded {
                    project,
                    merge_request,
                    refs,
                }),
            Cmd::LoadTestReport { project, pipeline } => self
                .client
                .pipeline_test_report(project, pipeline)
                .await
                .map(|report| Msg::TestReportLoaded {
                    project,
                    pipeline,
                    report,
                }),
            Cmd::ResolveThread {
                project,
                merge_request,
                discussion,
                resolved,
            } => self
                .client
                .resolve_merge_request_discussion(project, merge_request, &discussion, resolved)
                .await
                .map(|()| Msg::ThreadResolved {
                    project,
                    merge_request,
                    resolved,
                }),
            Cmd::LoadBranches { project, search } => self
                .client
                .list_branches(project, Some(search.as_str()).filter(|s| !s.is_empty()))
                .await
                .map(|branches| Msg::BranchesLoaded {
                    project,
                    search,
                    branches,
                }),
            Cmd::CreateMergeRequest {
                project,
                source,
                target,
                title,
            } => {
                let new = NewMergeRequest {
                    title: &title,
                    source_branch: &source,
                    target_branch: &target,
                    description: "",
                };
                (self.client)
                    .create_merge_request(project, &new)
                    .await
                    .map(|merge_request| Msg::MergeRequestCreated {
                        project,
                        merge_request,
                    })
            }
            Cmd::Comment {
                project,
                merge_request,
                discussion,
                line,
                body,
            } => match &discussion {
                Some(thread) => {
                    (self.client)
                        .add_merge_request_discussion_note(project, merge_request, thread, &body)
                        .await
                }
                None => {
                    (self.client)
                        .create_merge_request_discussion(
                            project,
                            merge_request,
                            &body,
                            line.as_ref(),
                        )
                        .await
                }
            }
            .map(|()| Msg::Commented {
                project,
                merge_request,
                reply: discussion.is_some(),
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

    /// git blocks, so it runs off the event loop's thread.
    async fn check_out(&self, cmd: Cmd) -> Msg {
        let (
            Cmd::CheckOut {
                project,
                web_url,
                revision,
            },
            Some(checkouts),
        ) = (cmd.clone(), self.checkouts.clone())
        else {
            return Msg::Failed {
                cmd,
                error: "no cache folder to check out into".into(),
            };
        };
        let git_ref = revision.name().to_string();
        let done = tokio::task::spawn_blocking(move || checkouts.check_out(&project, &revision));
        let error = match done.await {
            Ok(Ok(dir)) => {
                return Msg::CheckedOut(Browse {
                    dir,
                    project_url: web_url,
                    git_ref,
                });
            }
            Ok(Err(e)) => e.to_string(),
            Err(e) => format!("the checkout stopped: {e}"),
        };
        tracing::warn!(error = %error, "check out a project");
        Msg::Failed { cmd, error }
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
