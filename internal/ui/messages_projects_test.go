package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestFormatLoadErr: each class of load failure maps to its own exact status-bar message.
// Given error chains for 401, 403, 404, 429, and 502 plus a plain error and nil, when formatLoadErr
// renders each, then every case yields its precise phrase, from token guidance for 401 through the
// "Failed to load <action>" fallback down to an empty string for nil.
// Why it matters: this string is the only explanation a user gets for a failed load, and a wrong
// mapping would tell someone with an expired token that GitLab is down instead of pointing them at
// GITLAB_TOKEN.
//
// Errors are fabricated directly via *gl.ErrorResponse rather than provoked through the SDK, because
// the helper only cares about what errors.As can extract from the chain.
func TestFormatLoadErr(t *testing.T) {
	// Given: error chains for each failure class with their expected status lines
	tests := []struct {
		name   string
		action string
		err    error
		want   string
	}{
		{
			name:   "401 token rejected",
			action: "projects",
			err:    wrapHTTPErr(http.StatusUnauthorized),
			want:   "GitLab token rejected (401) — refresh GITLAB_TOKEN",
		},
		{
			name:   "429 rate limited",
			action: "projects",
			err:    wrapHTTPErr(http.StatusTooManyRequests),
			want:   "GitLab rate-limited (429) — retrying after backoff",
		},
		{
			name:   "403 forbidden",
			action: "projects",
			err:    wrapHTTPErr(http.StatusForbidden),
			want:   "GitLab denied access (403) — check token scopes",
		},
		{
			name:   "404 not found uses action noun",
			action: "pipelines",
			err:    wrapHTTPErr(http.StatusNotFound),
			want:   "GitLab pipelines not found (404)",
		},
		{
			name:   "502 server error",
			action: "projects",
			err:    wrapHTTPErr(http.StatusBadGateway),
			want:   "GitLab server error — will retry",
		},
		{
			name:   "non-API error falls back to generic with action",
			action: "projects",
			err:    errors.New("network unreachable"),
			want:   "Failed to load projects",
		},
		{
			name:   "generic fallback with custom action",
			action: "merge requests",
			err:    errors.New("network unreachable"),
			want:   "Failed to load merge requests",
		},
		{
			name:   "wrapped 401 still detected through extra layer",
			action: "projects",
			err:    fmt.Errorf("higher-level: %w", wrapHTTPErr(http.StatusUnauthorized)),
			want:   "GitLab token rejected (401) — refresh GITLAB_TOKEN",
		},
		{
			name:   "nil error returns empty string",
			action: "projects",
			err:    nil,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When/Then: rendering the error yields the exact expected phrase
			if got := formatLoadErr(tt.action, tt.err); got != tt.want {
				t.Errorf("formatLoadErr(%q, err) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

// pagedProjects builds sequential fake projects covering [from, to].
func pagedProjects(from, to int) []gitlab.ProjectNode {
	nodes := make([]gitlab.ProjectNode, 0, to-from+1)
	for id := from; id <= to; id++ {
		nodes = append(nodes, gitlab.ProjectNode{
			ID:                id,
			Name:              fmt.Sprintf("proj-%d", id),
			PathWithNamespace: fmt.Sprintf("org/proj-%d", id),
		})
	}
	return nodes
}

// pagedListProjects serves a fixed collection page by page the way the GitLab
// API would, so tests can watch the model walk every page.
func pagedListProjects(total int) func(context.Context, gitlab.ProjectListOptions) (gitlab.ProjectPage, error) {
	return func(_ context.Context, opts gitlab.ProjectListOptions) (gitlab.ProjectPage, error) {
		perPage := opts.PerPage
		if perPage <= 0 {
			perPage = defaultProjectsPerPage
		}
		page := max(opts.Page, 1)
		start := (page-1)*perPage + 1
		end := min(start+perPage-1, total)
		if start > total {
			return gitlab.ProjectPage{Page: page, TotalItems: total}, nil
		}
		totalPages := (total + perPage - 1) / perPage
		next := 0
		if page < totalPages {
			next = page + 1
		}
		return gitlab.ProjectPage{
			Projects:   pagedProjects(start, end),
			Page:       page,
			NextPage:   next,
			TotalPages: totalPages,
			TotalItems: total,
		}, nil
	}
}

// backgroundLoadModel builds a model over a paged mock collection with the
// auto-refresh tick marked alive, so drainCmd can follow the load chain
// without tripping on the 5-second tick.
func backgroundLoadModel(t *testing.T, total int) Model {
	t.Helper()
	svc := &mockService{ListProjectsFn: pagedListProjects(total)}
	m := NewModel(context.Background(), svc, Options{})
	m.pipelineTickAlive = true
	return m
}

// TestHandleCacheLoaded_PartialCacheLoadsTheRest: a cache holding only part of the collection
// triggers a background load of the remaining pages.
// Given a cached first page of 30 projects out of a recorded total of 75, when the cache-loaded
// message is handled and its command chain drains, then every project is loaded, the recorded total
// stands, and a search reaches a project from the last page.
// Why it matters: a first run caches only the foreground page. Treating that slice as the whole
// collection silently caps the project list and every search at 30 projects for the cache TTL.
func TestHandleCacheLoaded_PartialCacheLoadsTheRest(t *testing.T) {
	// Given: a model whose API serves 75 projects and a cache holding the first 30
	m := backgroundLoadModel(t, 75)

	// When: the partial cache lands
	res, cmd := m.Update(cacheLoadedMsg{projects: pagedProjects(1, 30), total: 75, found: true})
	m = res.(Model)

	// Then: the model knows the collection is larger than the cache
	if m.totalProjects != 75 {
		t.Fatalf("totalProjects = %d, want 75", m.totalProjects)
	}
	if !m.backgroundLoading {
		t.Fatal("expected a background load to start for the missing pages")
	}

	// And: draining the chain loads every remaining page
	m = drainCmd(t, m, cmd)
	if got := m.loadedProjectCount(); got != 75 {
		t.Fatalf("loaded %d projects after drain, want 75", got)
	}
	if m.backgroundLoading {
		t.Fatal("expected background loading to finish")
	}

	// And: a search reaches a project the cache never held
	m.search.query = "proj-75"
	(&m).invalidateVisibleCache()
	visible := (&m).visibleProjects()
	if len(visible) != 1 || visible[0].ID != 75 {
		t.Fatalf("search for proj-75 found %+v, want the one project from the last page", visible)
	}
}

// TestHandleCacheLoaded_CompleteCacheStaysQuiet: a cache holding the whole collection starts no fetch.
// Given a cached collection of 20 projects with a matching total, when the cache-loaded message is
// handled, then no background load starts and every page reads as ready.
// Why it matters: refetching a complete cache on every launch would defeat the cache's purpose and
// hammer the API with requests that change nothing.
func TestHandleCacheLoaded_CompleteCacheStaysQuiet(t *testing.T) {
	// Given: a model whose cache holds the entire 20-project collection
	m := backgroundLoadModel(t, 20)

	// When: the complete cache lands
	res, _ := m.Update(cacheLoadedMsg{projects: pagedProjects(1, 20), total: 20, found: true})
	m = res.(Model)

	// Then: nothing is left to fetch
	if m.backgroundLoading {
		t.Fatal("expected no background load for a complete cache")
	}
	if got := m.loadedProjectCount(); got != 20 {
		t.Fatalf("loaded %d projects, want 20", got)
	}
}

// TestHandleProjectsLoaded_BackgroundLoadsRemainingPages: a foreground first page chains background
// fetches until the whole collection is loaded.
// Given a foreground load of page 1 out of a 75-project collection, when the message is handled and
// its command chain drains, then all three pages land and background loading finishes.
// Why it matters: search and pagination walk allProjects, so a first run that stops at page 1 shows
// an app-wide 30-project world until the user forces a refresh.
func TestHandleProjectsLoaded_BackgroundLoadsRemainingPages(t *testing.T) {
	// Given: a model whose API serves 75 projects
	m := backgroundLoadModel(t, 75)
	page1, err := m.client.ListProjects(context.Background(), gitlab.ProjectListOptions{Page: 1, PerPage: m.apiPerPage()})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}

	// When: the foreground first page lands
	res, cmd := m.Update(projectsLoadedMsg{page: page1})
	m = res.(Model)

	// Then: the remaining pages load in the background
	if !m.backgroundLoading {
		t.Fatal("expected a background load to start after the foreground page")
	}
	m = drainCmd(t, m, cmd)
	if got := m.loadedProjectCount(); got != 75 {
		t.Fatalf("loaded %d projects after drain, want 75", got)
	}
	if m.backgroundLoading {
		t.Fatal("expected background loading to finish")
	}
}

// TestMovePage_DoesNotDuplicateAnInFlightFetch: paging onto a missing page while a background fetch
// runs does not dispatch a second fetch.
// Given a model with one of three pages loaded and a background fetch in flight, when the user pages
// forward onto a missing page, then the page moves but no new fetch command is produced, and the
// same navigation with no fetch in flight does produce one.
// Why it matters: every auto-refresh tick and page keystroke stacking its own fetch turns fast
// paging into a burst of duplicate API calls against the same pages.
func TestMovePage_DoesNotDuplicateAnInFlightFetch(t *testing.T) {
	// Given: page 1 of 3 loaded and a background fetch already in flight
	m := backgroundLoadModel(t, 90)
	m.allProjects = pagedProjects(1, 30)
	m.totalProjects = 90
	m.totalPages = 3
	m.pagesReady = map[int]bool{1: true}
	m.page = 1
	m.backgroundLoading = true

	// When: the user pages onto a missing page
	cmd := (&m).movePage(1)

	// Then: the page moves without a duplicate fetch
	if m.page != 2 {
		t.Fatalf("page = %d, want 2", m.page)
	}
	if cmd != nil {
		t.Fatal("expected no fetch while one is already in flight")
	}

	// And: the same navigation fetches once nothing is in flight
	m.backgroundLoading = false
	m.page = 1
	if cmd := (&m).movePage(1); cmd == nil {
		t.Fatal("expected a fetch for the missing page when none is in flight")
	}
}

// wrapHTTPErr fabricates an error chain that mirrors what the gitlab SDK
// produces for a given HTTP status: a *gl.ErrorResponse with the requisite
// *http.Response embedded, wrapped once with fmt.Errorf the way client.go
// would.
func wrapHTTPErr(status int) error {
	sdkErr := &gl.ErrorResponse{
		Response: &http.Response{
			StatusCode: status,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    must(url.Parse("https://gitlab.com/api/v4/projects")),
			},
		},
		Message: fmt.Sprintf("%d test", status),
	}
	return fmt.Errorf("list projects: %w", sdkErr)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
