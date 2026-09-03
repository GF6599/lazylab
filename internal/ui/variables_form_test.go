package ui

import (
	"slices"
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// variablesFormWith returns a form holding one row per pair, so a test states
// its input as data rather than as a sequence of keystrokes.
func variablesFormWith(pairs ...[2]string) variablesForm {
	f := newVariablesForm()
	f.rows = f.rows[:0]
	for _, p := range pairs {
		row := newVariableRow()
		row.key.SetValue(p[0])
		row.value.SetValue(p[1])
		f.rows = append(f.rows, row)
	}
	return f.applyFocus()
}

// focusedFieldIndexes lists every field index reporting focus, so a test can
// assert that exactly one input owns the keystrokes.
func focusedFieldIndexes(f variablesForm) []int {
	var focused []int
	for i, row := range f.rows {
		if row.key.Focused() {
			focused = append(focused, i*2)
		}
		if row.value.Focused() {
			focused = append(focused, i*2+1)
		}
	}
	return focused
}

// TestVariablesForm_CollectTrimsAndDropsUnnamedRows: collecting returns the named pairs, trimmed.
// Given a form holding a padded pair, a blank row, and a second pair, when collect runs, then it
// returns the two named pairs with the padding gone and the blank row dropped.
// Why it matters: an empty trailing row is always present, because the form seeds one and the add
// key seeds another, so sending it would post an empty-keyed variable that GitLab rejects.
func TestVariablesForm_CollectTrimsAndDropsUnnamedRows(t *testing.T) {
	// Given: a padded pair, a blank row, and a second pair.
	f := variablesFormWith(
		[2]string{"  DEPLOY_ENV  ", "  staging  "},
		[2]string{"", ""},
		[2]string{"DRY_RUN", "true"},
	)

	// When: collecting the variables.
	got := f.collect()

	// Then: only the named pairs survive, trimmed.
	want := []gitlab.PipelineVariable{
		{Key: "DEPLOY_ENV", Value: "staging"},
		{Key: "DRY_RUN", Value: "true"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("collect() = %+v, want %+v", got, want)
	}
}

// TestVariablesForm_ValidateRejectsLostAndDuplicateKeys: validation catches the two ways a form loses data.
// Given a well-formed pair, a value typed with no key, and a key entered twice, when validate runs,
// then only the well-formed form is accepted.
// Why it matters: collect drops a keyless row and GitLab keeps only the last of a duplicated key, so
// without validation the user watches a variable they typed vanish from the run they triggered.
func TestVariablesForm_ValidateRejectsLostAndDuplicateKeys(t *testing.T) {
	tests := []struct {
		name    string
		pairs   [][2]string
		wantErr bool
	}{
		{"named pair", [][2]string{{"DEPLOY_ENV", "staging"}}, false},
		{"blank row alongside a pair", [][2]string{{"DEPLOY_ENV", "staging"}, {"", ""}}, false},
		{"value with no key", [][2]string{{"", "staging"}}, true},
		{"key repeated", [][2]string{{"DEPLOY_ENV", "staging"}, {"DEPLOY_ENV", "prod"}}, true},
		{"key repeated after trimming", [][2]string{{"DEPLOY_ENV", "staging"}, {" DEPLOY_ENV ", "prod"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a form holding the case's rows.
			f := variablesFormWith(tt.pairs...)

			// When: validating it.
			err := f.validate()

			// Then: the form is accepted or rejected as the case requires.
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestVariablesForm_CycleFocusWrapsAndFocusesOneField: focus steps through every field and wraps.
// Given a two-row form focused on the first key, when focus advances four times, then it visits the
// first value, the second key and the second value before returning to the first key, and exactly
// one input reports focus at every step.
// Why it matters: two focused inputs both consume the same keystroke, so a typed character lands in
// two fields at once and the form silently sends a value the user never entered.
func TestVariablesForm_CycleFocusWrapsAndFocusesOneField(t *testing.T) {
	// Given: a two-row form focused on the first key.
	f := variablesFormWith([2]string{"A", "1"}, [2]string{"B", "2"})

	// When/Then: focus visits each field in turn and wraps back to the first.
	for step, want := range []int{1, 2, 3, 0} {
		f = f.cycleFocus(1)
		if got := focusedFieldIndexes(f); !slices.Equal(got, []int{want}) {
			t.Fatalf("step %d: focused fields = %v, want exactly [%d]", step, got, want)
		}
	}
}

// TestVariablesForm_RemoveRowKeepsTheLastRow: removing the only row leaves an empty row behind.
// Given a one-row form holding a pair, when the focused row is removed, then the form still holds a
// single row and that row is empty.
// Why it matters: a form with no rows renders no fields, so the user is left in a modal with
// nothing to type into and no way to add a variable back.
func TestVariablesForm_RemoveRowKeepsTheLastRow(t *testing.T) {
	// Given: a form holding one pair.
	f := variablesFormWith([2]string{"DEPLOY_ENV", "staging"})

	// When: removing the focused row.
	f = f.removeRow()

	// Then: one empty row remains.
	if len(f.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(f.rows))
	}
	if got := f.collect(); len(got) != 0 {
		t.Errorf("collect() = %+v, want no variables", got)
	}
}

// TestVariablesForm_AddRowFocusesTheNewKey: adding a row moves focus to the new key field.
// Given a one-row form focused on its value field, when a row is added, then the form holds two
// rows and focus sits on the second row's key.
// Why it matters: the add key exists to keep typing flowing, so leaving focus behind makes the
// user tab to the field they just asked for.
func TestVariablesForm_AddRowFocusesTheNewKey(t *testing.T) {
	// Given: a one-row form focused on its value field.
	f := variablesFormWith([2]string{"DEPLOY_ENV", "staging"}).cycleFocus(1)

	// When: adding a row.
	f = f.addRow()

	// Then: the new row's key holds the focus.
	if len(f.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(f.rows))
	}
	if got := focusedFieldIndexes(f); !slices.Equal(got, []int{2}) {
		t.Errorf("focused fields = %v, want exactly [2]", got)
	}
}
