package ui

import (
	"context"
	"fmt"
	"testing"
)

// TestEvictOldLogs_HoldsTheLimitWhenTheDisplayedLogIsOldest: eviction reaches the cache limit even
// when the displayed log is among the oldest entries.
// Given a log cache one entry over the limit whose oldest entry is the displayed log, when
// eviction runs, then the cache is back at the limit and the displayed log survives.
// Why it matters: skipping the displayed log without evicting a replacement leaves the cache over
// its limit, and a user tailing an old job accumulates one extra log per refresh.
func TestEvictOldLogs_HoldsTheLimitWhenTheDisplayedLogIsOldest(t *testing.T) {
	// Given: one entry over the limit, with the oldest entry on display
	m := NewModel(context.Background(), &mockService{}, Options{})
	for jobID := 1; jobID <= maxLogCacheEntries+1; jobID++ {
		m.pipelineView.logs.Set(jobID, fmt.Sprintf("log %d", jobID))
	}
	m.pipelineView.logJobID = 1

	// When: eviction runs
	(&m).evictOldLogs()

	// Then: the cache is back at its limit
	if got := m.pipelineView.logs.Len(); got != maxLogCacheEntries {
		t.Fatalf("log cache holds %d entries, want %d", got, maxLogCacheEntries)
	}

	// And: the displayed log survives
	if _, ok := m.pipelineView.logs.Get(1); !ok {
		t.Fatal("the displayed log was evicted")
	}
}
