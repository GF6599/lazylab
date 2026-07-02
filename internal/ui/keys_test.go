package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
)

// multiPanelKeyMap advertises the glab yank/preview keys for the detail pane, where
// handleMultiPanelKey intercepts y/Y before delegating to the panel handler.
// Why it matters: the ? overlay is how users discover "press y on any focused item",
// so a help list that omits the bindings hides a working feature.
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
