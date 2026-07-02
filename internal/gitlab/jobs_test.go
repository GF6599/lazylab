package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
)

// TestMapJob_NilJob: a nil *gl.Job maps to the zero PipelineJob.
// Given a nil job pointer, when mapJob converts it, then the result has
// zero-valued ID, Name, and Status instead of panicking.
// Why it matters: job lists can contain nil entries when the SDK partially
// decodes a response; a nil dereference in this mapper would crash the whole
// UI loop.
func TestMapJob_NilJob(t *testing.T) {
	// When/Then: a nil job converts to the zero value without panicking.
	got := mapJob(nil)
	if got.ID != 0 || got.Name != "" || got.Status != "" {
		t.Errorf("mapJob(nil) should return zero PipelineJob, got %+v", got)
	}
}

// TestListPipelineJobs_Success: a jobs response maps IDs, stages, runner, and artifact details.
// Given the canned three-job fixture, when ListPipelineJobs runs, then job
// 1001 carries its name, stage, duration, runner description, and single
// artifact, and the failed job keeps failure_reason "script_failure".
// Why it matters: these are the fields the jobs panel renders; dropping
// runner or failure_reason in the mapping would strip exactly the details
// needed to debug a red pipeline.
func TestListPipelineJobs_Success(t *testing.T) {
	// Given: a server answering with the three-job fixture.
	data, err := os.ReadFile("testdata/pipeline_jobs.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing the pipeline's jobs.
	jobs, err := client.ListPipelineJobs(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("ListPipelineJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}

	// Then: the first job maps its identity, timing, runner, and artifacts.
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

	// And: the failed job keeps its failure reason.
	j2 := jobs[2]
	if j2.FailureReason != "script_failure" {
		t.Errorf("expected failure_reason=script_failure, got %q", j2.FailureReason)
	}
}

// TestListPipelineJobs_Empty: a pipeline with no jobs surfaces the ErrNoJobs sentinel.
// Given a server returning an empty jobs array, when ListPipelineJobs runs,
// then the error matches ErrNoJobs via errors.Is.
// Why it matters: freshly created pipelines briefly have zero jobs; the UI
// matches this sentinel to show a waiting state instead of an error banner.
func TestListPipelineJobs_Empty(t *testing.T) {
	// Given: a server answering with an empty jobs list.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))

	// When/Then: listing yields the ErrNoJobs sentinel.
	_, err := client.ListPipelineJobs(context.Background(), 1, 100)
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("expected ErrNoJobs, got %v", err)
	}
}

// TestGetJobTrace_Success: a job's trace endpoint response is returned verbatim.
// Given a plain-text trace served at /projects/1/jobs/1001/trace, when
// GetJobTrace runs, then the returned string equals the raw body.
// Why it matters: the log viewer polls this call to tail live output; a wrong
// URL or mangled body would show the wrong job's log or corrupt the text.
func TestGetJobTrace_Success(t *testing.T) {
	// Given: a server serving a plain-text trace at the job's trace path.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/jobs/1001/trace" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Running build...\nBuild succeeded!"))
	}))

	// When: fetching the trace.
	trace, err := client.GetJobTrace(context.Background(), 1, 1001)
	// Then: the body comes back unmodified.
	if err != nil {
		t.Fatalf("GetJobTrace: %v", err)
	}
	if trace != "Running build...\nBuild succeeded!" {
		t.Errorf("unexpected trace: %q", trace)
	}
}

// TestRetryJob_Success: retrying a job POSTs and maps the returned job record.
// Given a server expecting POST and answering with a single job object, when
// RetryJob runs, then it succeeds and the mapped job keeps ID 1001.
// Why it matters: retry is a mutating call; the wrong verb would be rejected
// by the real API, and a mapping slip would deselect the retried job in the
// UI.
func TestRetryJob_Success(t *testing.T) {
	// Given: the retry endpoint answers with a single job object, carved
	// from the shared pipeline_jobs.json list fixture.
	data, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(extractFirstJSONArrayElement(data))
	}))

	// When: retrying the job.
	job, err := client.RetryJob(context.Background(), 1, 1001)
	// Then: the call succeeds and the returned job maps its ID.
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if job.ID != 1001 {
		t.Errorf("expected job ID=1001, got %d", job.ID)
	}
}

// TestCancelJob_Success: cancelling a job POSTs and reports no error on success.
// Given a server expecting POST and answering with a job object, when
// CancelJob runs, then it returns nil.
// Why it matters: this is the happy path behind the cancel keybinding; a
// wrong method or spurious error would make every cancel look failed even
// though the server accepted it.
func TestCancelJob_Success(t *testing.T) {
	// Given: the cancel endpoint expects POST and answers with a job object.
	data, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(extractFirstJSONArrayElement(data))
	}))

	// When/Then: cancelling succeeds without error.
	err := client.CancelJob(context.Background(), 1, 1001)
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
}

// TestPlayJob_Success: playing a manual job POSTs and maps the returned job.
// Given a server expecting POST and answering with a job object, when PlayJob
// runs, then it succeeds and the mapped job keeps ID 1001.
// Why it matters: manual gate jobs are triggered through this call; a verb or
// mapping regression would leave deploy gates untriggerable from the TUI.
func TestPlayJob_Success(t *testing.T) {
	// Given: the play endpoint expects POST and answers with a job object.
	data, _ := os.ReadFile("testdata/pipeline_jobs.json")

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(extractFirstJSONArrayElement(data))
	}))

	// When: playing the manual job.
	job, err := client.PlayJob(context.Background(), 1, 1001)
	// Then: the call succeeds and the returned job maps its ID.
	if err != nil {
		t.Fatalf("PlayJob: %v", err)
	}
	if job.ID != 1001 {
		t.Errorf("expected job ID=1001, got %d", job.ID)
	}
}

// TestPlayJob_Unauthorized: a 403 from the play endpoint surfaces as a forbidden error.
// Given a server answering 403 on /projects/1/jobs/1001/play, when PlayJob
// runs, then it returns an error matching IsForbidden rather than an empty
// job.
// Why it matters: users without trigger permission would otherwise see a
// silent no-op and assume the deploy started when it never did.
func TestPlayJob_Unauthorized(t *testing.T) {
	// Given: a server refusing the play request with 403.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/jobs/1001/play" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusForbidden)
	}))

	// When: playing the job without permission.
	_, err := client.PlayJob(context.Background(), 1, 1001)

	// Then: the refusal surfaces and classifies as forbidden.
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if !IsForbidden(err) {
		t.Errorf("IsForbidden should match: %v", err)
	}
}

// TestCancelJob_AlreadyFinished: cancelling a finished job surfaces GitLab's 403 refusal.
// Given a server answering 403 "Job is not cancellable" on the cancel
// endpoint, when CancelJob runs, then the error matches IsForbidden.
// Why it matters: swallowing this refusal would tell the user a completed job
// was cancelled, misrepresenting what actually ran.
func TestCancelJob_AlreadyFinished(t *testing.T) {
	// Given: a server refusing the cancel with GitLab's not-cancellable 403.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/1/jobs/1001/cancel" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, `{"message":"403 Forbidden  - Job is not cancellable"}`, http.StatusForbidden)
	}))

	// When: cancelling the already-finished job.
	err := client.CancelJob(context.Background(), 1, 1001)

	// Then: the refusal surfaces and classifies as forbidden.
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
