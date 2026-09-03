// variables_form.go implements the key/value editor shared by the play-job and
// run-pipeline modals.
//
// The form owns no Model state: every method takes and returns a value, so the
// two modals embed it without either owning it, and its behaviour is testable
// without a Bubble Tea program.
//
// Rows are addressed by a flattened field index: row i owns index 2i (its key)
// and 2i+1 (its value). That keeps focus cycling a single wrapping increment
// rather than a nested row/column pair.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// variableRow is one key/value pair under edit.
type variableRow struct {
	key   textinput.Model
	value textinput.Model
}

// variablesForm is a list of key/value rows with one focused field.
type variablesForm struct {
	rows  []variableRow
	focus int
}

// newVariableRow returns an empty row with both fields blurred.
func newVariableRow() variableRow {
	return variableRow{
		key:   newModalTextinput("KEY"),
		value: newModalTextinput("value"),
	}
}

// newVariablesForm returns a form holding a single empty row, focused on its
// key. The seeded row means the modal always shows somewhere to type, so an
// empty form and a form the user has not touched look the same.
func newVariablesForm() variablesForm {
	return variablesForm{rows: []variableRow{newVariableRow()}}.applyFocus()
}

// fieldCount is the number of focusable fields: two per row.
func (f variablesForm) fieldCount() int { return len(f.rows) * 2 }

// applyFocus focuses exactly the field at f.focus and blurs every other, after
// clamping the index into range. Every mutation routes through it, so no path
// can leave two inputs consuming the same keystroke.
func (f variablesForm) applyFocus() variablesForm {
	if f.fieldCount() == 0 {
		return f
	}
	if f.focus < 0 || f.focus >= f.fieldCount() {
		f.focus = 0
	}
	for i := range f.rows {
		f.rows[i].key.Blur()
		f.rows[i].value.Blur()
	}
	row, isValue := f.focus/2, f.focus%2 == 1
	if isValue {
		f.rows[row].value.Focus()
	} else {
		f.rows[row].key.Focus()
	}
	return f
}

// blur removes focus from every field, so an owning form can hand the
// keystrokes to a field of its own.
func (f variablesForm) blur() variablesForm {
	for i := range f.rows {
		f.rows[i].key.Blur()
		f.rows[i].value.Blur()
	}
	return f
}

// cycleFocus moves the focus by delta fields, wrapping at both ends.
func (f variablesForm) cycleFocus(delta int) variablesForm {
	n := f.fieldCount()
	if n == 0 {
		return f
	}
	f.focus = ((f.focus+delta)%n + n) % n
	return f.applyFocus()
}

// addRow appends an empty row and focuses its key field, so typing continues
// where the user asked for the new row.
func (f variablesForm) addRow() variablesForm {
	f.rows = append(f.rows, newVariableRow())
	f.focus = (len(f.rows) - 1) * 2
	return f.applyFocus()
}

// removeRow deletes the focused row. The last row is emptied rather than
// removed, because a form with no rows renders no fields and strands the user
// in a modal with nothing to type into.
func (f variablesForm) removeRow() variablesForm {
	if len(f.rows) <= 1 {
		f.rows = []variableRow{newVariableRow()}
		f.focus = 0
		return f.applyFocus()
	}
	row := f.focus / 2
	f.rows = append(f.rows[:row], f.rows[row+1:]...)
	f.focus = min(f.focus, f.fieldCount()-1)
	return f.applyFocus()
}

// update forwards a message to the focused field.
func (f variablesForm) update(msg tea.Msg) (variablesForm, tea.Cmd) {
	if f.fieldCount() == 0 {
		return f, nil
	}
	var cmd tea.Cmd
	row, isValue := f.focus/2, f.focus%2 == 1
	if isValue {
		f.rows[row].value, cmd = f.rows[row].value.Update(msg)
	} else {
		f.rows[row].key, cmd = f.rows[row].key.Update(msg)
	}
	return f, cmd
}

// collect returns the trimmed variables, dropping any row with a blank key. A
// blank row is always present (the form seeds one, and the add key seeds
// another), so dropping it here keeps an empty-keyed variable off the wire.
func (f variablesForm) collect() []gitlab.PipelineVariable {
	out := make([]gitlab.PipelineVariable, 0, len(f.rows))
	for _, row := range f.rows {
		key := strings.TrimSpace(row.key.Value())
		if key == "" {
			continue
		}
		out = append(out, gitlab.PipelineVariable{
			Key:   key,
			Value: strings.TrimSpace(row.value.Value()),
		})
	}
	return out
}

// validate reports the two ways a form silently loses what the user typed: a
// value with no key, which collect drops, and a repeated key, of which GitLab
// keeps only the last. Both are rejected here so the loss surfaces before the
// pipeline runs rather than after it behaves unexpectedly.
func (f variablesForm) validate() error {
	seen := make(map[string]bool, len(f.rows))
	for _, row := range f.rows {
		key := strings.TrimSpace(row.key.Value())
		if key == "" {
			if strings.TrimSpace(row.value.Value()) != "" {
				return fmt.Errorf("a variable value needs a key")
			}
			continue
		}
		if seen[key] {
			return fmt.Errorf("variable %s is set twice", key)
		}
		seen[key] = true
	}
	return nil
}
