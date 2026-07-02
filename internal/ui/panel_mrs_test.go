package ui

import (
	"strings"
	"testing"

	"github.com/GF6599/lazylab/internal/diffutil"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestExtractDiffContext_Addition: an addition-anchored comment yields a snippet containing its + line.
// Given a diff whose hunk adds two lines, when context is extracted for a comment with only NewLine
// set (OldLine zero) pointing at the first addition, then the snippet includes that + line.
// Why it matters: GitLab anchors comments on added code with a new-file line number only, and
// mislocating it would caption a reviewer's feedback with the wrong code in the comments pane.
func TestExtractDiffContext_Addition(t *testing.T) {
	// Given: a hunk starting at new line 10 whose first added line lands on new line 12
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -10,4 +10,6 @@ func init() {\n ctx := context.Background()\n fmt.Println(ctx)\n+\tnewLine1()\n+\tnewLine2()\n return nil\n }\n",
	}}

	// When: context is extracted for an addition-only position at new line 12
	got := diffutil.ExtractContext(toDiffutilFiles(diffs), "main.go", 0, 12, 2)

	// Then: the snippet contains the targeted + line
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "+\tnewLine1()") {
		t.Errorf("expected +newLine1() in context, got:\n%s", joined)
	}
}

// TestExtractDiffContext_Deletion: a deletion-anchored comment yields a snippet containing its - line.
// Given a diff that removes one line, when context is extracted for a comment with only OldLine set
// (NewLine zero) pointing at the removal, then the snippet includes that - line.
// Why it matters: comments on deleted code carry only an old-file line number, and resolving it
// against new-file numbering would attach the feedback to an unrelated surviving line.
func TestExtractDiffContext_Deletion(t *testing.T) {
	// Given: a hunk starting at old line 5 whose deletion lands on old line 6
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -5,4 +5,3 @@ package main\n import \"fmt\"\n-var old = true\n var keep = false\n",
	}}

	// When: context is extracted for a deletion-only position at old line 6
	got := diffutil.ExtractContext(toDiffutilFiles(diffs), "main.go", 6, 0, 2)

	// Then: the snippet contains the removed line
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "-var old = true") {
		t.Errorf("expected -var old in context, got:\n%s", joined)
	}
}

// TestExtractDiffContext_ContextLine: a comment on an unchanged line resolves to that context line.
// Given a diff where import "fmt" sits unchanged at old and new line 2, when context is extracted
// with both line numbers set, then the snippet includes that unchanged line.
// Why it matters: most review comments sit on unchanged lines carrying both numbers, and failing to
// resolve them would strip the code snippet from the bulk of MR discussions.
func TestExtractDiffContext_ContextLine(t *testing.T) {
	// Given: a hunk where the import "fmt" line is unchanged at old=2, new=2
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -1,3 +1,4 @@\n package main\n import \"fmt\"\n+import \"os\"\n var x = 1\n",
	}}

	// When: context is extracted for the dual-numbered position
	got := diffutil.ExtractContext(toDiffutilFiles(diffs), "main.go", 2, 2, 2)

	// Then: the snippet contains the unchanged line
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, " import \"fmt\"") {
		t.Errorf("expected context line in snippet, got:\n%s", joined)
	}
}

// TestExtractDiffContext_NoMatchingFile: a comment on a file absent from the diff yields no snippet.
// Given diffs that only cover other.go, when context is requested for main.go, then the result is nil.
// Why it matters: comment positions can reference files outside the fetched diff, and inventing a
// snippet from some other file would caption the feedback with unrelated code.
func TestExtractDiffContext_NoMatchingFile(t *testing.T) {
	// Given: a diff for a different file than the comment's
	diffs := []gitlab.MRDiffFile{{
		OldPath: "other.go", NewPath: "other.go",
		Diff: "@@ -1,2 +1,3 @@\n line1\n+line2\n line3\n",
	}}

	// When/Then: requesting context for main.go returns no snippet
	got := diffutil.ExtractContext(toDiffutilFiles(diffs), "main.go", 0, 2, 5)
	if got != nil {
		t.Errorf("expected nil for non-matching file, got %d lines", len(got))
	}
}

// TestExtractDiffContext_NoMatchingLine: a comment on a line the diff never touches yields no snippet.
// Given a three-line hunk, when context is requested for new line 999, then the result is nil.
// Why it matters: outdated comment positions routinely fall outside the current diff, and a
// fabricated nearest-match snippet would pair old feedback with code it was never about.
func TestExtractDiffContext_NoMatchingLine(t *testing.T) {
	// Given: a hunk whose new lines stop well short of 999
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -1,2 +1,3 @@\n line1\n+line2\n line3\n",
	}}

	// When/Then: requesting context for new line 999 returns no snippet
	got := diffutil.ExtractContext(toDiffutilFiles(diffs), "main.go", 0, 999, 5)
	if got != nil {
		t.Errorf("expected nil for non-matching line, got %d lines", len(got))
	}
}

// TestExtractDiffContext_DisabledWhenZero: contextLines zero turns snippet extraction off entirely.
// Given a diff that would otherwise match, when context is requested with contextLines set to zero,
// then the result is nil.
// Why it matters: setting DiffContextLines to 0 is the documented way to disable inline diff context,
// and returning a snippet anyway would keep injecting diff noise for users who opted out.
func TestExtractDiffContext_DisabledWhenZero(t *testing.T) {
	// Given: a diff containing the targeted line
	diffs := []gitlab.MRDiffFile{{
		OldPath: "main.go", NewPath: "main.go",
		Diff: "@@ -1,2 +1,3 @@\n line1\n+line2\n line3\n",
	}}

	// When/Then: requesting context with contextLines=0 returns no snippet
	got := diffutil.ExtractContext(toDiffutilFiles(diffs), "main.go", 0, 2, 0)
	if got != nil {
		t.Errorf("expected nil when contextLines=0, got %d lines", len(got))
	}
}

// TestExtractDiffContext_NilDiffs: requesting context with no diffs loaded yields no snippet.
// Given a nil diff slice, when context is requested, then the result is nil.
// Why it matters: comment threads can render before the MR's diff fetch lands, and the nil-in,
// nil-out contract lets them draw without snippets in that window instead of forcing every caller to
// special-case missing diffs.
func TestExtractDiffContext_NilDiffs(t *testing.T) {
	// When/Then: requesting context against nil diffs returns no snippet
	got := diffutil.ExtractContext(nil, "main.go", 0, 2, 5)
	if got != nil {
		t.Errorf("expected nil for nil diffs, got %d lines", len(got))
	}
}

// TestRenderDiffSnippet: rendering a snippet keeps each diff line's text in the output.
// Given one line each of context, addition, deletion, and hunk header, when the snippet renders with
// the MR panel styles, then the context, added, and removed line texts all appear in the output.
// Why it matters: a renderer that drops or mangles lines would show reviewers a snippet missing the
// very code a comment points at.
//
// The assertions check text presence only; which style wraps each line type (and the hunk-header
// line at all) is not verified here.
func TestRenderDiffSnippet(t *testing.T) {
	// Given: one line of each diff line type
	lines := []string{
		" context line",
		"+added line",
		"-removed line",
		"@@ -1,2 +1,3 @@",
	}

	// When: the snippet renders at width 80 with the MR panel styles
	result := diffutil.RenderSnippet(lines, 80, mrSnippetStyles())

	// Then: the context, added, and removed line texts survive in the output
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
