package gitlab

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestPipelineSummary_NilPipeline(t *testing.T) {
	got := pipelineSummary(nil)
	if got.ID != 0 || got.Status != "" || got.Ref != "" || got.User != "" {
		t.Errorf("pipelineSummary(nil) should return zero value, got %+v", got)
	}
}

func TestListPipelines_Success(t *testing.T) {
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

	page, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
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

func TestListPipelines_EmptyPage1(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	_, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 1})
	if !errors.Is(err, ErrNoPipelines) {
		t.Fatalf("expected ErrNoPipelines, got %v", err)
	}
}

func TestListPipelines_EmptyPage2(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "2")
		w.Header().Set("X-Total-Pages", "2")
		w.Write([]byte("[]"))
	}))

	page, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 2})
	if err != nil {
		t.Fatalf("expected no error for empty page 2, got %v", err)
	}
	if len(page.Pipelines) != 0 {
		t.Fatalf("expected 0 pipelines, got %d", len(page.Pipelines))
	}
}

func TestLatestPipeline_Success(t *testing.T) {
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

	summary, err := client.LatestPipeline(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("LatestPipeline: %v", err)
	}
	if summary.ID != 100 {
		t.Errorf("expected pipeline ID=100, got %d", summary.ID)
	}
	if len(summary.Stages) != 3 {
		t.Errorf("expected 3 stages, got %d", len(summary.Stages))
	}
}

func TestLatestPipeline_NoPipelines(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	_, err := client.LatestPipeline(context.Background(), 1, "main")
	if !errors.Is(err, ErrNoPipelines) {
		t.Fatalf("expected ErrNoPipelines, got %v", err)
	}
}

func TestCollectPipelineStages_Aggregation(t *testing.T) {
	jobsData, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(jobsData)
	}))

	stages, err := client.PipelineStages(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("PipelineStages: %v", err)
	}
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	// Build stage should be success
	if stages[0].Name != "build" || stages[0].Status != "success" {
		t.Errorf("stage[0] = %+v, want build/success", stages[0])
	}
	// Deploy stage should be failed
	if stages[2].Name != "deploy" || stages[2].Status != "failed" {
		t.Errorf("stage[2] = %+v, want deploy/failed", stages[2])
	}
}

func TestCancelPipeline_Success(t *testing.T) {
	pipelinesData, _ := os.ReadFile("testdata/pipelines.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v4/projects/1/pipelines/100/cancel" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Return the first pipeline object
		w.Write(pipelinesData[1 : len(pipelinesData)-1]) // extract first object
	}))

	err := client.CancelPipeline(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("CancelPipeline: %v", err)
	}
}

func TestGetPipelineTestReport_Success(t *testing.T) {
	data, _ := os.ReadFile("testdata/test_report.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	report, err := client.GetPipelineTestReport(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("GetPipelineTestReport: %v", err)
	}
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

func TestListPipelineBridges_WithDownstream(t *testing.T) {
	data, _ := os.ReadFile("testdata/bridges.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	bridges, err := client.ListPipelineBridges(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("ListPipelineBridges: %v", err)
	}
	if len(bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(bridges))
	}
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
	// Second bridge has no downstream
	if bridges[1].DownstreamPipeline != nil {
		t.Error("expected nil DownstreamPipeline for bridge[1]")
	}
}
