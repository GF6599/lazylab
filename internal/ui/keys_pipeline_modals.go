// keys_pipeline_modals.go handles the two modals that trigger CI work with
// variables: playing a manual job, and running a new pipeline.
//
// Both follow the create-MR modal's lifecycle: closed -> active (form shown) ->
// sending (API call in flight) -> closed. Both build a fresh form on open, so no
// value a user typed for one run is carried into the next.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// openPlayJobModal opens the play-job form for a manual job. The caller has
// already established that the row maps to a real, manual job.
func (m Model) openPlayJobModal(target jobActionTarget, jobID int, jobName string) (tea.Model, tea.Cmd) {
	m.pipelineView.playJob = playJobState{
		active:        true,
		projectID:     target.projectID,
		viewProjectID: target.viewProjectID,
		jobID:         jobID,
		jobName:       jobName,
		vars:          newVariablesForm(),
	}
	return m, textinput.Blink
}

// handlePlayJobKey handles keys while the play-job modal is active.
func (m Model) handlePlayJobKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.pipelineView.playJob = playJobState{}
		return m, nil
	case "enter":
		return m.submitPlayJob()
	case "tab":
		m.pipelineView.playJob.vars = m.pipelineView.playJob.vars.cycleFocus(1)
		return m, nil
	case "shift+tab":
		m.pipelineView.playJob.vars = m.pipelineView.playJob.vars.cycleFocus(-1)
		return m, nil
	case "ctrl+n":
		m.pipelineView.playJob.vars = m.pipelineView.playJob.vars.addRow()
		return m, nil
	case "ctrl+d":
		m.pipelineView.playJob.vars = m.pipelineView.playJob.vars.removeRow()
		return m, nil
	default:
		var cmd tea.Cmd
		m.pipelineView.playJob.vars, cmd = m.pipelineView.playJob.vars.update(msg)
		return m, cmd
	}
}

// submitPlayJob validates the form and plays the job. An untouched form
// collects no variables, so Enter straight after opening reproduces the plain
// play this modal replaced.
func (m Model) submitPlayJob() (tea.Model, tea.Cmd) {
	st := m.pipelineView.playJob
	if st.sending {
		return m, nil
	}
	if err := st.vars.validate(); err != nil {
		m.pipelineView.playJob.err = err
		return m, nil
	}
	m.pipelineView.playJob.sending = true
	m.pipelineView.playJob.err = nil
	m.status = fmt.Sprintf("Playing job %s (#%d)...", st.jobName, st.jobID)
	target := jobActionTarget{projectID: st.projectID, viewProjectID: st.viewProjectID}
	return m, playJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, target, st.jobID, st.vars.collect())
}

// openRunPipelineModal opens the run-pipeline form for the focused project,
// seeding the ref from the selected pipeline and falling back to the project's
// default branch.
func (m Model) openRunPipelineModal() (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID == 0 {
		m.status = "Select a project first"
		return m, nil
	}
	ref := m.pipelineView.project.DefaultBranch
	if pipeline := m.selectedPipeline(); pipeline != nil && pipeline.Ref != "" {
		ref = pipeline.Ref
	}
	refInput := newModalTextinput("Branch or tag (required)")
	refInput.SetValue(ref)
	refInput.Focus()

	m.pipelineView.runPipeline = runPipelineState{
		active:    true,
		projectID: m.pipelineView.project.ID,
		ref:       refInput,
		vars:      newVariablesForm(),
	}
	return m, textinput.Blink
}

// handleRunPipelineKey handles keys while the run-pipeline modal is active.
func (m Model) handleRunPipelineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.pipelineView.runPipeline = runPipelineState{}
		return m, nil
	case "enter":
		return m.submitRunPipeline()
	case "tab":
		return m.cycleRunPipelineFocus(1)
	case "shift+tab":
		return m.cycleRunPipelineFocus(-1)
	case "ctrl+n":
		m.pipelineView.runPipeline.vars = m.pipelineView.runPipeline.vars.addRow()
		return m.applyRunPipelineFocus(m.pipelineView.runPipeline.vars.focus + 1)
	case "ctrl+d":
		m.pipelineView.runPipeline.vars = m.pipelineView.runPipeline.vars.removeRow()
		return m.applyRunPipelineFocus(m.pipelineView.runPipeline.vars.focus + 1)
	default:
		return m.updateRunPipelineInput(msg)
	}
}

// fieldCount is the ref field plus one field per variable key and value.
func (s runPipelineState) fieldCount() int { return 1 + s.vars.fieldCount() }

// cycleRunPipelineFocus moves focus across the ref field and the variable
// fields as one wrapping sequence, so Tab walks the whole form.
func (m Model) cycleRunPipelineFocus(delta int) (tea.Model, tea.Cmd) {
	n := m.pipelineView.runPipeline.fieldCount()
	next := ((m.pipelineView.runPipeline.focus+delta)%n + n) % n
	return m.applyRunPipelineFocus(next)
}

// applyRunPipelineFocus focuses field index next, where 0 is the ref field and
// the rest address the variables form. It is the single place that decides
// which input owns the keystrokes, so the ref and a variable field can never
// both be focused.
func (m Model) applyRunPipelineFocus(next int) (tea.Model, tea.Cmd) {
	m.pipelineView.runPipeline.focus = next
	if next == 0 {
		m.pipelineView.runPipeline.ref.Focus()
		m.pipelineView.runPipeline.vars = m.pipelineView.runPipeline.vars.blur()
		return m, textinput.Blink
	}
	m.pipelineView.runPipeline.ref.Blur()
	m.pipelineView.runPipeline.vars.focus = next - 1
	m.pipelineView.runPipeline.vars = m.pipelineView.runPipeline.vars.applyFocus()
	return m, textinput.Blink
}

// updateRunPipelineInput forwards a key to whichever field holds the focus.
func (m Model) updateRunPipelineInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.pipelineView.runPipeline.focus == 0 {
		m.pipelineView.runPipeline.ref, cmd = m.pipelineView.runPipeline.ref.Update(msg)
		return m, cmd
	}
	m.pipelineView.runPipeline.vars, cmd = m.pipelineView.runPipeline.vars.update(msg)
	return m, cmd
}

// submitRunPipeline validates the form and triggers the pipeline.
func (m Model) submitRunPipeline() (tea.Model, tea.Cmd) {
	st := m.pipelineView.runPipeline
	if st.sending {
		return m, nil
	}
	ref := strings.TrimSpace(st.ref.Value())
	if ref == "" {
		m.pipelineView.runPipeline.err = fmt.Errorf("a branch or tag is required")
		return m, nil
	}
	if err := st.vars.validate(); err != nil {
		m.pipelineView.runPipeline.err = err
		return m, nil
	}
	m.pipelineView.runPipeline.sending = true
	m.pipelineView.runPipeline.err = nil
	m.status = fmt.Sprintf("Triggering pipeline on %s...", ref)
	return m, createPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, st.projectID, ref, st.vars.collect())
}
