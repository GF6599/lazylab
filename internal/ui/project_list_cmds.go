// This file defines all Bubble Tea commands (tea.Cmd) and the message types
// they produce. Commands are pure functions that perform async I/O (API calls,
// cache reads) in a goroutine and return a single typed message. The message
// is then routed by Model.Update to the appropriate handler in
// project_list_messages.go.
//
// Every command that makes an API call creates a child context with a timeout
// derived from parentCtx, ensuring graceful cancellation on quit.

package ui

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// batchConcurrencyLimit caps the number of concurrent API calls in
// batchFetchPipelineStatusCmd. Without this, ~30 goroutines fire
// simultaneously and trigger GitLab's rate limit (429), causing retry
// backoff that pushes requests past the 20-second timeout.
const batchConcurrencyLimit = 5

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
	viewProjectID int
	pipelineID    int
	jobID         int
	job           gitlab.PipelineJob
	err           error
}

type pipelineTickMsg struct{}

// selectionDebounceTickMsg fires after pipelineDebounceDelay to trigger
// eager data loading for the selected project. The timestamp acts as a
// generation counter — stale ticks (from earlier selections) are discarded.
type selectionDebounceTickMsg struct {
	projectID int
	timestamp time.Time
}

type searchDebounceTickMsg struct {
	query     string
	timestamp time.Time
}

type favoritesLoadedMsg struct {
	favOrder []int
	err      error
}

type favoritesSavedMsg struct {
	err error
}

func loadFavoritesCmd(store *favoritesStore) tea.Cmd {
	return func() tea.Msg {
		order, err := store.Load()
		return favoritesLoadedMsg{favOrder: order, err: err}
	}
}

func saveFavoritesCmd(store *favoritesStore, favOrder []int) tea.Cmd {
	return func() tea.Msg {
		// Copy slice to avoid races
		cp := make([]int, len(favOrder))
		copy(cp, favOrder)
		return favoritesSavedMsg{err: store.Save(cp)}
	}
}

type preferencesLoadedMsg struct {
	layoutMode LayoutMode
	screenMode ScreenMode
	theme      ThemeName
	err        error
}

type preferencesSavedMsg struct{ err error }

func loadPreferencesCmd(store *preferencesStore) tea.Cmd {
	return func() tea.Msg {
		layout, screen, theme, err := store.Load()
		return preferencesLoadedMsg{layoutMode: layout, screenMode: screen, theme: theme, err: err}
	}
}

func savePreferencesCmd(store *preferencesStore, layout LayoutMode, screen ScreenMode, theme ThemeName) tea.Cmd {
	return func() tea.Msg {
		return preferencesSavedMsg{err: store.Save(layout, screen, theme)}
	}
}

type batchPipelineStatusMsg struct {
	results map[int]pipelineStatusResult // projectID -> result
}

type pipelineStatusResult struct {
	pipeline gitlab.PipelineSummary
	err      error
	empty    bool
}

func fetchProjectsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, perPage, page int, background bool) tea.Cmd {
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

var errStatusFetchIncomplete = errors.New("pipeline status fetch did not complete")

// batchFetchPipelineStatusCmd fetches the latest pipeline status for multiple
// projects concurrently. All goroutines share a single context with the given
// timeout, so a slow API server won't block indefinitely.
//
// Concurrency is bounded by a batchConcurrencyLimit-slot semaphore to avoid
// tripping GitLab's rate limiter (429), which would otherwise provoke retry
// backoff storms. The launch loop acquires a slot before each goroutine and
// bails out early if the context is cancelled before it can acquire one,
// reporting anything it never reached as a failure. The result channel is
// buffered to len(projects) so no worker blocks on send.
//
// ErrNoPipelines is mapped to empty=true rather than treated as an error, so
// the UI can distinguish "no pipeline exists" from "API call failed".
func batchFetchPipelineStatusCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projects []gitlab.ProjectNode, inFlight *atomic.Bool) tea.Cmd {
	return func() tea.Msg {
		defer inFlight.Store(false)

		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		// Seeded so a batch that ends early still reports on every project it was given. The
		// caller marks a project as loading before this runs and only a reply clears that, so
		// one left out keeps the flag, is skipped by every later refresh, and never recovers.
		results := make(map[int]pipelineStatusResult, len(projects))
		for _, project := range projects {
			results[project.ID] = pipelineStatusResult{err: errStatusFetchIncomplete}
		}

		type fetchResult struct {
			projectID int
			pipeline  gitlab.PipelineSummary
			err       error
			empty     bool
		}
		resultCh := make(chan fetchResult, len(projects))

		// Semaphore limits concurrent goroutines to avoid triggering
		// GitLab's rate limit (429) which causes retry backoff storms.
		sem := make(chan struct{}, batchConcurrencyLimit)
		launched := 0
		for _, project := range projects {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return batchPipelineStatusMsg{results: results}
			}
			launched++
			go func(projectID int) {
				defer func() { <-sem }()
				pipeline, err := client.LatestPipeline(ctx, projectID, "")
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

		for range launched {
			select {
			case result := <-resultCh:
				results[result.projectID] = pipelineStatusResult{
					pipeline: result.pipeline,
					err:      result.err,
					empty:    result.empty,
				}
			case <-ctx.Done():
				return batchPipelineStatusMsg{results: results}
			}
		}

		return batchPipelineStatusMsg{results: results}
	}
}

func fetchTreeCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, ref, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		nodes, err := client.ListTree(ctx, projectID, gitlab.TreeListOptions{Ref: ref, Path: path})
		return treeLoadedMsg{projectID: projectID, path: path, entries: nodes, err: err}
	}
}

func fetchFileCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, ref, filePath string) tea.Cmd {
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

// fetchPipelineCmd fetches the latest pipeline for a single project. The
// sentinel value pipelineAllRefsRef ("__all__") is mapped to an empty ref
// string for the API call, requesting the latest pipeline across all branches.
func fetchPipelineCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, ref string) tea.Cmd {
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

func fetchPipelinesCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, page, perPage int) tea.Cmd {
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

func fetchPipelineStagesCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		stages, err := client.PipelineStages(ctx, projectID, pipelineID)
		return pipelineStagesLoadedMsg{projectID: projectID, pipelineID: pipelineID, stages: stages, err: err}
	}
}

func fetchPipelineJobsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		jobs, err := client.ListPipelineJobs(ctx, projectID, pipelineID)
		return pipelineJobsLoadedMsg{projectID: projectID, pipelineID: pipelineID, jobs: jobs, err: err}
	}
}

func fetchPipelineLogCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, jobID int) tea.Cmd {
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

// retryPipelineCmd retries an entire pipeline. The GitLab API may return a new
// pipeline ID (if it creates a fresh run) or the same ID (if it retries
// failed jobs in-place). The handler uses pendingSelectID to track the cursor
// to the resulting pipeline after reload.
func retryPipelineCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, pipelineID int, ref string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		pipeline, err := client.RetryPipeline(ctx, projectID, pipelineID, ref)
		return pipelineRetriedMsg{projectID: projectID, pipelineID: pipelineID, pipeline: pipeline, err: err}
	}
}

// jobActionTarget separates the project a job action calls from the view that issued it.
// The two differ for a bridge-child job, whose pipeline lives downstream. A reply carries
// viewProjectID because matching on the called project discards every downstream reply.
type jobActionTarget struct {
	projectID     int
	viewProjectID int
}

func retryJobCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, target jobActionTarget, pipelineID, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		job, err := client.RetryJob(ctx, target.projectID, jobID)
		return pipelineJobRetriedMsg{viewProjectID: target.viewProjectID, pipelineID: pipelineID, jobID: jobID, job: job, err: err}
	}
}

// pipelineTickCmd schedules a single pipelineTickMsg after
// pipelineRefreshInterval. The Update handler re-enqueues the next tick via
// [continuePipelineTickCmd] only while the current mode wants live refresh;
// otherwise the chain self-terminates and is restarted on the next transition
// into a refreshable mode by [ensurePipelineTickCmd].
//
// Call this only through the two helpers above so the Model.pipelineTickAlive
// flag stays in sync — a raw call here that bypasses the helpers would race
// with the alive-tracking and risk double-ticking.
func pipelineTickCmd() tea.Cmd {
	return tea.Tick(pipelineRefreshInterval, func(time.Time) tea.Msg {
		return pipelineTickMsg{}
	})
}

// modeWantsPipelineTick reports whether the auto-refresh ticker has anything
// to do in the given mode. modeExplorer is the only refreshable-state-free
// mode today.
func modeWantsPipelineTick(mode Mode) bool {
	switch mode {
	case modeProjects, modePipelines, modeMultiPanel:
		return true
	}
	return false
}

// continuePipelineTickCmd is called from the pipelineTickMsg handler to decide
// whether the tick chain continues. Returns a fresh tick if the current mode
// still wants live refresh; otherwise clears Model.pipelineTickAlive and
// returns nil so the chain dies.
func continuePipelineTickCmd(m *Model) tea.Cmd {
	if !modeWantsPipelineTick(m.mode) {
		m.pipelineTickAlive = false
		return nil
	}
	return pipelineTickCmd()
}

// ensurePipelineTickCmd starts a new tick chain if (a) the current mode wants
// live refresh and (b) no chain is currently in flight. Returns nil otherwise,
// so callers can unconditionally batch the result without risking a duplicate
// chain. Call this after every transition that may enter a refreshable mode
// from a state where the chain might have died.
// ensureSpinnerTickCmd starts the spinner animation when something needs animating and
// no tick is in flight. Without it the first idle moment ends the animation for the rest
// of the session, because a spinner tick is only ever produced by the tick before it.
func ensureSpinnerTickCmd(m *Model) tea.Cmd {
	if !m.needsAnimation() || m.spinnerTickAlive {
		return nil
	}
	m.spinnerTickAlive = true
	return m.spinner.Tick
}

func ensurePipelineTickCmd(m *Model) tea.Cmd {
	if !modeWantsPipelineTick(m.mode) {
		return nil
	}
	if m.pipelineTickAlive {
		return nil
	}
	m.pipelineTickAlive = true
	return pipelineTickCmd()
}

// selectionDebounceTickCmd fires a debounce tick after delay. The timestamp
// acts as a generation counter — the handler ignores stale ticks whose
// timestamp does not match the current timer, avoiding redundant API calls
// when the user scrolls through projects quickly.
func selectionDebounceTickCmd(projectID int, timestamp time.Time) tea.Cmd {
	return tea.Tick(pipelineDebounceDelay, func(time.Time) tea.Msg {
		return selectionDebounceTickMsg{projectID: projectID, timestamp: timestamp}
	})
}

// searchDebounceTickCmd fires a debounce tick after searchDebounceDelay using
// tea.Tick. The timestamp/query are compared against current state to discard
// stale ticks when the user is still typing.
func searchDebounceTickCmd(query string, timestamp time.Time) tea.Cmd {
	return tea.Tick(searchDebounceDelay, func(time.Time) tea.Msg {
		return searchDebounceTickMsg{query: query, timestamp: timestamp}
	})
}

type mrsLoadedMsg struct {
	projectID  int
	mrs        []gitlab.MergeRequestSummary
	page       int
	prevPage   int
	nextPage   int
	totalPages int
	err        error
}

type pipelineCanceledMsg struct {
	projectID  int
	pipelineID int
	err        error
}

type jobCanceledMsg struct {
	viewProjectID int
	jobID         int
	err           error
}

type jobPlayedMsg struct {
	viewProjectID int
	jobID         int
	job           gitlab.PipelineJob
	err           error
}

func fetchMRsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, state string, page, perPage int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		mrPage, err := client.ListMergeRequests(ctx, projectID, gitlab.MRListOptions{
			State:   state,
			Page:    page,
			PerPage: perPage,
		})
		if err != nil {
			return mrsLoadedMsg{projectID: projectID, err: err}
		}
		return mrsLoadedMsg{
			projectID:  projectID,
			mrs:        mrPage.MergeRequests,
			page:       mrPage.Page,
			prevPage:   mrPage.PrevPage,
			nextPage:   mrPage.NextPage,
			totalPages: mrPage.TotalPages,
		}
	}
}

func cancelPipelineCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		err := client.CancelPipeline(ctx, projectID, pipelineID)
		return pipelineCanceledMsg{projectID: projectID, pipelineID: pipelineID, err: err}
	}
}

func cancelJobCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, target jobActionTarget, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		err := client.CancelJob(ctx, target.projectID, jobID)
		return jobCanceledMsg{viewProjectID: target.viewProjectID, jobID: jobID, err: err}
	}
}

func playJobCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, target jobActionTarget, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		job, err := client.PlayJob(ctx, target.projectID, jobID)
		return jobPlayedMsg{viewProjectID: target.viewProjectID, jobID: jobID, job: job, err: err}
	}
}

// commitsLoadedMsg is sent when recent commits for a project have been fetched.
// Handled in handleCommitsLoaded to populate the detail pane's "Recent Commits" section.
type commitsLoadedMsg struct {
	projectID int
	commits   []gitlab.CommitSummary
	err       error
}

// fetchCommitsCmd fetches the 5 most recent commits for a project's branch.
// Uses the project's default branch when ref is provided; the result is cached
// per project ID so subsequent visits skip the API call.
func fetchCommitsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, ref string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		commits, err := client.ListProjectCommits(ctx, projectID, ref, 5)
		return commitsLoadedMsg{projectID: projectID, commits: commits, err: err}
	}
}

type childPipelineJobsLoadedMsg struct {
	childPipelineID int
	jobs            []gitlab.PipelineJob
	err             error
}

func fetchChildPipelineJobsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, childProjectID, childPipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		jobs, err := client.ListPipelineJobs(ctx, childProjectID, childPipelineID)
		return childPipelineJobsLoadedMsg{childPipelineID: childPipelineID, jobs: jobs, err: err}
	}
}

type bridgesLoadedMsg struct {
	projectID  int
	pipelineID int
	bridges    []gitlab.PipelineBridge
	err        error
}

type testReportLoadedMsg struct {
	projectID  int
	pipelineID int
	report     *gitlab.TestReport
	err        error
}

func fetchBridgesCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		bridges, err := client.ListPipelineBridges(ctx, projectID, pipelineID)
		return bridgesLoadedMsg{projectID: projectID, pipelineID: pipelineID, bridges: bridges, err: err}
	}
}

type mrDiscussionsLoadedMsg struct {
	projectID   int
	mrIID       int
	discussions []gitlab.MRDiscussion
	err         error
}

type mrDiffsLoadedMsg struct {
	projectID int
	mrIID     int
	diffs     []gitlab.MRDiffFile
	err       error
}

func fetchMRDiscussionsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, mrIID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		discussions, err := client.ListMergeRequestDiscussions(ctx, projectID, mrIID)
		return mrDiscussionsLoadedMsg{projectID: projectID, mrIID: mrIID, discussions: discussions, err: err}
	}
}

type mrDiscussionResolvedMsg struct {
	projectID    int
	mrIID        int
	discussionID string
	resolved     bool
	err          error
}

type mrDiscussionReplyMsg struct {
	projectID    int
	mrIID        int
	discussionID string
	err          error
}

func resolveMRDiscussionCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, mrIID int, discussionID string, resolved bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		err := client.ResolveMergeRequestDiscussion(ctx, projectID, mrIID, discussionID, resolved)
		return mrDiscussionResolvedMsg{projectID: projectID, mrIID: mrIID, discussionID: discussionID, resolved: resolved, err: err}
	}
}

func replyMRDiscussionCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, mrIID int, discussionID, body string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		err := client.AddMergeRequestDiscussionNote(ctx, projectID, mrIID, discussionID, body)
		return mrDiscussionReplyMsg{projectID: projectID, mrIID: mrIID, discussionID: discussionID, err: err}
	}
}

type mrDiffRefsLoadedMsg struct {
	projectID int
	mrIID     int
	diffRefs  gitlab.MRDiffRefs
	err       error
}

func fetchMRDiffRefsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, mrIID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		refs, err := client.GetMergeRequestDiffRefs(ctx, projectID, mrIID)
		return mrDiffRefsLoadedMsg{projectID: projectID, mrIID: mrIID, diffRefs: refs, err: err}
	}
}

type mrDiscussionCreatedMsg struct {
	projectID int
	mrIID     int
	err       error
}

func createMRDiscussionCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, mrIID int, body string, pos *gitlab.MRCommentPosition) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		err := client.CreateMergeRequestDiscussion(ctx, projectID, mrIID, body, pos)
		return mrDiscussionCreatedMsg{projectID: projectID, mrIID: mrIID, err: err}
	}
}

func fetchMRDiffsCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, mrIID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		diffs, err := client.ListMergeRequestDiffs(ctx, projectID, mrIID)
		return mrDiffsLoadedMsg{projectID: projectID, mrIID: mrIID, diffs: diffs, err: err}
	}
}

func fetchTestReportCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		report, err := client.GetPipelineTestReport(ctx, projectID, pipelineID)
		return testReportLoadedMsg{projectID: projectID, pipelineID: pipelineID, report: report, err: err}
	}
}

type mrCreatedMsg struct {
	projectID int
	mr        gitlab.MergeRequestSummary
	err       error
}

func createMRCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, opts gitlab.CreateMROptions) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		mr, err := client.CreateMergeRequest(ctx, projectID, opts)
		return mrCreatedMsg{projectID: projectID, mr: mr, err: err}
	}
}

type branchesLoadedMsg struct {
	projectID int
	branches  []string
	err       error
}

func fetchBranchesCmd(parentCtx context.Context, client gitlab.Service, timeout time.Duration, projectID int, search string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()
		branches, err := client.ListBranches(ctx, projectID, search)
		return branchesLoadedMsg{projectID: projectID, branches: branches, err: err}
	}
}
