package demo

import (
	"fmt"
	"time"

	"github.com/GF6599/lazylab/internal/gitlab"
)

func demoPipelines(projectID int) []gitlab.PipelineSummary {
	base := projectID * 1000
	branch := "main"

	statuses := []struct {
		status   string
		ref      string
		source   string
		duration float64
		offset   time.Duration
		user     string
	}{
		{"success", branch, "push", 247, -30 * time.Minute, "alice.chen"},
		{"failed", branch, "push", 183, -2 * time.Hour, "bob.smith"},
		{"running", "feature/add-metrics", "push", 0, -5 * time.Minute, "carol.jones"},
		{"success", branch, "schedule", 312, -26 * time.Hour, "scheduled"},
		{"success", "fix/memory-leak", "merge_request_event", 198, -50 * time.Hour, "dave.wilson"},
		{"manual", branch, "web", 0, -74 * time.Hour, "alice.chen"},
		{"canceled", "chore/deps-update", "push", 45, -98 * time.Hour, "bob.smith"},
	}

	pipelines := make([]gitlab.PipelineSummary, len(statuses))
	for i, s := range statuses {
		pipelines[i] = gitlab.PipelineSummary{
			ID:        base + i + 1,
			Status:    s.status,
			Ref:       s.ref,
			SHA:       fmt.Sprintf("%07x%07x", base+i+1, projectID),
			WebURL:    fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/pipelines/%d", base+i+1),
			UpdatedAt: refTime.Add(s.offset),
			Source:    s.source,
			Duration:  s.duration,
			User:      s.user,
		}
	}
	return pipelines
}

func demoStages(pipelineID int) []gitlab.PipelineStage {
	// Vary stages slightly based on pipeline status inferred from ID suffix.
	suffix := pipelineID % 1000
	switch {
	case suffix == 2: // failed pipeline
		return []gitlab.PipelineStage{
			{Name: "build", Status: "success"},
			{Name: "test", Status: "failed"},
			{Name: "lint", Status: "success"},
			{Name: "deploy", Status: "skipped"},
		}
	case suffix == 3: // running pipeline
		return []gitlab.PipelineStage{
			{Name: "build", Status: "success"},
			{Name: "test", Status: "running"},
			{Name: "lint", Status: "running"},
			{Name: "deploy", Status: "created"},
		}
	case suffix == 6: // manual pipeline
		return []gitlab.PipelineStage{
			{Name: "build", Status: "success"},
			{Name: "test", Status: "success"},
			{Name: "lint", Status: "success"},
			{Name: "deploy", Status: "manual"},
		}
	case suffix == 7: // canceled pipeline
		return []gitlab.PipelineStage{
			{Name: "build", Status: "success"},
			{Name: "test", Status: "canceled"},
			{Name: "lint", Status: "canceled"},
			{Name: "deploy", Status: "canceled"},
		}
	default: // success
		return []gitlab.PipelineStage{
			{Name: "build", Status: "success"},
			{Name: "test", Status: "success"},
			{Name: "lint", Status: "success"},
			{Name: "deploy", Status: "success"},
		}
	}
}

func demoJobs(projectID, pipelineID int) []gitlab.PipelineJob {
	stages := demoStages(pipelineID)
	jobBase := pipelineID * 100
	var jobs []gitlab.PipelineJob
	for i, stage := range stages {
		jobs = append(jobs, gitlab.PipelineJob{
			ID:                jobBase + i + 1,
			Name:              stage.Name,
			Stage:             stage.Name,
			Status:            stage.Status,
			WebURL:            fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/jobs/%d", jobBase+i+1),
			Duration:          float64(30 + i*20),
			StartedAt:         refTime.Add(-time.Duration(i) * time.Hour),
			FinishedAt:        refTime.Add(-time.Duration(i)*time.Hour + 90*time.Second),
			RunnerDescription: "shared-runner-01",
		})
		// Add a second job for the test stage.
		if stage.Name == "test" {
			jobs = append(jobs, gitlab.PipelineJob{
				ID:                jobBase + i + 10,
				Name:              "test:integration",
				Stage:             "test",
				Status:            stage.Status,
				WebURL:            fmt.Sprintf("https://gitlab.example.com/acme-corp/project/-/jobs/%d", jobBase+i+10),
				Duration:          float64(60 + i*15),
				StartedAt:         refTime.Add(-time.Duration(i) * time.Hour),
				FinishedAt:        refTime.Add(-time.Duration(i)*time.Hour + 120*time.Second),
				RunnerDescription: "shared-runner-02",
			})
		}
	}
	return jobs
}

func demoJobTrace(jobID int) string {
	return fmt.Sprintf(`Running with gitlab-runner 16.11.0 (abc1234f)
  on shared-runner-01 abcDEF12
Preparing the "docker" executor
Using Docker executor with image golang:1.24-alpine ...
Pulling docker image golang:1.24-alpine ...
Using docker image sha256:a1b2c3d4... for golang:1.24-alpine with digest golang@sha256:e5f6a7b8...

$ cd /builds/acme-corp/project
$ go mod download
go: downloading github.com/stretchr/testify v1.9.0
go: downloading github.com/charmbracelet/bubbletea v1.3.4
go: downloading github.com/charmbracelet/lipgloss v1.1.0

$ go build ./...
Build completed successfully.

$ go test -race -count=1 ./...
ok   acme-corp/project/internal/handler   0.847s
ok   acme-corp/project/internal/service    1.203s
ok   acme-corp/project/pkg/config          0.124s

Uploading artifacts...
coverage.out: found 1 matching artifact file
Uploading artifacts as "archive" to coordinator... 200 OK

Job succeeded (job ID: %d)
`, jobID)
}
