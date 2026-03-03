package gitlab

import (
	"context"
	"net/http"
	"os"
	"testing"
)

func TestListProjectCommits_Success(t *testing.T) {
	data, err := os.ReadFile("testdata/commits.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	commits, err := client.ListProjectCommits(context.Background(), 1, "main", 10)
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

func TestListProjectCommits_DefaultLimit(t *testing.T) {
	data, _ := os.ReadFile("testdata/commits.json")

	var capturedPerPage string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPerPage = r.URL.Query().Get("per_page")
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))

	// Pass 0 limit — should default to 5
	_, err := client.ListProjectCommits(context.Background(), 1, "", 0)
	if err != nil {
		t.Fatalf("ListProjectCommits: %v", err)
	}
	if capturedPerPage != "5" {
		t.Errorf("expected per_page=5 for zero limit, got %q", capturedPerPage)
	}
}
