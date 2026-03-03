// panel.go defines the panel identity and focus state machine for the
// multi-panel layout.
//
// The layout has four sidebar panels (Projects, Pipelines, Stages, MRs) plus
// a Detail pane on the right. Focus tracks which panel receives keyboard input.
// Only one panel is active at a time; the Detail pane remembers which sidebar
// panel the user came from (PrevActive) so "back" navigation works correctly.
//
// Screen and layout modes let the user control how space is distributed without
// changing the panel structure:
//   - ScreenMode (Normal/Half/Full): vertical accordion aggressiveness
//   - LayoutMode (Default/Wide): sidebar-to-detail width ratio (30/70 vs 50/50)
//
// Tab types (pipelineDetailTab, mrDetailTab) control which content appears in
// the Detail pane. They are separate from PanelID because the Detail pane is
// a single panel whose content changes based on context.
package ui

// PanelID identifies a panel in the multi-panel layout.
type PanelID int

const (
	PanelProjects  PanelID = iota // Left sidebar: project list
	PanelPipelines                // Left sidebar: pipeline list
	PanelStages                   // Left sidebar: stages/jobs
	PanelMRs                      // Left sidebar: merge requests
	PanelDetail                   // Right area: contextual content (not a sidebar panel)
)

// SidebarPanels lists the left-side panels in display order. The Detail pane
// is intentionally excluded — it is navigated to via h/l, not Tab.
var SidebarPanels = []PanelID{
	PanelProjects,
	PanelPipelines,
	PanelStages,
	PanelMRs,
}

// ScreenMode controls how much space the focused panel consumes.
type ScreenMode int

const (
	ScreenNormal ScreenMode = iota // All panels visible, accordion sizing
	ScreenHalf                     // Focused panel takes half, others share rest
	ScreenFull                     // Focused panel takes full sidebar
)

// LayoutMode controls the sidebar-to-detail width ratio.
type LayoutMode int

const (
	LayoutDefault LayoutMode = iota // 30/70 split
	LayoutWide                      // 50/50 split
)

// FocusState tracks which panel owns keyboard input and how the layout is
// configured. PrevActive is set when entering the Detail pane so that "back"
// (h/left) returns to the correct sidebar panel, and so the Detail pane knows
// which context to render (pipeline logs vs MR content).
type FocusState struct {
	Active     PanelID    // Currently focused panel
	ScreenMode ScreenMode // Normal/half/full layout mode
	PrevActive PanelID    // Sidebar panel to return to from Detail
	LayoutMode LayoutMode // Default (30/70) or Wide (50/50) split
}

// ToggleLayoutMode switches between Default and Wide layout modes.
func (f *FocusState) ToggleLayoutMode() {
	if f.LayoutMode == LayoutDefault {
		f.LayoutMode = LayoutWide
	} else {
		f.LayoutMode = LayoutDefault
	}
}

// NextScreenMode cycles through screen modes.
func (f *FocusState) NextScreenMode() {
	f.ScreenMode = (f.ScreenMode + 1) % 3
}

// pipelineDetailTab selects which content the Detail pane renders when the
// user is in a pipeline/stages context. Tabs are cycled with 't'/'T'.
type pipelineDetailTab int

const (
	detailTabLog       pipelineDetailTab = iota // Job log output
	detailTabTests                              // Pipeline test report
	detailTabArtifacts                          // Job artifacts list
)

var pipelineDetailTabLabels = []string{"Log", "Tests", "Artifacts"}

// mrDetailTab selects which content the Detail pane renders when the user
// is in the MR context. Comments and diffs are fetched lazily on first view.
type mrDetailTab int

const (
	mrDetailTabInfo     mrDetailTab = iota // Basic MR info
	mrDetailTabComments                    // Threaded discussions
	mrDetailTabDiff                        // Unified diff
)

var mrDetailTabLabels = []string{"Info", "Comments", "Diff"}

// panelLabel returns the display title for a panel, with a number hint
// for sidebar panels so users know they can press 1-4 to jump directly.
func panelLabel(id PanelID) string {
	switch id {
	case PanelProjects:
		return "1 Projects"
	case PanelPipelines:
		return "2 Pipelines"
	case PanelStages:
		return "3 Stages"
	case PanelMRs:
		return "4 Merge Requests"
	case PanelDetail:
		return "Detail"
	default:
		return "Unknown"
	}
}

// panelShortcut returns the number key shortcut for sidebar panels (1-indexed).
func panelShortcut(id PanelID) int {
	for i, p := range SidebarPanels {
		if p == id {
			return i + 1
		}
	}
	return 0
}

// panelByShortcut returns the panel for a 1-based shortcut number.
func panelByShortcut(n int) (PanelID, bool) {
	if n < 1 || n > len(SidebarPanels) {
		return 0, false
	}
	return SidebarPanels[n-1], true
}

// nextSidebarPanel returns the next sidebar panel (wrapping around).
func nextSidebarPanel(current PanelID) PanelID {
	for i, p := range SidebarPanels {
		if p == current {
			return SidebarPanels[(i+1)%len(SidebarPanels)]
		}
	}
	return SidebarPanels[0]
}

// prevSidebarPanel returns the previous sidebar panel (wrapping around).
func prevSidebarPanel(current PanelID) PanelID {
	for i, p := range SidebarPanels {
		if p == current {
			return SidebarPanels[(i-1+len(SidebarPanels))%len(SidebarPanels)]
		}
	}
	return SidebarPanels[len(SidebarPanels)-1]
}
