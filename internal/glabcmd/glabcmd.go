// Package glabcmd maps a focused lazylab entity to the equivalent glab CLI commands.
//
// It is a pure projection from UI state to intent: the TUI hands it a Selection
// describing what is focused, and it returns the ordered glab invocations the hotkeys
// surface. Command [0] is the yank default; the full list populates the preview overlay.
// The package deliberately knows nothing about the UI or the GitLab client so the
// mapping stays trivially testable and free of rendering or I/O concerns.
package glabcmd

import (
	"fmt"
	"strings"
)

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
// target explicitly. Host is the full instance URL; when it names anything other than
// gitlab.com the project is spelled as a full repo URL so the pasted command does not
// resolve against glab's own default host. Only the fields relevant to Kind are read.
type Selection struct {
	Kind        Kind
	ProjectPath string
	Host        string

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
	target := shellQuote(repoArg(sel))
	repo := "-R " + target

	switch sel.Kind {
	case KindProject:
		return []Command{
			{Label: "View project", Cmd: "glab repo view " + target},
		}
	case KindPipeline:
		return pipelineCommands(sel, repo)
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

// pipelineCommands puts the ID-precise lookup first: it is the yank default and the
// only form guaranteed to target the selected run, since "ci view <ref>" resolves the
// latest pipeline on the ref. The ref-based view is offered only for plain branch or
// tag names; detached refs like refs/merge-requests/57/head are not resolvable by
// ci view's positional argument.
func pipelineCommands(sel Selection, repo string) []Command {
	cmds := []Command{
		{Label: "Get pipeline by ID", Cmd: fmt.Sprintf("glab ci get -p %d %s", sel.PipelineID, repo)},
	}
	if sel.Ref != "" && !strings.HasPrefix(sel.Ref, "refs/") {
		cmds = append(cmds, Command{
			Label: "View latest pipeline on ref",
			Cmd:   fmt.Sprintf("glab ci view %s %s", shellQuote(sel.Ref), repo),
		})
	}
	return append(cmds,
		Command{Label: "List pipelines", Cmd: "glab ci list " + repo},
		Command{Label: "Cancel pipeline", Cmd: fmt.Sprintf("glab ci cancel pipeline %d %s", sel.PipelineID, repo)},
	)
}

// repoArg names the target project for glab. gitlab.com is glab's default host, so a
// bare path stays short and readable there; any other instance is spelled as a full
// URL because a bare path would resolve against whatever default host the user's glab
// is configured with.
func repoArg(sel Selection) string {
	if sel.Host == "" || instanceHostname(sel.Host) == "gitlab.com" {
		return sel.ProjectPath
	}
	return strings.TrimSuffix(sel.Host, "/") + "/" + sel.ProjectPath
}

// instanceHostname extracts the bare hostname from an instance URL, tolerating a
// missing scheme, a port, and a trailing path.
func instanceHostname(host string) string {
	h := strings.TrimPrefix(host, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexAny(h, "/:"); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

// shellQuote returns s unchanged when every character is one a POSIX shell treats
// literally, keeping common refs and paths readable in the overlay. Anything else is
// single-quoted, with embedded single quotes escaped the standard POSIX way, because
// git ref names may legally contain metacharacters (; $ & # backticks, parens)
// that would run or truncate a second command when the yanked invocation is pasted.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsFunc(s, shellUnsafe) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellUnsafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("@%_+=:,./-", r)
}
