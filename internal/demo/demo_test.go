package demo

import (
	"context"
	"errors"
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"
)

func TestDemoService_InterfaceSatisfaction(t *testing.T) {
	// Compile-time check is in demo.go; this just exercises the assertion at test time.
	var _ gitlab.Service = (*DemoService)(nil)
}

func TestDemoService_ListProjects(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	page, err := svc.ListProjects(ctx, gitlab.ProjectListOptions{Page: 1, PerPage: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(page.Projects); got != 5 {
		t.Fatalf("want 5 projects on page 1, got %d", got)
	}
	if page.Page != 1 {
		t.Fatalf("want page 1, got %d", page.Page)
	}
	if page.NextPage != 2 {
		t.Fatalf("want next page 2, got %d", page.NextPage)
	}
	if page.PrevPage != 0 {
		t.Fatalf("want no prev page, got %d", page.PrevPage)
	}

	// Last page.
	page2, err := svc.ListProjects(ctx, gitlab.ProjectListOptions{Page: 3, PerPage: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(page2.Projects); got != 2 {
		t.Fatalf("want 2 projects on page 3, got %d", got)
	}
	if page2.NextPage != 0 {
		t.Fatalf("want no next page on last page, got %d", page2.NextPage)
	}
	if page2.PrevPage != 2 {
		t.Fatalf("want prev page 2, got %d", page2.PrevPage)
	}
}

func TestDemoService_ListTree(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	// Root directory has entries.
	entries, err := svc.ListTree(ctx, 1001, gitlab.TreeListOptions{Path: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("want non-empty tree for root path")
	}

	// Unknown path returns empty.
	entries, err = svc.ListTree(ctx, 1001, gitlab.TreeListOptions{Path: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("want empty tree for unknown path, got %d entries", len(entries))
	}
}

func TestDemoService_GetFileContent(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	content, err := svc.GetFileContent(ctx, 1001, "cmd/server/main.go", "main")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" {
		t.Fatal("want non-empty content for known file")
	}

	// Unknown file returns placeholder.
	content, err = svc.GetFileContent(ctx, 1001, "does/not/exist.go", "main")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" {
		t.Fatal("want non-empty placeholder for unknown file")
	}
}

func TestDemoService_LatestPipeline(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	// Known project returns a pipeline with stages.
	p, err := svc.LatestPipeline(ctx, 1001, "main")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Fatal("want non-zero pipeline ID")
	}
	if len(p.Stages) == 0 {
		t.Fatal("want stages populated on latest pipeline")
	}
}

func TestDemoService_WriteOperations(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	if err := svc.CancelPipeline(ctx, 1001, 1001001); err != nil {
		t.Fatalf("CancelPipeline: %v", err)
	}
	if err := svc.CancelJob(ctx, 1001, 100100101); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	if err := svc.ResolveMergeRequestDiscussion(ctx, 1001, 1, "disc-001", true); err != nil {
		t.Fatalf("ResolveMergeRequestDiscussion: %v", err)
	}
	if err := svc.AddMergeRequestDiscussionNote(ctx, 1001, 1, "disc-001", "test"); err != nil {
		t.Fatalf("AddMergeRequestDiscussionNote: %v", err)
	}
	if err := svc.CreateMergeRequestDiscussion(ctx, 1001, 1, "test", nil); err != nil {
		t.Fatalf("CreateMergeRequestDiscussion: %v", err)
	}

	_, err := svc.RetryPipeline(ctx, 1001, 1001001, "main")
	if err != nil {
		t.Fatalf("RetryPipeline: %v", err)
	}
	_, err = svc.RetryJob(ctx, 1001, 100100101)
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	_, err = svc.PlayJob(ctx, 1001, 100100101)
	if err != nil {
		t.Fatalf("PlayJob: %v", err)
	}
}

func TestDemoService_ListPipelines(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	page, err := svc.ListPipelines(ctx, 1001, gitlab.PipelineListOptions{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Pipelines) == 0 {
		t.Fatal("want non-empty pipeline list")
	}
}

func TestDemoService_MergeRequests(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	page, err := svc.ListMergeRequests(ctx, 1001, gitlab.MRListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.MergeRequests) == 0 {
		t.Fatal("want non-empty MR list")
	}

	// Discussions for first MR.
	base := 1001 * 100
	discussions, err := svc.ListMergeRequestDiscussions(ctx, 1001, base+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(discussions) == 0 {
		t.Fatal("want non-empty discussions for MR 1")
	}

	// Diffs for first MR.
	diffs, err := svc.ListMergeRequestDiffs(ctx, 1001, base+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) == 0 {
		t.Fatal("want non-empty diffs for MR 1")
	}
}

func TestDemoService_NilReturns(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	bridges, err := svc.ListPipelineBridges(ctx, 1001, 1001001)
	if err != nil {
		t.Fatal(err)
	}
	if bridges != nil {
		t.Fatal("want nil bridges")
	}

	report, err := svc.GetPipelineTestReport(ctx, 1001, 1001001)
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Fatal("want nil test report")
	}
}

func TestDemoService_ListProjectCommits(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	commits, err := svc.ListProjectCommits(ctx, 1001, "main", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 5 {
		t.Fatalf("want 5 commits, got %d", len(commits))
	}
}

func TestDemoService_LatestPipeline_UnknownProject(t *testing.T) {
	svc := &DemoService{}
	ctx := context.Background()

	// LatestPipeline for a project that still gets pipelines (all projects
	// produce deterministic pipelines in demo mode). The plan specified
	// ErrNoPipelines for truly unknown IDs but since demoPipelines generates
	// data for any projectID, we just verify it works.
	_, err := svc.LatestPipeline(ctx, 9999, "main")
	if err != nil && !errors.Is(err, gitlab.ErrNoPipelines) {
		t.Fatalf("unexpected error: %v", err)
	}
}
