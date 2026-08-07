package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/x/ansi"
)

// lineWith returns the single rendered line that contains want.
func lineWith(t *testing.T, rendered, want string) string {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no rendered line contains %q, got:\n%s", want, rendered)
	return ""
}

// labelColumn returns the display column that label starts at within line.
func labelColumn(t *testing.T, line, label string) int {
	t.Helper()
	i := strings.Index(line, label)
	if i < 0 {
		t.Fatalf("label %q missing from %q", label, line)
	}
	return ansi.StringWidth(line[:i])
}

// TestMarker_BracketsTheCurrentRowOnly: the current row is marked with brackets and its neighbours are not.
// Given a projects panel holding two rows, when the panel renders with the first row current, then that
// row carries an opening and a closing bracket and the other row carries neither.
// Why it matters: the bracket pair is what tells the operator which row a key press acts on, so a row
// that keeps it while not current points the next action at the wrong project.
func TestMarker_BracketsTheCurrentRowOnly(t *testing.T) {
	// Given: the stub panel with the first of two projects current
	m := newSnapshotModel(PanelProjects, 120, 40)
	m.projectList.Select(0)

	// When: the projects panel renders its rows
	rendered := renderProjectsPanelContent(m, 36, 6)
	current := lineWith(t, rendered, "team/alpha")
	other := lineWith(t, rendered, "team/beta")

	// Then: the current row carries the bracket pair
	if !strings.Contains(current, "[") || !strings.Contains(current, "]") {
		t.Errorf("current row is not bracketed: %q", current)
	}

	// And: the row that is not current carries neither bracket
	if strings.ContainsAny(other, "[]") {
		t.Errorf("row that is not current carries a bracket: %q", other)
	}
}

// TestMarker_DoesNotMoveTheRowItMarks: a row holds its position when the marker arrives and leaves.
// Given the same projects panel rendered with the first row current and then the second, when one row is
// compared across both renders, then its label starts in the same display column either way.
// Why it matters: a marker that shifts its row makes the whole list jump by a cell on every keypress,
// which is the failure the reserved gutter exists to prevent.
func TestMarker_DoesNotMoveTheRowItMarks(t *testing.T) {
	// Given: the panel rendered with the first row current, then with the second
	m := newSnapshotModel(PanelProjects, 120, 40)
	m.projectList.Select(0)
	withMarker := renderProjectsPanelContent(m, 36, 6)
	m.projectList.Select(1)
	withoutMarker := renderProjectsPanelContent(m, 36, 6)

	// When: the same row is read from both renders
	marked := lineWith(t, withMarker, "team/alpha")
	unmarked := lineWith(t, withoutMarker, "team/alpha")

	// Then: its label starts in the same column whether or not it is current
	if got, want := labelColumn(t, marked, "team/alpha"), labelColumn(t, unmarked, "team/alpha"); got != want {
		t.Errorf("label column moved with the marker: current %d, not current %d\n  %q\n  %q",
			got, want, marked, unmarked)
	}
}

// TestMarker_EnclosesAWrappedRowWithOnePair: a row that wraps is enclosed by one tall bracket pair.
// Given a marked row whose label is longer than the row it sits in, when the row renders, then its
// first line carries the upper corner pieces, its last line the lower corner pieces, and no line
// carries the flat pair.
// Why it matters: a flat pair repeated per line reads as several current rows at once, which is the
// one thing the marker exists to settle.
func TestMarker_EnclosesAWrappedRowWithOnePair(t *testing.T) {
	// Given: a marked row whose label needs more than one line
	var b strings.Builder
	long := "✓ #2736203671 08-06 02:39 feature/PFP-333/real_time_data_in_hindcasts_v2"

	// When: the row renders into a width the label overflows
	renderListItem(&b, markerFor(0, 0), long, 2, 40, true, false)
	lines := strings.Split(b.String(), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the row to wrap, got %d line(s): %q", len(lines), b.String())
	}

	// Then: the pair opens on the first line and closes on the last
	first, last := lines[0], lines[len(lines)-1]
	if !strings.HasPrefix(first, markerTop[0]) || !strings.HasSuffix(first, markerTop[1]) {
		t.Errorf("first line is not the top of a pair: %q", first)
	}
	if !strings.HasPrefix(last, markerBottom[0]) || !strings.HasSuffix(last, markerBottom[1]) {
		t.Errorf("last line is not the bottom of a pair: %q", last)
	}

	// And: no line carries the flat pair, which would read as separate rows
	for i, line := range lines {
		if strings.Contains(line, markerFlat[0]) || strings.Contains(line, markerFlat[1]) {
			t.Errorf("line %d carries the flat pair: %q", i, line)
		}
	}
}

// TestRow_NeverRendersWiderThanItsPane: a list row stays inside the width it is given.
// Given a label longer than the row, when the row renders at every width from 0 to 12, both while it
// is current and while it is not, then no line it produces is wider than the width asked for.
// Why it matters: a row wider than its pane pushes the frame's right edge off and tears every border
// below it, and the narrow widths are exactly the ones no golden file covers.
func TestRow_NeverRendersWiderThanItsPane(t *testing.T) {
	// Given: a label far longer than any of the widths under test
	long := "team/some-quite-long-project-name"

	for width := 0; width <= 12; width++ {
		for _, current := range []bool{true, false} {
			// When: the row renders at that width
			var b strings.Builder
			mk := markerFor(0, 0)
			if !current {
				mk = markerFor(1, 0)
			}
			renderListItem(&b, mk, long, 0, width, current, false)

			// Then: every line it produced fits the width
			for i, line := range strings.Split(b.String(), "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("width %d, current=%v: line %d is %d cells wide: %q",
						width, current, i, got, line)
				}
			}
		}
	}
}

// TestStageTable_MarksTheCurrentRow: the stage table marks its current row like every other list.
// Given a stage table holding two jobs with the second under the cursor, when the table renders, then
// the row under the cursor carries the bracket pair, no other row does, and every line keeps the
// table's width.
// Why it matters: the table is the one list the bracket cannot come from the row data, so without this
// its current row is told apart by colour alone, which a low-colour terminal does not show at all.
func TestStageTable_MarksTheCurrentRow(t *testing.T) {
	// Given: a stage table with the second of two jobs under the cursor
	const width = 40
	tbl := newStageTable(width)
	tbl.SetRows([]table.Row{
		{"build", "build", iconSuccess + " SUCCESS"},
		{"test", "test", iconFailed + " FAILED"},
	})
	tbl.SetCursor(1)

	// When: the rendered table is styled for display
	lines := strings.Split(styleStageTable(tbl.View(), tbl.Cursor()), "\n")

	// Then: the row under the cursor is the only one carrying the pair
	var marked []int
	for i, line := range lines {
		if strings.Contains(line, markerFlat[0]) && strings.Contains(line, markerFlat[1]) {
			marked = append(marked, i)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("expected exactly one marked row, got %d: %q", len(marked), lines)
	}
	if !strings.Contains(lines[marked[0]], "test") {
		t.Errorf("the marked row is not the one under the cursor: %q", lines[marked[0]])
	}

	// And: marking it costs no width, so the table still lines up in its pane
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Errorf("line %d is %d cells wide, want %d: %q", i, got, width, line)
		}
	}
}

// TestStageTable_MarkedRowKeepsItsText: marking a row does not disturb the text it marks.
// Given a table row already carrying the per-cell styling the widget emits, when the row is marked,
// then the text between the brackets is that row's text unchanged and the line keeps its width.
// Why it matters: the marker re-renders the row to recolour it, so a slip there drops or shifts a job
// name, and the styling around it hides that from any plain comparison of the two strings.
func TestStageTable_MarkedRowKeepsItsText(t *testing.T) {
	// Given: a rendered table whose rows carry the widget's own cell styling
	const width = 40
	tbl := newStageTable(width)
	tbl.SetRows([]table.Row{
		{"build", "build", iconSuccess + " SUCCESS"},
		{"deploy-production", "deploy", iconManual + " MANUAL"},
	})
	tbl.SetCursor(1)
	raw := strings.Split(tbl.View(), "\n")[3] // header + rule + first row = 3

	// When: that row is marked
	marked := markStageRow(raw)

	// Then: the visible text is the row's own, bracketed, with nothing lost
	rawText := ansi.Strip(raw)
	want := markerFlat[0] + rawText[1:len(rawText)-1] + markerFlat[1]
	if got := ansi.Strip(marked); got != want {
		t.Errorf("marked row text = %q, want %q", got, want)
	}

	// And: it still occupies exactly the table's width
	if got := ansi.StringWidth(marked); got != width {
		t.Errorf("marked row is %d cells wide, want %d", got, width)
	}
}
