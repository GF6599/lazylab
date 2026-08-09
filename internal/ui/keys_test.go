package ui

import (
	"context"
	"testing"

	"github.com/charmbracelet/bubbles/key"
)

// TestMultiPanelKeyMap_DetailIncludesGlabKeys: the detail pane's help overlay advertises the glab yank and preview keys.
// Given every detail-pane help branch (each MR tab plus the pipeline detail), when the help overlay asks
// for the detail pane's bindings, then bindings with help keys y and Y are listed.
// Why it matters: handleMultiPanelKey intercepts y/Y before delegating to the panel handler, so the ?
// overlay is how users discover "press y on any focused item", and a help list that omits the bindings
// hides a working feature.
func TestMultiPanelKeyMap_DetailIncludesGlabKeys(t *testing.T) {
	// Given: every detail-pane help branch (each MR tab plus the pipeline detail)
	mrModelWithTab := func(tab mrDetailTab) *Model {
		m := &Model{}
		m.mrView.detailTab = tab
		return m
	}
	tests := []struct {
		name       string
		prevActive PanelID
		model      *Model
	}{
		{"mr info tab", PanelMRs, mrModelWithTab(mrDetailTabInfo)},
		{"mr comments tab", PanelMRs, mrModelWithTab(mrDetailTabComments)},
		{"mr diff tab", PanelMRs, mrModelWithTab(mrDetailTabDiff)},
		{"pipeline detail", PanelPipelines, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the help overlay asks for the detail pane's bindings
			bindings := multiPanelKeyMap(PanelDetail, tt.prevActive, tt.model)

			// Then: the glab yank and preview keys are listed
			for _, helpKey := range []string{"y", "Y"} {
				if !hasBindingWithHelpKey(bindings, helpKey) {
					t.Errorf("detail help omits the %q binding", helpKey)
				}
			}
		})
	}
}

func hasBindingWithHelpKey(bindings []key.Binding, helpKey string) bool {
	for _, b := range bindings {
		if b.Help().Key == helpKey {
			return true
		}
	}
	return false
}

// TestCancelPipeline_ReachesAPipelineGitLabHasNotStartedYet: cancel acts on any pipeline still in
// flight, not only one already running.
// Given a pipelines panel over a pipeline in one status, when cancel is requested, then the request
// reaches GitLab for a pipeline still in flight and is withheld for one that is finished or already
// cancelling.
// Why it matters: the seconds between creating a pipeline and its first job starting are when a user
// cancels a run they did not mean to start, and refusing there sends them to the web UI to do the
// thing the app just declined to do.
func TestCancelPipeline_ReachesAPipelineGitLabHasNotStartedYet(t *testing.T) {
	for _, tc := range []struct {
		status  string
		cancels bool
	}{
		{"created", true},
		{"waiting_for_resource", true},
		{"preparing", true},
		{"waiting_for_callback", true},
		{"pending", true},
		{"running", true},
		{"scheduled", true},
		{"canceling", false},
		{"success", false},
		{"failed", false},
		{"canceled", false},
		{"skipped", false},
		{"manual", false},
	} {
		// Given: a pipelines panel over a pipeline in this status
		var asked bool
		m := pipelineStatusModel(tc.status)
		m.client = &mockService{CancelPipelineFn: func(context.Context, int, int) error {
			asked = true
			return nil
		}}

		// When: cancel is requested and whatever it returns is run
		updated, cmd := m.cancelPipelineAction()
		if cmd != nil {
			cmd()
		}

		// Then: the request reaches GitLab only for a pipeline still in flight
		if asked != tc.cancels {
			t.Errorf("status %q: the cancel reached GitLab = %v, want %v (the app said %q)",
				tc.status, asked, tc.cancels, updated.(Model).status)
		}
	}
}
