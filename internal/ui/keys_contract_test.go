package ui

import (
	"fmt"
	"sort"
	"testing"

	"github.com/charmbracelet/bubbles/key"
)

// helpSurfaces returns every binding list the app puts in front of a user, named so a failure says
// which surface is wrong. Each panel of the multi-panel help overlay is one surface, and so is each
// legacy mode help bar.
func helpSurfaces() map[string][]key.Binding {
	k := newKeyMap()
	mrTabModel := func(tab mrDetailTab) *Model {
		m := &Model{}
		m.mrView.detailTab = tab
		return m
	}
	surfaces := map[string][]key.Binding{
		"multipanel/projects":  multiPanelKeyMap(PanelProjects, PanelProjects, nil),
		"multipanel/pipelines": multiPanelKeyMap(PanelPipelines, PanelPipelines, nil),
		"multipanel/stages":    multiPanelKeyMap(PanelStages, PanelStages, nil),
		"multipanel/mrs":       multiPanelKeyMap(PanelMRs, PanelMRs, nil),
		"multipanel/detail/mr-info": multiPanelKeyMap(
			PanelDetail, PanelMRs, mrTabModel(mrDetailTabInfo),
		),
		"multipanel/detail/mr-comments": multiPanelKeyMap(
			PanelDetail, PanelMRs, mrTabModel(mrDetailTabComments),
		),
		"multipanel/detail/mr-diff": multiPanelKeyMap(
			PanelDetail, PanelMRs, mrTabModel(mrDetailTabDiff),
		),
		"multipanel/detail/pipeline": multiPanelKeyMap(PanelDetail, PanelPipelines, nil),
		"mode/projects":              projectsKeyMap(),
		"mode/explorer":              explorerKeyMap(),
		"mode/pipelines":             pipelinesKeyMap(),
		"shortbar/projects":          projectsShortHelp(k),
		"shortbar/explorer":          explorerShortHelp(k),
		"shortbar/pipelines":         pipelinesShortHelp(k),
		"shortbar/default":           k.ShortHelp(),
	}
	for i, row := range k.FullHelp() {
		surfaces[fmt.Sprintf("fullhelp/column-%d", i)] = row
	}
	return surfaces
}

// TestHelpSurfaces_NeverAdvertiseOneKeyTwice: no help surface offers the same key for two things.
// Given every binding list the app shows a user, when the keys on one surface are collected, then
// no key appears under two bindings.
// Why it matters: a panel handler is a switch over key.Matches, so the first arm that claims a key
// wins and the second becomes unreachable. Nothing reports that. The help overlay keeps advertising
// both, and the losing feature is only found by a user pressing the key and getting the other one.
func TestHelpSurfaces_NeverAdvertiseOneKeyTwice(t *testing.T) {
	for name, bindings := range helpSurfaces() {
		// Given: one help surface
		owner := map[string]string{}
		var clashes []string

		// When: the keys on it are collected
		for _, b := range bindings {
			for _, k := range b.Keys() {
				if previous, taken := owner[k]; taken {
					clashes = append(clashes, fmt.Sprintf("%q is offered as both %q and %q", k, previous, b.Help().Desc))
					continue
				}
				owner[k] = b.Help().Desc
			}
		}

		// Then: no key appears under two bindings
		sort.Strings(clashes)
		for _, clash := range clashes {
			t.Errorf("%s: %s", name, clash)
		}
	}
}

// TestHelpSurfaces_OnlyAdvertiseKeysThatExist: every advertised binding can actually be pressed.
// Given every binding list the app shows a user, when each binding is inspected, then it carries at
// least one key and the help text naming it.
// Why it matters: key.Binding zero values are silently legal. One reaching a help list draws a blank
// row in the overlay and matches no key ever, so the feature it stands for cannot be reached and the
// overlay gives the user no name to ask about.
func TestHelpSurfaces_OnlyAdvertiseKeysThatExist(t *testing.T) {
	for name, bindings := range helpSurfaces() {
		// Given: one help surface
		for i, b := range bindings {
			// When: each binding is inspected
			// Then: it carries at least one key, and the help text naming it
			if len(b.Keys()) == 0 {
				t.Errorf("%s: binding %d is advertised but matches no key", name, i)
			}
			if b.Help().Key == "" || b.Help().Desc == "" {
				t.Errorf("%s: binding %d is advertised with no help text (key=%q desc=%q)",
					name, i, b.Help().Key, b.Help().Desc)
			}
		}
	}
}

// TestHelpSurfaces_CoverEveryPanel: the overlay has something to say wherever focus can land.
// Given every panel focus can reach, when the help overlay asks for its bindings, then the panel
// gets a list of its own rather than the fallback.
// Why it matters: multiPanelKeyMap ends in a default arm offering three keys. A new panel that
// nobody adds a case for silently takes that arm, and its whole key set goes undocumented while the
// overlay still looks populated.
func TestHelpSurfaces_CoverEveryPanel(t *testing.T) {
	fallback := len(multiPanelKeyMap(PanelID(-1), PanelProjects, nil))
	for _, panel := range []PanelID{PanelProjects, PanelPipelines, PanelStages, PanelMRs, PanelDetail} {
		// Given: a panel focus can reach
		// When: the help overlay asks for its bindings
		bindings := multiPanelKeyMap(panel, PanelProjects, nil)

		// Then: the panel gets a list of its own rather than the fallback
		if len(bindings) <= fallback {
			t.Errorf("panel %v falls through to the default help list (%d bindings)", panel, len(bindings))
		}
	}
}
