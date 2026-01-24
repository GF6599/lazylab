package ui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lazylab/internal/gitlab"
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
	projectID  int
	pipelines  []gitlab.PipelineSummary
	page       int
	prevPage   int
	nextPage   int
	totalPages int
	err        error
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

type pipelineRetriedMsg struct {
	projectID  int
	pipelineID int
	pipeline   gitlab.PipelineSummary
	err        error
}

type pipelineJobRetriedMsg struct {
	projectID  int
	pipelineID int
	jobID      int
	job        gitlab.PipelineJob
	err        error
}

type pipelineTickMsg struct{}

type pipelineDebounceTickMsg struct {
	projectID int
	timestamp time.Time
}

type batchPipelineStatusMsg struct {
	results map[int]pipelineStatusResult // projectID -> result
}

type pipelineStatusResult struct {
	pipeline gitlab.PipelineSummary
	err      error
	empty    bool
}

func fetchProjectsCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, perPage, page int, background bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
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

func batchFetchPipelineStatusCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projects []gitlab.ProjectNode) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		results := make(map[int]pipelineStatusResult)

		// Use a channel to collect results from concurrent fetches
		type fetchResult struct {
			projectID int
			pipeline  gitlab.PipelineSummary
			err       error
			empty     bool
		}
		resultCh := make(chan fetchResult, len(projects))

		// Fetch pipeline status for each project concurrently
		for _, project := range projects {
			go func(projectID int) {
				pipeline, err := client.LatestPipeline(ctx, projectID, pipelineAllRefsRef)
				if err != nil {
					if errors.Is(err, gitlab.ErrNoPipelines) {
						resultCh <- fetchResult{projectID: projectID, empty: true}
					} else {
						resultCh <- fetchResult{projectID: projectID, err: err}
					}
					return
				}
				resultCh <- fetchResult{projectID: projectID, pipeline: pipeline}
			}(project.ID)
		}

		// Collect all results
		for i := 0; i < len(projects); i++ {
			result := <-resultCh
			results[result.projectID] = pipelineStatusResult{
				pipeline: result.pipeline,
				err:      result.err,
				empty:    result.empty,
			}
		}

		return batchPipelineStatusMsg{results: results}
	}
}

func fetchTreeCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID int, ref, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		nodes, err := client.ListTree(ctx, projectID, gitlab.TreeListOptions{Ref: ref, Path: path})
		return treeLoadedMsg{projectID: projectID, path: path, entries: nodes, err: err}
	}
}

func fetchFileCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID int, ref, filePath string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		content, err := client.GetFileContent(ctx, projectID, filePath, ref)
		if err != nil {
			return fileLoadedMsg{projectID: projectID, path: filePath, err: err}
		}
		return fileLoadedMsg{projectID: projectID, path: filePath, content: content}
	}
}

func fetchPipelineCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID int, ref string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		requestRef := ref
		if ref == pipelineAllRefsRef {
			requestRef = ""
		}
		summary, err := client.LatestPipeline(ctx, projectID, requestRef)
		return pipelineStatusMsg{projectID: projectID, ref: ref, pipeline: summary, err: err}
	}
}

func fetchPipelinesCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID, page, perPage int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		pipelinePage, err := client.ListPipelines(ctx, projectID, gitlab.PipelineListOptions{Page: page, PerPage: perPage})
		if err != nil {
			return pipelinesLoadedMsg{projectID: projectID, page: page, err: err}
		}
		return pipelinesLoadedMsg{
			projectID:  projectID,
			pipelines:  pipelinePage.Pipelines,
			page:       pipelinePage.Page,
			prevPage:   pipelinePage.PrevPage,
			nextPage:   pipelinePage.NextPage,
			totalPages: pipelinePage.TotalPages,
		}
	}
}

func fetchPipelineStagesCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		stages, err := client.PipelineStages(ctx, projectID, pipelineID)
		return pipelineStagesLoadedMsg{projectID: projectID, pipelineID: pipelineID, stages: stages, err: err}
	}
}

func fetchPipelineJobsCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		jobs, err := client.ListPipelineJobs(ctx, projectID, pipelineID)
		return pipelineJobsLoadedMsg{projectID: projectID, pipelineID: pipelineID, jobs: jobs, err: err}
	}
}

func fetchPipelineLogCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		content, err := client.GetJobTrace(ctx, projectID, jobID)
		if err != nil {
			return pipelineLogLoadedMsg{projectID: projectID, jobID: jobID, err: err}
		}
		return pipelineLogLoadedMsg{projectID: projectID, jobID: jobID, content: content}
	}
}

func retryPipelineCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID, pipelineID int, ref string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		pipeline, err := client.RetryPipeline(ctx, projectID, pipelineID, ref)
		return pipelineRetriedMsg{projectID: projectID, pipelineID: pipelineID, pipeline: pipeline, err: err}
	}
}

func retryJobCmd(parentCtx context.Context, client *gitlab.Client, timeout time.Duration, projectID, pipelineID, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		job, err := client.RetryJob(ctx, projectID, jobID)
		return pipelineJobRetriedMsg{projectID: projectID, pipelineID: pipelineID, jobID: jobID, job: job, err: err}
	}
}

func pipelineTickCmd() tea.Cmd {
	return tea.Tick(pipelineRefreshInterval, func(time.Time) tea.Msg {
		return pipelineTickMsg{}
	})
}

func pipelineDebounceTickCmd(projectID int, timestamp time.Time, delay time.Duration) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(delay)
		return pipelineDebounceTickMsg{projectID: projectID, timestamp: timestamp}
	}
}
