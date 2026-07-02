package ui

import (
	"context"
	"fmt"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// mockService is a hand-written mock implementing gitlab.Service for UI tests.
type mockService struct {
	ListProjectsFn                  func(ctx context.Context, opts gitlab.ProjectListOptions) (gitlab.ProjectPage, error)
	ListTreeFn                      func(ctx context.Context, projectID int, opts gitlab.TreeListOptions) ([]gitlab.TreeNode, error)
	GetFileContentFn                func(ctx context.Context, projectID int, path, ref string) (string, error)
	LatestPipelineFn                func(ctx context.Context, projectID int, ref string) (gitlab.PipelineSummary, error)
	ListPipelinesFn                 func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error)
	PipelineStagesFn                func(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineStage, error)
	ListPipelineJobsFn              func(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineJob, error)
	GetJobTraceFn                   func(ctx context.Context, projectID, jobID int) (string, error)
	RetryPipelineFn                 func(ctx context.Context, projectID, pipelineID int, ref string) (gitlab.PipelineSummary, error)
	RetryJobFn                      func(ctx context.Context, projectID, jobID int) (gitlab.PipelineJob, error)
	CancelPipelineFn                func(ctx context.Context, projectID, pipelineID int) error
	CancelJobFn                     func(ctx context.Context, projectID, jobID int) error
	PlayJobFn                       func(ctx context.Context, projectID, jobID int) (gitlab.PipelineJob, error)
	ListMergeRequestsFn             func(ctx context.Context, projectID int, opts gitlab.MRListOptions) (gitlab.MRPage, error)
	ListMergeRequestDiscussionsFn   func(ctx context.Context, projectID, mrIID int) ([]gitlab.MRDiscussion, error)
	ListMergeRequestDiffsFn         func(ctx context.Context, projectID, mrIID int) ([]gitlab.MRDiffFile, error)
	ListPipelineBridgesFn           func(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineBridge, error)
	GetPipelineTestReportFn         func(ctx context.Context, projectID, pipelineID int) (*gitlab.TestReport, error)
	ListProjectCommitsFn            func(ctx context.Context, projectID int, ref string, limit int) ([]gitlab.CommitSummary, error)
	ResolveMergeRequestDiscussionFn func(ctx context.Context, projectID, mrIID int, discussionID string, resolved bool) error
	AddMergeRequestDiscussionNoteFn func(ctx context.Context, projectID, mrIID int, discussionID string, body string) error
	CreateMergeRequestDiscussionFn  func(ctx context.Context, projectID, mrIID int, body string, pos *gitlab.MRCommentPosition) error
	GetMergeRequestDiffRefsFn       func(ctx context.Context, projectID, mrIID int) (gitlab.MRDiffRefs, error)
	ListBranchesFn                  func(ctx context.Context, projectID int, search string) ([]string, error)
	CreateMergeRequestFn            func(ctx context.Context, projectID int, opts gitlab.CreateMROptions) (gitlab.MergeRequestSummary, error)
	GetPipelineFn                   func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error)
	LatestPipelineForSHAFn          func(ctx context.Context, projectID int, sha string) (gitlab.PipelineSummary, error)
	GetJobFn                        func(ctx context.Context, projectID, jobID int) (gitlab.PipelineJob, error)
}

var _ gitlab.Service = (*mockService)(nil)

func (m *mockService) ListProjects(ctx context.Context, opts gitlab.ProjectListOptions) (gitlab.ProjectPage, error) {
	if m.ListProjectsFn != nil {
		return m.ListProjectsFn(ctx, opts)
	}
	return gitlab.ProjectPage{}, nil
}

func (m *mockService) ListTree(ctx context.Context, projectID int, opts gitlab.TreeListOptions) ([]gitlab.TreeNode, error) {
	if m.ListTreeFn != nil {
		return m.ListTreeFn(ctx, projectID, opts)
	}
	return nil, nil
}

func (m *mockService) GetFileContent(ctx context.Context, projectID int, path, ref string) (string, error) {
	if m.GetFileContentFn != nil {
		return m.GetFileContentFn(ctx, projectID, path, ref)
	}
	return "", nil
}

func (m *mockService) LatestPipeline(ctx context.Context, projectID int, ref string) (gitlab.PipelineSummary, error) {
	if m.LatestPipelineFn != nil {
		return m.LatestPipelineFn(ctx, projectID, ref)
	}
	return gitlab.PipelineSummary{}, nil
}

func (m *mockService) ListPipelines(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
	if m.ListPipelinesFn != nil {
		return m.ListPipelinesFn(ctx, projectID, opts)
	}
	return gitlab.PipelinePage{}, nil
}

func (m *mockService) PipelineStages(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineStage, error) {
	if m.PipelineStagesFn != nil {
		return m.PipelineStagesFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *mockService) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineJob, error) {
	if m.ListPipelineJobsFn != nil {
		return m.ListPipelineJobsFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *mockService) GetJobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	if m.GetJobTraceFn != nil {
		return m.GetJobTraceFn(ctx, projectID, jobID)
	}
	return "", nil
}

func (m *mockService) RetryPipeline(ctx context.Context, projectID, pipelineID int, ref string) (gitlab.PipelineSummary, error) {
	if m.RetryPipelineFn != nil {
		return m.RetryPipelineFn(ctx, projectID, pipelineID, ref)
	}
	return gitlab.PipelineSummary{}, nil
}

func (m *mockService) RetryJob(ctx context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
	if m.RetryJobFn != nil {
		return m.RetryJobFn(ctx, projectID, jobID)
	}
	return gitlab.PipelineJob{}, nil
}

func (m *mockService) CancelPipeline(ctx context.Context, projectID, pipelineID int) error {
	if m.CancelPipelineFn != nil {
		return m.CancelPipelineFn(ctx, projectID, pipelineID)
	}
	return nil
}

func (m *mockService) CancelJob(ctx context.Context, projectID, jobID int) error {
	if m.CancelJobFn != nil {
		return m.CancelJobFn(ctx, projectID, jobID)
	}
	return nil
}

func (m *mockService) PlayJob(ctx context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
	if m.PlayJobFn != nil {
		return m.PlayJobFn(ctx, projectID, jobID)
	}
	return gitlab.PipelineJob{}, nil
}

func (m *mockService) ListMergeRequests(ctx context.Context, projectID int, opts gitlab.MRListOptions) (gitlab.MRPage, error) {
	if m.ListMergeRequestsFn != nil {
		return m.ListMergeRequestsFn(ctx, projectID, opts)
	}
	return gitlab.MRPage{}, nil
}

func (m *mockService) ListMergeRequestDiscussions(ctx context.Context, projectID, mrIID int) ([]gitlab.MRDiscussion, error) {
	if m.ListMergeRequestDiscussionsFn != nil {
		return m.ListMergeRequestDiscussionsFn(ctx, projectID, mrIID)
	}
	return nil, nil
}

func (m *mockService) ListMergeRequestDiffs(ctx context.Context, projectID, mrIID int) ([]gitlab.MRDiffFile, error) {
	if m.ListMergeRequestDiffsFn != nil {
		return m.ListMergeRequestDiffsFn(ctx, projectID, mrIID)
	}
	return nil, nil
}

func (m *mockService) ListPipelineBridges(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineBridge, error) {
	if m.ListPipelineBridgesFn != nil {
		return m.ListPipelineBridgesFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *mockService) GetPipelineTestReport(ctx context.Context, projectID, pipelineID int) (*gitlab.TestReport, error) {
	if m.GetPipelineTestReportFn != nil {
		return m.GetPipelineTestReportFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *mockService) ListProjectCommits(ctx context.Context, projectID int, ref string, limit int) ([]gitlab.CommitSummary, error) {
	if m.ListProjectCommitsFn != nil {
		return m.ListProjectCommitsFn(ctx, projectID, ref, limit)
	}
	return nil, nil
}

func (m *mockService) ResolveMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, discussionID string, resolved bool) error {
	if m.ResolveMergeRequestDiscussionFn != nil {
		return m.ResolveMergeRequestDiscussionFn(ctx, projectID, mrIID, discussionID, resolved)
	}
	return nil
}

func (m *mockService) AddMergeRequestDiscussionNote(ctx context.Context, projectID, mrIID int, discussionID string, body string) error {
	if m.AddMergeRequestDiscussionNoteFn != nil {
		return m.AddMergeRequestDiscussionNoteFn(ctx, projectID, mrIID, discussionID, body)
	}
	return nil
}

func (m *mockService) CreateMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, body string, pos *gitlab.MRCommentPosition) error {
	if m.CreateMergeRequestDiscussionFn != nil {
		return m.CreateMergeRequestDiscussionFn(ctx, projectID, mrIID, body, pos)
	}
	return nil
}

func (m *mockService) GetMergeRequestDiffRefs(ctx context.Context, projectID, mrIID int) (gitlab.MRDiffRefs, error) {
	if m.GetMergeRequestDiffRefsFn != nil {
		return m.GetMergeRequestDiffRefsFn(ctx, projectID, mrIID)
	}
	return gitlab.MRDiffRefs{}, nil
}

func (m *mockService) ListBranches(ctx context.Context, projectID int, search string) ([]string, error) {
	if m.ListBranchesFn != nil {
		return m.ListBranchesFn(ctx, projectID, search)
	}
	return nil, nil
}

func (m *mockService) CreateMergeRequest(ctx context.Context, projectID int, opts gitlab.CreateMROptions) (gitlab.MergeRequestSummary, error) {
	if m.CreateMergeRequestFn != nil {
		return m.CreateMergeRequestFn(ctx, projectID, opts)
	}
	return gitlab.MergeRequestSummary{}, nil
}

// Methods below default to returning an explicit "Fn not set" error
// rather than a zero value. Defaulting to nil-error meant tests that
// forgot to wire a method silently exercised phantom data; the error
// forces each test to opt in to the surface area it actually needs.
// Older methods keep the permissive zero-value default to avoid
// retrofitting unrelated tests.

func (m *mockService) GetPipeline(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
	if m.GetPipelineFn != nil {
		return m.GetPipelineFn(ctx, projectID, pipelineID)
	}
	return gitlab.PipelineSummary{}, fmt.Errorf("mockService: GetPipelineFn not set")
}

func (m *mockService) LatestPipelineForSHA(ctx context.Context, projectID int, sha string) (gitlab.PipelineSummary, error) {
	if m.LatestPipelineForSHAFn != nil {
		return m.LatestPipelineForSHAFn(ctx, projectID, sha)
	}
	return gitlab.PipelineSummary{}, fmt.Errorf("mockService: LatestPipelineForSHAFn not set")
}

func (m *mockService) GetJob(ctx context.Context, projectID, jobID int) (gitlab.PipelineJob, error) {
	if m.GetJobFn != nil {
		return m.GetJobFn(ctx, projectID, jobID)
	}
	return gitlab.PipelineJob{}, fmt.Errorf("mockService: GetJobFn not set")
}
