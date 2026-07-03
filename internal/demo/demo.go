// Package demo provides a stateless DemoService implementing gitlab.Service
// with hardcoded fake data. It enables VHS terminal recordings, offline
// exploration, and contributor onboarding without a real GitLab token.
package demo

import (
	"context"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// DemoService implements gitlab.Service with deterministic fake data so the
// CLI and TUI can be exercised offline. It is stateless, so all methods are
// safe for concurrent use. Two cross-method contracts hold regardless of which
// method is called:
//
//   - Reads (List*, Get*, Latest*) return canned data from the demo* helpers.
//     The lookup methods that can miss — LatestPipeline, LatestPipelineForSHA,
//     and GetPipeline — return the gitlab.ErrNoPipelines sentinel (matchable
//     with errors.Is) rather than empty values, so error-handling paths still
//     get exercised.
//   - Writes (Retry*, Cancel*, Play*, Create*, Resolve*, Add*) never mutate
//     state. They either no-op (returning nil) or fabricate a plausible record
//     with a synthetic ID; subsequent reads will not reflect the write.
type DemoService struct{}

// Compile-time interface check.
var _ gitlab.Service = (*DemoService)(nil)

// ListProjects paginates the static sample project set, honoring opts.Page and
// opts.PerPage (defaulting PerPage to 30) so the TUI's paging logic is testable.
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
	end := min(start+perPage, total)

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

// ListTree returns the canned directory listing for projectID at opts.Path.
func (d *DemoService) ListTree(_ context.Context, projectID int, opts gitlab.TreeListOptions) ([]gitlab.TreeNode, error) {
	return demoTree(projectID, opts.Path), nil
}

// GetFileContent returns the canned contents of path within projectID; the ref
// argument is ignored since demo files have no history.
func (d *DemoService) GetFileContent(_ context.Context, projectID int, path, _ string) (string, error) {
	return demoFileContent(projectID, path), nil
}

// LatestPipeline returns the newest demo pipeline for projectID (with its
// stages attached), ignoring the ref argument. It returns gitlab.ErrNoPipelines
// (matchable with errors.Is) when projectID has no demo pipelines.
func (d *DemoService) LatestPipeline(_ context.Context, projectID int, _ string) (gitlab.PipelineSummary, error) {
	pipelines := demoPipelines(projectID)
	if len(pipelines) == 0 {
		return gitlab.PipelineSummary{}, gitlab.ErrNoPipelines
	}
	latest := pipelines[0]
	latest.Stages = demoStages(latest.ID)
	return latest, nil
}

// ListPipelines filters the demo pipelines for projectID by opts.Ref and
// opts.Status (when set), then paginates the result (PerPage defaults to 20).
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
	end := min(start+perPage, total)

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

// PipelineStages returns the canned stages for pipelineID.
func (d *DemoService) PipelineStages(_ context.Context, _, pipelineID int) ([]gitlab.PipelineStage, error) {
	return demoStages(pipelineID), nil
}

// ListPipelineJobs returns the canned jobs for the given project and pipeline.
func (d *DemoService) ListPipelineJobs(_ context.Context, projectID, pipelineID int) ([]gitlab.PipelineJob, error) {
	return demoJobs(projectID, pipelineID), nil
}

// GetJobTrace returns the canned log trace for jobID.
func (d *DemoService) GetJobTrace(_ context.Context, _, jobID int) (string, error) {
	return demoJobTrace(jobID), nil
}

// RetryPipeline is a no-op write that fabricates a pending pipeline with a
// synthetic ID (pipelineID+1); nothing is persisted.
func (d *DemoService) RetryPipeline(_ context.Context, projectID, pipelineID int, ref string) (gitlab.PipelineSummary, error) {
	return gitlab.PipelineSummary{
		ID:        pipelineID + 1,
		Status:    "pending",
		Ref:       ref,
		UpdatedAt: refTime,
	}, nil
}

// RetryJob is a no-op write that fabricates a pending job with a synthetic ID
// (jobID+1); nothing is persisted.
func (d *DemoService) RetryJob(_ context.Context, _, jobID int) (gitlab.PipelineJob, error) {
	return gitlab.PipelineJob{
		ID:     jobID + 1,
		Status: "pending",
	}, nil
}

// CancelPipeline is a no-op write that always succeeds.
func (d *DemoService) CancelPipeline(_ context.Context, _, _ int) error {
	return nil
}

// CancelJob is a no-op write that always succeeds.
func (d *DemoService) CancelJob(_ context.Context, _, _ int) error {
	return nil
}

// PlayJob is a no-op write that echoes jobID back as a pending job; nothing is
// persisted.
func (d *DemoService) PlayJob(_ context.Context, _, jobID int) (gitlab.PipelineJob, error) {
	return gitlab.PipelineJob{
		ID:     jobID,
		Status: "pending",
	}, nil
}

// ListMergeRequests filters the demo MRs for projectID by opts.State (unless
// empty or "all"), then paginates the result (PerPage defaults to 25).
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
	end := min(start+perPage, total)

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

// ListMergeRequestDiscussions returns the canned discussion threads for the MR.
func (d *DemoService) ListMergeRequestDiscussions(_ context.Context, projectID, mrIID int) ([]gitlab.MRDiscussion, error) {
	return demoDiscussions(projectID, mrIID), nil
}

// ListMergeRequestDiffs returns the canned changed-file diffs for the MR.
func (d *DemoService) ListMergeRequestDiffs(_ context.Context, projectID, mrIID int) ([]gitlab.MRDiffFile, error) {
	return demoDiffs(projectID, mrIID), nil
}

// ListPipelineBridges always returns nil; the demo dataset has no child
// (downstream) pipelines.
func (d *DemoService) ListPipelineBridges(_ context.Context, _, _ int) ([]gitlab.PipelineBridge, error) {
	return nil, nil
}

// GetPipelineTestReport always returns a nil report; the demo dataset has no
// test reports.
func (d *DemoService) GetPipelineTestReport(_ context.Context, _, _ int) (*gitlab.TestReport, error) {
	return nil, nil
}

// ListProjectCommits returns the canned commit history for projectID, ignoring
// the ref and limit arguments.
func (d *DemoService) ListProjectCommits(_ context.Context, projectID int, _ string, _ int) ([]gitlab.CommitSummary, error) {
	return demoCommits(projectID), nil
}

// ResolveMergeRequestDiscussion is a no-op write that always succeeds.
func (d *DemoService) ResolveMergeRequestDiscussion(_ context.Context, _, _ int, _ string, _ bool) error {
	return nil
}

// AddMergeRequestDiscussionNote is a no-op write that always succeeds.
func (d *DemoService) AddMergeRequestDiscussionNote(_ context.Context, _, _ int, _ string, _ string) error {
	return nil
}

// CreateMergeRequestDiscussion is a no-op write that always succeeds.
func (d *DemoService) CreateMergeRequestDiscussion(_ context.Context, _, _ int, _ string, _ *gitlab.MRCommentPosition) error {
	return nil
}

// GetMergeRequestDiffRefs returns fixed placeholder base/head/start SHAs so the
// inline-comment positioning path can be exercised offline.
func (d *DemoService) GetMergeRequestDiffRefs(_ context.Context, _, _ int) (gitlab.MRDiffRefs, error) {
	return gitlab.MRDiffRefs{
		BaseSHA:  "abc123def456",
		HeadSHA:  "789abc012def",
		StartSHA: "345678abcdef",
	}, nil
}

// ListBranches returns a fixed set of sample branch names for any project.
func (d *DemoService) ListBranches(_ context.Context, _ int, _ string) ([]string, error) {
	return []string{"main", "develop", "feature/auth", "feature/dashboard", "fix/login-bug", "release/v2.0"}, nil
}

// CreateMergeRequest is a no-op write that fabricates an opened MR (synthetic
// IID 999) echoing opts; nothing is persisted.
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
