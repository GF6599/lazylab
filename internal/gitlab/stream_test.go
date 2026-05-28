package gitlab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedService is a Service whose GetJobTrace and GetJob return a
// scripted sequence of values, one per call. Each call advances a
// counter shared between the two methods so the test can describe the
// exact "trace grew while status was still running, then status flipped
// to terminal" sequence StreamJobTrace must handle.
//
// Embedding Service means any non-overridden method panics with a nil
// dereference if the streamer accidentally calls it — a deliberate
// safety net rather than a foot-gun, because surprising calls indicate
// a regression in the streamer's contract.
type scriptedService struct {
	Service
	traces        []string // one per GetJobTrace call
	traceErrs     []error  // matches traces 1:1; nil = success at that index
	statuses      []string // one per GetJob call (advances independently)
	statusErrs    []error  // matches statuses 1:1; nil = success at that index
	cappedTrace   string   // returned by the optional GetJobTraceCapped path
	traceCalls    int32
	statusCalls   int32
	cappedCalls   int32
	overflowTrace string // returned if traces is exhausted
}

func (s *scriptedService) GetJobTrace(_ context.Context, _, _ int) (string, error) {
	i := atomic.AddInt32(&s.traceCalls, 1) - 1
	if int(i) >= len(s.traces) {
		return s.overflowTrace, nil
	}
	if int(i) < len(s.traceErrs) && s.traceErrs[i] != nil {
		return "", s.traceErrs[i]
	}
	return s.traces[i], nil
}

func (s *scriptedService) GetJob(_ context.Context, _, jobID int) (PipelineJob, error) {
	i := atomic.AddInt32(&s.statusCalls, 1) - 1
	if int(i) >= len(s.statuses) {
		return PipelineJob{ID: jobID, Status: s.statuses[len(s.statuses)-1]}, nil
	}
	if int(i) < len(s.statusErrs) && s.statusErrs[i] != nil {
		return PipelineJob{ID: jobID, Status: s.statuses[i]}, s.statusErrs[i]
	}
	return PipelineJob{ID: jobID, Status: s.statuses[i]}, nil
}

// GetJobTraceCapped implements the optional cappedTraceFetcher capability
// the streamer type-asserts on. Returning a non-empty value also verifies
// that the streamer wires the capped path on the degraded-final-fetch
// branch.
func (s *scriptedService) GetJobTraceCapped(_ context.Context, _, _ int) (string, error) {
	atomic.AddInt32(&s.cappedCalls, 1)
	return s.cappedTrace, nil
}

// TestStreamJobTrace_DiffEmitsOnlyNewBytes is the load-bearing contract:
// a growing trace must produce exactly the appended bytes on the
// writer, never the cumulative re-emission. Failing this turns 'job
// log --follow' from useful into broken (each poll would print the
// whole log again).
func TestStreamJobTrace_DiffEmitsOnlyNewBytes(t *testing.T) {
	svc := &scriptedService{
		traces: []string{
			"setup...\n",
			"setup...\ncompiling...\n",
			"setup...\ncompiling...\ntests pass\n",
		},
		statuses: []string{"running", "running", "success"},
	}
	var buf bytes.Buffer
	status, err := StreamJobTrace(context.Background(), svc, 1, 2, StreamTraceOptions{
		Writer:   &buf,
		Interval: time.Nanosecond, // make the loop tick as fast as possible
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "success" {
		t.Errorf("status: got %q want success", status)
	}
	got := buf.String()
	want := "setup...\ncompiling...\ntests pass\n"
	if got != want {
		t.Errorf("writer received cumulative or wrong output\n got: %q\nwant: %q", got, want)
	}
}

// TestStreamJobTrace_RaceRecoveryRefetch verifies the "one more fetch
// after terminal" behavior: a runner that flips status to "success"
// before flushing its final lines should still produce those final
// lines on the writer. Without this, the streamer would lose trailing
// bytes for ~5% of jobs in practice.
func TestStreamJobTrace_RaceRecoveryRefetch(t *testing.T) {
	svc := &scriptedService{
		traces: []string{
			"build started\n",
			"build started\n",                 // status flipped to success here
			"build started\nbuild finished\n", // race-recovery fetch sees the extra line
		},
		statuses: []string{"running", "success"},
	}
	var buf bytes.Buffer
	status, err := StreamJobTrace(context.Background(), svc, 1, 2, StreamTraceOptions{
		Writer:   &buf,
		Interval: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "success" {
		t.Fatalf("status: got %q want success", status)
	}
	if !strings.Contains(buf.String(), "build finished") {
		t.Errorf("race-recovery refetch should have captured 'build finished'; got: %q", buf.String())
	}
}

// TestStreamJobTrace_FailedStatusBubbles confirms that the streamer
// returns whatever the final status is — it doesn't filter or
// transform. The CLI layer is responsible for mapping status → exit
// code; the streamer is the pure data provider.
func TestStreamJobTrace_FailedStatusBubbles(t *testing.T) {
	svc := &scriptedService{
		traces:   []string{"oh no\n", "oh no\n"},
		statuses: []string{"failed"},
	}
	var buf bytes.Buffer
	status, err := StreamJobTrace(context.Background(), svc, 1, 2, StreamTraceOptions{
		Writer:   &buf,
		Interval: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "failed" {
		t.Errorf("status: got %q want failed", status)
	}
}

// TestStreamJobTrace_NoWriter guards against accidentally calling the
// streamer with a nil writer (a common ctor-omission bug). The check
// must fire before any API call so the test passes a Service that
// would panic if invoked.
func TestStreamJobTrace_NoWriter(t *testing.T) {
	_, err := StreamJobTrace(context.Background(), (*scriptedService)(nil), 1, 2, StreamTraceOptions{})
	if err == nil {
		t.Fatal("expected error when Writer is nil")
	}
	if !strings.Contains(err.Error(), "writer") {
		t.Errorf("error should mention 'writer', got: %v", err)
	}
}

// TestStreamJobTrace_TraceTooLargeDoesNotAbort verifies that an
// ErrTraceTooLarge mid-stream degrades to status-only polling rather
// than killing the watch. Without the degradation flag the streamer
// would return on the first oversize trace and the CLI user would never
// see the job's terminal status.
func TestStreamJobTrace_TraceTooLargeDoesNotAbort(t *testing.T) {
	svc := &scriptedService{
		traces: []string{
			"line 1\n", // first poll succeeds
			"",         // second-poll slot replaced by error below
			"",         // third-poll slot replaced by error below
		},
		traceErrs: []error{
			nil,
			fmt.Errorf("oh no: %w", ErrTraceTooLarge),
			fmt.Errorf("oh no: %w", ErrTraceTooLarge),
		},
		statuses:    []string{"running", "running", "success"},
		cappedTrace: "line 1\n[truncated]\n",
	}
	var buf bytes.Buffer
	status, err := StreamJobTrace(context.Background(), svc, 1, 2, StreamTraceOptions{
		Writer:   &buf,
		Interval: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "success" {
		t.Errorf("status: got %q want success", status)
	}
	// The first byte block must still land on the writer, and the
	// final capped fetch must have been invoked at least once.
	if !strings.Contains(buf.String(), "line 1") {
		t.Errorf("writer missing pre-degradation bytes; got %q", buf.String())
	}
	if atomic.LoadInt32(&svc.cappedCalls) == 0 {
		t.Errorf("expected at least one GetJobTraceCapped call once degraded; got 0")
	}
}

// TestStreamJobTrace_PollErrorReturnsLastStatus exercises the F3
// contract: when a poll errors after the streamer has already observed
// a status, that status must come back alongside the error. A bare ""
// would force CLI callers to invent "unknown" diagnostics for what is
// actually "we last saw it as running".
func TestStreamJobTrace_PollErrorReturnsLastStatus(t *testing.T) {
	boom := errors.New("boom")
	svc := &scriptedService{
		traces:     []string{"started\n", "started\nmore\n"},
		statuses:   []string{"running", "running"},
		statusErrs: []error{nil, boom},
	}
	var buf bytes.Buffer
	status, err := StreamJobTrace(context.Background(), svc, 1, 2, StreamTraceOptions{
		Writer:   &buf,
		Interval: time.Nanosecond,
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom in error chain, got %v", err)
	}
	if status != "running" {
		t.Errorf("status on error: got %q want running (last observed)", status)
	}
}
