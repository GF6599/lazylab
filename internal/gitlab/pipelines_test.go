package gitlab

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestPipelineSummary_NilPipeline: a nil *gl.Pipeline maps to the zero PipelineSummary.
// Given a nil pipeline pointer, when pipelineSummary converts it, then ID,
// Status, Ref, and User all stay at their zero values instead of panicking.
// Why it matters: the retry and get paths can produce a nil pipeline on odd
// API responses; a nil dereference in this mapper would crash the TUI.
func TestPipelineSummary_NilPipeline(t *testing.T) {
	// When/Then: a nil pipeline converts to the zero value without panicking.
	got := pipelineSummary(nil)
	if got.ID != 0 || got.Status != "" || got.Ref != "" || got.User != "" {
		t.Errorf("pipelineSummary(nil) should return zero value, got %+v", got)
	}
}

// TestListPipelines_Success: a pipeline page maps rows with status, ref, and source intact.
// Given a canned two-pipeline page served from /projects/1/pipelines, when
// ListPipelines runs, then both rows return and the first keeps ID 100,
// status success, ref main, and source push.
// Why it matters: the pipelines panel sorts and colours rows by exactly these
// fields; a mapping slip would render wrong statuses for every run.
func TestListPipelines_Success(t *testing.T) {
	// Given: a server answering the pipelines path with the two-row fixture.
	data, err := os.ReadFile("testdata/pipelines.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/pipelines" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Next-Page", "")
		w.Header().Set("X-Total-Pages", "1")
		w.Write(data)
	}))

	// When: listing the first page of pipelines.
	page, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}

	// Then: both rows map with their display fields intact.
	if len(page.Pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %d", len(page.Pipelines))
	}
	p := page.Pipelines[0]
	if p.ID != 100 || p.Status != "success" || p.Ref != "main" {
		t.Errorf("unexpected pipeline: %+v", p)
	}
	if p.Source != "push" {
		t.Errorf("expected source=push, got %q", p.Source)
	}
}

// TestListPipelines_EmptyPage1: an empty first page means the project has no pipelines at all.
// Given a server returning an empty array for page one, when ListPipelines
// runs, then the error matches ErrNoPipelines.
// Why it matters: the UI turns this sentinel into a "no CI runs" empty state;
// returning an empty page instead would render a blank panel with no
// explanation.
func TestListPipelines_EmptyPage1(t *testing.T) {
	// Given: a server answering page one with an empty list.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	// When/Then: listing page one yields the ErrNoPipelines sentinel.
	_, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 1})
	if !errors.Is(err, ErrNoPipelines) {
		t.Fatalf("expected ErrNoPipelines, got %v", err)
	}
}

// TestListPipelines_EmptyPage2: an empty later page is a normal end of results, not an error.
// Given a server returning an empty array for page two, when ListPipelines
// runs, then it returns an empty page with a nil error.
// Why it matters: paging past the last pipeline is routine navigation;
// raising ErrNoPipelines there would flash an error on a project that plainly
// has pipelines.
func TestListPipelines_EmptyPage2(t *testing.T) {
	// Given: a server whose page two is empty but well-formed.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "2")
		w.Header().Set("X-Total-Pages", "2")
		w.Write([]byte("[]"))
	}))

	// When: listing page two.
	page, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 2})
	// Then: the empty page comes back without an error.
	if err != nil {
		t.Fatalf("expected no error for empty page 2, got %v", err)
	}
	if len(page.Pipelines) != 0 {
		t.Fatalf("expected 0 pipelines, got %d", len(page.Pipelines))
	}
}

// TestLatestPipeline_Success: the newest pipeline is returned with its stages pre-aggregated.
// Given canned pipeline and job listings, when LatestPipeline runs with no
// ref filter, then the summary carries pipeline ID 100 plus the three stages
// folded from its jobs.
// Why it matters: the project list's status badge comes from this call; a
// regression in the two-request stitch (pipeline list, then its jobs) would
// show stale or stageless statuses.
func TestLatestPipeline_Success(t *testing.T) {
	// Given: a server routing the pipelines list and the jobs list by path.
	pipelinesData, _ := os.ReadFile("testdata/pipelines.json")
	jobsData, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v4/projects/1/pipelines" && r.Method == "GET":
			w.Write(pipelinesData)
		case r.URL.Path == "/api/v4/projects/1/pipelines/100/jobs" && r.Method == "GET":
			w.Write(jobsData)
		default:
			http.NotFound(w, r)
		}
	}))

	// When: fetching the latest pipeline across all refs.
	summary, err := client.LatestPipeline(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("LatestPipeline: %v", err)
	}

	// Then: the newest pipeline arrives with its stages aggregated.
	if summary.ID != 100 {
		t.Errorf("expected pipeline ID=100, got %d", summary.ID)
	}
	if len(summary.Stages) != 3 {
		t.Errorf("expected 3 stages, got %d", len(summary.Stages))
	}
}

// TestLatestPipeline_NoPipelines: a project with no CI history surfaces ErrNoPipelines.
// Given a server returning an empty pipeline list, when LatestPipeline runs,
// then the error matches ErrNoPipelines via errors.Is.
// Why it matters: projects without CI hit this path on every refresh; mapping
// it to a generic error would spam failure banners across the project list.
func TestLatestPipeline_NoPipelines(t *testing.T) {
	// Given: a server answering with an empty pipeline list.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	// When/Then: the lookup yields the ErrNoPipelines sentinel.
	_, err := client.LatestPipeline(context.Background(), 1, "main")
	if !errors.Is(err, ErrNoPipelines) {
		t.Fatalf("expected ErrNoPipelines, got %v", err)
	}
}

// TestCollectPipelineStages_Aggregation: jobs fold into per-stage statuses in declaration order.
// Given the three-job fixture spanning build, test, and deploy, when
// PipelineStages runs, then three stages return with build reporting success
// and deploy reporting failed.
// Why it matters: stage badges are computed from this fold; a grouping or
// priority bug would hide the failed deploy stage behind its passing
// siblings.
func TestCollectPipelineStages_Aggregation(t *testing.T) {
	// Given: a server answering with the three-job fixture.
	jobsData, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(jobsData)
	}))

	// When: aggregating the pipeline's stages.
	stages, err := client.PipelineStages(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("PipelineStages: %v", err)
	}

	// Then: three stages return in order with their folded statuses.
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	if stages[0].Name != "build" || stages[0].Status != "success" {
		t.Errorf("stage[0] = %+v, want build/success", stages[0])
	}
	if stages[2].Name != "deploy" || stages[2].Status != "failed" {
		t.Errorf("stage[2] = %+v, want deploy/failed", stages[2])
	}
}

// TestCancelPipeline_Success: cancelling a pipeline POSTs to the cancel endpoint and reports success.
// Given a server expecting POST /projects/1/pipelines/100/cancel, when
// CancelPipeline runs, then it returns nil.
// Why it matters: cancel is a mutating call bound to a hotkey; a wrong path
// or method would no-op while the user believes the pipeline is stopping.
func TestCancelPipeline_Success(t *testing.T) {
	// Given: a cancel endpoint that checks the method and path.
	pipelinesData, _ := os.ReadFile("testdata/pipelines.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v4/projects/1/pipelines/100/cancel" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Stripping the fixture's array brackets leaves the first pipeline
		// object readable: the JSON decoder stops after the first value.
		w.Write(pipelinesData[1 : len(pipelinesData)-1])
	}))

	// When/Then: cancelling succeeds without error.
	err := client.CancelPipeline(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("CancelPipeline: %v", err)
	}
}

// TestGetPipelineTestReport_Success: a JUnit test report maps with counts, suites, and cases.
// Given a canned report fixture, when GetPipelineTestReport runs, then the
// report carries TotalCount 3, FailedCount 1, two suites, and the unit
// suite's two cases.
// Why it matters: the test-report view aggregates these counters; a mapping
// slip would misreport how many tests failed in a pipeline.
func TestGetPipelineTestReport_Success(t *testing.T) {
	// Given: a server answering with the canned test report.
	data, _ := os.ReadFile("testdata/test_report.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: fetching the pipeline's test report.
	report, err := client.GetPipelineTestReport(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("GetPipelineTestReport: %v", err)
	}

	// Then: totals, suites, and cases all map through.
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.TotalCount != 3 {
		t.Errorf("expected TotalCount=3, got %d", report.TotalCount)
	}
	if report.FailedCount != 1 {
		t.Errorf("expected FailedCount=1, got %d", report.FailedCount)
	}
	if len(report.Suites) != 2 {
		t.Fatalf("expected 2 suites, got %d", len(report.Suites))
	}
	if report.Suites[0].Name != "unit" {
		t.Errorf("expected first suite name=unit, got %q", report.Suites[0].Name)
	}
	if len(report.Suites[0].Cases) != 2 {
		t.Errorf("expected 2 test cases in unit suite, got %d", len(report.Suites[0].Cases))
	}
}

// TestListPipelineBridges_WithDownstream: bridge jobs map with their downstream pipeline link when present.
// Given a canned response with one triggered bridge and one whose downstream
// is absent, when ListPipelineBridges runs, then the first bridge carries its
// name, status, and downstream pipeline ID 200, and the second keeps a nil
// DownstreamPipeline.
// Why it matters: child-pipeline navigation follows DownstreamPipeline; a
// nil-handling bug would either crash on untriggered bridges or invent
// downstreams that do not exist.
func TestListPipelineBridges_WithDownstream(t *testing.T) {
	// Given: a server answering with the two-bridge fixture.
	data, _ := os.ReadFile("testdata/bridges.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing the pipeline's bridges.
	bridges, err := client.ListPipelineBridges(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("ListPipelineBridges: %v", err)
	}
	if len(bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(bridges))
	}

	// Then: the triggered bridge maps its fields and downstream link.
	b0 := bridges[0]
	if b0.Name != "trigger-child" || b0.Status != "success" {
		t.Errorf("bridge[0] = %+v", b0)
	}
	if b0.DownstreamPipeline == nil {
		t.Fatal("expected non-nil DownstreamPipeline for bridge[0]")
	}
	if b0.DownstreamPipeline.ID != 200 {
		t.Errorf("expected downstream ID=200, got %d", b0.DownstreamPipeline.ID)
	}

	// And: the bridge without a downstream stays nil.
	if bridges[1].DownstreamPipeline != nil {
		t.Error("expected nil DownstreamPipeline for bridge[1]")
	}
}

// TestListPipelineBridges_FollowsEveryPage: bridges spanning multiple pages all come back.
// Given a server splitting three bridges across two pages, when ListPipelineBridges runs, then both
// pages are fetched and all three bridges return in ID order.
// Why it matters: a parent pipeline fanning out to more than one page of downstream triggers would
// silently lose the tail, and the stages panel would show a pipeline smaller than the real one.
func TestListPipelineBridges_FollowsEveryPage(t *testing.T) {
	// Given: a server answering page 1 with a next-page header and page 2 without one
	page1, err := os.ReadFile("testdata/bridges.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	page2, err := os.ReadFile("testdata/bridges_page2.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.Header().Set("X-Page", "2")
			w.Write(page2)
			return
		}
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Next-Page", "2")
		w.Header().Set("X-Total-Pages", "2")
		w.Write(page1)
	}))

	// When: listing the pipeline's bridges
	bridges, err := client.ListPipelineBridges(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("ListPipelineBridges: %v", err)
	}

	// Then: both pages came back in ID order
	if len(bridges) != 3 {
		t.Fatalf("expected 3 bridges across two pages, got %d", len(bridges))
	}
	for i, wantID := range []int{2001, 2002, 2003} {
		if bridges[i].ID != wantID {
			t.Errorf("bridge[%d].ID = %d, want %d", i, bridges[i].ID, wantID)
		}
	}
}

// TestGetPipeline_CarriesTheStartTime: the single-pipeline fetch brings back the moment a run
// began.
// Given a server answering with one pipeline that started at a known time, when GetPipeline runs,
// then the summary carries that start time and the pipeline's own identity.
// Why it matters: the pipelines list is built from a lighter type that carries no start time at
// all, so this call is the only source for how long a run has been going.
func TestGetPipeline_CarriesTheStartTime(t *testing.T) {
	// Given: a server answering with one pipeline that started at a known time
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/pipelines/100" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":100,"status":"running","ref":"main",
			"started_at":"2026-08-09T12:00:00Z","created_at":"2026-08-09T11:59:00Z"}`))
	}))

	// When: the pipeline is fetched
	summary, err := client.GetPipeline(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("GetPipeline: %v", err)
	}

	// Then: it carries the start time, and the pipeline it describes
	want := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if !summary.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", summary.StartedAt, want)
	}
	if summary.ID != 100 || summary.Status != "running" {
		t.Errorf("got pipeline %d in status %q, want 100 running", summary.ID, summary.Status)
	}
}

// TestGetPipeline_ReportsAFailedFetch: a fetch that fails says so rather than returning a blank
// pipeline.
// Given a server answering 500, when GetPipeline runs, then it returns an error.
// Why it matters: this runs on the refresh path, and a blank summary returned as success would
// reset the start time on screen to nothing every time the call failed.
func TestGetPipeline_ReportsAFailedFetch(t *testing.T) {
	// Given: a server that fails the fetch
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	// When: the pipeline is fetched
	_, err := client.GetPipeline(context.Background(), 1, 100)

	// Then: the failure is reported
	if err == nil {
		t.Error("a failed fetch returned no error, so a blank pipeline would read as success")
	}
}
