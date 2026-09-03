package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// TestViewErrors_RedactTokens: every surface that renders raw error text scrubs credentials first.
// Given an error carrying a GitLab token on each of the five error-rendering surfaces (pipeline
// retry banner, project detail pipeline section, MR reply modal, create-MR modal, branch picker),
// when each surface renders, then the output shows [REDACTED-TOKEN] and never the raw token.
// Why it matters: a host configured as https://oauth2:glpat-...@gitlab.example.com puts the
// credential inside every SDK error URL, and an unredacted render leaves it in terminal scrollback.
func TestViewErrors_RedactTokens(t *testing.T) {
	const token = "glpat-view0secret123456"
	tokErr := errors.New("request https://oauth2:" + token + "@gitlab.example.com failed")

	newModel := func() Model {
		return NewModel(context.Background(), &mockService{}, Options{})
	}

	tests := []struct {
		name   string
		render func(t *testing.T) string
	}{
		{
			name: "pipeline retry banner",
			render: func(t *testing.T) string {
				m := newModel()
				m.pipelineView.retryErr = tokErr
				return renderPipelineListPane(m, 200, 20, true)
			},
		},
		{
			name: "project detail pipeline section",
			render: func(t *testing.T) string {
				m := newModel()
				m.pipelineStatus.Set(1, pipelineState{err: tokErr})
				return renderPipelineSection(&m, gitlab.ProjectNode{ID: 1}, 200)
			},
		},
		{
			name: "MR reply modal",
			render: func(t *testing.T) string {
				m := newModel()
				m.mrView.reply = mrReplyState{active: true, input: m.newMRTextarea("reply"), err: tokErr}
				return renderMRReplyModal(m, 200)
			},
		},
		{
			name: "create-MR modal",
			render: func(t *testing.T) string {
				m := newModel()
				m.mrView.createMR = createMRState{
					active:       true,
					title:        newModalTextinput("title"),
					sourceBranch: newModalTextinput("source"),
					targetBranch: newModalTextinput("target"),
					description:  m.newMRTextarea("description"),
					err:          tokErr,
				}
				return renderCreateMRModal(m, 200)
			},
		},
		{
			name: "branch picker",
			render: func(t *testing.T) string {
				return renderBranchPicker(branchPickerState{search: newModalTextinput("filter"), err: tokErr}, 200)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the surface renders its error state
			view := tt.render(t)

			// Then: the token is scrubbed
			if strings.Contains(view, token) {
				t.Fatalf("raw token survived into the view:\n%s", view)
			}
			if !strings.Contains(view, "[REDACTED-TOKEN]") {
				t.Fatalf("expected a redaction marker in the view:\n%s", view)
			}
		})
	}
}
