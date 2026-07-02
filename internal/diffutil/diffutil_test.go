package diffutil

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseHunkHeader: hunk headers yield their old and new starting line
// numbers, and malformed headers fall back to safe defaults instead of failing.
// Given headers with both ranges, single-line ranges, trailing function
// context, no @@ markers at all, and a missing closing @@, when each line is
// parsed, then well-formed headers produce their starting numbers, the line
// without markers falls back to (1, 1), and the unterminated header still
// parses both sides.
// Why it matters: these numbers seed the line counters behind every
// diff-position lookup, so a bad parse would anchor MR review comments and
// their context snippets to the wrong source lines.
func TestParseHunkHeader(t *testing.T) {
	// Given: well-formed and malformed hunk header lines.
	tests := []struct {
		name    string
		line    string
		wantOld int
		wantNew int
	}{
		{"valid both sides", "@@ -10,5 +20,7 @@", 10, 20},
		{"single-line ranges", "@@ -1 +1 @@", 1, 1},
		{"with trailing context", "@@ -42,3 +100,3 @@ func Foo()", 42, 100},
		{"malformed no markers", "not a hunk header", 1, 1},
		{"missing second @@", "@@ -1,2 +3,4", 1, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the header line is parsed.
			gotOld, gotNew := ParseHunkHeader(tc.line)
			// Then: both starting numbers match the expectation.
			if gotOld != tc.wantOld || gotNew != tc.wantNew {
				t.Errorf("ParseHunkHeader(%q) = (%d, %d), want (%d, %d)",
					tc.line, gotOld, gotNew, tc.wantOld, tc.wantNew)
			}
		})
	}
}

// multiHunkDiff is the shared fixture: one file's unified diff with two hunks,
// so tests can observe the line counters resetting at the second hunk header.
const multiHunkDiff = `@@ -1,3 +1,4 @@
 keep
-old
+new
+added
@@ -10,2 +11,2 @@
 ctx
-bye
+hello`

// TestBuildLineMap_MultiHunk: mapping a two-file diff yields one entry per
// rendered line, with each hunk header restarting the line counters.
// Given the two-hunk fixture as one file plus a single-hunk second file, when
// BuildLineMap flattens them, then the map holds one entry per rendered line
// (17 in total), file 0 opens with header and divider entries, its first hunk
// entry carries old=1 new=1, and its second hunk entry carries the restarted
// old=10 new=11.
// Why it matters: the MR diff panel indexes this map by cursor row to anchor
// inline comments, so a missing or shifted entry would attach a comment to a
// neighboring line or the wrong file.
func TestBuildLineMap_MultiHunk(t *testing.T) {
	// Given: a two-hunk file plus a single-hunk second file.
	diffs := []FileDiff{
		{OldPath: "a.go", NewPath: "a.go", Diff: multiHunkDiff},
		{OldPath: "b.go", NewPath: "b.go", Diff: "@@ -1 +1 @@\n-x\n+y"},
	}

	// When: the rendered-line map is built.
	m := BuildLineMap(diffs)

	// Then: the map is line-for-line with the rendered output. File 0 is
	// header + divider + 9 diff lines (2 hunk headers, 2 context, 2
	// deletions, 3 additions) = 11, then 1 separator, then file 1 is
	// header + divider + 3 diff lines = 5, so 17 entries in total.
	if got, want := len(m), 17; got != want {
		t.Fatalf("BuildLineMap length = %d, want %d", got, want)
	}

	// And: file 0 opens with its header and divider entries.
	if m[0].Kind != 'H' || m[1].Kind != 'D' {
		t.Errorf("file 0 prelude = %c%c, want HD", m[0].Kind, m[1].Kind)
	}
	// And: the first hunk header is recorded as '@' with its parsed numbers.
	if m[2].Kind != '@' || m[2].OldLine != 1 || m[2].NewLine != 1 {
		t.Errorf("first hunk entry = %+v, want @ old=1 new=1", m[2])
	}
	// And: the second '@' entry within file 0 restarts the counters from
	// its own header.
	var secondHunk LineInfo
	seen := 0
	for _, e := range m {
		if e.FileIdx == 0 && e.Kind == '@' {
			seen++
			if seen == 2 {
				secondHunk = e
				break
			}
		}
	}
	if secondHunk.OldLine != 10 || secondHunk.NewLine != 11 {
		t.Errorf("second hunk = %+v, want old=10 new=11", secondHunk)
	}
}

// TestFindTargetLine: a diff position (old/new line pair) resolves to the
// index of the matching raw diff line, or -1 when nothing matches.
// Given the multi-hunk fixture split into lines, when positions for two
// additions, a pure deletion, a context line, an out-of-range line, and an
// empty position are looked up, then each resolves to the expected index,
// with -1 for the last two.
// Why it matters: this lookup places MR review comments and their context
// snippets, so an off-by-one would show a reviewer's comment against the
// wrong line of code.
func TestFindTargetLine(t *testing.T) {
	// Given: the fixture's raw lines and positions covering each line kind.
	lines := strings.Split(multiHunkDiff, "\n")

	tests := []struct {
		name    string
		oldLine int
		newLine int
		want    int
	}{
		// "+new" is the addition at new-side line 2 (after " keep" at 1).
		{"addition on new side", 0, 2, 3},
		// "+added": new-side line 3.
		{"second addition", 0, 3, 4},
		// "-old": pure deletion at old-side line 2.
		{"deletion on old side", 2, 0, 2},
		// Context line " keep": new-side line 1.
		{"context line new side", 0, 1, 1},
		// Out of range.
		{"outside any hunk", 0, 9999, -1},
		// Both zero: no match possible.
		{"no position", 0, 0, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the position is resolved against the diff lines.
			got := FindTargetLine(lines, tc.oldLine, tc.newLine)
			// Then: the resolved index matches.
			if got != tc.want {
				t.Errorf("FindTargetLine(old=%d new=%d) = %d, want %d",
					tc.oldLine, tc.newLine, got, tc.want)
			}
		})
	}
}

// TestExtractContext: the snippet around a commented diff line includes its
// neighbors, stops at hunk boundaries, and is nil when there is nothing to
// show.
// Given a file carrying the two-hunk fixture, when context is extracted around
// an addition with a one-line window, with an oversized window, for an unknown
// file, with a zero-line window, and through the pre-rename path of a renamed
// file, then the snippet holds the neighboring lines, never crosses into the
// second hunk, comes back nil for the unknown-file and zero-window cases, and
// still matches when only OldPath fits.
// Why it matters: these snippets are the code excerpts shown under MR review
// comments, so a bad window would show reviewers lines from an unrelated hunk
// or drop the excerpt entirely.
func TestExtractContext(t *testing.T) {
	// Given: a single file carrying the two-hunk fixture.
	diffs := []FileDiff{
		{OldPath: "a.go", NewPath: "a.go", Diff: multiHunkDiff},
	}

	t.Run("addition with surrounding context", func(t *testing.T) {
		// When: extracting a one-line window around "+new", which sits at
		// split-index 3, so the window covers indexes 2..4.
		got := ExtractContext(diffs, "a.go", 0, 2, 1)
		// Then: the snippet is the deletion plus both additions.
		want := []string{"-old", "+new", "+added"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("clamped to hunk boundary", func(t *testing.T) {
		// When: asking for a huge window around the first addition.
		got := ExtractContext(diffs, "a.go", 0, 2, 100)
		// Then: the snippet stops before the second hunk's @@ header.
		for _, l := range got {
			if strings.HasPrefix(l, "@@ -10") {
				t.Errorf("snippet leaked into next hunk: %q", got)
			}
		}
	})

	t.Run("unknown file returns nil", func(t *testing.T) {
		// When/Then: a path absent from the diffs yields no snippet.
		if got := ExtractContext(diffs, "missing.go", 0, 1, 2); got != nil {
			t.Errorf("expected nil, got %q", got)
		}
	})

	t.Run("zero context returns nil", func(t *testing.T) {
		// When/Then: a zero-line window yields no snippet.
		if got := ExtractContext(diffs, "a.go", 0, 1, 0); got != nil {
			t.Errorf("expected nil, got %q", got)
		}
	})

	t.Run("matches by OldPath for renames", func(t *testing.T) {
		// Given: a renamed file whose pre-rename path is old.go.
		renamed := []FileDiff{
			{OldPath: "old.go", NewPath: "new.go", Diff: "@@ -1 +1 @@\n-x\n+y"},
		}
		// When: extracting context addressed by the old path.
		got := ExtractContext(renamed, "old.go", 1, 0, 1)
		// Then: the file still matches via OldPath.
		if len(got) == 0 {
			t.Errorf("expected match via OldPath, got nil")
		}
	})
}
