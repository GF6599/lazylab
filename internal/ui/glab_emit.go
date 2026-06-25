package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/glabcmd"
)

const glabNoCommandStatus = "No glab command for this selection"

// glabPreviewState backs the command-preview overlay: the glab commands available
// for the focused entity, the cursor into them, and the owning project for the
// title. The zero value means the overlay is closed.
type glabPreviewState struct {
	active   bool
	commands []glabcmd.Command
	cursor   int
	project  string
}

// glabSelection projects the currently focused panel and its selected item into a
// glabcmd.Selection. The bool is false when nothing emittable is focused (an empty
// list, or a bridge/downstream child job whose project is identified only by numeric
// ID and so cannot form a correct -R). The project path always comes from the panel
// that owns the item, since pipeline/job/MR entities do not carry their own path.
func (m Model) glabSelection() (glabcmd.Selection, bool) {
	active := m.focus.Active
	if active == PanelDetail {
		active = m.focus.PrevActive // detail mirrors whichever sidebar panel opened it
	}

	switch active {
	case PanelProjects:
		if p, ok := m.selectedProject(); ok {
			return glabcmd.Selection{Kind: glabcmd.KindProject, ProjectPath: p.PathWithNamespace}, true
		}
	case PanelPipelines:
		if pl := m.selectedPipeline(); pl != nil {
			return glabcmd.Selection{
				Kind:        glabcmd.KindPipeline,
				ProjectPath: m.pipelineView.project.PathWithNamespace,
				Ref:         pl.Ref,
				PipelineID:  pl.ID,
			}, true
		}
	case PanelStages:
		if row := m.selectedStageJobRow(); row != nil && row.Kind == rowKindBridgeChild {
			return glabcmd.Selection{}, false
		}
		if job := m.selectedPipelineJob(); job != nil {
			return glabcmd.Selection{
				Kind:        glabcmd.KindJob,
				ProjectPath: m.pipelineView.project.PathWithNamespace,
				JobID:       job.ID,
			}, true
		}
	case PanelMRs:
		if mr := m.mrView.selectedMR(); mr != nil {
			return glabcmd.Selection{
				Kind:        glabcmd.KindMergeRequest,
				ProjectPath: m.mrView.project.PathWithNamespace,
				MRIID:       mr.IID,
			}, true
		}
	}

	return glabcmd.Selection{}, false
}

// glabCommands resolves the focused selection into its ordered glab commands and
// the owning project path. ok is false when nothing emittable is focused. It is the
// shared seam both hotkeys read: yank takes the first command, the overlay shows all.
func (m Model) glabCommands() (cmds []glabcmd.Command, project string, ok bool) {
	sel, ok := m.glabSelection()
	if !ok {
		return nil, "", false
	}
	cmds = glabcmd.For(sel)
	if len(cmds) == 0 {
		return nil, "", false
	}
	return cmds, sel.ProjectPath, true
}

// yankGlabCommand copies the default (first) glab command for the focused entity to
// the clipboard; with nothing emittable it sets a status instead.
func (m *Model) yankGlabCommand() tea.Cmd {
	cmds, _, ok := m.glabCommands()
	if !ok {
		m.status = glabNoCommandStatus
		return nil
	}
	return writeClipboardCmd(cmds[0].Cmd, "Copied: "+cmds[0].Cmd)
}

// openGlabPreview opens the command-preview overlay for the focused entity; with
// nothing emittable it sets a status and leaves the overlay closed.
func (m *Model) openGlabPreview() tea.Cmd {
	cmds, project, ok := m.glabCommands()
	if !ok {
		m.status = glabNoCommandStatus
		return nil
	}
	m.glabPreview = glabPreviewState{active: true, commands: cmds, project: project}
	return nil
}

// glabPreviewMove moves the overlay cursor by delta, clamped to the command list.
func (m *Model) glabPreviewMove(delta int) {
	n := len(m.glabPreview.commands)
	if n == 0 {
		return
	}
	c := m.glabPreview.cursor + delta
	c = max(c, 0)
	c = min(c, n-1)
	m.glabPreview.cursor = c
}

// glabPreviewSelected returns the command currently under the overlay cursor.
func (m Model) glabPreviewSelected() (glabcmd.Command, bool) {
	i := m.glabPreview.cursor
	if i < 0 || i >= len(m.glabPreview.commands) {
		return glabcmd.Command{}, false
	}
	return m.glabPreview.commands[i], true
}

// handleGlabPreviewKey routes keys while the command-preview overlay is open:
// j/k navigate, enter or y copies the highlighted command and closes, esc dismisses.
func (m Model) handleGlabPreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.glabPreview = glabPreviewState{}
		return m, nil
	case "j", "down":
		m.glabPreviewMove(1)
		return m, nil
	case "k", "up":
		m.glabPreviewMove(-1)
		return m, nil
	case "enter", "y":
		cmd, ok := m.glabPreviewSelected()
		m.glabPreview = glabPreviewState{}
		if !ok {
			return m, nil
		}
		return m, writeClipboardCmd(cmd.Cmd, "Copied: "+cmd.Cmd)
	}
	return m, nil
}
