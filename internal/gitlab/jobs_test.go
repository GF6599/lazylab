package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestMapJob_NilJob(t *testing.T) {
	got := mapJob(nil)
	if got.ID != 0 || got.Name != "" || got.Status != "" {
		t.Errorf("mapJob(nil) should return zero PipelineJob, got %+v", got)
	}
}

func TestListPipelineJobs_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/pipeline_jobs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	jobs, err := client.ListPipelineJobs(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("ListPipelineJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}

	j := jobs[0]
	if j.ID != 1001 || j.Name != "build" || j.Stage != "build" {
		t.Errorf("unexpected job[0]: %+v", j)
	}
	if j.Duration != 30.5 {
		t.Errorf("expected duration=30.5, got %f", j.Duration)
	}
	if j.RunnerDescription != "shared-runner-1" {
		t.Errorf("expected runner=shared-runner-1, got %q", j.RunnerDescription)
	}
	if j.ArtifactsCount != 1 {
		t.Errorf("expected 1 artifact, got %d", j.ArtifactsCount)
	}
	if len(j.Artifacts) != 1 || j.Artifacts[0].Filename != "artifacts.zip" {
		t.Errorf("unexpected artifacts: %+v", j.Artifacts)
	}

	// Failed job
	j2 := jobs[2]
	if j2.FailureReason != "script_failure" {
		t.Errorf("expected failure_reason=script_failure, got %q", j2.FailureReason)
	}
}

func TestListPipelineJobs_Empty(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	_, err := client.ListPipelineJobs(context.Background(), 1, 100)
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("expected ErrNoJobs, got %v", err)
	}
}

func TestGetJobTrace_Success(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/jobs/1001/trace" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Running build...\nBuild succeeded!"))
	}))

	trace, err := client.GetJobTrace(context.Background(), 1, 1001)
	if err != nil {
		t.Fatalf("GetJobTrace: %v", err)
	}
	if trace != "Running build...\nBuild succeeded!" {
		t.Errorf("unexpected trace: %q", trace)
	}
}

func TestRetryJob_Success(t *testing.T) {
	data, _ := os.ReadFile("testdata/pipeline_jobs.json")
	// The API returns a single job object, use just the first one from array
	// For simplicity, return the whole first job. The client-go library expects a Job object.

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		// Return a single job object (first element of the array)
		w.Write(extractFirstJSONArrayElement(data))
	}))

	job, err := client.RetryJob(context.Background(), 1, 1001)
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if job.ID != 1001 {
		t.Errorf("expected job ID=1001, got %d", job.ID)
	}
}

func TestCancelJob_Success(t *testing.T) {
	data, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(extractFirstJSONArrayElement(data))
	}))

	err := client.CancelJob(context.Background(), 1, 1001)
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
}

func TestPlayJob_Success(t *testing.T) {
	data, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(extractFirstJSONArrayElement(data))
	}))

	job, err := client.PlayJob(context.Background(), 1, 1001)
	if err != nil {
		t.Fatalf("PlayJob: %v", err)
	}
	if job.ID != 1001 {
		t.Errorf("expected job ID=1001, got %d", job.ID)
	}
}

// TestPlayJob_Unauthorized verifies that a 403 response (e.g. user lacks
// permission to trigger a manual job) surfaces an error rather than silently
// returning an empty PipelineJob.
func TestPlayJob_Unauthorized(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/jobs/1001/play" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusForbidden)
	}))

	_, err := client.PlayJob(context.Background(), 1, 1001)
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if !IsForbidden(err) {
		t.Errorf("IsForbidden should match: %v", err)
	}
}

// TestCancelJob_AlreadyFinished verifies the GitLab 403/422 behavior where
// cancelling a finished job returns an error rather than silently succeeding.
func TestCancelJob_AlreadyFinished(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/jobs/1001/cancel" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, `{"message":"403 Forbidden  - Job is not cancellable"}`, http.StatusForbidden)
	}))

	err := client.CancelJob(context.Background(), 1, 1001)
	if err == nil {
		t.Fatal("expected error for already-finished job, got nil")
	}
	if !IsForbidden(err) {
		t.Errorf("IsForbidden should match: %v", err)
	}
}

// extractFirstJSONArrayElement returns the raw JSON of the first element of a
// JSON array, leaving the original bytes intact. Used by tests that share the
// pipeline_jobs.json fixture (a list) with endpoints that expect a single
// object. Falls back to returning the input on parse failure so test failures
// surface as comparison mismatches rather than panics.
func extractFirstJSONArrayElement(data []byte) []byte {
	var elements []json.RawMessage
	if err := json.Unmarshal(data, &elements); err != nil || len(elements) == 0 {
		return data
	}
	return elements[0]
}
