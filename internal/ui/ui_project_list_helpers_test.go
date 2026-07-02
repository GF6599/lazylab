package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestClampLineANSI: ANSI-styled lines clamp to the visible width with escape codes preserved.
// Given plain and ANSI-wrapped inputs at fitting, overflowing, and degenerate widths, when each line is
// clamped, then the output equals the exact expected string (styles and the trailing reset survive
// truncation) and the printable width never exceeds a positive limit.
// Why it matters: an escape-unaware clamp either drops the color reset, bleeding one log line's style
// into every row below it, or counts escape bytes as cells and cuts lines far short of the pane edge.
func TestClampLineANSI(t *testing.T) {
	// Given: plain and ANSI-styled lines at fitting, overflowing, and degenerate widths
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{
			name:  "plain text within width",
			input: "hello",
			width: 10,
			want:  "hello",
		},
		{
			name:  "plain text exceeds width",
			input: "hello world this is a long string",
			width: 10,
			want:  "hello wor…",
		},
		{
			name:  "text with ANSI codes within width",
			input: "\x1b[31mred text\x1b[0m",
			width: 10,
			want:  "\x1b[31mred text\x1b[0m",
		},
		{
			name:  "text with ANSI codes exceeds width",
			input: "\x1b[31mthis is very long red text that will be truncated\x1b[0m",
			width: 10,
			want:  "\x1b[31mthis is v…\x1b[0m",
		},
		{
			name:  "width 1 collapses to ellipsis",
			input: "hello",
			width: 1,
			want:  "…",
		},
		{
			name:  "zero width returns original",
			input: "hello",
			width: 0,
			want:  "hello",
		},
		{
			name:  "negative width returns original",
			input: "hello",
			width: -5,
			want:  "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the line is clamped
			got := clampLineANSI(tt.input, tt.width)

			// Then: the output is exactly the expected string
			if got != tt.want {
				t.Errorf("clampLineANSI(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.want)
			}

			// And: for positive widths the printable width fits within the limit
			if tt.width > 0 && ansi.StringWidth(got) > tt.width {
				t.Errorf("clampLineANSI(%q, %d) printable width = %d, want <= %d",
					tt.input, tt.width, ansi.StringWidth(got), tt.width)
			}
		})
	}
}

// TestFuzzyMatch: subsequence matching is case-insensitive, order-sensitive, and edge-safe.
// Given haystack/needle pairs covering exact, case-differing, skipping, out-of-order, and empty inputs,
// when each pair is matched, then only in-order subsequences match, the empty needle matches anything,
// and a needle longer than its haystack never matches.
// Why it matters: search is how users find one project among hundreds, and out-of-order matches would
// flood the results while a broken empty-query case would blank the whole list.
func TestFuzzyMatch(t *testing.T) {
	// Given: haystack/needle pairs with their expected match outcomes
	tests := []struct {
		name     string
		haystack string
		needle   string
		want     bool
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
			// When/Then: the pair matches exactly when an in-order subsequence exists
			got := fuzzyMatch(tt.haystack, tt.needle)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
			}
		})
	}
}

// TestPageSlice: each page maps to its exact slice of loaded projects.
// Given 100 projects with pages 1-4 marked ready at 30 per page, when a page is sliced, then full pages
// return 30 projects starting at the right ID, the final page returns its 10-project remainder, page 0
// falls back to page 1, and pages beyond the data return nothing.
// Why it matters: an off-by-one here duplicates or skips projects at page boundaries, and an unclamped
// final page would slice past the backing array and panic.
func TestPageSlice(t *testing.T) {
	// Given: 100 projects across four ready pages of 30
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
		name      string
		page      int
		wantLen   int
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
			// When: the page is sliced
			got := m.pageSlice(tt.page)

			// Then: the slice has the expected size and starts at the expected project
			if len(got) != tt.wantLen {
				t.Errorf("pageSlice(%d) returned %d projects, want %d", tt.page, len(got), tt.wantLen)
			}
			if tt.wantLen > 0 && got[0].ID != tt.wantFirst {
				t.Errorf("pageSlice(%d) first project ID = %d, want %d", tt.page, got[0].ID, tt.wantFirst)
			}
		})
	}
}

// TestInvalidateVisibleCache: invalidation resets the memoized visible-projects state.
// Given a populated visible cache with a query and page recorded, when it is invalidated, then the slice
// is nil, the query is empty, and the page marker moves to -1.
// Why it matters: a partially cleared memo makes visibleProjects keep serving the previous filter, so the
// list stops reacting to search input and page changes.
func TestInvalidateVisibleCache(t *testing.T) {
	// Given: a populated visible-projects memo
	m := Model{
		visibleCache:      []gitlab.ProjectNode{{ID: 1, Name: "test"}},
		visibleCacheQuery: "test",
		visibleCachePage:  1,
	}

	// When: the memo is invalidated
	m.invalidateVisibleCache()

	// Then: every memo field is reset
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

// TestInvalidateDetailCache: invalidation empties the detail pane's render memo.
// Given a populated detailCache, when it is invalidated, then the state is back to the zero value.
// Why it matters: a surviving render memo keeps the detail pane showing the previous project after the
// selection has moved on.
func TestInvalidateDetailCache(t *testing.T) {
	// Given: a populated detail render memo
	m := Model{
		detailCache: detailCacheState{
			projectID:   123,
			pipelineID:  456,
			pipelineHas: true,
			width:       80,
			height:      24,
			output:      "cached output",
		},
	}

	// When: the memo is invalidated
	m.invalidateDetailCache()

	// Then: the state is the zero value, which forces the next render
	if m.detailCache != (detailCacheState{}) {
		t.Errorf("invalidateDetailCache() didn't clear detailCache, got %+v", m.detailCache)
	}
}

// TestClampLine: overlong lines truncate to width with a trailing ellipsis.
// Given lines under, at, and over the width plus degenerate widths, when each is clamped, then fitting
// text passes through untouched, overflow is cut to fit with an ellipsis, width 0 returns the input, and
// width 1 collapses to the bare ellipsis.
// Why it matters: one unclamped cell overflows its column and shears the whole table row out of alignment.
func TestClampLine(t *testing.T) {
	// Given: lines at fitting, overflowing, and degenerate widths
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
			// When/Then: clamping yields the exact expected string
			got := clampLine(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("clampLine(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

// TestPipelineStatusIcon: every pipeline status maps to its icon and unrecognized ones degrade safely.
// Given each known status plus empty and unrecognized strings, when the icon is looked up, then known
// statuses map to their glyphs and everything else falls back to the unknown icon.
// Why it matters: the status glyph is the only CI signal in the project list, and a missing fallback would
// render a blank or wrong cell for statuses GitLab adds later.
func TestPipelineStatusIcon(t *testing.T) {
	// Given: known, empty, and unrecognized statuses
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
			// When/Then: the status resolves to its expected glyph
			got := pipelineStatusIcon(tt.status)
			if got != tt.want {
				t.Errorf("pipelineStatusIcon(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// TestVisibilityIcon: project visibility levels map to icons with a generic fallback.
// Given public, private, internal, unknown, and empty visibility strings, when the icon is looked up,
// then the three known levels map to their glyphs and the rest fall back to the plain project icon.
// Why it matters: a wrong glyph misrepresents whether a project is exposed publicly.
func TestVisibilityIcon(t *testing.T) {
	// Given: known, unknown, and empty visibility levels
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
			// When/Then: the visibility resolves to its expected glyph
			got := visibilityIcon(tt.visibility)
			if got != tt.want {
				t.Errorf("visibilityIcon(%q) = %q, want %q", tt.visibility, got, tt.want)
			}
		})
	}
}

// TestFormatTimeAgo: every relative-time bucket formats to its exact phrase.
// Given timestamps placed safely inside each bucket from seconds to months plus the zero time, when each
// is formatted, then the output is the exact expected string, including the singular 1m/1h/1d/1w/1mo forms.
// Why it matters: this string sits on every project row, and a bucket slip (say "36h ago" instead of
// "1d ago") breaks the at-a-glance activity scan users rely on to pick the live project.
func TestFormatTimeAgo(t *testing.T) {
	// Given: timestamps well inside each formatting bucket, so clock jitter
	// between now and formatTimeAgo's own time.Since cannot cross a boundary
	now := time.Now()
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"zero time", time.Time{}, "never"},
		{"under a minute", now.Add(-30 * time.Second), "just now"},
		{"one minute singular", now.Add(-90 * time.Second), "1m ago"},
		{"minutes", now.Add(-30 * time.Minute), "30m ago"},
		{"one hour singular", now.Add(-90 * time.Minute), "1h ago"},
		{"hours", now.Add(-12 * time.Hour), "12h ago"},
		{"one day singular", now.Add(-36 * time.Hour), "1d ago"},
		{"days", now.Add(-84 * time.Hour), "3d ago"},
		{"one week singular", now.Add(-10 * 24 * time.Hour), "1w ago"},
		{"weeks", now.Add(-20 * 24 * time.Hour), "2w ago"},
		{"one month singular", now.Add(-45 * 24 * time.Hour), "1mo ago"},
		{"months", now.Add(-100 * 24 * time.Hour), "3mo ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When/Then: formatting yields the exact bucket phrase
			if got := formatTimeAgo(tt.at); got != tt.want {
				t.Errorf("formatTimeAgo(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestStageTableColumns: the three stage-table columns always fit the requested width.
// Given widths from 10 to 200, when the columns are computed, then exactly Job, Stage, and Status exist,
// their widths plus padding never exceed the input, every width is at least 1, and at usable widths Job
// is the widest.
// Why it matters: columns wider than the pane wrap every row of the stages table, and a Job column
// squeezed below Stage truncates the one field that distinguishes matrix jobs.
func TestStageTableColumns(t *testing.T) {
	// Given: pane widths from tiny to very wide
	tests := []struct {
		name  string
		width int
	}{
		{"narrow 20", 20},
		{"minimum 28", 28},
		{"typical sidebar 40", 40},
		{"default 56", 56},
		{"wide 100", 100},
		{"very wide 200", 200},
		{"tiny 10", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the columns are computed for the width
			cols := stageTableColumns(tt.width)

			// Then: the three expected columns exist
			if len(cols) != 3 {
				t.Fatalf("expected 3 columns, got %d", len(cols))
			}
			if cols[0].Title != "Job" || cols[1].Title != "Stage" || cols[2].Title != "Status" {
				t.Errorf("unexpected column titles: %v", cols)
			}

			// And: widths plus padding (2 per column) fit inside the input width
			sum := cols[0].Width + cols[1].Width + cols[2].Width + 6
			if sum > tt.width {
				t.Errorf("columns overflow: sum=%d (widths %d+%d+%d + 6 padding) > width=%d",
					sum, cols[0].Width, cols[1].Width, cols[2].Width, tt.width)
			}

			// And: every column keeps a positive width
			for i, c := range cols {
				if c.Width < 1 {
					t.Errorf("column %d (%s) width=%d, want >= 1", i, c.Title, c.Width)
				}
			}

			// And: at usable widths, Job stays the widest column
			if tt.width >= 40 && cols[0].Width <= cols[1].Width {
				t.Errorf("Job (%d) should be wider than Stage (%d) at width=%d",
					cols[0].Width, cols[1].Width, tt.width)
			}
		})
	}
}

// TestWrapSelectedItem: the selected row wraps to indented continuation lines capped at maxLines.
// Given texts of varying length with width, indent, and maxLines settings, when each is wrapped, then
// short text stays on one line, continuation lines carry the indent, the last permitted line ends in an
// ellipsis when text remains, width 0 passes through, and maxLines 0 falls back to the default cap.
// Why it matters: the selected project's full path is usually wider than the sidebar, and without capped
// wrapping it either disappears or pushes the rest of the list off-screen.
func TestWrapSelectedItem(t *testing.T) {
	// Given: texts with width, indent, and line-cap combinations
	tests := []struct {
		name     string
		text     string
		width    int
		indent   int
		maxLines int
		want     []string
	}{
		{
			name:     "fits in width",
			text:     "short",
			width:    20,
			indent:   4,
			maxLines: 2,
			want:     []string{"short"},
		},
		{
			name:     "wraps to two lines",
			text:     "> group/subgroup/very-long-project-name",
			width:    20,
			indent:   2,
			maxLines: 2,
			want:     []string{"> group/subgroup/ver", "  y-long-project-na…"},
		},
		{
			name:     "wraps to three lines",
			text:     "> group/subgroup/very-long-project-name-here",
			width:    20,
			indent:   2,
			maxLines: 3,
			want:     []string{"> group/subgroup/ver", "  y-long-project-nam", "  e-here"},
		},
		{
			name:     "truncates last line",
			text:     "abcdefghijklmnopqrstuvwxyz0123456789",
			width:    10,
			indent:   2,
			maxLines: 2,
			want:     []string{"abcdefghij", "  klmnopq…"},
		},
		{
			name:     "zero width returns original",
			text:     "hello",
			width:    0,
			indent:   2,
			maxLines: 2,
			want:     []string{"hello"},
		},
		{
			name:     "default maxLines when zero",
			text:     "abcdefghijklmnop",
			width:    10,
			indent:   2,
			maxLines: 0,
			want:     []string{"abcdefghij", "  klmnop"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the text is wrapped
			got := wrapSelectedItem(tt.text, tt.width, tt.indent, tt.maxLines)

			// Then: the exact expected lines come back
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines %v, want %d lines %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestWrapText: text wraps at word boundaries measured in visible cells, not bytes.
// Given ASCII and CJK strings at various widths, when each is wrapped, then fitting text stays on one
// line, breaks land on word boundaries, an overlong word takes its own line, degenerate inputs pass
// through, and double-width CJK text wraps by cell count.
// Why it matters: byte-based wrapping lets double-width text overflow its pane and misalign every
// adjacent panel border.
func TestWrapText(t *testing.T) {
	// Given: ASCII and CJK texts with their expected wrapped forms
	tests := []struct {
		name  string
		text  string
		width int
		want  string
	}{
		{
			name:  "short text fits on one line",
			text:  "hello",
			width: 80,
			want:  "hello",
		},
		{
			name:  "wraps at word boundary",
			text:  "alpha bravo charlie",
			width: 11,
			want:  "alpha bravo\ncharlie",
		},
		{
			name:  "long word exceeds width on its own line",
			text:  "tiny supercalifragilistic end",
			width: 10,
			want:  "tiny\nsupercalifragilistic\nend",
		},
		{
			name:  "zero width returns input verbatim",
			text:  "hello world",
			width: 0,
			want:  "hello world",
		},
		{
			name:  "empty input returns empty",
			text:  "",
			width: 10,
			want:  "",
		},
		{
			// CJK characters are 2 cells wide each, so "你好" is 4 cells. Byte len
			// is 6. Width 5 should fit "你好" but not "你好 abc" (would be 8).
			name:  "wraps cjk by visible cells not bytes",
			text:  "你好 abc",
			width: 5,
			want:  "你好\nabc",
		},
		{
			// Without width-aware measurement this would not wrap because the
			// byte length 13 < width 12; with cells it's 4+1+5=10, still fits
			// on one line. Add another word to force a wrap.
			name:  "cjk multi-line wrap",
			text:  "你好 世界 done",
			width: 6,
			want:  "你好\n世界\ndone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When/Then: wrapping yields the exact expected line breaks
			got := wrapText(tt.text, tt.width)
			if got != tt.want {
				t.Errorf("wrapText(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
			}
		})
	}
}
