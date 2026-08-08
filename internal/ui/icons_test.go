package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// iconCells is every glyph the UI draws, against the number of cells the layout
// arithmetic budgets for it. The wide entries are the emoji that Unicode gives
// default emoji presentation, which every terminal draws at two cells.
var iconCells = map[string]struct {
	glyph string
	cells int
}{
	"iconSuccess":       {iconSuccess, 1},
	"iconFailed":        {iconFailed, 1},
	"iconRunning":       {iconRunning, 1},
	"iconPending":       {iconPending, 1},
	"iconCanceled":      {iconCanceled, 1},
	"iconSkipped":       {iconSkipped, 1},
	"iconManual":        {iconManual, 1},
	"iconBlocked":       {iconBlocked, 1},
	"iconUnknown":       {iconUnknown, 1},
	"iconNoPipeline":    {iconNoPipeline, 1},
	"iconClock":         {iconClock, 1},
	"iconTreeCollapsed": {iconTreeCollapsed, 1},
	"iconTreeExpanded":  {iconTreeExpanded, 1},
	"iconSelection":     {iconSelection, 1},
	"iconProject":       {iconProject, 2},
	"iconPrivate":       {iconPrivate, 2},
	"iconPublic":        {iconPublic, 2},
	"iconInternal":      {iconInternal, 2},
	"iconStar":          {iconStar, 2},
}

// TestIcons_MeasureTheSameWidthInEveryTerminal: no glyph the UI draws has a width the terminal decides.
// Given the icon set, when each glyph is measured, then none is East Asian Ambiguous and each occupies
// the cell count the layout budgets for it.
// Why it matters: a pane is drawn to an exact cell count, so a glyph one cell wider than Go measured
// pushes its row past the border and tears the frame around it.
func TestIcons_MeasureTheSameWidthInEveryTerminal(t *testing.T) {
	// Given: one icon from the set
	for name, want := range iconCells {
		assertCellWidth(t, name, want.glyph, want.cells)
	}

	// And: every animation frame, which a row draws in the same column as a status icon
	for i, frame := range appSpinner.Frames {
		assertCellWidth(t, fmt.Sprintf("animation frame %d", i), strings.TrimSpace(frame), 1)
	}
}

func assertCellWidth(t *testing.T, name, glyph string, cells int) {
	t.Helper()

	// Then: no rune in it is one a terminal may widen on its own
	for _, r := range glyph {
		if runewidth.IsAmbiguousWidth(r) {
			t.Errorf("%s (%q, U+%04X) is East Asian Ambiguous: a terminal set to draw "+
				"ambiguous glyphs wide gives it 2 cells while the layout budgets %d",
				name, glyph, r, cells)
		}
	}

	// And: it occupies the cells the layout budgets, by every measure the UI relies on
	for measure, got := range map[string]int{
		"ansi":      ansi.StringWidth(glyph),
		"lipgloss":  lipgloss.Width(glyph),
		"runewidth": runewidth.StringWidth(glyph),
		"uniseg":    uniseg.StringWidth(glyph),
	} {
		if got != cells {
			t.Errorf("%s (%q): %s measures %d cells, the layout budgets %d",
				name, glyph, measure, got, cells)
		}
	}
}
