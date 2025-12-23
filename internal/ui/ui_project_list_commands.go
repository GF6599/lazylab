package ui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"gitlab-tui-codex/internal/gitlab"
)

type projectsLoadedMsg struct {
	page       gitlab.ProjectPage
	err        error
	background bool
}

type cacheLoadedMsg struct {
	projects []gitlab.ProjectNode
	err      error
	found    bool
}

type cacheSavedMsg struct {
	err error
}

type treeLoadedMsg struct {
	projectID int
	path      string
	entries   []gitlab.TreeNode
	err       error
}

type fileLoadedMsg struct {
	projectID int
	path      string
	content   string
	err       error
}

type pipelineStatusMsg struct {
	projectID int
	ref       string
	pipeline  gitlab.PipelineSummary
	err       error
}

type pipelinesLoadedMsg struct {
	projectID int
	pipelines []gitlab.PipelineSummary
	err       error
}

type pipelineStagesLoadedMsg struct {
	projectID  int
	pipelineID int
	stages     []gitlab.PipelineStage
	err        error
}

type pipelineJobsLoadedMsg struct {
	projectID  int
	pipelineID int
	jobs       []gitlab.PipelineJob
	err        error
}

type pipelineLogLoadedMsg struct {
	projectID int
	jobID     int
	content   string
	err       error
}

type pipelineTickMsg struct{}

func fetchProjectsCmd(client *gitlab.Client, perPage, page int, background bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		pageData, err := client.ListProjects(ctx, gitlab.ProjectListOptions{PerPage: perPage, Page: page})
		return projectsLoadedMsg{page: pageData, err: err, background: background}
	}
}

func loadCacheCmd(cache *projectCache) tea.Cmd {
	return func() tea.Msg {
		projects, err := cache.Load()
		if err != nil {
			if errors.Is(err, errCacheNotFound) {
				return cacheLoadedMsg{found: false}
			}
			return cacheLoadedMsg{err: err}
		}
		return cacheLoadedMsg{projects: projects, found: true}
	}
}

func saveCacheCmd(cache *projectCache, projects []gitlab.ProjectNode) tea.Cmd {
	return func() tea.Msg {
		if err := cache.Save(projects); err != nil {
			return cacheSavedMsg{err: err}
		}
		return cacheSavedMsg{}
	}
}

func fetchTreeCmd(client *gitlab.Client, projectID int, ref, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		nodes, err := client.ListTree(ctx, projectID, gitlab.TreeListOptions{Ref: ref, Path: path})
		return treeLoadedMsg{projectID: projectID, path: path, entries: nodes, err: err}
	}
}

func fetchFileCmd(client *gitlab.Client, projectID int, ref, filePath string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		content, err := client.GetFileContent(ctx, projectID, filePath, ref)
		if err != nil {
			return fileLoadedMsg{projectID: projectID, path: filePath, err: err}
		}
		return fileLoadedMsg{projectID: projectID, path: filePath, content: content}
	}
}

func fetchPipelineCmd(client *gitlab.Client, projectID int, ref string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		summary, err := client.LatestPipeline(ctx, projectID, ref)
		return pipelineStatusMsg{projectID: projectID, ref: ref, pipeline: summary, err: err}
	}
}

func fetchPipelinesCmd(client *gitlab.Client, projectID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pipelines, err := client.ListPipelines(ctx, projectID)
		return pipelinesLoadedMsg{projectID: projectID, pipelines: pipelines, err: err}
	}
}

func fetchPipelineStagesCmd(client *gitlab.Client, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		stages, err := client.PipelineStages(ctx, projectID, pipelineID)
		return pipelineStagesLoadedMsg{projectID: projectID, pipelineID: pipelineID, stages: stages, err: err}
	}
}

func fetchPipelineJobsCmd(client *gitlab.Client, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		jobs, err := client.ListPipelineJobs(ctx, projectID, pipelineID)
		return pipelineJobsLoadedMsg{projectID: projectID, pipelineID: pipelineID, jobs: jobs, err: err}
	}
}

func fetchPipelineLogCmd(client *gitlab.Client, projectID, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		content, err := client.GetJobTrace(ctx, projectID, jobID)
		if err != nil {
			return pipelineLogLoadedMsg{projectID: projectID, jobID: jobID, err: err}
		}
		return pipelineLogLoadedMsg{projectID: projectID, jobID: jobID, content: content}
	}
}

func pipelineTickCmd() tea.Cmd {
	return tea.Tick(pipelineRefreshInterval, func(time.Time) tea.Msg {
		return pipelineTickMsg{}
	})
}
