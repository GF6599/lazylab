// Package demo provides a stateless DemoService implementing gitlab.Service
// with hardcoded fake data. It enables VHS terminal recordings, offline
// exploration, and contributor onboarding without a real GitLab token.
package demo

import (
	"context"

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
