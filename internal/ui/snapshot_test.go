package ui

import (
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/x/exp/golden"
)

// newSnapshotModel builds a Model with richer stub data for snapshot testing.
// It extends newMultiPanelModel with configurable dimensions and additional state.
func newSnapshotModel(active PanelID, width, height int) Model {
	m := newMultiPanelModel(active)
	m.width = width
	m.height = height
	m.status = "Ready"

	// Add pipeline status for first project so info bar and detail pane render content
	m.pipelineStatus[1] = pipelineState{
		hasInfo: true,
		info:    gitlab.PipelineSummary{ID: 100, Ref: "main", Status: "success", WebURL: "https://gitlab.com/team/alpha/-/pipelines/100"},
	}

	// Initialize pipeline list with proper delegate (required for SetSize)
	pipelineItems := []list.Item{
		pipelineItem{summary: gitlab.PipelineSummary{ID: 10, Ref: "main", Status: "success"}},
	}
	m.pipelineView.pipelineList = newBareList(pipelineItems, pipelineDelegate{}, 40, 10)

	// Populate MR view with stub data
	m.mrView = mrViewState{
		project: m.allProjects[0],
		mrs: []gitlab.MergeRequestSummary{
			{IID: 42, Title: "Add dark mode support", State: "opened", Author: "alice", SourceBranch: "feature/dark-mode", TargetBranch: "main"},
			{IID: 41, Title: "Fix login redirect", State: "merged", Author: "bob", SourceBranch: "fix/login", TargetBranch: "main"},
		},
		selected:   0,
		mrViewport: viewport.New(60, 20),
	}

	return m
}

func TestSnapshot_MultiPanel_Default(t *testing.T) {
	m := newSnapshotModel(PanelProjects, 120, 40)
	output := renderMultiPanelView(&m, m.width, m.height)
	golden.RequireEqual(t, output)
}

func TestSnapshot_MultiPanel_Small(t *testing.T) {
	m := newSnapshotModel(PanelProjects, 80, 24)
	output := renderMultiPanelView(&m, m.width, m.height)
	golden.RequireEqual(t, output)
}

func TestSnapshot_MultiPanel_PipelinesFocused(t *testing.T) {
	m := newSnapshotModel(PanelPipelines, 120, 40)
	output := renderMultiPanelView(&m, m.width, m.height)
	golden.RequireEqual(t, output)
}

func TestSnapshot_MultiPanel_DetailFocused(t *testing.T) {
	m := newSnapshotModel(PanelDetail, 120, 40)
	m.focus.PrevActive = PanelProjects
	output := renderMultiPanelView(&m, m.width, m.height)
	golden.RequireEqual(t, output)
}

func TestSnapshot_MultiPanel_TooSmall(t *testing.T) {
	m := newSnapshotModel(PanelProjects, 30, 10)
	output := renderMultiPanelView(&m, m.width, m.height)
	golden.RequireEqual(t, output)
}

func TestSnapshot_BorderedPane_Focused(t *testing.T) {
	content := "Line 1\nLine 2\nLine 3"
	tabs := []string{"Tab A", "Tab B"}
	output := renderBorderedPane(content, 40, 5, true, "My Panel", tabs, 0, "1 of 3")
	golden.RequireEqual(t, output)
}

func TestSnapshot_BorderedPane_Unfocused(t *testing.T) {
	content := "Some content here"
	output := renderBorderedPane(content, 40, 5, false, "Unfocused Panel", nil, 0, "")
	golden.RequireEqual(t, output)
}

func TestSnapshot_ProjectsPanel_Empty(t *testing.T) {
	m := newSnapshotModel(PanelProjects, 120, 40)
	m.allProjects = nil
	m.projectList.SetItems(nil)
	m.invalidateVisibleCache()
	output := renderProjectsPanelContent(&m, 40, 10)
	golden.RequireEqual(t, output)
}

func TestSnapshot_ProjectsPanel_FavoritesEmpty(t *testing.T) {
	m := newSnapshotModel(PanelProjects, 120, 40)
	m.projectTab = projectTabFavorites
	m.favorites = make(map[int]bool) // no favorites set
	m.invalidateVisibleCache()
	output := renderProjectsPanelContent(&m, 40, 10)
	golden.RequireEqual(t, output)
}

func TestSnapshot_PipelinesPanel_NoProject(t *testing.T) {
	m := newSnapshotModel(PanelPipelines, 120, 40)
	m.pipelineView.project = gitlab.ProjectNode{}
	output := renderPipelinesPanelContent(&m, 40, 10)
	golden.RequireEqual(t, output)
}

func TestSnapshot_PipelinesPanel_Loading(t *testing.T) {
	m := newSnapshotModel(PanelPipelines, 120, 40)
	m.pipelineView.loading = true
	m.pipelineView.pipelines = nil
	output := renderPipelinesPanelContent(&m, 40, 10)
	golden.RequireEqual(t, output)
}

func TestSnapshot_StagesPanel_NoSelection(t *testing.T) {
	m := newSnapshotModel(PanelStages, 120, 40)
	m.pipelineView.pipelines = nil
	output := renderStagesPanelContent(&m, 40, 10)
	golden.RequireEqual(t, output)
}

func TestSnapshot_MRsPanel_Empty(t *testing.T) {
	m := newSnapshotModel(PanelMRs, 120, 40)
	m.mrView.mrs = nil
	output := renderMRsPanel(&m, 40, 10)
	golden.RequireEqual(t, output)
}

func TestSnapshot_InfoBar(t *testing.T) {
	m := newSnapshotModel(PanelProjects, 120, 40)
	output := renderInfoBar(&m, 120)
	golden.RequireEqual(t, output)
}
