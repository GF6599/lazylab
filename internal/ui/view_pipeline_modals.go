// view_pipeline_modals.go renders the play-job and run-pipeline modals.
//
// Both draw the same variables table, so the key/value rows look and behave
// identically wherever CI work is triggered.

package ui

import (
	"fmt"
	"strings"

	"github.com/GF6599/lazylab/internal/redacting"
)

// modalInnerWidth sizes a centered modal against the terminal, matching the
// create-MR form so every modal in the app lands on the same footprint. The
// result is the total width handed to modalBorderStyle.Width.
func modalInnerWidth(width int) int {
	inner := width / 2
	if inner < 50 {
		inner = min(width-4, 70)
	}
	if inner < 30 {
		inner = max(20, width-6)
	}
	return inner
}

// modalContentWidth is the space left for content once modalBorderStyle's
// horizontal padding is taken out. lipgloss counts padding inside Width, so
// laying content out against the full inner width overflows and wraps.
func modalContentWidth(inner int) int {
	return max(8, inner-4)
}

// textinputChrome is the cells a textinput.Model draws on top of its Width: a
// 2-cell prompt plus the cell it reserves for the cursor. Measured at 3 against
// bubbles v1.0.0. Sizing an input without it overflows the row, and lipgloss
// wraps the surplus, which puts the row's closing marker on a line of its own.
const textinputChrome = 3

// renderVariablesForm draws the key/value rows with the focused field marked,
// so the user can see which input the next keystroke reaches.
func renderVariablesForm(f variablesForm, width int) string {
	b := &strings.Builder{}
	b.WriteString(modalLabelStyle.Render("Variables"))
	b.WriteString("\n")
	// Each row draws a 1-cell marker either side, a 2-cell gap between the two
	// inputs, and each input's own chrome.
	const rowOverhead = 2 + 2 + 2*textinputChrome
	avail := max(12, width-rowOverhead)
	keyWidth := max(6, avail/3)
	valueWidth := max(6, avail-keyWidth)
	// A row is marked when either of its fields holds the focus, so the marker
	// tracks the row being edited rather than the individual input.
	focusedRow := f.focus / 2
	for i := range f.rows {
		// The inputs are copied before sizing: variablesForm is passed by value
		// but its rows slice aliases the model's backing array, so writing Width
		// through f.rows would let a render mutate live state.
		key, value := f.rows[i].key, f.rows[i].value
		key.Width = keyWidth
		value.Width = valueWidth
		b.WriteString(markerFor(i, focusedRow).render(key.View() + "  " + value.View()))
		b.WriteString("\n")
	}
	return b.String()
}

// renderPlayJobModal renders the play-job form as a centered overlay.
func renderPlayJobModal(m Model, width int) string {
	inner := modalInnerWidth(width)
	st := m.pipelineView.playJob

	b := &strings.Builder{}
	title := fmt.Sprintf("Play job: %s", st.jobName)
	if strings.TrimSpace(st.jobName) == "" {
		title = fmt.Sprintf("Play job #%d", st.jobID)
	}
	content := modalContentWidth(inner)
	b.WriteString(detailHeaderStyle.Render(clampLine(title, content)))
	b.WriteString("\n\n")
	b.WriteString(renderVariablesForm(st.vars, content))
	b.WriteString("\n")
	writeModalFooter(b, st.err, st.sending, "Playing job...", content)
	return modalBorderStyle.Width(inner).Render(strings.TrimSuffix(b.String(), "\n"))
}

// renderRunPipelineModal renders the run-pipeline form as a centered overlay.
func renderRunPipelineModal(m Model, width int) string {
	inner := modalInnerWidth(width)
	st := m.pipelineView.runPipeline

	content := modalContentWidth(inner)
	b := &strings.Builder{}
	b.WriteString(detailHeaderStyle.Render(clampLine("Run pipeline · "+m.pipelineView.project.PathWithNamespace, content)))
	b.WriteString("\n\n")

	refLabel := modalLabelStyle
	if st.focus == 0 {
		refLabel = modalFocusLabelStyle
	}
	st.ref.Width = max(6, content-textinputChrome)
	b.WriteString(refLabel.Render("Branch or tag"))
	b.WriteString("\n")
	b.WriteString(st.ref.View())
	b.WriteString("\n\n")
	b.WriteString(renderVariablesForm(st.vars, content))
	b.WriteString("\n")
	writeModalFooter(b, st.err, st.sending, "Triggering pipeline...", content)
	return modalBorderStyle.Width(inner).Render(strings.TrimSuffix(b.String(), "\n"))
}

// writeModalFooter appends the error, the in-flight notice, and the key hints
// shared by both trigger modals. The error is redacted because GitLab echoes
// request context into some rejections, and a variable value is often a secret.
func writeModalFooter(b *strings.Builder, err error, sending bool, sendingLabel string, inner int) {
	if err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(redacting.Redact(err.Error()), inner)))
		b.WriteString("\n")
	}
	if sending {
		b.WriteString(explorerHintStyle.Render(sendingLabel))
		b.WriteString("\n")
	}
	b.WriteString(explorerHintStyle.Render(clampLine("tab cycle · ctrl+n add row · ctrl+d remove row", inner)))
	b.WriteString("\n")
	b.WriteString(explorerHintStyle.Render(clampLine("enter run · esc cancel", inner)))
}
