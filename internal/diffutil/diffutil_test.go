package diffutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseHunkHeader(t *testing.T) {
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
			gotOld, gotNew := ParseHunkHeader(tc.line)
			if gotOld != tc.wantOld || gotNew != tc.wantNew {
				t.Errorf("ParseHunkHeader(%q) = (%d, %d), want (%d, %d)",
					tc.line, gotOld, gotNew, tc.wantOld, tc.wantNew)
			}
		})
	}
}

// multiHunkDiff exercises BuildLineMap and FindTargetLine across two hunks
// in a single file plus a second file, so counter resets are observable.
const multiHunkDiff = `@@ -1,3 +1,4 @@
 keep
-old
+new
+added
@@ -10,2 +11,2 @@
 ctx
-bye
+hello`

func TestBuildLineMap_MultiHunk(t *testing.T) {
	diffs := []FileDiff{
		{OldPath: "a.go", NewPath: "a.go", Diff: multiHunkDiff},
		{OldPath: "b.go", NewPath: "b.go", Diff: "@@ -1 +1 @@\n-x\n+y"},
	}
	m := BuildLineMap(diffs)

	// File 0: header + divider + 9 diff lines (2 @@ + 2 context + 2 dels + 3 adds... actually
	// 2 hunk headers, 2 context, 2 deletions, 3 additions = 9) = 11
	// Separator before file 1: 1
	// File 1: header + divider + 3 diff lines = 5
	// Total: 17
	if got, want := len(m), 17; got != want {
		t.Fatalf("BuildLineMap length = %d, want %d", got, want)
	}

	// First entries for file 0
	if m[0].Kind != 'H' || m[1].Kind != 'D' {
		t.Errorf("file 0 prelude = %c%c, want HD", m[0].Kind, m[1].Kind)
	}
	// First hunk header recorded as '@' with parsed line numbers
	if m[2].Kind != '@' || m[2].OldLine != 1 || m[2].NewLine != 1 {
		t.Errorf("first hunk entry = %+v, want @ old=1 new=1", m[2])
	}
	// After the second hunk header, counters should reset
	// Find the second '@' entry within file 0
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

func TestFindTargetLine(t *testing.T) {
	lines := strings.Split(multiHunkDiff, "\n")

	tests := []struct {
		name    string
		oldLine int
		newLine int
		want    int
	}{
		// "+new" is the addition at new-side line 2 (after " keep" at 1)
		{"addition on new side", 0, 2, 3},
		// "+added" — new-side line 3
		{"second addition", 0, 3, 4},
		// "-old" — pure deletion at old-side line 2
		{"deletion on old side", 2, 0, 2},
		// Context line " keep" — new-side line 1
		{"context line new side", 0, 1, 1},
		// Out of range
		{"outside any hunk", 0, 9999, -1},
		// Both zero — no match possible
		{"no position", 0, 0, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FindTargetLine(lines, tc.oldLine, tc.newLine)
			if got != tc.want {
				t.Errorf("FindTargetLine(old=%d new=%d) = %d, want %d",
					tc.oldLine, tc.newLine, got, tc.want)
			}
		})
	}
}

func TestExtractContext(t *testing.T) {
	diffs := []FileDiff{
		{OldPath: "a.go", NewPath: "a.go", Diff: multiHunkDiff},
	}

	t.Run("addition with surrounding context", func(t *testing.T) {
		// "+new" is at split-index 3; ±1 line window yields lines 2..4.
		got := ExtractContext(diffs, "a.go", 0, 2, 1)
		want := []string{"-old", "+new", "+added"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("clamped to hunk boundary", func(t *testing.T) {
		// Asking for huge context around the first addition should still stop
		// before the second hunk's @@ header.
		got := ExtractContext(diffs, "a.go", 0, 2, 100)
		for _, l := range got {
			if strings.HasPrefix(l, "@@ -10") {
				t.Errorf("snippet leaked into next hunk: %q", got)
			}
		}
	})

	t.Run("unknown file returns nil", func(t *testing.T) {
		if got := ExtractContext(diffs, "missing.go", 0, 1, 2); got != nil {
			t.Errorf("expected nil, got %q", got)
		}
	})

	t.Run("zero context returns nil", func(t *testing.T) {
		if got := ExtractContext(diffs, "a.go", 0, 1, 0); got != nil {
			t.Errorf("expected nil, got %q", got)
		}
	})

	t.Run("matches by OldPath for renames", func(t *testing.T) {
		renamed := []FileDiff{
			{OldPath: "old.go", NewPath: "new.go", Diff: "@@ -1 +1 @@\n-x\n+y"},
		}
		got := ExtractContext(renamed, "old.go", 1, 0, 1)
		if len(got) == 0 {
			t.Errorf("expected match via OldPath, got nil")
		}
	})
}
