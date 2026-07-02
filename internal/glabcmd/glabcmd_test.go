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
			name: "focused pipeline yields ID-precise get first, then branch view, list, cancel",
			sel:  Selection{Kind: KindPipeline, ProjectPath: "acme/widgets", Ref: "main", PipelineID: 4242},
			want: []string{
				"glab ci get -p 4242 -R acme/widgets",
				"glab ci view main -R acme/widgets",
				"glab ci list -R acme/widgets",
				"glab ci cancel pipeline 4242 -R acme/widgets",
			},
		},
		{
			name: "detached merge request ref omits the branch view",
			sel:  Selection{Kind: KindPipeline, ProjectPath: "acme/widgets", Ref: "refs/merge-requests/57/head", PipelineID: 4242},
			want: []string{
				"glab ci get -p 4242 -R acme/widgets",
				"glab ci list -R acme/widgets",
				"glab ci cancel pipeline 4242 -R acme/widgets",
			},
		},
		{
			name: "empty ref omits the branch view",
			sel:  Selection{Kind: KindPipeline, ProjectPath: "acme/widgets", PipelineID: 4242},
			want: []string{
				"glab ci get -p 4242 -R acme/widgets",
				"glab ci list -R acme/widgets",
				"glab ci cancel pipeline 4242 -R acme/widgets",
			},
		},
		{
			name: "hostile ref is quoted so pasting cannot run a second command",
			sel:  Selection{Kind: KindPipeline, ProjectPath: "acme/widgets", Ref: "fix;rm -rf ~", PipelineID: 4242},
			want: []string{
				"glab ci get -p 4242 -R acme/widgets",
				"glab ci view 'fix;rm -rf ~' -R acme/widgets",
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
			name: "gitlab.com host keeps the bare project path",
			sel:  Selection{Kind: KindMergeRequest, Host: "https://gitlab.com", ProjectPath: "acme/widgets", MRIID: 42},
			want: []string{
				"glab mr view 42 -R acme/widgets",
				"glab mr diff 42 -R acme/widgets",
			},
		},
		{
			name: "self-hosted instance is spelled as a full repo URL",
			sel:  Selection{Kind: KindPipeline, Host: "https://gitlab.mycompany.com", ProjectPath: "acme/widgets", Ref: "main", PipelineID: 4242},
			want: []string{
				"glab ci get -p 4242 -R https://gitlab.mycompany.com/acme/widgets",
				"glab ci view main -R https://gitlab.mycompany.com/acme/widgets",
				"glab ci list -R https://gitlab.mycompany.com/acme/widgets",
				"glab ci cancel pipeline 4242 -R https://gitlab.mycompany.com/acme/widgets",
			},
		},
		{
			name: "trailing slash on the host does not double up",
			sel:  Selection{Kind: KindProject, Host: "https://gitlab.mycompany.com/", ProjectPath: "acme/widgets"},
			want: []string{"glab repo view https://gitlab.mycompany.com/acme/widgets"},
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

// shellQuote keeps ordinary values readable and neutralizes shell metacharacters.
// Why it matters: yanked commands get pasted into a shell verbatim, and git ref
// names may legally contain ; $ & ( ) # and friends, so an unquoted ref could run
// or silently truncate a second command on the user's machine.
func TestShellQuote(t *testing.T) {
	// Given: safe values that must pass through and hostile ones that must be quoted
	tests := []struct {
		in   string
		want string
	}{
		{"main", "main"},
		{"acme/platform/widgets", "acme/platform/widgets"},
		{"release-1.2.3", "release-1.2.3"},
		{"https://gitlab.mycompany.com/acme/widgets", "https://gitlab.mycompany.com/acme/widgets"},
		{"fix;rm -rf ~", "'fix;rm -rf ~'"},
		{"release$(date)", "'release$(date)'"},
		{"feat(login)", "'feat(login)'"},
		{"wip&test", "'wip&test'"},
		{"topic#7", "'topic#7'"},
		{"back`tick`", "'back`tick`'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			// When/Then: quoting yields exactly the expected shell-safe form
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
