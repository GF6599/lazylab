package gitlab

import (
	"context"
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

// extractFirstJSONArrayElement extracts the first element from a JSON array.
func extractFirstJSONArrayElement(data []byte) []byte {
	depth := 0
	start := -1
	for i, b := range data {
		switch b {
		case '[':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case '{':
			if start >= 0 && depth == 1 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 1 {
				return data[start : i+1]
			}
		case ']':
			depth--
		}
	}
	return data
}
