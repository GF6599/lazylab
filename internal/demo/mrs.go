package demo

import (
	"fmt"
	"time"

	"github.com/GF6599/lazylab/internal/gitlab"
)

func demoMergeRequests(projectID int) []gitlab.MergeRequestSummary {
	base := projectID * 100
	return []gitlab.MergeRequestSummary{
		{
			IID:          base + 1,
			Title:        "feat: add health check endpoint with readiness probe",
			State:        "opened",
			Author:       "Alice Chen",
			SourceBranch: "feature/health-check",
			TargetBranch: "main",
			WebURL:       fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/merge_requests/%d", base+1),
			UpdatedAt:    refTime.Add(-2 * time.Hour),
		},
		{
			IID:          base + 2,
			Title:        "fix: prevent race condition in connection pool",
			State:        "opened",
			Author:       "Bob Smith",
			SourceBranch: "fix/conn-pool-race",
			TargetBranch: "main",
			WebURL:       fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/merge_requests/%d", base+2),
			UpdatedAt:    refTime.Add(-6 * time.Hour),
		},
		{
			IID:          base + 3,
			Title:        "refactor: extract middleware into shared package",
			State:        "merged",
			Author:       "Carol Jones",
			SourceBranch: "refactor/middleware",
			TargetBranch: "main",
			WebURL:       fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/merge_requests/%d", base+3),
			UpdatedAt:    refTime.Add(-24 * time.Hour),
		},
		{
			IID:          base + 4,
			Title:        "chore: upgrade Go to 1.24 and update dependencies",
			State:        "closed",
			Author:       "Dave Wilson",
			SourceBranch: "chore/go-1.24",
			TargetBranch: "main",
			WebURL:       fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/merge_requests/%d", base+4),
			UpdatedAt:    refTime.Add(-72 * time.Hour),
		},
		{
			IID:          base + 5,
			Title:        "feat: add OpenTelemetry tracing to HTTP handlers",
			State:        "opened",
			Author:       "Alice Chen",
			SourceBranch: "feature/otel-tracing",
			TargetBranch: "main",
			WebURL:       fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/merge_requests/%d", base+5),
			UpdatedAt:    refTime.Add(-4 * time.Hour),
		},
	}
}

func demoDiscussions(_ int, mrIID int) []gitlab.MRDiscussion {
	suffix := mrIID % 100
	switch suffix {
	case 1:
		return []gitlab.MRDiscussion{
			{
				ID: "disc-001",
				Notes: []gitlab.MRNote{
					{
						ID:         5001,
						Author:     "Bob Smith",
						Body:       "Should we also add a `/livez` endpoint for the kubelet?",
						Resolvable: true,
						Resolved:   true,
						CreatedAt:  refTime.Add(-90 * time.Minute),
						FilePath:   "internal/handler/handler.go",
						Line:       14,
						OldLine:    14,
						NewLine:    14,
					},
					{
						ID:        5002,
						Author:    "Alice Chen",
						Body:      "Good idea — added in the latest push.",
						CreatedAt: refTime.Add(-60 * time.Minute),
					},
				},
			},
			{
				ID: "disc-002",
				Notes: []gitlab.MRNote{
					{
						ID:         5003,
						Author:     "Carol Jones",
						Body:       "nit: this timeout should probably be configurable",
						Resolvable: true,
						Resolved:   false,
						CreatedAt:  refTime.Add(-45 * time.Minute),
						FilePath:   "internal/handler/handler.go",
						Line:       22,
						NewLine:    22,
					},
				},
			},
			{
				ID: "disc-003",
				Notes: []gitlab.MRNote{
					{
						ID:        5004,
						Author:    "Dave Wilson",
						Body:      "LGTM overall, nice and clean!",
						CreatedAt: refTime.Add(-30 * time.Minute),
					},
				},
			},
		}
	case 2:
		return []gitlab.MRDiscussion{
			{
				ID: "disc-010",
				Notes: []gitlab.MRNote{
					{
						ID:         5010,
						Author:     "Alice Chen",
						Body:       "Can you add a regression test for the race scenario?",
						Resolvable: true,
						Resolved:   false,
						CreatedAt:  refTime.Add(-5 * time.Hour),
						FilePath:   "internal/service/service.go",
						Line:       16,
						NewLine:    16,
					},
					{
						ID:        5011,
						Author:    "Bob Smith",
						Body:      "Working on it — need to figure out a reliable way to trigger the race in tests.",
						CreatedAt: refTime.Add(-4 * time.Hour),
					},
				},
			},
		}
	default:
		return nil
	}
}

func demoDiffs(_ int, mrIID int) []gitlab.MRDiffFile {
	suffix := mrIID % 100
	switch suffix {
	case 1:
		return []gitlab.MRDiffFile{
			{
				OldPath: "internal/handler/handler.go",
				NewPath: "internal/handler/handler.go",
				Diff: `@@ -12,6 +12,16 @@ func Register(mux *http.ServeMux, logger *slog.Logger) {
 		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
 	})

+	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
+		// TODO: check database connectivity
+		w.WriteHeader(http.StatusOK)
+		json.NewEncoder(w).Encode(map[string]string{"ready": "true"})
+	})
+
+	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusOK)
+	})
+
 	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
`,
			},
			{
				OldPath: "internal/handler/handler_test.go",
				NewPath: "internal/handler/handler_test.go",
				Diff: `@@ -20,3 +20,18 @@ func TestHealthz(t *testing.T) {
 		t.Fatalf("want 200, got %d", rec.Code)
 	}
 }
+
+func TestReadyz(t *testing.T) {
+	mux := http.NewServeMux()
+	handler.Register(mux, slog.Default())
+
+	req := httptest.NewRequest("GET", "/readyz", nil)
+	rec := httptest.NewRecorder()
+	mux.ServeHTTP(rec, req)
+
+	if rec.Code != http.StatusOK {
+		t.Fatalf("want 200, got %d", rec.Code)
+	}
+}
`,
				NewFile: false,
			},
		}
	case 2:
		return []gitlab.MRDiffFile{
			{
				OldPath: "internal/service/service.go",
				NewPath: "internal/service/service.go",
				Diff: `@@ -3,6 +3,7 @@ package service
 import (
 	"context"
 	"fmt"
+	"sync"
 )

 type Store interface {
@@ -12,6 +13,7 @@ type Store interface {

 type Service struct {
 	store Store
+	mu    sync.Mutex
 }
`,
			},
		}
	default:
		return nil
	}
}

func demoCommits(projectID int) []gitlab.CommitSummary {
	_ = projectID
	return []gitlab.CommitSummary{
		{
			ShortID:   "a1b2c3d",
			Title:     "feat: add health check endpoint",
			Author:    "Alice Chen",
			CreatedAt: refTime.Add(-2 * time.Hour),
		},
		{
			ShortID:   "e4f5a6b",
			Title:     "fix: handle nil pointer in middleware",
			Author:    "Bob Smith",
			CreatedAt: refTime.Add(-8 * time.Hour),
		},
		{
			ShortID:   "c7d8e9f",
			Title:     "refactor: simplify error handling in service layer",
			Author:    "Carol Jones",
			CreatedAt: refTime.Add(-26 * time.Hour),
		},
		{
			ShortID:   "0a1b2c3",
			Title:     "chore: update golangci-lint to v2.0",
			Author:    "Dave Wilson",
			CreatedAt: refTime.Add(-50 * time.Hour),
		},
		{
			ShortID:   "d4e5f6a",
			Title:     "docs: add API usage examples to README",
			Author:    "Alice Chen",
			CreatedAt: refTime.Add(-74 * time.Hour),
		},
	}
}
