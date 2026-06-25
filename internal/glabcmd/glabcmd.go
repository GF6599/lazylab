// Package glabcmd maps a focused lazylab entity to the equivalent glab CLI commands.
//
// It is a pure projection from UI state to intent: the TUI hands it a Selection
// describing what is focused, and it returns the ordered glab invocations the hotkeys
// surface. Command [0] is the yank default; the full list populates the preview overlay.
// The package deliberately knows nothing about the UI or the GitLab client so the
// mapping stays trivially testable and free of rendering or I/O concerns.
package glabcmd

import "fmt"

// Kind identifies which kind of entity is focused. The zero value, KindNone, maps to
// no commands so a zero Selection is safely inert.
type Kind int

const (
	KindNone Kind = iota
	KindProject
	KindPipeline
	KindJob
	KindMergeRequest
)

// Selection describes the focused entity and the project that owns it.
//
// ProjectPath is the group/project (path-with-namespace) used for the glab -R flag and
// must be set for any command to be emitted: lazylab browses arbitrary projects rather
// than the current working directory's repository, so every command has to name its
// target explicitly. Only the fields relevant to Kind are read.
type Selection struct {
	Kind        Kind
	ProjectPath string

	Ref        string // pipeline branch or tag (KindPipeline)
	PipelineID int    // KindPipeline
	JobID      int    // KindJob
	MRIID      int    // KindMergeRequest
}

// Command is a single glab invocation paired with a short label for the preview overlay.
type Command struct {
	Label string
	Cmd   string
}

// For returns the glab commands available for sel, most-natural first. It returns nil
// when the selection is unsupported or lacks the project context required for -R.
func For(sel Selection) []Command {
	if sel.ProjectPath == "" {
		return nil
	}
	repo := "-R " + sel.ProjectPath

	switch sel.Kind {
	case KindProject:
		return []Command{
			{Label: "View project", Cmd: "glab repo view " + sel.ProjectPath},
		}
	case KindPipeline:
		return []Command{
			{Label: "View pipeline (branch)", Cmd: fmt.Sprintf("glab ci view %s %s", sel.Ref, repo)},
			{Label: "Get pipeline by ID", Cmd: fmt.Sprintf("glab ci get -p %d %s", sel.PipelineID, repo)},
			{Label: "List pipelines", Cmd: fmt.Sprintf("glab ci list %s", repo)},
			{Label: "Cancel pipeline", Cmd: fmt.Sprintf("glab ci cancel pipeline %d %s", sel.PipelineID, repo)},
		}
	case KindJob:
		return []Command{
			{Label: "Trace job log", Cmd: fmt.Sprintf("glab ci trace %d %s", sel.JobID, repo)},
			{Label: "Retry job", Cmd: fmt.Sprintf("glab ci retry %d %s", sel.JobID, repo)},
			{Label: "Cancel job", Cmd: fmt.Sprintf("glab ci cancel job %d %s", sel.JobID, repo)},
		}
	case KindMergeRequest:
		return []Command{
			{Label: "View merge request", Cmd: fmt.Sprintf("glab mr view %d %s", sel.MRIID, repo)},
			{Label: "Diff merge request", Cmd: fmt.Sprintf("glab mr diff %d %s", sel.MRIID, repo)},
		}
	}
	return nil
}
