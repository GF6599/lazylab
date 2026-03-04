// matrix.go handles GitLab CI matrix job grouping for the stages table.
//
// GitLab's "parallel:matrix" feature creates multiple jobs from one definition
// with varying variables. These jobs are named "base: [var1, var2]" by
// convention. Without grouping, a 3x3 matrix would flood the stages table with
// 9 individual rows. This file parses the naming convention, groups jobs by
// base name within each stage, and emits a tree structure:
//
//	▾ lint [3]          ← rowKindMatrixGroup (aggregated status)
//	  ├─ go, v1.21      ← rowKindMatrixChild
//	  ├─ go, v1.22      ← rowKindMatrixChild
//	  └─ go, v1.23      ← rowKindMatrixChild
//
// Bridge jobs (downstream/child pipelines) use a similar expand/collapse
// pattern but are toggled manually since their child jobs require an extra
// API call to a different project.
//
// Status aggregation uses worst-case semantics: if any child job failed, the
// group shows "failed". Priority order mirrors internal/gitlab/pipelines.go.

package ui

import (
	"fmt"
	"regexp"
	"strings"

	"lazylab/internal/gitlab"
)

// stageJobRowKind distinguishes the five row types in the stage table.
type stageJobRowKind int

const (
	rowKindJob         stageJobRowKind = iota // Regular (non-matrix) job
	rowKindMatrixGroup                        // Always-expanded group header
	rowKindMatrixChild                        // Sub-job within a group
	rowKindBridge                             // Bridge job with downstream pipeline
	rowKindBridgeChild                        // Child pipeline job under an expanded bridge
)

// stageJobRow is the rich row model for the stage table. It carries enough
// context to render tree lines (├─/└─), compute aggregate statuses, and map
// cursor positions back to actionable jobs for retry/cancel/play operations.
type stageJobRow struct {
	Kind           stageJobRowKind
	Job            *gitlab.PipelineJob    // Set for rowKindJob, rowKindMatrixChild, and rowKindBridgeChild
	Jobs           []gitlab.PipelineJob   // All sub-jobs for rowKindMatrixGroup
	Bridge         *gitlab.PipelineBridge // Set for rowKindBridge
	GroupKey       string                 // "stage:baseName" or "bridge:<id>" for expand state lookup
	BaseName       string                 // Parsed base name (without matrix vars)
	Vars           string                 // Matrix variables (content inside brackets)
	Stage          string                 // Stage name
	Status         string                 // Aggregated for groups, direct for jobs
	IsLast         bool                   // Last child in a group (for └─)
	ChildProjectID int                    // Project ID for rowKindBridgeChild (may differ from parent)
}

// matrixNameRe matches GitLab's matrix job naming convention: "base: [var1, var2]".
var matrixNameRe = regexp.MustCompile(`^(.+?):\s*\[(.+)\]$`)

// parseMatrixName splits a matrix job name into base and variables.
// Non-matrix names return the full name as baseName with isMatrix=false.
func parseMatrixName(name string) (string, string, bool) {
	m := matrixNameRe.FindStringSubmatch(name)
	if m == nil {
		return name, "", false
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), true
}

// matrixGroup holds jobs that share a base name within a stage.
type matrixGroup struct {
	baseName string
	isMatrix bool
	jobs     []gitlab.PipelineJob
}

// buildStageJobRows flattens the (stages, jobs, bridges) triplet into a single
// slice suitable for the stage table. Processing order:
//  1. For each stage (preserving pipeline stage order), collect matching jobs.
//  2. Group jobs by parsed base name; single non-matrix jobs become rowKindJob,
//     groups become rowKindMatrixGroup + rowKindMatrixChild rows.
//  3. Append bridge rows for the stage. If a bridge is expanded and child jobs
//     have been fetched, emit rowKindBridgeChild rows underneath.
//
// The expanded map and childJobs are owned by pipelineViewState and updated
// when the user toggles bridges or child job fetches complete.
func buildStageJobRows(stages []gitlab.PipelineStage, jobs []gitlab.PipelineJob, bridges []gitlab.PipelineBridge, expanded map[string]bool, childJobs map[int][]gitlab.PipelineJob) []stageJobRow {
	if len(stages) == 0 || (len(jobs) == 0 && len(bridges) == 0) {
		return nil
	}

	var rows []stageJobRow

	for _, stage := range stages {
		// Collect jobs for this stage in order
		var stageJobs []gitlab.PipelineJob
		for _, job := range jobs {
			if job.Stage == stage.Name {
				stageJobs = append(stageJobs, job)
			}
		}
		if len(stageJobs) > 0 {
			// Group by base name, preserving insertion order
			var groups []matrixGroup
			groupIdx := make(map[string]int) // baseName → index in groups
			for _, job := range stageJobs {
				baseName, _, isMatrix := parseMatrixName(job.Name)
				if idx, ok := groupIdx[baseName]; ok {
					groups[idx].jobs = append(groups[idx].jobs, job)
					if isMatrix {
						groups[idx].isMatrix = true
					}
				} else {
					groupIdx[baseName] = len(groups)
					groups = append(groups, matrixGroup{
						baseName: baseName,
						isMatrix: isMatrix,
						jobs:     []gitlab.PipelineJob{job},
					})
				}
			}

			// Emit rows for each group
			for _, g := range groups {
				groupKey := stage.Name + ":" + g.baseName
				if len(g.jobs) == 1 && !g.isMatrix {
					// Single non-matrix job → regular row
					job := g.jobs[0]
					rows = append(rows, stageJobRow{
						Kind:   rowKindJob,
						Job:    &job,
						Stage:  stage.Name,
						Status: normalizeJobStatus(job.Status),
					})
				} else {
					// Matrix group header
					rows = append(rows, stageJobRow{
						Kind:     rowKindMatrixGroup,
						Jobs:     g.jobs,
						GroupKey: groupKey,
						BaseName: g.baseName,
						Stage:    stage.Name,
						Status:   aggregateMatrixStatus(g.jobs),
					})
					// Always emit children for matrix groups
					for i, job := range g.jobs {
						_, vars, _ := parseMatrixName(job.Name)
						j := job // copy for pointer stability
						rows = append(rows, stageJobRow{
							Kind:     rowKindMatrixChild,
							Job:      &j,
							GroupKey: groupKey,
							BaseName: g.baseName,
							Vars:     vars,
							Stage:    stage.Name,
							Status:   normalizeJobStatus(job.Status),
							IsLast:   i == len(g.jobs)-1,
						})
					}
				}
			}
		}

		// Emit bridge rows for bridges matching this stage
		for i := range bridges {
			if bridges[i].Stage != stage.Name {
				continue
			}
			b := bridges[i] // copy for pointer stability
			groupKey := fmt.Sprintf("bridge:%d", b.ID)
			rows = append(rows, stageJobRow{
				Kind:     rowKindBridge,
				Bridge:   &b,
				GroupKey: groupKey,
				BaseName: b.Name,
				Stage:    stage.Name,
				Status:   normalizeJobStatus(b.Status),
			})
			// Emit child rows if expanded and has downstream pipeline
			if expanded[groupKey] && b.DownstreamPipeline != nil {
				dsID := b.DownstreamPipeline.ID
				dsProjectID := b.DownstreamPipeline.ProjectID
				if cJobs, ok := childJobs[dsID]; ok && len(cJobs) > 0 {
					for i, cj := range cJobs {
						j := cj // copy for pointer stability
						rows = append(rows, stageJobRow{
							Kind:           rowKindBridgeChild,
							Job:            &j,
							Bridge:         &b,
							GroupKey:       groupKey,
							BaseName:       j.Name,
							Stage:          stage.Name,
							Status:         normalizeJobStatus(j.Status),
							IsLast:         i == len(cJobs)-1,
							ChildProjectID: dsProjectID,
						})
					}
				} else {
					// Placeholder while child jobs load
					rows = append(rows, stageJobRow{
						Kind:     rowKindBridge,
						Bridge:   &b,
						GroupKey: groupKey,
						BaseName: b.Name,
						Stage:    stage.Name,
						Status:   normalizeJobStatus(b.DownstreamPipeline.Status),
						IsLast:   true,
					})
				}
			}
		}
	}

	return rows
}

// aggregateMatrixStatus returns the highest-priority (worst-case) status among
// a group of matrix jobs. "failed" beats everything; "success" only wins if
// all jobs succeeded.
func aggregateMatrixStatus(jobs []gitlab.PipelineJob) string {
	if len(jobs) == 0 {
		return "unknown"
	}
	result := normalizeJobStatus(jobs[0].Status)
	for _, job := range jobs[1:] {
		candidate := normalizeJobStatus(job.Status)
		if statusRank(candidate) < statusRank(result) {
			result = candidate
		}
	}
	return result
}

// statusRank returns the priority rank of a job status. Lower rank = higher
// priority (shown when aggregating). Mirrors stageStatusPriority in
// internal/gitlab/pipelines.go.
func statusRank(status string) int {
	ranks := map[string]int{
		"failed":               0,
		"canceled":             1,
		"manual":               2,
		"blocked":              2,
		"running":              3,
		"pending":              4,
		"waiting_for_resource": 4,
		"scheduled":            4,
		"created":              5,
		"success":              6,
		"skipped":              7,
	}
	if r, ok := ranks[status]; ok {
		return r
	}
	return 9
}

func normalizeJobStatus(status string) string {
	s := strings.TrimSpace(strings.ToLower(status))
	if s == "" {
		return "unknown"
	}
	return s
}

// bridgePreviewContent builds a text summary for the detail pane when a bridge
// row is selected. Unlike regular jobs, bridges have no trace/log output, so
// this provides metadata (status, URL, downstream pipeline info) instead.
func bridgePreviewContent(bridge *gitlab.PipelineBridge, isChild bool) string {
	if bridge == nil {
		return ""
	}
	b := &strings.Builder{}
	if isChild && bridge.DownstreamPipeline != nil {
		ds := bridge.DownstreamPipeline
		b.WriteString(fmt.Sprintf("Child Pipeline #%d\n", ds.ID))
		b.WriteString(fmt.Sprintf("Status: %s\n", ds.Status))
		if ds.WebURL != "" {
			b.WriteString(fmt.Sprintf("URL:    %s\n", ds.WebURL))
		}
	} else {
		b.WriteString(fmt.Sprintf("Bridge: %s\n", bridge.Name))
		b.WriteString(fmt.Sprintf("Stage:  %s\n", bridge.Stage))
		b.WriteString(fmt.Sprintf("Status: %s\n", bridge.Status))
		if bridge.Ref != "" {
			b.WriteString(fmt.Sprintf("Ref:    %s\n", bridge.Ref))
		}
		if bridge.Duration > 0 {
			b.WriteString(fmt.Sprintf("Duration: %.1fs\n", bridge.Duration))
		}
		if bridge.DownstreamPipeline != nil {
			ds := bridge.DownstreamPipeline
			b.WriteString(fmt.Sprintf("\nChild Pipeline #%d\n", ds.ID))
			b.WriteString(fmt.Sprintf("Status: %s\n", ds.Status))
			if ds.WebURL != "" {
				b.WriteString(fmt.Sprintf("URL:    %s\n", ds.WebURL))
			}
		}
	}
	return b.String()
}

// selectedStageJobRow returns the rich row for the current stage table cursor,
// or nil if out of bounds.
func (m *Model) selectedStageJobRow() *stageJobRow {
	idx := m.pipelineView.stageSelected
	if idx < 0 || idx >= len(m.pipelineView.stageJobRows) {
		return nil
	}
	return &m.pipelineView.stageJobRows[idx]
}
