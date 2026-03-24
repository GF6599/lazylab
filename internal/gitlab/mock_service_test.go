package gitlab

import "context"

// MockService is a hand-written mock implementing all Service interface methods.
// Each method delegates to its function field; unset fields return zero values.
type MockService struct {
	ListProjectsFn                  func(ctx context.Context, opts ProjectListOptions) (ProjectPage, error)
	ListTreeFn                      func(ctx context.Context, projectID int, opts TreeListOptions) ([]TreeNode, error)
	GetFileContentFn                func(ctx context.Context, projectID int, path, ref string) (string, error)
	LatestPipelineFn                func(ctx context.Context, projectID int, ref string) (PipelineSummary, error)
	ListPipelinesFn                 func(ctx context.Context, projectID int, opts PipelineListOptions) (PipelinePage, error)
	PipelineStagesFn                func(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error)
	ListPipelineJobsFn              func(ctx context.Context, projectID, pipelineID int) ([]PipelineJob, error)
	GetJobTraceFn                   func(ctx context.Context, projectID, jobID int) (string, error)
	RetryPipelineFn                 func(ctx context.Context, projectID, pipelineID int, ref string) (PipelineSummary, error)
	RetryJobFn                      func(ctx context.Context, projectID, jobID int) (PipelineJob, error)
	CancelPipelineFn                func(ctx context.Context, projectID, pipelineID int) error
	CancelJobFn                     func(ctx context.Context, projectID, jobID int) error
	PlayJobFn                       func(ctx context.Context, projectID, jobID int) (PipelineJob, error)
	ListMergeRequestsFn             func(ctx context.Context, projectID int, opts MRListOptions) (MRPage, error)
	ListMergeRequestDiscussionsFn   func(ctx context.Context, projectID, mrIID int) ([]MRDiscussion, error)
	ListMergeRequestDiffsFn         func(ctx context.Context, projectID, mrIID int) ([]MRDiffFile, error)
	ListPipelineBridgesFn           func(ctx context.Context, projectID, pipelineID int) ([]PipelineBridge, error)
	GetPipelineTestReportFn         func(ctx context.Context, projectID, pipelineID int) (*TestReport, error)
	ListProjectCommitsFn            func(ctx context.Context, projectID int, ref string, limit int) ([]CommitSummary, error)
	ResolveMergeRequestDiscussionFn func(ctx context.Context, projectID, mrIID int, discussionID string, resolved bool) error
	AddMergeRequestDiscussionNoteFn func(ctx context.Context, projectID, mrIID int, discussionID string, body string) error
	CreateMergeRequestDiscussionFn  func(ctx context.Context, projectID, mrIID int, body string, pos *MRCommentPosition) error
	GetMergeRequestDiffRefsFn       func(ctx context.Context, projectID, mrIID int) (MRDiffRefs, error)
	ListBranchesFn                  func(ctx context.Context, projectID int, search string) ([]string, error)
	CreateMergeRequestFn            func(ctx context.Context, projectID int, opts CreateMROptions) (MergeRequestSummary, error)
}

// Compile-time check: MockService implements Service.
var _ Service = (*MockService)(nil)

func (m *MockService) ListProjects(ctx context.Context, opts ProjectListOptions) (ProjectPage, error) {
	if m.ListProjectsFn != nil {
		return m.ListProjectsFn(ctx, opts)
	}
	return ProjectPage{}, nil
}

func (m *MockService) ListTree(ctx context.Context, projectID int, opts TreeListOptions) ([]TreeNode, error) {
	if m.ListTreeFn != nil {
		return m.ListTreeFn(ctx, projectID, opts)
	}
	return nil, nil
}

func (m *MockService) GetFileContent(ctx context.Context, projectID int, path, ref string) (string, error) {
	if m.GetFileContentFn != nil {
		return m.GetFileContentFn(ctx, projectID, path, ref)
	}
	return "", nil
}

func (m *MockService) LatestPipeline(ctx context.Context, projectID int, ref string) (PipelineSummary, error) {
	if m.LatestPipelineFn != nil {
		return m.LatestPipelineFn(ctx, projectID, ref)
	}
	return PipelineSummary{}, nil
}

func (m *MockService) ListPipelines(ctx context.Context, projectID int, opts PipelineListOptions) (PipelinePage, error) {
	if m.ListPipelinesFn != nil {
		return m.ListPipelinesFn(ctx, projectID, opts)
	}
	return PipelinePage{}, nil
}

func (m *MockService) PipelineStages(ctx context.Context, projectID, pipelineID int) ([]PipelineStage, error) {
	if m.PipelineStagesFn != nil {
		return m.PipelineStagesFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *MockService) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]PipelineJob, error) {
	if m.ListPipelineJobsFn != nil {
		return m.ListPipelineJobsFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *MockService) GetJobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	if m.GetJobTraceFn != nil {
		return m.GetJobTraceFn(ctx, projectID, jobID)
	}
	return "", nil
}

func (m *MockService) RetryPipeline(ctx context.Context, projectID, pipelineID int, ref string) (PipelineSummary, error) {
	if m.RetryPipelineFn != nil {
		return m.RetryPipelineFn(ctx, projectID, pipelineID, ref)
	}
	return PipelineSummary{}, nil
}

func (m *MockService) RetryJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	if m.RetryJobFn != nil {
		return m.RetryJobFn(ctx, projectID, jobID)
	}
	return PipelineJob{}, nil
}

func (m *MockService) CancelPipeline(ctx context.Context, projectID, pipelineID int) error {
	if m.CancelPipelineFn != nil {
		return m.CancelPipelineFn(ctx, projectID, pipelineID)
	}
	return nil
}

func (m *MockService) CancelJob(ctx context.Context, projectID, jobID int) error {
	if m.CancelJobFn != nil {
		return m.CancelJobFn(ctx, projectID, jobID)
	}
	return nil
}

func (m *MockService) PlayJob(ctx context.Context, projectID, jobID int) (PipelineJob, error) {
	if m.PlayJobFn != nil {
		return m.PlayJobFn(ctx, projectID, jobID)
	}
	return PipelineJob{}, nil
}

func (m *MockService) ListMergeRequests(ctx context.Context, projectID int, opts MRListOptions) (MRPage, error) {
	if m.ListMergeRequestsFn != nil {
		return m.ListMergeRequestsFn(ctx, projectID, opts)
	}
	return MRPage{}, nil
}

func (m *MockService) ListMergeRequestDiscussions(ctx context.Context, projectID, mrIID int) ([]MRDiscussion, error) {
	if m.ListMergeRequestDiscussionsFn != nil {
		return m.ListMergeRequestDiscussionsFn(ctx, projectID, mrIID)
	}
	return nil, nil
}

func (m *MockService) ListMergeRequestDiffs(ctx context.Context, projectID, mrIID int) ([]MRDiffFile, error) {
	if m.ListMergeRequestDiffsFn != nil {
		return m.ListMergeRequestDiffsFn(ctx, projectID, mrIID)
	}
	return nil, nil
}

func (m *MockService) ListPipelineBridges(ctx context.Context, projectID, pipelineID int) ([]PipelineBridge, error) {
	if m.ListPipelineBridgesFn != nil {
		return m.ListPipelineBridgesFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *MockService) GetPipelineTestReport(ctx context.Context, projectID, pipelineID int) (*TestReport, error) {
	if m.GetPipelineTestReportFn != nil {
		return m.GetPipelineTestReportFn(ctx, projectID, pipelineID)
	}
	return nil, nil
}

func (m *MockService) ListProjectCommits(ctx context.Context, projectID int, ref string, limit int) ([]CommitSummary, error) {
	if m.ListProjectCommitsFn != nil {
		return m.ListProjectCommitsFn(ctx, projectID, ref, limit)
	}
	return nil, nil
}

func (m *MockService) ResolveMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, discussionID string, resolved bool) error {
	if m.ResolveMergeRequestDiscussionFn != nil {
		return m.ResolveMergeRequestDiscussionFn(ctx, projectID, mrIID, discussionID, resolved)
	}
	return nil
}

func (m *MockService) AddMergeRequestDiscussionNote(ctx context.Context, projectID, mrIID int, discussionID string, body string) error {
	if m.AddMergeRequestDiscussionNoteFn != nil {
		return m.AddMergeRequestDiscussionNoteFn(ctx, projectID, mrIID, discussionID, body)
	}
	return nil
}

func (m *MockService) CreateMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, body string, pos *MRCommentPosition) error {
	if m.CreateMergeRequestDiscussionFn != nil {
		return m.CreateMergeRequestDiscussionFn(ctx, projectID, mrIID, body, pos)
	}
	return nil
}

func (m *MockService) GetMergeRequestDiffRefs(ctx context.Context, projectID, mrIID int) (MRDiffRefs, error) {
	if m.GetMergeRequestDiffRefsFn != nil {
		return m.GetMergeRequestDiffRefsFn(ctx, projectID, mrIID)
	}
	return MRDiffRefs{}, nil
}

func (m *MockService) ListBranches(ctx context.Context, projectID int, search string) ([]string, error) {
	if m.ListBranchesFn != nil {
		return m.ListBranchesFn(ctx, projectID, search)
	}
	return nil, nil
}

func (m *MockService) CreateMergeRequest(ctx context.Context, projectID int, opts CreateMROptions) (MergeRequestSummary, error) {
	if m.CreateMergeRequestFn != nil {
		return m.CreateMergeRequestFn(ctx, projectID, opts)
	}
	return MergeRequestSummary{}, nil
}
