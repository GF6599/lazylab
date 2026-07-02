package gitlab

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// TestListProjectCommits_Success: a commit-list response maps onto CommitSummary values.
// Given a canned three-commit JSON response, when ListProjectCommits runs,
// then all three commits come back with short ID, title, and author name
// carried over.
// Why it matters: a mapping slip, like dropping author_name, would render
// blank commit rows in the project detail pane.
func TestListProjectCommits_Success(t *testing.T) {
	// Given: a server answering with the canned three-commit fixture.
	data, err := os.ReadFile("testdata/commits.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing recent commits.
	commits, err := client.ListProjectCommits(context.Background(), 1, "main", 10)
	// Then: all three commits map with their display fields intact.
	if err != nil {
		t.Fatalf("ListProjectCommits: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}

	c := commits[0]
	if c.ShortID != "abc123" {
		t.Errorf("expected short_id=abc123, got %q", c.ShortID)
	}
	if c.Title != "feat: Add initial implementation" {
		t.Errorf("unexpected title: %q", c.Title)
	}
	if c.Author != "Dev User" {
		t.Errorf("expected author=Dev User, got %q", c.Author)
	}
}

// TestListProjectCommits_DefaultLimit: a zero limit is replaced by the default of five.
// Given a request with limit 0, when ListProjectCommits issues the API call,
// then the per_page query parameter sent to the server is 5.
// Why it matters: forwarding per_page=0 would let the server pick its own
// page size and fetch far more than the detail pane shows, slowing every
// panel refresh.
func TestListProjectCommits_DefaultLimit(t *testing.T) {
	// Given: a server that records the per_page query parameter.
	data, _ := os.ReadFile("testdata/commits.json")

	var capturedPerPage string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPerPage = r.URL.Query().Get("per_page")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// When: listing commits with a zero limit.
	_, err := client.ListProjectCommits(context.Background(), 1, "", 0)
	if err != nil {
		t.Fatalf("ListProjectCommits: %v", err)
	}

	// Then: the request asked for the default of five commits.
	if capturedPerPage != "5" {
		t.Errorf("expected per_page=5 for zero limit, got %q", capturedPerPage)
	}
}
