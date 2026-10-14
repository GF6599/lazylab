//! The rows of the jobs pane: each stage's jobs and bridges together in stage order, matrix jobs
//! folded under their shared name, and a bridge's child pipeline under the bridge once it opens.

use std::collections::{HashMap, HashSet};

use crate::gitlab::{Bridge, Job, Status};

/// What a row stands for, which stays the same when a refresh rebuilds the rows.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum RowKey {
    Job(u64),
    Group { stage: String, name: String },
    Bridge(u64),
    Waiting(u64),
}

#[derive(Clone, Debug, PartialEq)]
pub enum JobRow {
    /// `project` is where the job runs, which for a child pipeline is the downstream project.
    Job {
        job: Job,
        project: u64,
        depth: u8,
    },
    /// Matrix jobs that share a name, with the most urgent status among them.
    Group {
        stage: String,
        name: String,
        status: Status,
        count: usize,
        open: bool,
    },
    Bridge {
        bridge: Bridge,
        open: bool,
    },
    /// An open bridge whose child pipeline has not answered yet.
    Waiting {
        pipeline: u64,
    },
}

impl JobRow {
    pub fn key(&self) -> RowKey {
        match self {
            JobRow::Job { job, .. } => RowKey::Job(job.id),
            JobRow::Group { stage, name, .. } => RowKey::Group {
                stage: stage.clone(),
                name: name.clone(),
            },
            JobRow::Bridge { bridge, .. } => RowKey::Bridge(bridge.id),
            JobRow::Waiting { pipeline } => RowKey::Waiting(*pipeline),
        }
    }

    /// The stage of a row at the top level, and `None` for a row nested under another.
    pub fn stage(&self) -> Option<&str> {
        match self {
            JobRow::Job { job, depth: 0, .. } => Some(&job.stage),
            JobRow::Group { stage, .. } => Some(stage),
            JobRow::Bridge { bridge, .. } => Some(&bridge.stage),
            JobRow::Job { .. } | JobRow::Waiting { .. } => None,
        }
    }
}

/// The name before a matrix job's variables, `unit` for `unit: [go, 1.21]`. It follows the Go
/// release's `^(.+?):\s*\[(.+)\]$`, so the first colon that leaves a bracketed list ends it.
pub fn matrix_name(name: &str) -> Option<&str> {
    name.match_indices(':').find_map(|(at, _)| {
        let variables = name[at + 1..].trim_start();
        let bracketed =
            variables.len() > 2 && variables.starts_with('[') && variables.ends_with(']');
        (at > 0 && bracketed).then(|| name[..at].trim())
    })
}

/// A stage sits where its first job or bridge was created, as GitLab orders a pipeline's stages.
fn stages<'a>(jobs: &'a [Job], bridges: &'a [Bridge]) -> Vec<&'a str> {
    let mut firsts: Vec<(u64, &str)> = (jobs.iter().map(|j| (j.id, j.stage.as_str())))
        .chain(bridges.iter().map(|b| (b.id, b.stage.as_str())))
        .collect();
    firsts.sort_by_key(|&(id, _)| id);
    let mut stages = Vec::new();
    for (_, stage) in firsts {
        if !stages.contains(&stage) {
            stages.push(stage);
        }
    }
    stages
}

pub fn rows(
    project: u64,
    jobs: &[Job],
    bridges: &[Bridge],
    children: &HashMap<u64, Vec<Job>>,
    open: &HashSet<RowKey>,
) -> Vec<JobRow> {
    let mut rows = Vec::new();
    for stage in stages(jobs, bridges) {
        let mut groups: Vec<(&str, Vec<&Job>, bool)> = Vec::new();
        for job in jobs.iter().filter(|j| j.stage == stage) {
            match matrix_name(&job.name) {
                Some(name) => match groups.iter_mut().find(|g| g.2 && g.0 == name) {
                    Some(group) => group.1.push(job),
                    None => groups.push((name, vec![job], true)),
                },
                None => groups.push((&job.name, vec![job], false)),
            }
        }
        for (name, members, matrix) in groups {
            let row = |job: &Job, depth| JobRow::Job {
                job: job.clone(),
                project,
                depth,
            };
            if !matrix {
                rows.extend(members.into_iter().map(|job| row(job, 0)));
                continue;
            }
            let key = RowKey::Group {
                stage: stage.to_string(),
                name: name.to_string(),
            };
            let opened = open.contains(&key);
            let status = (members.iter().map(|j| j.status))
                .min_by_key(|s| s.rank())
                .unwrap_or(Status::Unknown);
            rows.push(JobRow::Group {
                stage: stage.to_string(),
                name: name.to_string(),
                status,
                count: members.len(),
                open: opened,
            });
            if opened {
                rows.extend(members.into_iter().map(|job| row(job, 1)));
            }
        }
        for bridge in bridges.iter().filter(|b| b.stage == stage) {
            let downstream = bridge.downstream_pipeline.as_ref();
            let opened = downstream.is_some() && open.contains(&RowKey::Bridge(bridge.id));
            rows.push(JobRow::Bridge {
                bridge: bridge.clone(),
                open: opened,
            });
            let Some(downstream) = downstream.filter(|_| opened) else {
                continue;
            };
            match children.get(&downstream.id) {
                Some(child_jobs) => rows.extend(child_jobs.iter().map(|job| JobRow::Job {
                    job: job.clone(),
                    project: downstream.project_id.unwrap_or(project),
                    depth: 1,
                })),
                None => rows.push(JobRow::Waiting {
                    pipeline: downstream.id,
                }),
            }
        }
    }
    rows
}

#[cfg(test)]
mod tests {
    use super::matrix_name;

    #[test]
    fn a_matrix_name_is_the_part_before_a_bracketed_list_after_a_colon() {
        assert_eq!(matrix_name("unit: [go, 1.21]"), Some("unit"));
        assert_eq!(matrix_name("unit:[go]"), Some("unit"));
        assert_eq!(matrix_name("deploy:prod: [eu]"), Some("deploy:prod"));
        assert_eq!(matrix_name("a: [x]: [y]"), Some("a"));
        assert_eq!(matrix_name("unit"), None);
        assert_eq!(matrix_name("unit: go"), None);
        assert_eq!(matrix_name("unit: []"), None);
        assert_eq!(matrix_name(": [go]"), None);
    }
}
