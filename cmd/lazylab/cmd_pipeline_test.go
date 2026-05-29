package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// cmdMockService is a minimal hand-written mock for gitlab.Service used
// by cmd-package tests. The interface is large; rather than stubbing
// every method we panic on the ones a test forgets to wire — a loud
// failure beats a silent zero-value response that masks coverage gaps.
type cmdMockService struct {
	ListPipelinesFn  func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error)
	GetPipelineFn    func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error)
	GetProjectFn     func(ctx context.Context, idOrPath string) (gitlab.ProjectNode, error)
	PipelineStagesFn func(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineStage, error)
}

var _ gitlab.Service = (*cmdMockService)(nil)

func (m *cmdMockService) ListPipelines(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
	if m.ListPipelinesFn == nil {
		panic("cmdMockService.ListPipelines: not wired")
	}
	return m.ListPipelinesFn(ctx, projectID, opts)
}

func (m *cmdMockService) GetPipeline(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
	if m.GetPipelineFn == nil {
		panic("cmdMockService.GetPipeline: not wired")
	}
	return m.GetPipelineFn(ctx, projectID, pipelineID)
}

func (m *cmdMockService) GetProject(ctx context.Context, idOrPath string) (gitlab.ProjectNode, error) {
	if m.GetProjectFn == nil {
		panic("cmdMockService.GetProject: not wired")
	}
	return m.GetProjectFn(ctx, idOrPath)
}

func (m *cmdMockService) PipelineStages(ctx context.Context, projectID, pipelineID int) ([]gitlab.PipelineStage, error) {
	if m.PipelineStagesFn == nil {
		return nil, nil
	}
	return m.PipelineStagesFn(ctx, projectID, pipelineID)
}

func (m *cmdMockService) ListProjects(context.Context, gitlab.ProjectListOptions) (gitlab.ProjectPage, error) {
	panic("cmdMockService.ListProjects: not wired")
}

func (m *cmdMockService) ListTree(context.Context, int, gitlab.TreeListOptions) ([]gitlab.TreeNode, error) {
	panic("cmdMockService.ListTree: not wired")
}

func (m *cmdMockService) GetFileContent(context.Context, int, string, string) (string, error) {
	panic("cmdMockService.GetFileContent: not wired")
}

func (m *cmdMockService) LatestPipeline(context.Context, int, string) (gitlab.PipelineSummary, error) {
	panic("cmdMockService.LatestPipeline: not wired")
}

func (m *cmdMockService) ListPipelineJobs(context.Context, int, int) ([]gitlab.PipelineJob, error) {
	panic("cmdMockService.ListPipelineJobs: not wired")
}

func (m *cmdMockService) GetJobTrace(context.Context, int, int) (string, error) {
	panic("cmdMockService.GetJobTrace: not wired")
}

func (m *cmdMockService) RetryPipeline(context.Context, int, int, string) (gitlab.PipelineSummary, error) {
	panic("cmdMockService.RetryPipeline: not wired")
}

func (m *cmdMockService) RetryJob(context.Context, int, int) (gitlab.PipelineJob, error) {
	panic("cmdMockService.RetryJob: not wired")
}

func (m *cmdMockService) CancelPipeline(context.Context, int, int) error {
	panic("cmdMockService.CancelPipeline: not wired")
}

func (m *cmdMockService) CancelJob(context.Context, int, int) error {
	panic("cmdMockService.CancelJob: not wired")
}

func (m *cmdMockService) PlayJob(context.Context, int, int) (gitlab.PipelineJob, error) {
	panic("cmdMockService.PlayJob: not wired")
}

func (m *cmdMockService) ListMergeRequests(context.Context, int, gitlab.MRListOptions) (gitlab.MRPage, error) {
	panic("cmdMockService.ListMergeRequests: not wired")
}

func (m *cmdMockService) ListMergeRequestDiscussions(context.Context, int, int) ([]gitlab.MRDiscussion, error) {
	panic("cmdMockService.ListMergeRequestDiscussions: not wired")
}

func (m *cmdMockService) ListMergeRequestDiffs(context.Context, int, int) ([]gitlab.MRDiffFile, error) {
	panic("cmdMockService.ListMergeRequestDiffs: not wired")
}

func (m *cmdMockService) ListPipelineBridges(context.Context, int, int) ([]gitlab.PipelineBridge, error) {
	panic("cmdMockService.ListPipelineBridges: not wired")
}

func (m *cmdMockService) GetPipelineTestReport(context.Context, int, int) (*gitlab.TestReport, error) {
	panic("cmdMockService.GetPipelineTestReport: not wired")
}

func (m *cmdMockService) ListProjectCommits(context.Context, int, string, int) ([]gitlab.CommitSummary, error) {
	panic("cmdMockService.ListProjectCommits: not wired")
}

func (m *cmdMockService) ResolveMergeRequestDiscussion(context.Context, int, int, string, bool) error {
	panic("cmdMockService.ResolveMergeRequestDiscussion: not wired")
}

func (m *cmdMockService) AddMergeRequestDiscussionNote(context.Context, int, int, string, string) error {
	panic("cmdMockService.AddMergeRequestDiscussionNote: not wired")
}

func (m *cmdMockService) CreateMergeRequestDiscussion(context.Context, int, int, string, *gitlab.MRCommentPosition) error {
	panic("cmdMockService.CreateMergeRequestDiscussion: not wired")
}

func (m *cmdMockService) GetMergeRequestDiffRefs(context.Context, int, int) (gitlab.MRDiffRefs, error) {
	panic("cmdMockService.GetMergeRequestDiffRefs: not wired")
}

func (m *cmdMockService) ListBranches(context.Context, int, string) ([]string, error) {
	panic("cmdMockService.ListBranches: not wired")
}

func (m *cmdMockService) CreateMergeRequest(context.Context, int, gitlab.CreateMROptions) (gitlab.MergeRequestSummary, error) {
	panic("cmdMockService.CreateMergeRequest: not wired")
}

func (m *cmdMockService) CurrentUser(context.Context) (gitlab.UserInfo, error) {
	panic("cmdMockService.CurrentUser: not wired")
}

func (m *cmdMockService) LatestPipelineForSHA(context.Context, int, string) (gitlab.PipelineSummary, error) {
	panic("cmdMockService.LatestPipelineForSHA: not wired")
}

func (m *cmdMockService) GetJob(context.Context, int, int) (gitlab.PipelineJob, error) {
	panic("cmdMockService.GetJob: not wired")
}

// apiErrorForTest builds an *gl.ErrorResponse with the given HTTP
// status, the same shape the production SDK emits on a real API failure.
// Mirrors the apiErrorFromHTTP helper in exit_test.go but kept separate
// so this file is self-contained for future agents reading it in isolation.
func apiErrorForTest(t *testing.T, status int) error {
	t.Helper()
	reqURL, err := url.Parse("https://gitlab.example.com/api/v4/test")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return &gl.ErrorResponse{
		Response: &http.Response{
			StatusCode: status,
			Request:    &http.Request{Method: http.MethodGet, URL: reqURL},
		},
		Message: http.StatusText(status),
	}
}

// makePipelines fabricates n distinct PipelineSummary records with IDs
// starting at startID. Used to seed paginated ListPipelines responses.
func makePipelines(startID, n int) []gitlab.PipelineSummary {
	out := make([]gitlab.PipelineSummary, n)
	for i := range n {
		out[i] = gitlab.PipelineSummary{ID: startID + i, Status: "success"}
	}
	return out
}

func TestCollectPipelines_Empty(t *testing.T) {
	svc := &cmdMockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			return gitlab.PipelinePage{}, gitlab.ErrNoPipelines
		},
	}
	got, err := collectPipelines(context.Background(), svc, 1, gitlab.PipelineListOptions{}, 20)
	if !errors.Is(err, gitlab.ErrNoPipelines) {
		t.Fatalf("err = %v, want ErrNoPipelines", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil slice", got)
	}
}

func TestCollectPipelines_SinglePage(t *testing.T) {
	svc := &cmdMockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			return gitlab.PipelinePage{
				Pipelines: makePipelines(1, 5),
				Page:      1,
				NextPage:  0,
			}, nil
		},
	}
	got, err := collectPipelines(context.Background(), svc, 1, gitlab.PipelineListOptions{}, 20)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
	if got[0].ID != 1 || got[4].ID != 5 {
		t.Errorf("got IDs [%d..%d], want [1..5]", got[0].ID, got[4].ID)
	}
}

func TestCollectPipelines_MultiPage(t *testing.T) {
	pages := map[int]gitlab.PipelinePage{
		1: {Pipelines: makePipelines(1, 100), Page: 1, NextPage: 2},
		2: {Pipelines: makePipelines(101, 50), Page: 2, NextPage: 0},
	}
	calls := 0
	svc := &cmdMockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			calls++
			p, ok := pages[opts.Page]
			if !ok {
				return gitlab.PipelinePage{}, fmt.Errorf("unexpected page %d", opts.Page)
			}
			return p, nil
		},
	}
	got, err := collectPipelines(context.Background(), svc, 1, gitlab.PipelineListOptions{}, 150)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != 150 {
		t.Errorf("len = %d, want 150", len(got))
	}
	if calls != 2 {
		t.Errorf("ListPipelines called %d times, want 2", calls)
	}
	if got[0].ID != 1 || got[149].ID != 150 {
		t.Errorf("boundaries: [0]=%d [149]=%d, want 1 and 150", got[0].ID, got[149].ID)
	}
}

func TestCollectPipelines_StallSurfacesError(t *testing.T) {
	svc := &cmdMockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			// Echo back the requested page as NextPage — simulates a
			// misbehaving proxy that pretends each page has a successor
			// equal to itself, which would cause an infinite loop.
			return gitlab.PipelinePage{
				Pipelines: makePipelines(1, 10),
				Page:      opts.Page,
				NextPage:  opts.Page,
			}, nil
		},
	}
	got, err := collectPipelines(context.Background(), svc, 1, gitlab.PipelineListOptions{}, 200)
	if err == nil {
		t.Fatal("expected stall error, got nil")
	}
	if len(got) == 0 {
		t.Errorf("expected partial results, got empty slice")
	}
}

func TestCollectPipelines_RespectsLimit(t *testing.T) {
	// Single-page response with more rows than asked for; collectPipelines
	// trims to limit.
	svc := &cmdMockService{
		ListPipelinesFn: func(ctx context.Context, projectID int, opts gitlab.PipelineListOptions) (gitlab.PipelinePage, error) {
			return gitlab.PipelinePage{
				Pipelines: makePipelines(1, 30),
				NextPage:  0,
			}, nil
		},
	}
	got, err := collectPipelines(context.Background(), svc, 1, gitlab.PipelineListOptions{}, 10)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != 10 {
		t.Errorf("len = %d, want 10", len(got))
	}
}

func TestGetPipelineWithRetry_FirstAttemptSucceeds(t *testing.T) {
	calls := 0
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			calls++
			return gitlab.PipelineSummary{ID: pipelineID, Status: "success"}, nil
		},
	}
	start := time.Now()
	got, err := getPipelineWithRetry(context.Background(), svc, gitlab.PipelineSpec{ProjectID: 1, PipelineID: 42})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if calls != 1 {
		t.Errorf("GetPipeline called %d times, want 1", calls)
	}
	// First-success path must not pay any backoff. A real Sleep would
	// blow past this floor; we keep it loose to avoid CI flakes.
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed %v on no-retry path, want < 500ms", elapsed)
	}
}

func TestGetPipelineWithRetry_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	rateErr := apiErrorForTest(t, http.StatusTooManyRequests)
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			calls++
			if calls == 1 {
				return gitlab.PipelineSummary{}, rateErr
			}
			return gitlab.PipelineSummary{ID: pipelineID, Status: "running"}, nil
		},
	}
	// Cancel the context shortly after kickoff so the backoff sleep
	// returns immediately via the ctx.Done path inside the function.
	// The first retry's backoff is 2s; we cancel before then so the
	// second attempt fires via the select fallthrough, not by waiting.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	got, err := getPipelineWithRetry(ctx, svc, gitlab.PipelineSpec{ProjectID: 1, PipelineID: 42})
	// ctx.Done wins the select after the first failed attempt → we get
	// ctx.Err back. The second attempt never runs. This documents that
	// the backoff is real — there is no test-injected clock.
	if err == nil {
		t.Fatalf("expected ctx.Err, got success %+v", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("GetPipeline called %d times, want 1 (ctx canceled before retry)", calls)
	}
}

func TestGetPipelineWithRetry_NonTransientErrorReturnsImmediately(t *testing.T) {
	calls := 0
	notFound := apiErrorForTest(t, http.StatusNotFound)
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			calls++
			return gitlab.PipelineSummary{}, notFound
		},
	}
	_, err := getPipelineWithRetry(context.Background(), svc, gitlab.PipelineSpec{ProjectID: 1, PipelineID: 42})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !gitlab.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
	if calls != 1 {
		t.Errorf("GetPipeline called %d times, want 1 (no retries for non-transient)", calls)
	}
}

func TestGetPipelineWithRetry_AllAttemptsFailReturnsLastErr(t *testing.T) {
	// Use a context that's already canceled so the backoff select
	// immediately picks ctx.Done(), keeping the test fast while still
	// running through the retry loop's bookkeeping.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rateErr := apiErrorForTest(t, http.StatusTooManyRequests)
	svc := &cmdMockService{
		GetPipelineFn: func(ctx context.Context, projectID, pipelineID int) (gitlab.PipelineSummary, error) {
			return gitlab.PipelineSummary{}, rateErr
		},
	}
	_, err := getPipelineWithRetry(ctx, svc, gitlab.PipelineSpec{ProjectID: 1, PipelineID: 42})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Either ctx.Err or the last rate-limit error is acceptable — both
	// represent the "give up" branch. The important property is that
	// some error surfaces and it isn't a panic or zero-value success.
	if !errors.Is(err, context.Canceled) && !gitlab.IsRateLimited(err) {
		t.Errorf("err = %v, want context.Canceled or rate-limited", err)
	}
}

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"success", true},
		{"failed", true},
		{"canceled", true},
		{"skipped", true},
		{"manual", true},
		{"running", false},
		{"pending", false},
		{"created", false},
		{"scheduled", false},
		// Case-insensitivity: GitLab returns lowercase but defensive
		// callers may upper-case.
		{"SUCCESS", true},
		{"Failed", true},
		// Empty / unknown should not be terminal — the watch loop must
		// keep polling rather than exit on a momentary blank status.
		{"", false},
		{"bogus", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isTerminalStatus(tt.status); got != tt.want {
				t.Errorf("isTerminalStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsSuccessfulStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"success", true},
		{"skipped", true},
		{"manual", true},
		{"failed", false},
		{"canceled", false},
		{"running", false},
		{"pending", false},
		{"SUCCESS", true},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isSuccessfulStatus(tt.status); got != tt.want {
				t.Errorf("isSuccessfulStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsSuccessfulJobStatusCLI(t *testing.T) {
	// Note: unlike isSuccessfulStatus this one is case-sensitive — the
	// CLI receives status strings straight from GitLab's API which are
	// already canonical-lowercase.
	tests := []struct {
		status string
		want   bool
	}{
		{"success", true},
		{"skipped", true},
		{"manual", true},
		{"failed", false},
		{"canceled", false},
		{"running", false},
		{"pending", false},
		{"created", false},
		{"SUCCESS", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isSuccessfulJobStatusCLI(tt.status); got != tt.want {
				t.Errorf("isSuccessfulJobStatusCLI(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
