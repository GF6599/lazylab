package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/GF6599/lazylab/internal/cliout"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// pipelineStatusRecord is the structured representation of a pipeline
// snapshot rendered by --format json. Kept here rather than in cliout
// because its shape is CLI-specific (combines PipelineSpec with the
// gitlab.PipelineSummary fields the user cares about).
type pipelineStatusRecord struct {
	ProjectID   int           `json:"project_id"`
	ProjectPath string        `json:"project_path,omitempty"`
	PipelineID  int           `json:"pipeline_id"`
	Status      string        `json:"status"`
	Ref         string        `json:"ref"`
	SHA         string        `json:"sha"`
	WebURL      string        `json:"web_url"`
	Source      string        `json:"source,omitempty"`
	UpdatedAt   string        `json:"updated_at,omitempty"`
	Stages      []stageRecord `json:"stages,omitempty"`
}

// stageRecord is the per-stage JSON shape nested inside a status record.
// Distinct from gitlab.PipelineStage so we can keep the public CLI JSON
// schema independent from internal renames.
type stageRecord struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// pipelineListRecord is the JSON-shape for one pipeline in `list`
// output. Mirrors pipelineStatusRecord but without stages — listing
// implies "I don't know which pipeline I want yet, give me the
// overview." Stages would balloon the per-row API cost and the table
// output.
type pipelineListRecord struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	Source    string `json:"source,omitempty"`
	WebURL    string `json:"web_url"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// toPipelineStatusRecord converts the in-memory pipeline + spec + stages
// triple into the public JSON record shape. UpdatedAt is normalized to
// RFC3339 UTC so consumers piping into jq get a stable timestamp format
// regardless of the host's locale.
func toPipelineStatusRecord(spec gitlab.PipelineSpec, p gitlab.PipelineSummary, stages []gitlab.PipelineStage) pipelineStatusRecord {
	r := pipelineStatusRecord{
		ProjectID:   spec.ProjectID,
		ProjectPath: spec.ProjectPath,
		PipelineID:  p.ID,
		Status:      p.Status,
		Ref:         p.Ref,
		SHA:         p.SHA,
		WebURL:      p.WebURL,
		Source:      p.Source,
	}
	if !p.UpdatedAt.IsZero() {
		r.UpdatedAt = p.UpdatedAt.UTC().Format(time.RFC3339)
	}
	for _, s := range stages {
		r.Stages = append(r.Stages, stageRecord{Name: s.Name, Status: s.Status})
	}
	return r
}

// writePipelineStatus renders one pipeline-status snapshot in the
// requested format. JSON uses the RFC3339 UpdatedAt from the record
// (machine-friendly, sortable); the table form swaps in a humanized
// "5m ago"-style timestamp so the interactive reader doesn't have to
// translate UTC in their head.
func writePipelineStatus(w io.Writer, spec gitlab.PipelineSpec, p gitlab.PipelineSummary, stages []gitlab.PipelineStage, format cliout.Format) error {
	rec := toPipelineStatusRecord(spec, p, stages)
	if format == cliout.FormatJSON {
		return cliout.PrintJSON(w, rec)
	}
	rows := []cliout.KV{
		{Key: "Pipeline", Value: strconv.Itoa(p.ID)},
		{Key: "Project", Value: projectLabel(spec)},
		{Key: "Status", Value: p.Status},
		{Key: "Ref", Value: p.Ref},
		{Key: "SHA", Value: shortDisplaySHA(p.SHA)},
		{Key: "Source", Value: p.Source},
		{Key: "Updated", Value: cliout.HumanizeTime(p.UpdatedAt)},
		{Key: "URL", Value: p.WebURL},
	}
	if len(stages) > 0 {
		rows = append(rows, cliout.KV{Key: "Stages", Value: ""})
		for _, s := range stages {
			rows = append(rows, cliout.KV{Key: "  " + s.Name, Value: s.Status})
		}
	}
	return cliout.PrintKV(w, rows)
}

// writeWatchLine renders one row of the watch transition log. The table
// form is a single line per status change so logs stay scannable. JSON
// emits one record per line (newline-delimited JSON, jq-compatible).
func writeWatchLine(w io.Writer, spec gitlab.PipelineSpec, p gitlab.PipelineSummary, format cliout.Format) error {
	if format == cliout.FormatJSON {
		return cliout.PrintJSON(w, toPipelineStatusRecord(spec, p, nil))
	}
	ts := time.Now().UTC().Format("15:04:05")
	_, err := fmt.Fprintf(w, "%s  pipeline %d  %s  ref=%s  sha=%s\n",
		ts, p.ID, p.Status, p.Ref, shortDisplaySHA(p.SHA))
	return err
}

// writePipelineList renders a slice of pipelines in the requested
// format. The empty-list path emits a single line to stdout (table) or a
// `null` JSON value (jq treats this as the absence of records) so
// downstream scripts don't have to special-case "no rows" against the
// table-vs-json branch.
func writePipelineList(w io.Writer, pipelines []gitlab.PipelineSummary, format cliout.Format) error {
	if format == cliout.FormatJSON {
		records := make([]pipelineListRecord, len(pipelines))
		for i, p := range pipelines {
			records[i] = pipelineListRecord{
				ID:     p.ID,
				Status: p.Status,
				Ref:    p.Ref,
				SHA:    p.SHA,
				Source: p.Source,
				WebURL: p.WebURL,
			}
			if !p.UpdatedAt.IsZero() {
				records[i].UpdatedAt = p.UpdatedAt.UTC().Format(time.RFC3339)
			}
		}
		return cliout.PrintJSON(w, records)
	}
	if len(pipelines) == 0 {
		_, err := fmt.Fprintln(w, "no pipelines found")
		return err
	}
	tbl := cliout.NewTable("ID", "STATUS", "REF", "SHA", "UPDATED", "SOURCE")
	for _, p := range pipelines {
		tbl.AddRow(
			strconv.Itoa(p.ID),
			p.Status,
			p.Ref,
			shortDisplaySHA(p.SHA),
			cliout.HumanizeTime(p.UpdatedAt),
			p.Source,
		)
	}
	return tbl.Render(w)
}

// shortDisplaySHA returns the first eight characters of sha (the
// abbreviation git uses by default) for compact display. Long SHAs are
// preserved in the JSON output via the raw .sha field.
func shortDisplaySHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}
