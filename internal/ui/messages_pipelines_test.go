package ui

import (
	"context"
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestHandlePipelineJobsLoaded_NoJobsIsEmptyNotError: a pipeline with no jobs settles as a known-empty
// result instead of an error.
// Given a multi-panel model viewing project 7, when a jobs load answers with gitlab.ErrNoJobs for
// pipeline 42, then the jobs cache reports a present, empty entry with no error recorded.
// Why it matters: a bridge-only parent pipeline has zero regular jobs on every fetch. An entry left
// absent makes queuePipelineLogPreview refetch the jobs on every 5-second tick forever, and the
// bridge-metadata preview branch is never reached.
func TestHandlePipelineJobsLoaded_NoJobsIsEmptyNotError(t *testing.T) {
	// Given: a multi-panel model viewing project 7
	m := NewModel(context.Background(), &mockService{}, Options{})
	m.mode = modeMultiPanel
	m.pipelineView.project = gitlab.ProjectNode{ID: 7}

	// When: the jobs load answers with the no-jobs sentinel
	res, _ := m.Update(pipelineJobsLoadedMsg{projectID: 7, pipelineID: 42, err: gitlab.ErrNoJobs})
	m = res.(Model)

	// Then: the cache holds a present, empty entry
	jobs, ok := m.pipelineView.jobs.Get(42)
	if !ok {
		t.Fatal("expected jobs cache to report pipeline 42 as loaded")
	}
	if len(jobs) != 0 {
		t.Fatalf("expected an empty job list, got %d jobs", len(jobs))
	}

	// And: no error state remains
	if err := m.pipelineView.jobs.Err(42); err != nil {
		t.Fatalf("expected no cached error, got %v", err)
	}
}

// TestHandleChildPipelineJobsLoaded_NoJobsIsEmptyNotError: a child pipeline with no jobs settles as a
// known-empty result instead of an error.
// Given a multi-panel model, when a child-jobs load answers with gitlab.ErrNoJobs for child pipeline
// 900, then the child-jobs cache reports a present, empty entry with no error recorded.
// Why it matters: an expanded bridge whose downstream pipeline has not scheduled jobs yet would
// otherwise sit in an error state and refetch on every refresh instead of rendering as empty.
func TestHandleChildPipelineJobsLoaded_NoJobsIsEmptyNotError(t *testing.T) {
	// Given: a multi-panel model
	m := NewModel(context.Background(), &mockService{}, Options{})
	m.mode = modeMultiPanel

	// When: the child-jobs load answers with the no-jobs sentinel
	res, _ := m.Update(childPipelineJobsLoadedMsg{childPipelineID: 900, err: gitlab.ErrNoJobs})
	m = res.(Model)

	// Then: the cache holds a present, empty entry with no error
	jobs, ok := m.pipelineView.childJobs.Get(900)
	if !ok {
		t.Fatal("expected child-jobs cache to report pipeline 900 as loaded")
	}
	if len(jobs) != 0 {
		t.Fatalf("expected an empty job list, got %d jobs", len(jobs))
	}
	if err := m.pipelineView.childJobs.Err(900); err != nil {
		t.Fatalf("expected no cached error, got %v", err)
	}
}
