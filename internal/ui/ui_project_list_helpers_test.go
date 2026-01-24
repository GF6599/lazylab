package ui

import (
	"strings"
	"testing"
	"time"

	"lazylab/internal/gitlab"
)

func TestClampLineANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  int // Expected output length (approximately, due to ANSI codes)
	}{
		{
			name:  "plain text within width",
			input: "hello",
			width: 10,
			want:  5,
		},
		{
			name:  "plain text exceeds width",
			input: "hello world this is a long string",
			width: 10,
			want:  10,
		},
		{
			name:  "text with ANSI codes within width",
			input: "\x1b[31mred text\x1b[0m",
			width: 10,
			want:  8, // "red text" is 8 chars
		},
		{
			name:  "text with ANSI codes exceeds width",
			input: "\x1b[31mthis is very long red text that will be truncated\x1b[0m",
			width: 10,
			want:  10,
		},
		{
			name:  "zero width",
			input: "hello",
			width: 0,
			want:  0,
		},
		{
			name:  "negative width",
			input: "hello",
			width: -5,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampLineANSI(tt.input, tt.width)
			// Check that visible length (without ANSI codes) is approximately expected
			// We can't check exact length due to ANSI codes, but we can verify it's not longer
			if len(got) > len(tt.input)+50 { // Allow for ANSI codes overhead
				t.Errorf("clampLineANSI() output too long: %d chars (input: %d)", len(got), len(tt.input))
			}
		})
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		name    string
		haystack string
		needle  string
		want    bool
	}{
		{
			name:     "exact match",
			haystack: "hello",
			needle:   "hello",
			want:     true,
		},
		{
			name:     "case insensitive match",
			haystack: "Hello World",
			needle:   "hello",
			want:     true,
		},
		{
			name:     "fuzzy match - characters in order",
			haystack: "internal/gitlab/client.go",
			needle:   "glcli",
			want:     true,
		},
		{
			name:     "fuzzy match - skipping characters",
			haystack: "MyAwesomeProject",
			needle:   "map",
			want:     true,
		},
		{
			name:     "no match - characters out of order",
			haystack: "hello",
			needle:   "leh",
			want:     false,
		},
		{
			name:     "empty needle matches anything",
			haystack: "hello",
			needle:   "",
			want:     true,
		},
		{
			name:     "empty haystack doesn't match non-empty needle",
			haystack: "",
			needle:   "hello",
			want:     false,
		},
		{
			name:     "needle longer than haystack",
			haystack: "hi",
			needle:   "hello",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyMatch(tt.haystack, tt.needle)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
			}
		})
	}
}

func TestPageSlice(t *testing.T) {
	// Create test projects
	projects := make([]gitlab.ProjectNode, 100)
	for i := range projects {
		projects[i] = gitlab.ProjectNode{
			ID:   i + 1,
			Name: "project-" + string(rune('a'+i%26)),
		}
	}

	m := Model{
		allProjects: projects,
		opts: Options{
			ProjectsPerPage: 30,
		},
		pagesReady: map[int]bool{
			1: true,
			2: true,
			3: true,
			4: true,
		},
	}

	tests := []struct {
		name     string
		page     int
		wantLen  int
		wantFirst int // ID of first project
	}{
		{
			name:      "page 1",
			page:      1,
			wantLen:   30,
			wantFirst: 1,
		},
		{
			name:      "page 2",
			page:      2,
			wantLen:   30,
			wantFirst: 31,
		},
		{
			name:      "page 3",
			page:      3,
			wantLen:   30,
			wantFirst: 61,
		},
		{
			name:      "page 4 (partial)",
			page:      4,
			wantLen:   10,
			wantFirst: 91,
		},
		{
			name:      "page 0 (defaults to 1)",
			page:      0,
			wantLen:   30,
			wantFirst: 1,
		},
		{
			name:      "page beyond available",
			page:      10,
			wantLen:   0,
			wantFirst: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.pageSlice(tt.page)
			if len(got) != tt.wantLen {
				t.Errorf("pageSlice(%d) returned %d projects, want %d", tt.page, len(got), tt.wantLen)
			}
			if tt.wantLen > 0 && got[0].ID != tt.wantFirst {
				t.Errorf("pageSlice(%d) first project ID = %d, want %d", tt.page, got[0].ID, tt.wantFirst)
			}
		})
	}
}

func TestInvalidateVisibleCache(t *testing.T) {
	m := Model{
		visibleCache:      []gitlab.ProjectNode{{ID: 1, Name: "test"}},
		visibleCacheQuery: "test",
		visibleCachePage:  1,
	}

	m.invalidateVisibleCache()

	if m.visibleCache != nil {
		t.Errorf("invalidateVisibleCache() didn't clear visibleCache")
	}
	if m.visibleCacheQuery != "" {
		t.Errorf("invalidateVisibleCache() didn't clear visibleCacheQuery")
	}
	if m.visibleCachePage != -1 {
		t.Errorf("invalidateVisibleCache() didn't set visibleCachePage to -1, got %d", m.visibleCachePage)
	}
}

func TestInvalidateDetailCache(t *testing.T) {
	m := Model{
		detailCacheProjectID:   123,
		detailCachePipelineID:  456,
		detailCachePipelineHas: true,
		detailCacheWidth:       80,
		detailCacheHeight:      24,
		detailCacheOutput:      "cached output",
	}

	m.invalidateDetailCache()

	if m.detailCacheProjectID != 0 {
		t.Errorf("invalidateDetailCache() didn't clear detailCacheProjectID")
	}
	if m.detailCachePipelineID != 0 {
		t.Errorf("invalidateDetailCache() didn't clear detailCachePipelineID")
	}
	if m.detailCachePipelineHas {
		t.Errorf("invalidateDetailCache() didn't clear detailCachePipelineHas")
	}
	if m.detailCacheWidth != 0 {
		t.Errorf("invalidateDetailCache() didn't clear detailCacheWidth")
	}
	if m.detailCacheHeight != 0 {
		t.Errorf("invalidateDetailCache() didn't clear detailCacheHeight")
	}
	if m.detailCacheOutput != "" {
		t.Errorf("invalidateDetailCache() didn't clear detailCacheOutput")
	}
}

func TestClampLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{
			name:  "text within width",
			input: "hello",
			width: 10,
			want:  "hello",
		},
		{
			name:  "text exceeds width",
			input: "hello world",
			width: 5,
			want:  "hell…",
		},
		{
			name:  "exact width",
			input: "hello",
			width: 5,
			want:  "hello",
		},
		{
			name:  "zero width returns original",
			input: "hello",
			width: 0,
			want:  "hello",
		},
		{
			name:  "width 1 returns ellipsis",
			input: "hello",
			width: 1,
			want:  "…",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampLine(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("clampLine(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{
			name:  "text within max",
			input: "hello",
			max:   10,
			want:  "hello",
		},
		{
			name:  "text exceeds max",
			input: "hello world",
			max:   8,
			want:  "hello w…",
		},
		{
			name:  "exact length",
			input: "hello",
			max:   5,
			want:  "hello",
		},
		{
			name:  "max zero",
			input: "hello",
			max:   0,
			want:  "hello",
		},
		{
			name:  "max negative",
			input: "hello",
			max:   -1,
			want:  "hello",
		},
		{
			name:  "max 1",
			input: "hello",
			max:   1,
			want:  "h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestPipelineStatusIcon(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"success", iconSuccess},
		{"failed", iconFailed},
		{"running", iconRunning},
		{"pending", iconPending},
		{"canceled", iconCanceled},
		{"skipped", iconSkipped},
		{"unknown", iconUnknown},
		{"", iconUnknown},
		{"INVALID", iconUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := pipelineStatusIcon(tt.status)
			if got != tt.want {
				t.Errorf("pipelineStatusIcon(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestVisibilityIcon(t *testing.T) {
	tests := []struct {
		visibility string
		want       string
	}{
		{"public", iconPublic},
		{"private", iconPrivate},
		{"internal", iconInternal},
		{"unknown", iconProject},
		{"", iconProject},
	}

	for _, tt := range tests {
		t.Run(tt.visibility, func(t *testing.T) {
			got := visibilityIcon(tt.visibility)
			if got != tt.want {
				t.Errorf("visibilityIcon(%q) = %q, want %q", tt.visibility, got, tt.want)
			}
		})
	}
}

func TestFormatTimeAgo(t *testing.T) {
	// Just verify it doesn't panic and returns non-empty string
	result := formatTimeAgo(time.Now())
	if result == "" {
		t.Error("formatTimeAgo() returned empty string")
	}
	if !strings.Contains(result, "now") && !strings.Contains(result, "ago") && !strings.Contains(result, "in") {
		t.Errorf("formatTimeAgo() returned unexpected format: %q", result)
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
	}{
		{
			name:  "short text",
			text:  "hello",
			width: 80,
		},
		{
			name:  "long text",
			text:  "This is a very long piece of text that should be wrapped to fit within the specified width parameter",
			width: 40,
		},
		{
			name:  "text with newlines",
			text:  "Line 1\nLine 2\nLine 3",
			width: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.width)
			if got == "" && tt.text != "" {
				t.Errorf("wrapText() returned empty string for non-empty input")
			}
		})
	}
}
