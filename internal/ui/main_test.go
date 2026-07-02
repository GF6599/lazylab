package ui

import (
	"os"
	"testing"
)

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
