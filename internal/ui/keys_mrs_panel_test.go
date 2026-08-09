package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/x/ansi"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// twoMRModel puts two merge requests on screen, each with one resolvable discussion, and selects
// the second. A resolve fired on the first can then be answered while the user looks at the other.
func twoMRModel() Model {
	m := newMultiPanelModel(PanelDetail)
	m.focus.PrevActive = PanelMRs
	m.mrView.detailTab = mrDetailTabComments
	m.mrView.project = gitlab.ProjectNode{ID: 1}
	m.mrView.mrs = []gitlab.MergeRequestSummary{
		{IID: 1, Title: "first"},
		{IID: 2, Title: "second"},
	}
	m.mrView.selected = 1
	m.mrView.mrViewport = viewport.New(80, 24)
	m.mrView.discussions = NewAsyncCache[int, []gitlab.MRDiscussion]()
	m.mrView.diffs = NewAsyncCache[int, []gitlab.MRDiffFile]()
	m.mrView.discussions.Set(1, []gitlab.MRDiscussion{{
		ID:    "d1",
		Notes: []gitlab.MRNote{{ID: 11, Author: "ana", Body: "ONLY-ON-MR-ONE", Resolvable: true}},
	}})
	m.mrView.discussions.Set(2, []gitlab.MRDiscussion{{
		ID:    "d2",
		Notes: []gitlab.MRNote{{ID: 22, Author: "bo", Body: "ONLY-ON-MR-TWO", Resolvable: true}},
	}})
	return m
}

// mrPane returns what the comments viewport currently shows.
func mrPane(m Model) string {
	return ansi.Strip(m.mrView.mrViewport.View())
}

// TestMRResolve_LeavesThePaneOnTheMergeRequestTheUserIsReading: a failed resolve reverts its own
// merge request and nothing else.
// Given a resolve fired on one merge request and the user moved to another before the answer came
// back, when the resolve fails, then the pane still shows the merge request the user is reading.
// Why it matters: the resolve is optimistic, so its answer arrives after an unbounded delay, and
// the user is free to move in the meantime. Redrawing the pane from the merge request the answer
// names replaces what the user is reading with somebody else's thread.
func TestMRResolve_LeavesThePaneOnTheMergeRequestTheUserIsReading(t *testing.T) {
	// Given: a resolve fired on merge request 1, and the user now reading merge request 2
	m := twoMRModel()
	updated, _ := m.refreshMRCommentsViewport()
	m = updated.(Model)
	if !strings.Contains(mrPane(m), "ONLY-ON-MR-TWO") {
		t.Fatalf("the fixture does not start on merge request 2:\n%s", mrPane(m))
	}

	// When: the resolve on merge request 1 fails
	answered, _ := m.handleMRDiscussionResolved(mrDiscussionResolvedMsg{
		projectID:    1,
		mrIID:        1,
		discussionID: "d1",
		resolved:     true,
		err:          errors.New("network is unreachable"),
	})

	// Then: the pane still shows the merge request the user is reading
	pane := mrPane(answered.(Model))
	if strings.Contains(pane, "ONLY-ON-MR-ONE") {
		t.Errorf("the failed resolve replaced the pane with another merge request's thread:\n%s", pane)
	}
	if !strings.Contains(pane, "ONLY-ON-MR-TWO") {
		t.Errorf("the pane no longer shows the merge request the user is reading:\n%s", pane)
	}
}

// TestMRResolve_PutsTheFlagBackWhenGitLabRefuses: the optimistic flag does not outlive its request.
// Given a discussion marked resolved on screen before GitLab answered, when the resolve fails, then
// the cached discussion reads unresolved again.
// Why it matters: the app draws the resolved mark from this cache, so a flag left set after a
// refused request tells the user a thread is closed on GitLab when it is still open.
func TestMRResolve_PutsTheFlagBackWhenGitLabRefuses(t *testing.T) {
	// Given: a discussion the app has already marked resolved on screen
	m := twoMRModel()
	m.mrView.selected = 0
	(&m).optimisticToggleResolved(1, "d1", true)
	if discussions, _ := m.mrView.discussions.Get(1); !discussions[0].Notes[0].Resolved {
		t.Fatal("the fixture did not mark the discussion resolved")
	}

	// When: the resolve fails
	answered, _ := m.handleMRDiscussionResolved(mrDiscussionResolvedMsg{
		projectID:    1,
		mrIID:        1,
		discussionID: "d1",
		resolved:     true,
		err:          errors.New("403 forbidden"),
	})

	// Then: the cached discussion reads unresolved again
	after := answered.(Model)
	discussions, ok := after.mrView.discussions.Get(1)
	if !ok {
		t.Fatal("the discussion left the cache entirely")
	}
	if discussions[0].Notes[0].Resolved {
		t.Error("a refused resolve left the discussion marked resolved")
	}
}

// TestMRResolve_WithholdsTheRequestForAThreadGitLabCannotResolve: the key is refused where GitLab
// has nothing to act on.
// Given a discussion whose notes are not resolvable, when resolve is requested, then no request is
// built and the app says why.
// Why it matters: a general comment carries no resolve state, so sending the request anyway spends
// a round trip to be told nothing happened, and the user is left with no reason on screen.
func TestMRResolve_WithholdsTheRequestForAThreadGitLabCannotResolve(t *testing.T) {
	// Given: a discussion whose notes are not resolvable
	m := twoMRModel()
	m.mrView.selected = 0
	m.mrView.discussions.Set(1, []gitlab.MRDiscussion{{
		ID:    "d1",
		Notes: []gitlab.MRNote{{ID: 11, Author: "ana", Body: "a general comment"}},
	}})

	// When: resolve is requested
	updated, cmd := m.toggleDiscussionResolved()

	// Then: no request is built and the app says why
	if cmd != nil {
		t.Error("the app asked GitLab to resolve a thread it cannot resolve")
	}
	if !strings.Contains(updated.(Model).status, "not resolvable") {
		t.Errorf("the app gave no reason: status = %q", updated.(Model).status)
	}
}
