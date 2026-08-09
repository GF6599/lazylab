package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// longJobPipelineModel holds jobCount jobs in one stage. Callers pass more than
// the stage table's height, so the view has to scroll to follow the cursor.
func longJobPipelineModel(jobCount int) Model {
	stagesCache := NewAsyncCache[int, []gitlab.PipelineStage]()
	stagesCache.Set(10, []gitlab.PipelineStage{{Name: "test", Status: "success"}})

	jobs := make([]gitlab.PipelineJob, jobCount)
	for i := range jobs {
		jobs[i] = gitlab.PipelineJob{
			ID:     i + 1,
			Name:   fmt.Sprintf("job-%02d", i),
			Stage:  "test",
			Status: "success",
		}
	}
	jobsCache := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobsCache.Set(10, jobs)

	m := Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		keys:   newKeyMap(),
		pipelineView: pipelineViewState{
			project:     gitlab.ProjectNode{ID: 1},
			pipelines:   []gitlab.PipelineSummary{{ID: 10, Ref: "main", Status: "success"}},
			selected:    0,
			stages:      stagesCache,
			jobs:        jobsCache,
			stageTable:  newStageTable(56),
			logs:        NewAsyncCache[int, string](),
			bridges:     NewAsyncCache[int, []gitlab.PipelineBridge](),
			logViewport: viewport.New(60, 20),
		},
	}
	m.updateStageTable()
	return withPanelLists(m)
}

func currentRowOnScreen(t table.Model) bool {
	return stageRowLine(strings.Split(t.View(), "\n"), t.SelectedRow()) >= 0
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// TestStagesPanel_AJumpScrollsToTheRowItLandsOn: a jump brings its destination into view.
// Given more jobs than the stage panel can show, when the user jumps to the bottom, half a page down,
// and back to the top, then the row the table calls current is drawn on screen at every stop.
// Why it matters: a cursor the view never scrolled to leaves the panel looking unchanged while every
// later key acts on a row the user cannot see.
func TestStagesPanel_AJumpScrollsToTheRowItLandsOn(t *testing.T) {
	// Given: thirty jobs in a table that shows ten
	m := longJobPipelineModel(30)

	for _, step := range []struct {
		name string
		key  rune
	}{
		{"jump to bottom", 'G'},
		{"jump to top", 'g'},
	} {
		// When: the user presses the jump key
		updated, _ := m.handleStagesPanelKey(runeKey(step.key))
		got := updated.(Model)

		// Then: the row it landed on is one the table actually draws
		if !currentRowOnScreen(got.pipelineView.stageTable) {
			t.Errorf("%s: row %d is off screen:\n%s",
				step.name, got.pipelineView.stageTable.Cursor(), got.pipelineView.stageTable.View())
		}
		m = got
	}
}

// TestStagesPanel_HalfPageStepsStayOnScreen: paging through a long list never loses the cursor.
// Given more jobs than the stage panel can show, when the user pages down to the end and back up to the
// start, then the row the table calls current is drawn on screen after every press.
// Why it matters: paging is how a long job list is read, so a row left off screen here is the common
// case rather than an edge one.
func TestStagesPanel_HalfPageStepsStayOnScreen(t *testing.T) {
	// Given: thirty jobs in a table that shows ten
	m := longJobPipelineModel(30)

	// When: the user pages down to the end, then back up to the start
	down, up := tea.KeyMsg{Type: tea.KeyCtrlD}, tea.KeyMsg{Type: tea.KeyCtrlU}
	for _, msg := range []tea.KeyMsg{down, down, down, down, up, up, up, up} {
		updated, _ := m.handleStagesPanelKey(msg)
		m = updated.(Model)

		// Then: the row it landed on is one the table actually draws
		if !currentRowOnScreen(m.pipelineView.stageTable) {
			t.Errorf("row %d is off screen after %s:\n%s",
				m.pipelineView.stageTable.Cursor(), msg.String(), m.pipelineView.stageTable.View())
		}
	}
}

// TestStageTable_RefreshKeepsTheCurrentRowOnScreen: the 5-second refresh does not strand the cursor.
// Given the cursor deep in a long job list, when a refresh rebuilds the table with fewer jobs than the
// cursor's index, then the row it settles on is drawn on screen.
// Why it matters: the refresh runs on a timer with no keypress behind it, so a row stranded here goes
// missing while the user is reading, with nothing on screen to explain it.
func TestStageTable_RefreshKeepsTheCurrentRowOnScreen(t *testing.T) {
	// Given: the cursor sitting on the last of thirty jobs
	m := longJobPipelineModel(30)
	updated, _ := m.handleStagesPanelKey(runeKey('G'))
	m = updated.(Model)

	// When: a refresh returns a pipeline whose job list has shrunk
	shrunk := make([]gitlab.PipelineJob, 12)
	for i := range shrunk {
		shrunk[i] = gitlab.PipelineJob{ID: i + 1, Name: fmt.Sprintf("job-%02d", i), Stage: "test", Status: "success"}
	}
	m.pipelineView.jobs.Set(10, shrunk)
	m.updateStageTable()

	// Then: the row the cursor settled on is drawn on screen
	if !currentRowOnScreen(m.pipelineView.stageTable) {
		t.Errorf("row %d is off screen after the refresh:\n%s",
			m.pipelineView.stageTable.Cursor(), m.pipelineView.stageTable.View())
	}
}
