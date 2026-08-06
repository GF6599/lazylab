package ui

import (
	"strings"
	"testing"

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
