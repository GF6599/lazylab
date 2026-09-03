package demo

import (
	"context"
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestDemoService_ListProjects: paging the demo project set at five per page
// yields full pages with forward links and a final short page linking back.
// Given a zero-value demo service over the twelve canned projects, when page 1
// and page 3 are requested at five per page, then page 1 holds five projects
// with next page 2 and no previous, and page 3 holds the remaining two with
// previous page 2 and no next.
// Why it matters: the TUI's project list drives its paging keys off these
// fields, so wrong links would strand demo users on a page or point them at
// pages that do not exist.
func TestDemoService_ListProjects(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: page 1 is requested at five per page.
	page, err := svc.ListProjects(ctx, gitlab.ProjectListOptions{Page: 1, PerPage: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Then: it holds five projects and links forward but not back.
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

	// And: the last page holds the two leftover projects and links back but
	// not forward.
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

// TestDemoService_ListTree: the demo file tree has entries at the root, and an
// unknown path lists as empty rather than failing.
// Given a zero-value demo service, when the root path and a nonexistent path
// of project 1001 are listed, then the root yields a non-empty tree and the
// unknown path yields zero entries, both without error.
// Why it matters: the explorer panel renders straight from this listing, so an
// empty root would boot demo mode into a blank explorer, and an error on a
// missing path would surface a failure state instead of an empty directory.
func TestDemoService_ListTree(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: the root directory of a known project is listed.
	entries, err := svc.ListTree(ctx, 1001, gitlab.TreeListOptions{Path: ""})
	if err != nil {
		t.Fatal(err)
	}
	// Then: it has entries.
	if len(entries) == 0 {
		t.Fatal("want non-empty tree for root path")
	}

	// And: an unknown path lists as empty without an error.
	entries, err = svc.ListTree(ctx, 1001, gitlab.TreeListOptions{Path: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("want empty tree for unknown path, got %d entries", len(entries))
	}
}

// TestDemoService_GetFileContent: a known demo file returns its canned content,
// and an unknown path returns placeholder text instead of an empty string.
// Given a zero-value demo service, when a known file and a nonexistent path
// are fetched from project 1001, then both calls return non-empty content
// without error, the second being the placeholder text.
// Why it matters: the preview pane shows this string verbatim, so an empty
// return would leave demo users staring at a blank preview instead of file
// content or a placeholder naming the missing path.
func TestDemoService_GetFileContent(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: a file that exists in the demo data is fetched.
	content, err := svc.GetFileContent(ctx, 1001, "cmd/server/main.go", "main")
	if err != nil {
		t.Fatal(err)
	}
	// Then: its content is non-empty.
	if content == "" {
		t.Fatal("want non-empty content for known file")
	}

	// And: an unknown file still yields non-empty placeholder text.
	content, err = svc.GetFileContent(ctx, 1001, "does/not/exist.go", "main")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" {
		t.Fatal("want non-empty placeholder for unknown file")
	}
}

// TestDemoService_LatestPipeline: the newest demo pipeline for a known project
// arrives with a real ID and its stages already attached.
// Given a zero-value demo service, when the latest pipeline for project 1001
// on main is fetched, then it carries a non-zero ID and a populated stage list.
// Why it matters: the pipeline panel's headline row and stage lane render from
// this call, so a zero ID or missing stages would leave demo recordings
// showing an empty pipeline view.
func TestDemoService_LatestPipeline(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: the latest pipeline for a known project is fetched.
	p, err := svc.LatestPipeline(ctx, 1001, "main")
	if err != nil {
		t.Fatal(err)
	}
	// Then: it has a real ID and stages attached.
	if p.ID == 0 {
		t.Fatal("want non-zero pipeline ID")
	}
	if len(p.Stages) == 0 {
		t.Fatal("want stages populated on latest pipeline")
	}
}

// TestDemoService_WriteOperations: every demo write, from cancels to retries,
// reports success.
// Given a zero-value demo service, when each pipeline and merge request write
// method is invoked once, then none of them returns an error.
// Why it matters: the TUI surfaces write failures to the user, so a demo write
// returning an error would interrupt recordings and offline walkthroughs with
// a failure message for actions that are meant to no-op.
func TestDemoService_WriteOperations(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When/Then: the no-op writes all succeed.
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

	// And: the writes that fabricate a result succeed too.
	_, err := svc.RetryPipeline(ctx, 1001, 1001001, "main")
	if err != nil {
		t.Fatalf("RetryPipeline: %v", err)
	}
	_, err = svc.RetryJob(ctx, 1001, 100100101)
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	_, err = svc.PlayJob(ctx, 1001, 100100101, nil)
	if err != nil {
		t.Fatalf("PlayJob: %v", err)
	}
}

// TestDemoService_ListPipelines: the first page of demo pipelines for a known
// project is not empty.
// Given a zero-value demo service, when page 1 of pipelines for project 1001
// is listed, then at least one pipeline comes back without error.
// Why it matters: the pipelines panel is a core demo surface, so an empty
// first page would boot demo mode into a blank pipeline list.
func TestDemoService_ListPipelines(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: the first pipeline page is listed.
	page, err := svc.ListPipelines(ctx, 1001, gitlab.PipelineListOptions{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	// Then: it is non-empty.
	if len(page.Pipelines) == 0 {
		t.Fatal("want non-empty pipeline list")
	}
}

// TestDemoService_MergeRequests: a known demo project serves merge requests,
// and its first MR serves discussions and diffs.
// Given a zero-value demo service whose MR IIDs follow projectID*100+n, when
// the MR list, the first MR's discussions, and its diffs are fetched for
// project 1001, then every list is non-empty.
// Why it matters: the MR panel, its review threads, and its diff view all draw
// on these calls, so an empty result would hollow out the merge request half
// of demo recordings and offline onboarding.
func TestDemoService_MergeRequests(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: the MR list for a known project is fetched.
	page, err := svc.ListMergeRequests(ctx, 1001, gitlab.MRListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatal(err)
	}
	// Then: it is non-empty.
	if len(page.MergeRequests) == 0 {
		t.Fatal("want non-empty MR list")
	}

	// And: the first MR (IID base+1 under the projectID*100 scheme) has
	// discussions.
	base := 1001 * 100
	discussions, err := svc.ListMergeRequestDiscussions(ctx, 1001, base+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(discussions) == 0 {
		t.Fatal("want non-empty discussions for MR 1")
	}

	// And: the same MR has diffs.
	diffs, err := svc.ListMergeRequestDiffs(ctx, 1001, base+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) == 0 {
		t.Fatal("want non-empty diffs for MR 1")
	}
}

// TestDemoService_NilReturns: pipeline bridges and the pipeline test report are
// absent from demo data, returned as nil with no error.
// Given a zero-value demo service, when bridges and the test report are
// fetched for a demo pipeline, then both come back nil without error.
// Why it matters: the TUI reads nil as nothing-to-show and an error as a
// failure, so fabricated bridges would invent downstream pipelines in the UI
// and an error would flash a spurious failure in demo mode.
func TestDemoService_NilReturns(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: pipeline bridges are fetched.
	bridges, err := svc.ListPipelineBridges(ctx, 1001, 1001001)
	if err != nil {
		t.Fatal(err)
	}
	// Then: they are nil.
	if bridges != nil {
		t.Fatal("want nil bridges")
	}

	// And: the test report is nil too.
	report, err := svc.GetPipelineTestReport(ctx, 1001, 1001001)
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Fatal("want nil test report")
	}
}

// TestDemoService_ListProjectCommits: a project's demo commit history is
// exactly the five canned commits.
// Given a zero-value demo service, whose commit fixture ignores the ref and
// limit arguments, when project 1001's commits are listed, then exactly five
// commits come back without error.
// Why it matters: the project view's commit strip renders this list, so a
// drifted fixture would silently change what demo recordings and offline
// walkthroughs show.
//
// The count pins the fixture's size, not limit handling: the demo service
// ignores the limit argument, which merely happens to match the five entries.
func TestDemoService_ListProjectCommits(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: commits are listed for a known project.
	commits, err := svc.ListProjectCommits(ctx, 1001, "main", 5)
	if err != nil {
		t.Fatal(err)
	}
	// Then: the full five-commit fixture comes back.
	if len(commits) != 5 {
		t.Fatalf("want 5 commits, got %d", len(commits))
	}
}

// TestDemoService_LatestPipeline_UnknownProject: a project outside the demo
// set still gets a fabricated latest pipeline rather than an error.
// Given a zero-value demo service, whose pipeline generator fabricates data
// for any project ID, when the latest pipeline for unknown project 9999 is
// fetched, then the call succeeds with a populated pipeline.
// Why it matters: the projects panel polls LatestPipeline for whichever row
// is selected, so an error here would blank the status column that demo mode
// exists to show off.
func TestDemoService_LatestPipeline_UnknownProject(t *testing.T) {
	// Given: a zero-value demo service.
	svc := &DemoService{}
	ctx := context.Background()

	// When: the latest pipeline is fetched for a project ID outside the
	// demo set.
	pipeline, err := svc.LatestPipeline(ctx, 9999, "main")
	// Then: the call succeeds with a fabricated pipeline.
	if err != nil {
		t.Fatalf("LatestPipeline(9999) error: %v", err)
	}
	if pipeline.ID == 0 {
		t.Error("expected a fabricated pipeline with a non-zero ID")
	}
}
