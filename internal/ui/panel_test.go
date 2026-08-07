package ui

import (
	"strings"
	"testing"
)

// TestPanelLabel: every panel maps to its numbered display title, with a fallback for unknown IDs.
// Given each PanelID plus an out-of-range one, when its label is looked up, then sidebar panels carry
// their 1-4 number hint, the detail pane is plain "detail", and unknown IDs read "unknown".
// Why it matters: the border titles are where users learn the 1-4 jump keys, so a wrong or missing number
// hint teaches a shortcut that goes somewhere else.
func TestPanelLabel(t *testing.T) {
	// Given: every panel ID and an out-of-range one
	tests := []struct {
		id   PanelID
		want string
	}{
		{PanelProjects, "1 projects"},
		{PanelPipelines, "2 pipelines"},
		{PanelStages, "3 stages"},
		{PanelMRs, "4 merge requests"},
		{PanelDetail, "detail"},
		{PanelID(99), "unknown"},
	}
	for _, tt := range tests {
		// When/Then: the label matches the panel's display title
		if got := panelLabel(tt.id); got != tt.want {
			t.Errorf("panelLabel(%d) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

// TestPanelByShortcut: number keys 1-4 resolve to sidebar panels and everything else is rejected.
// Given shortcut numbers in and out of the valid range, when each is resolved, then 1-4 return the
// matching sidebar panel with ok=true while 0, 5, and -1 report ok=false.
// Why it matters: without the range guard, a stray number key would index past SidebarPanels and panic the
// key handler.
func TestPanelByShortcut(t *testing.T) {
	// Given: in-range and out-of-range shortcut numbers
	tests := []struct {
		n      int
		wantID PanelID
		wantOK bool
	}{
		{1, PanelProjects, true},
		{2, PanelPipelines, true},
		{3, PanelStages, true},
		{4, PanelMRs, true},
		{0, 0, false},
		{5, 0, false},
		{-1, 0, false},
	}
	for _, tt := range tests {
		// When/Then: valid numbers resolve to their panel and invalid ones report not-ok
		id, ok := panelByShortcut(tt.n)
		if ok != tt.wantOK || (ok && id != tt.wantID) {
			t.Errorf("panelByShortcut(%d) = (%d, %v), want (%d, %v)", tt.n, id, ok, tt.wantID, tt.wantOK)
		}
	}
}

// TestNextSidebarPanel: forward cycling steps through the sidebar and wraps to the first panel.
// Given each sidebar panel, when the next panel is computed, then the order runs Projects -> Pipelines ->
// Stages -> MRs and wraps from MRs back to Projects.
// Why it matters: a broken wrap would strand tab-cycling on the last panel.
func TestNextSidebarPanel(t *testing.T) {
	// Given: each sidebar panel with its successor
	tests := []struct {
		current PanelID
		want    PanelID
	}{
		{PanelProjects, PanelPipelines},
		{PanelPipelines, PanelStages},
		{PanelStages, PanelMRs},
		{PanelMRs, PanelProjects}, // wraps
	}
	for _, tt := range tests {
		// When/Then: the next panel follows the cycle order
		if got := nextSidebarPanel(tt.current); got != tt.want {
			t.Errorf("nextSidebarPanel(%d) = %d, want %d", tt.current, got, tt.want)
		}
	}
}

// TestPrevSidebarPanel: backward cycling steps through the sidebar in reverse and wraps to the last panel.
// Given each sidebar panel, when the previous panel is computed, then the order runs MRs -> Stages ->
// Pipelines -> Projects and wraps from Projects back to MRs.
// Why it matters: a broken reverse wrap would strand shift+tab cycling on the first panel.
func TestPrevSidebarPanel(t *testing.T) {
	// Given: each sidebar panel with its predecessor
	tests := []struct {
		current PanelID
		want    PanelID
	}{
		{PanelProjects, PanelMRs}, // wraps
		{PanelPipelines, PanelProjects},
		{PanelStages, PanelPipelines},
		{PanelMRs, PanelStages},
	}
	for _, tt := range tests {
		// When/Then: the previous panel follows the reverse cycle order
		if got := prevSidebarPanel(tt.current); got != tt.want {
			t.Errorf("prevSidebarPanel(%d) = %d, want %d", tt.current, got, tt.want)
		}
	}
}

// TestFocusState_NextScreenMode: cycling walks Normal -> Half -> Full and wraps back to Normal.
// Given a fresh FocusState in ScreenNormal, when NextScreenMode runs three times, then the mode visits
// ScreenHalf and ScreenFull and returns to ScreenNormal.
// Why it matters: a broken cycle would trap the user in full-screen with no key left to restore the
// normal layout.
func TestFocusState_NextScreenMode(t *testing.T) {
	// Given: a fresh focus state starting in ScreenNormal
	f := FocusState{}
	if f.ScreenMode != ScreenNormal {
		t.Fatalf("expected initial ScreenMode = ScreenNormal")
	}

	// When/Then: each cycle advances one mode and the third wraps back to normal
	f.NextScreenMode()
	if f.ScreenMode != ScreenHalf {
		t.Fatalf("expected ScreenHalf after first cycle, got %d", f.ScreenMode)
	}
	f.NextScreenMode()
	if f.ScreenMode != ScreenFull {
		t.Fatalf("expected ScreenFull after second cycle, got %d", f.ScreenMode)
	}
	f.NextScreenMode()
	if f.ScreenMode != ScreenNormal {
		t.Fatalf("expected ScreenNormal after third cycle (wrap), got %d", f.ScreenMode)
	}
}

// TestPanelLabels_AreLowerCase: every frame title is lower case.
// Given each sidebar panel and the detail pane, when its label is read, then the label carries no
// upper-case letter.
// Why it matters: a frame title is machine truth cut into the top rule, and sentence case turns that
// rule into a heading which competes with the human label inside the panel.
func TestPanelLabels_AreLowerCase(t *testing.T) {
	// Given: every panel that draws a frame
	panels := append([]PanelID{}, SidebarPanels...)
	panels = append(panels, PanelDetail)

	for _, id := range panels {
		// When: its frame title is read
		label := panelLabel(id)

		// Then: it carries no upper-case letter
		if label != strings.ToLower(label) {
			t.Errorf("panel %d label %q is not lower case", id, label)
		}
	}
}

// TestDetailFrame_TitleDoesNotRepeatTheBodyHeading: the detail frame's rule and its body differ.
// Given a project selected, when the multi-panel frame renders, then the detail frame's rule carries
// the project path and not the heading the body already prints.
// Why it matters: a rule and a body printing the same word spend two lines on one thing, and the path,
// the only part that says what is on screen, is the part that gets truncated away.
func TestDetailFrame_TitleDoesNotRepeatTheBodyHeading(t *testing.T) {
	// Given: the stub model with a project selected
	m := newSnapshotModel(PanelProjects, 120, 40)

	// When: the full frame renders and its top rule is read
	rule := strings.Split(renderMultiPanelView(m, m.width, m.height), "\n")[0]

	// Then: the rule carries the project path
	if !strings.Contains(rule, "team/alpha") {
		t.Errorf("detail rule does not carry the project path: %q", rule)
	}

	// And: it does not repeat the heading the body prints
	if strings.Contains(strings.ToLower(rule), "details") {
		t.Errorf("detail rule repeats the body heading: %q", rule)
	}
}
