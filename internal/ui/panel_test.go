package ui

import "testing"

func TestPanelLabel(t *testing.T) {
	tests := []struct {
		id   PanelID
		want string
	}{
		{PanelProjects, "1 Projects"},
		{PanelPipelines, "2 Pipelines"},
		{PanelStages, "3 Stages"},
		{PanelMRs, "4 Merge Requests"},
		{PanelDetail, "Detail"},
		{PanelID(99), "Unknown"},
	}
	for _, tt := range tests {
		if got := panelLabel(tt.id); got != tt.want {
			t.Errorf("panelLabel(%d) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestPanelShortcut(t *testing.T) {
	tests := []struct {
		id   PanelID
		want int
	}{
		{PanelProjects, 1},
		{PanelPipelines, 2},
		{PanelStages, 3},
		{PanelMRs, 4},
		{PanelDetail, 0},
	}
	for _, tt := range tests {
		if got := panelShortcut(tt.id); got != tt.want {
			t.Errorf("panelShortcut(%d) = %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestPanelByShortcut(t *testing.T) {
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
		id, ok := panelByShortcut(tt.n)
		if ok != tt.wantOK || (ok && id != tt.wantID) {
			t.Errorf("panelByShortcut(%d) = (%d, %v), want (%d, %v)", tt.n, id, ok, tt.wantID, tt.wantOK)
		}
	}
}

func TestNextSidebarPanel(t *testing.T) {
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
		if got := nextSidebarPanel(tt.current); got != tt.want {
			t.Errorf("nextSidebarPanel(%d) = %d, want %d", tt.current, got, tt.want)
		}
	}
}

func TestPrevSidebarPanel(t *testing.T) {
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
		if got := prevSidebarPanel(tt.current); got != tt.want {
			t.Errorf("prevSidebarPanel(%d) = %d, want %d", tt.current, got, tt.want)
		}
	}
}

func TestFocusState_NextScreenMode(t *testing.T) {
	f := FocusState{}
	if f.ScreenMode != ScreenNormal {
		t.Fatalf("expected initial ScreenMode = ScreenNormal")
	}
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
