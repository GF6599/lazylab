package ui

// PanelID identifies a panel in the multi-panel layout.
type PanelID int

const (
	PanelProjects  PanelID = iota // Left sidebar: project list
	PanelPipelines                // Left sidebar: pipeline list
	PanelStages                   // Left sidebar: stages/jobs
	PanelMRs                      // Left sidebar: merge requests
	PanelDetail                   // Right area: contextual content
)

// SidebarPanels lists the left-side panels in display order.
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

// FocusState tracks which panel is active and screen layout mode.
type FocusState struct {
	Active     PanelID    // Currently focused panel
	ScreenMode ScreenMode // Normal/half/full layout mode
	PrevActive PanelID    // For returning from overlays
}

// NextScreenMode cycles through screen modes.
func (f *FocusState) NextScreenMode() {
	f.ScreenMode = (f.ScreenMode + 1) % 3
}

// pipelineDetailTab selects which tab is shown in the detail pane for pipeline context.
type pipelineDetailTab int

const (
	detailTabLog       pipelineDetailTab = iota // Job log output
	detailTabTests                              // Pipeline test report
	detailTabArtifacts                          // Job artifacts list
)

var pipelineDetailTabLabels = []string{"Log", "Tests", "Artifacts"}

// panelLabel returns the display title for a panel.
func panelLabel(id PanelID) string {
	switch id {
	case PanelProjects:
		return "Projects"
	case PanelPipelines:
		return "Pipelines"
	case PanelStages:
		return "Stages"
	case PanelMRs:
		return "Merge Requests"
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
