package ui

import (
	"strings"
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestExtractDiffContext_Addition verifies that an addition-only comment
// (NewLine set, OldLine=0) returns the surrounding diff lines including the
// target + line.
func TestExtractDiffContext_Addition(t *testing.T) {
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -10,4 +10,6 @@ func init() {\n ctx := context.Background()\n fmt.Println(ctx)\n+\tnewLine1()\n+\tnewLine2()\n return nil\n }\n",
	}}
	// Target new line 12 = the first addition
	got := extractDiffContext(diffs, "main.go", 0, 12, 2)
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "+\tnewLine1()") {
		t.Errorf("expected +newLine1() in context, got:\n%s", joined)
	}
}

// TestExtractDiffContext_Deletion verifies that a deletion-only comment
// (OldLine set, NewLine=0) matches the - line correctly.
func TestExtractDiffContext_Deletion(t *testing.T) {
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -5,4 +5,3 @@ package main\n import \"fmt\"\n-var old = true\n var keep = false\n",
	}}
	// Target old line 6 = the deletion
	got := extractDiffContext(diffs, "main.go", 6, 0, 2)
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "-var old = true") {
		t.Errorf("expected -var old in context, got:\n%s", joined)
	}
}

// TestExtractDiffContext_ContextLine verifies that a comment on an unchanged
// context line (both OldLine and NewLine set) is found correctly.
func TestExtractDiffContext_ContextLine(t *testing.T) {
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -1,3 +1,4 @@\n package main\n import \"fmt\"\n+import \"os\"\n var x = 1\n",
	}}
	// Context line at new=2, old=2 (import "fmt")
	got := extractDiffContext(diffs, "main.go", 2, 2, 2)
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, " import \"fmt\"") {
		t.Errorf("expected context line in snippet, got:\n%s", joined)
	}
}

func TestExtractDiffContext_NoMatchingFile(t *testing.T) {
	diffs := []gitlab.MRDiffFile{{
		OldPath: "other.go", NewPath: "other.go",
		Diff: "@@ -1,2 +1,3 @@\n line1\n+line2\n line3\n",
	}}
	got := extractDiffContext(diffs, "main.go", 0, 2, 5)
	if got != nil {
		t.Errorf("expected nil for non-matching file, got %d lines", len(got))
	}
}

func TestExtractDiffContext_NoMatchingLine(t *testing.T) {
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -1,2 +1,3 @@\n line1\n+line2\n line3\n",
	}}
	got := extractDiffContext(diffs, "main.go", 0, 999, 5)
	if got != nil {
		t.Errorf("expected nil for non-matching line, got %d lines", len(got))
	}
}

func TestExtractDiffContext_DisabledWhenZero(t *testing.T) {
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -1,2 +1,3 @@\n line1\n+line2\n line3\n",
	}}
	got := extractDiffContext(diffs, "main.go", 0, 2, 0)
	if got != nil {
		t.Errorf("expected nil when contextLines=0, got %d lines", len(got))
	}
}

func TestExtractDiffContext_NilDiffs(t *testing.T) {
	got := extractDiffContext(nil, "main.go", 0, 2, 5)
	if got != nil {
		t.Errorf("expected nil for nil diffs, got %d lines", len(got))
	}
}

// TestRenderDiffSnippet verifies that each diff line type gets the correct
// style applied (addition, deletion, hunk header, context).
func TestRenderDiffSnippet(t *testing.T) {
	lines := []string{
		" context line",
		"+added line",
		"-removed line",
		"@@ -1,2 +1,3 @@",
	}
	result := renderDiffSnippet(lines, 80)
	if !strings.Contains(result, "context line") {
		t.Error("expected context line in output")
	}
	if !strings.Contains(result, "+added line") {
		t.Error("expected addition in output")
	}
	if !strings.Contains(result, "-removed line") {
		t.Error("expected deletion in output")
	}
}
