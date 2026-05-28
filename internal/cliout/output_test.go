package cliout

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

// TestTable_RendersAlignedColumns is the core contract: column widths
// derive from header-vs-cells max, and inter-column spacing is exactly
// two spaces. Failing this turns `lazylab pipeline list | column -t`
// into a no-op and breaks awk-style consumers that expect predictable
// whitespace.
func TestTable_RendersAlignedColumns(t *testing.T) {
	tbl := NewTable("ID", "STATUS", "REF")
	tbl.AddRow("1", "success", "main")
	tbl.AddRow("123456", "failed", "feat/very-long-branch-name")
	tbl.AddRow("99", "running", "main")

	var buf bytes.Buffer
	if err := tbl.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (header + 3 rows), got %d:\n%s", len(lines), out)
	}
	// Header pads ID to width 6 (max of "ID","1","123456","99").
	if !strings.HasPrefix(lines[0], "ID    ") {
		t.Errorf("ID column should be padded to width 6; header: %q", lines[0])
	}
	// Last column must NOT have trailing whitespace — scripts depend on this.
	for i, line := range lines {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line %d has trailing whitespace: %q", i, line)
		}
	}
}

// TestTable_PadsShortRows: missing cells should not crash; they're
// padded so column alignment is preserved.
func TestTable_PadsShortRows(t *testing.T) {
	tbl := NewTable("A", "B", "C")
	tbl.AddRow("1") // only one cell — pad the other two
	var buf bytes.Buffer
	_ = tbl.Render(&buf)
	if !strings.Contains(buf.String(), "1") {
		t.Errorf("short row should still render its first cell, got: %q", buf.String())
	}
}

// TestTable_EmptyIsSilent: an empty table emits nothing rather than a
// blank header row, so callers can iterate without worrying about
// "no data" cases producing visual noise.
func TestTable_EmptyIsSilent(t *testing.T) {
	tbl := &Table{}
	var buf bytes.Buffer
	if err := tbl.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty table should emit nothing, got %q", buf.String())
	}
}

// TestHumanizeTime covers each branch of the duration classifier with
// precise assertions: substring matches like Contains("-") used to pass
// for any output containing a dash (including "—" or relative formats),
// which masked real formatting regressions. Each case here either
// matches an exact prefix or a tight regex so a wrong branch is caught.
func TestHumanizeTime(t *testing.T) {
	now := time.Now()
	isoDate := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// HumanizeTime stamps future timestamps with the full minute-level
	// ISO form. The trailing literal "Z" is what makes it disambiguate
	// from the older-than-week branch's bare date.
	futureFmt := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}Z$`)

	type check struct {
		name  string
		input time.Time
		// Exactly one of wantExact / wantPrefix / wantRegex applies.
		wantExact  string
		wantPrefix string
		wantRegex  *regexp.Regexp
	}
	tests := []check{
		{name: "zero", input: time.Time{}, wantExact: "—"},
		{name: "sub-minute", input: now.Add(-10 * time.Second), wantExact: "<1m ago"},
		// Don't pin the minute count because the test run shaves
		// seconds off "5m"; a prefix check tolerates that drift while
		// still verifying the unit and "ago" suffix.
		{name: "minutes", input: now.Add(-5 * time.Minute), wantRegex: regexp.MustCompile(`^\d+m ago$`)},
		{name: "hours", input: now.Add(-3 * time.Hour), wantRegex: regexp.MustCompile(`^\d+h ago$`)},
		{name: "days", input: now.Add(-3 * 24 * time.Hour), wantRegex: regexp.MustCompile(`^\d+d ago$`)},
		{name: "older than week", input: now.Add(-30 * 24 * time.Hour), wantRegex: isoDate},
		// Clock skew: server timestamp is in the future relative to the
		// caller. Should render the absolute time, not "-Xm ago".
		{name: "future (clock skew)", input: now.Add(5 * time.Minute), wantRegex: futureFmt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HumanizeTime(tc.input)
			switch {
			case tc.wantExact != "":
				if got != tc.wantExact {
					t.Errorf("HumanizeTime(%v) = %q, want exact %q", tc.input, got, tc.wantExact)
				}
			case tc.wantPrefix != "":
				if !strings.HasPrefix(got, tc.wantPrefix) {
					t.Errorf("HumanizeTime(%v) = %q, want prefix %q", tc.input, got, tc.wantPrefix)
				}
			case tc.wantRegex != nil:
				if !tc.wantRegex.MatchString(got) {
					t.Errorf("HumanizeTime(%v) = %q, want match %s", tc.input, got, tc.wantRegex)
				}
			}
		})
	}
}

// TestTable_RendersUnicodeAligned guards the rune-width path: a column
// containing multi-byte ("François") and CJK ("日本語", width 2 per
// glyph) cells must still produce equal-display-width columns. The old
// byte-length code under-padded these by exactly the number of extra
// bytes, which manifested in production as misaligned MR author and
// branch columns whenever non-ASCII names appeared.
func TestTable_RendersUnicodeAligned(t *testing.T) {
	tbl := NewTable("NAME", "STATUS")
	tbl.AddRow("alice", "ok")
	tbl.AddRow("François", "ok")
	tbl.AddRow("日本語", "ok")

	var buf bytes.Buffer
	if err := tbl.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), buf.String())
	}

	// Every line should have the second column ("ok") start at the
	// same display column, i.e. the prefix "<name padded> ok" has
	// identical display width across rows. Comparing the display
	// width of each line up to (but not including) the final "ok"
	// proves alignment regardless of byte length.
	prefixes := make([]int, len(lines))
	for i, line := range lines {
		idx := strings.LastIndex(line, "  ")
		if idx < 0 {
			t.Fatalf("line %d missing column separator: %q", i, line)
		}
		// Width up to and including the two-space separator.
		prefixes[i] = runewidth.StringWidth(line[:idx+2])
	}
	for i := 1; i < len(prefixes); i++ {
		if prefixes[i] != prefixes[0] {
			t.Errorf("line %d prefix display-width=%d, want %d (line=%q vs %q)",
				i, prefixes[i], prefixes[0], lines[i], lines[0])
		}
	}
}
