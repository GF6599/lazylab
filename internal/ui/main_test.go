package ui

import (
	"os"
	"testing"
)

// withPanelLists fills in the lists a launch always builds, for a fixture that assembles a Model by
// hand. A zero value list.Model carries no delegate, and sizing the panels dereferences it.
func withPanelLists(m Model) Model {
	pipelineStatus := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	m.pipelineStatus = &pipelineStatus
	m.projectList = newBareList(nil, projectDelegate{
		pipelineStatus: &pipelineStatus,
		favorites:      map[int]bool{},
	}, 40, 20)
	m.pipelineView.pipelineList = newPipelineListModel(statusFrames{})
	return m
}

// TestMain isolates this package's persistent stores (project cache, favorites,
// preferences) to a throwaway directory for the duration of the suite. Without
// it, any test that builds a Model via NewModel opens the real user cache, and
// one that drives saveCacheCmd writes its fixtures into the developer's actual
// ~/Library/Caches/lazylab, clobbering the cached projects for whatever host
// resolves (sanitizeHost("") == "https_gitlab_com").
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "lazylab-test-cache-*")
	if err != nil {
		panic(err)
	}
	cacheBaseOverride = tmp

	code := m.Run()

	_ = os.RemoveAll(tmp)
	os.Exit(code)
}
