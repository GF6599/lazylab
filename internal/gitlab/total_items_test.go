package gitlab

import (
	"context"
	"net/http"
	"testing"
)

// TestListEndpoints_ReportHowManyItemsTheCollectionHolds: every listing says how big the whole set is.
// Given a server that reports a collection total alongside a page, when each listing is fetched, then
// each reports that total.
// Why it matters: a caller that knows only the page size can say which row of the page it is on, and
// has to guess at how far through the collection that is.
func TestListEndpoints_ReportHowManyItemsTheCollectionHolds(t *testing.T) {
	// Given: a server that answers every listing with one row and a collection total of 137
	body := map[string]string{
		"/api/v4/projects":                  `[{"id":1,"name":"a","path_with_namespace":"t/a"}]`,
		"/api/v4/projects/1/pipelines":      `[{"id":100,"status":"success","ref":"main"}]`,
		"/api/v4/projects/1/merge_requests": `[{"id":1,"iid":2,"title":"t","state":"opened"}]`,
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Total-Pages", "6")
		w.Header().Set("X-Total", "137")
		w.Write([]byte(body[r.URL.Path]))
	}))

	// When: each listing is fetched
	projects, err := client.ListProjects(context.Background(), ProjectListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	pipelines, err := client.ListPipelines(context.Background(), 1, PipelineListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	mrs, err := client.ListMergeRequests(context.Background(), 1, MRListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListMergeRequests: %v", err)
	}

	// Then: each reports the collection total, not the size of the page it returned
	for _, tc := range []struct {
		name  string
		total int
	}{
		{"projects", projects.TotalItems},
		{"pipelines", pipelines.TotalItems},
		{"merge requests", mrs.TotalItems},
	} {
		if tc.total != 137 {
			t.Errorf("%s reported %d items in the collection, want 137", tc.name, tc.total)
		}
	}
}

// TestListEndpoints_ReportNoTotalWhenGitLabWithholdsIt: an absent total is reported as unknown.
// Given a server that omits the collection total, when a listing is fetched, then it reports zero.
// Why it matters: GitLab stops sending the total once a collection passes ten thousand items, so a
// caller that treats a missing header as a real count would tell the user the set is empty.
func TestListEndpoints_ReportNoTotalWhenGitLabWithholdsIt(t *testing.T) {
	// Given: a server that answers with a page but no collection total
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Total-Pages", "6")
		w.Write([]byte(`[{"id":1,"name":"a","path_with_namespace":"t/a"}]`))
	}))

	// When: the listing is fetched
	page, err := client.ListProjects(context.Background(), ProjectListOptions{Page: 1, PerPage: 25})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}

	// Then: the total reads as unknown rather than as none
	if page.TotalItems != 0 {
		t.Errorf("a withheld total was reported as %d", page.TotalItems)
	}
	if page.TotalPages != 6 {
		t.Errorf("the page count was lost along with the item total: %d", page.TotalPages)
	}
}
