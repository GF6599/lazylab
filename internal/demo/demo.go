// Package demo provides a stateless DemoService implementing gitlab.Service
// with hardcoded fake data. It enables VHS terminal recordings, offline
// exploration, and contributor onboarding without a real GitLab token.
package demo

import (
	"context"
	"fmt"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// DemoService implements gitlab.Service with deterministic fake data.
// All methods are stateless — write operations (retry, cancel, play, resolve)
// return plausible static values without mutating any state.
type DemoService struct{}

// Compile-time interface check.
var _ gitlab.Service = (*DemoService)(nil)

func (d *DemoService) ListProjects(_ context.Context, opts gitlab.ProjectListOptions) (gitlab.ProjectPage, error) {
	all := demoProjects()
	perPage := opts.PerPage
	if perPage <= 0 {
		perPage = 30
	}
	page := opts.Page
	if page <= 0 {
		page = 1
	}
	total := len(all)
	totalPages := (total + perPage - 1) / perPage

	start := (page - 1) * perPage
	if start >= total {
		return gitlab.ProjectPage{Page: page, TotalPages: totalPages}, nil
	}
	end := start + perPage
	if end > total {
		end = total
	}

	pp := gitlab.ProjectPage{
		Projects:   all[start:end],
		Page:       page,
		TotalPages: totalPages,
	}
	if page > 1 {
		pp.PrevPage = page - 1
	}
	if page < totalPages {
		pp.NextPage = page + 1
	}
	return pp, nil
}

func (d *DemoService) ListTree(_ context.Context, projectID int, opts gitlab.TreeListOptions) ([]gitlab.TreeNode, error) {
	return demoTree(projectID, opts.Path), nil
}

func (d *DemoService) GetFileContent(_ context.Context, projectID int, path, _ string) (string, error) {
	return demoFileContent(projectID, path), nil
}

func (d *DemoService) LatestPipeline(_ context.Context, projectID int, _ string) (gitlab.PipelineSummary, error) {
	pipelines := demoPipelines(projectID)
	if len(pipelines) == 0 {
		return gitlab.PipelineSummary{}, gitlab.ErrNoPipelines
	}
	latest := pipelines[0]
	latest.Stages = demoStages(latest.ID)
	return latest, nil
}

func (d *DemoService) ListPipelines(_ context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
	all := demoPipelines(projectID)
	// Apply CLI-driven filters in demo too so `pipeline list --ref X
	// --demo` produces realistic results — filtering after the fact is
	// O(n) but the demo dataset is small (~dozens of pipelines).
	// Allocate fresh slices for each filter rather than reusing
	// all[:0]: the original `all` is the shared slice returned by
	// demoPipelines, and aliasing its backing array would let one
	// filter call mutate data seen by future callers (or by a
	// concurrent ListPipelines on a different ref/status).
	if opts.Ref != "" {
		filtered := make([]gitlab.PipelineSummary, 0, len(all))
		for _, p := range all {
			if p.Ref == opts.Ref {
				filtered = append(filtered, p)
			}
		}
		all = filtered
	}
	if opts.Status != "" {
		filtered := make([]gitlab.PipelineSummary, 0, len(all))
		for _, p := range all {
			if p.Status == opts.Status {
				filtered = append(filtered, p)
			}
		}
		all = filtered
	}
	perPage := opts.PerPage
	if perPage <= 0 {
		perPage = 20
	}
	page := opts.Page
	if page <= 0 {
		page = 1
	}
	total := len(all)
	totalPages := (total + perPage - 1) / perPage

	start := (page - 1) * perPage
	if start >= total {
		return gitlab.PipelinePage{Page: page, TotalPages: totalPages}, nil
	}
	end := start + perPage
	if end > total {
		end = total
	}

	pp := gitlab.PipelinePage{
		Pipelines:  all[start:end],
		Page:       page,
		TotalPages: totalPages,
	}
	if page > 1 {
		pp.PrevPage = page - 1
	}
	if page < totalPages {
		pp.NextPage = page + 1
	}
	return pp, nil
}

func (d *DemoService) PipelineStages(_ context.Context, _, pipelineID int) ([]gitlab.PipelineStage, error) {
	return demoStages(pipelineID), nil
}

func (d *DemoService) ListPipelineJobs(_ context.Context, projectID, pipelineID int) ([]gitlab.PipelineJob, error) {
	return demoJobs(projectID, pipelineID), nil
}

func (d *DemoService) GetJobTrace(_ context.Context, _, jobID int) (string, error) {
	return demoJobTrace(jobID), nil
}

func (d *DemoService) RetryPipeline(_ context.Context, projectID, pipelineID int, ref string) (gitlab.PipelineSummary, error) {
	return gitlab.PipelineSummary{
		ID:        pipelineID + 1,
		Status:    "pending",
		Ref:       ref,
		UpdatedAt: refTime,
	}, nil
}

func (d *DemoService) RetryJob(_ context.Context, _, jobID int) (gitlab.PipelineJob, error) {
	return gitlab.PipelineJob{
		ID:     jobID + 1,
		Status: "pending",
	}, nil
}

func (d *DemoService) CancelPipeline(_ context.Context, _, _ int) error {
	return nil
}

func (d *DemoService) CancelJob(_ context.Context, _, _ int) error {
	return nil
}

func (d *DemoService) PlayJob(_ context.Context, _, jobID int) (gitlab.PipelineJob, error) {
	return gitlab.PipelineJob{
		ID:     jobID,
		Status: "pending",
	}, nil
}

func (d *DemoService) ListMergeRequests(_ context.Context, projectID int, opts gitlab.MRListOptions) (gitlab.MRPage, error) {
	all := demoMergeRequests(projectID)

	// Filter by state if specified.
	if opts.State != "" && opts.State != "all" {
		filtered := all[:0:0]
		for _, mr := range all {
			if mr.State == opts.State {
				filtered = append(filtered, mr)
			}
		}
		all = filtered
	}

	perPage := opts.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	page := opts.Page
	if page <= 0 {
		page = 1
	}
	total := len(all)
	totalPages := (total + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}

	start := (page - 1) * perPage
	if start >= total {
		return gitlab.MRPage{Page: page, TotalPages: totalPages}, nil
	}
	end := start + perPage
	if end > total {
		end = total
	}

	mp := gitlab.MRPage{
		MergeRequests: all[start:end],
		Page:          page,
		TotalPages:    totalPages,
	}
	if page > 1 {
		mp.PrevPage = page - 1
	}
	if page < totalPages {
		mp.NextPage = page + 1
	}
	return mp, nil
}

func (d *DemoService) ListMergeRequestDiscussions(_ context.Context, projectID, mrIID int) ([]gitlab.MRDiscussion, error) {
	return demoDiscussions(projectID, mrIID), nil
}

func (d *DemoService) ListMergeRequestDiffs(_ context.Context, projectID, mrIID int) ([]gitlab.MRDiffFile, error) {
	return demoDiffs(projectID, mrIID), nil
}

func (d *DemoService) ListPipelineBridges(_ context.Context, _, _ int) ([]gitlab.PipelineBridge, error) {
	return nil, nil
}

func (d *DemoService) GetPipelineTestReport(_ context.Context, _, _ int) (*gitlab.TestReport, error) {
	return nil, nil
}

func (d *DemoService) ListProjectCommits(_ context.Context, projectID int, _ string, _ int) ([]gitlab.CommitSummary, error) {
	return demoCommits(projectID), nil
}

func (d *DemoService) ResolveMergeRequestDiscussion(_ context.Context, _, _ int, _ string, _ bool) error {
	return nil
}

func (d *DemoService) AddMergeRequestDiscussionNote(_ context.Context, _, _ int, _ string, _ string) error {
	return nil
}

func (d *DemoService) CreateMergeRequestDiscussion(_ context.Context, _, _ int, _ string, _ *gitlab.MRCommentPosition) error {
	return nil
}

func (d *DemoService) GetMergeRequestDiffRefs(_ context.Context, _, _ int) (gitlab.MRDiffRefs, error) {
	return gitlab.MRDiffRefs{
		BaseSHA:  "abc123def456",
		HeadSHA:  "789abc012def",
		StartSHA: "345678abcdef",
	}, nil
}

func (d *DemoService) ListBranches(_ context.Context, _ int, _ string) ([]string, error) {
	return []string{"main", "develop", "feature/auth", "feature/dashboard", "fix/login-bug", "release/v2.0"}, nil
}

func (d *DemoService) CreateMergeRequest(_ context.Context, _ int, opts gitlab.CreateMROptions) (gitlab.MergeRequestSummary, error) {
	return gitlab.MergeRequestSummary{
		IID:          999,
		Title:        opts.Title,
		State:        "opened",
		Author:       "Demo User",
		SourceBranch: opts.SourceBranch,
		TargetBranch: opts.TargetBranch,
		WebURL:       "https://gitlab.example.com/demo/project/-/merge_requests/999",
		UpdatedAt:    refTime,
	}, nil
}

func (d *DemoService) CurrentUser(_ context.Context) (gitlab.UserInfo, error) {
	return gitlab.UserInfo{
		ID:       1,
		Username: "demo",
		Name:     "Demo User",
		Email:    "demo@gitlab.example.com",
		State:    "active",
		WebURL:   "https://gitlab.example.com/demo",
	}, nil
}

func (d *DemoService) GetProject(_ context.Context, idOrPath string) (gitlab.ProjectNode, error) {
	// Demo mode resolves any identifier to the first sample project so the
	// CLI path can be exercised offline without needing to teach the demo
	// data set about every fake project slug.
	all := demoProjects()
	if len(all) == 0 {
		return gitlab.ProjectNode{}, fmt.Errorf("demo: no projects available for %q", idOrPath)
	}
	return all[0], nil
}

func (d *DemoService) GetPipeline(_ context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
	for _, p := range demoPipelines(projectID) {
		if p.ID == pipelineID {
			return p, nil
		}
	}
	return gitlab.PipelineSummary{}, gitlab.ErrNoPipelines
}

func (d *DemoService) GetJob(_ context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
	// Demo data is keyed loosely by project; surface the first matching
	// job so the CLI streaming path can be exercised offline. A real
	// streamer would care about transitions, but demo jobs are already
	// terminal so a single fetch terminates the loop.
	for _, pipe := range demoPipelines(projectID) {
		for _, j := range demoJobs(projectID, pipe.ID) {
			if j.ID == jobID {
				return j, nil
			}
		}
	}
	return gitlab.PipelineJob{ID: jobID, Status: "success"}, nil
}

func (d *DemoService) LatestPipelineForSHA(_ context.Context, projectID int, sha string) (gitlab.PipelineSummary, error) {
	// Match the real gitlab client's contract: ErrNoPipelines on any
	// miss, whether the project has no pipelines at all or none for
	// the requested SHA. Returning pipelines[0] on a SHA miss used to
	// silently lie — callers expecting errors.Is(err, ErrNoPipelines)
	// would never trigger their fallback path in demo mode, hiding
	// real bugs in the CLI's not-found handling.
	for _, p := range demoPipelines(projectID) {
		if p.SHA == sha {
			return p, nil
		}
	}
	return gitlab.PipelineSummary{}, gitlab.ErrNoPipelines
}
