package glabcmd

import "testing"

// For maps the focused TUI entity to the ordered glab commands the hotkeys surface.
// Why it matters: command [0] is what the yank key copies and the whole list populates
// the preview overlay, so both the exact invocation and its order are a user-visible
// contract. lazylab browses arbitrary projects, so every command must carry its project.
func TestFor(t *testing.T) {
	// Given: a representative selection for each focusable entity, plus the unsupported cases
	tests := []struct {
		name string
		sel  Selection
		want []string // expected Command.Cmd values, in surfaced order
	}{
		{
			name: "focused project yields repo view",
			sel:  Selection{Kind: KindProject, ProjectPath: "acme/widgets"},
			want: []string{"glab repo view acme/widgets"},
		},
		{
			name: "focused pipeline yields branch view, get, list, cancel",
			sel:  Selection{Kind: KindPipeline, ProjectPath: "acme/widgets", Ref: "main", PipelineID: 4242},
			want: []string{
				"glab ci view main -R acme/widgets",
				"glab ci get -p 4242 -R acme/widgets",
				"glab ci list -R acme/widgets",
				"glab ci cancel pipeline 4242 -R acme/widgets",
			},
		},
		{
			name: "focused job yields trace, retry, cancel",
			sel:  Selection{Kind: KindJob, ProjectPath: "acme/widgets", JobID: 99821},
			want: []string{
				"glab ci trace 99821 -R acme/widgets",
				"glab ci retry 99821 -R acme/widgets",
				"glab ci cancel job 99821 -R acme/widgets",
			},
		},
		{
			name: "focused merge request yields view and diff",
			sel:  Selection{Kind: KindMergeRequest, ProjectPath: "acme/widgets", MRIID: 42},
			want: []string{
				"glab mr view 42 -R acme/widgets",
				"glab mr diff 42 -R acme/widgets",
			},
		},
		{
			name: "subgroup project path is preserved verbatim",
			sel:  Selection{Kind: KindMergeRequest, ProjectPath: "acme/platform/widgets", MRIID: 7},
			want: []string{
				"glab mr view 7 -R acme/platform/widgets",
				"glab mr diff 7 -R acme/platform/widgets",
			},
		},
		{
			name: "no focus yields nothing",
			sel:  Selection{Kind: KindNone, ProjectPath: "acme/widgets"},
			want: nil,
		},
		{
			name: "missing project context yields nothing (no -R can be formed)",
			sel:  Selection{Kind: KindPipeline, Ref: "main", PipelineID: 4242},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: we ask glabcmd for the commands available for that selection
			got := For(tt.sel)

			// Then: the commands match the expected glab invocations, in order, each labelled
			if len(got) != len(tt.want) {
				t.Fatalf("For() returned %d commands, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, want := range tt.want {
				if got[i].Cmd != want {
					t.Errorf("For()[%d].Cmd = %q, want %q", i, got[i].Cmd, want)
				}
				if got[i].Label == "" {
					t.Errorf("For()[%d].Label is empty for %q; every command needs a menu label", i, got[i].Cmd)
				}
			}
		})
	}
}
