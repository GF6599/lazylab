package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/demo"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// demoAppModel builds a NewModel over the real demo service, seeds page 1 of
// demo projects onto the given projects tab (mirroring demoPipelinesPanelModel),
// and applies a 180x50 window size through Update so every pane gets real
// dimensions before the scenario starts driving keys.
func demoAppModel(t *testing.T, tab projectTab) Model {
	t.Helper()
	svc := &demo.DemoService{}
	m := NewModel(context.Background(), svc, Options{})

	page, err := svc.ListProjects(context.Background(), gitlab.ProjectListOptions{Page: 1, PerPage: m.opts.ProjectsPerPage})
	if err != nil {
		t.Fatalf("demo ListProjects: %v", err)
	}
	m.loading = false
	m.projectTab = tab
	m.allProjects = page.Projects
	m.pagesReady = map[int]bool{1: true}
	m.invalidateVisibleCache()
	m.updateProjectList()

	res, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	return res.(Model)
}

// press routes one message through Update and hands back the typed model
// together with whatever command the update produced.
func press(m Model, msg tea.Msg) (Model, tea.Cmd) {
	res, cmd := m.Update(msg)
	return res.(Model), cmd
}

// typeString feeds s through Update one rune at a time, discarding the per-key
// commands (cursor blinks and debounce timers); scenarios drive the timer
// messages explicitly where the outcome depends on them.
func typeString(m Model, s string) Model {
	for _, r := range s {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// drainCmd executes cmd against the demo service, feeding every produced
// message back through Update and following tea.BatchMsg fan-out until the
// command chain settles. The step cap fails fast on a chain that never
// settles, and reaching the auto-refresh tick is reported explicitly because
// scenario chains are expected to finish without live-refresh scheduling.
func drainCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	steps := 0
	var run func(Model, tea.Cmd) Model
	run = func(m Model, c tea.Cmd) Model {
		if c == nil {
			return m
		}
		steps++
		if steps > 60 {
			t.Fatal("drainCmd: command chain did not settle after 60 steps")
		}
		msg := c()
		if _, ok := msg.(pipelineTickMsg); ok {
			t.Fatal("drainCmd: chain reached the auto-refresh tick")
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				m = run(m, sub)
			}
			return m
		}
		res, next := m.Update(msg)
		return run(res.(Model), next)
	}
	return run(m, cmd)
}

// requireContains fails the test when the rendered view is missing any of the
// expected substrings.
func requireContains(t *testing.T, view string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q in:\n%s", want, view)
		}
	}
}

// requireNotContains fails the test when the rendered view still shows any of
// the given substrings.
func requireNotContains(t *testing.T, view string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(view, s) {
			t.Fatalf("view unexpectedly contains %q in:\n%s", s, view)
		}
	}
}

// TestAppWalk_TabsSearchPanelJumpsAndMRDetail: one continuous session walk
// through tab switching, project search, numbered panel jumps, and the MR
// detail tabs, asserting the rendered content at every hop.
// Given a demo-backed model launched on the empty Favorites tab, when the user
// switches to All, filters for "gateway", clears the search, jumps to the
// pipelines and MRs panels, and opens the MR detail with its Comments tab,
// then each step renders the advertised demo content.
// Why it matters: a broken hop anywhere in this chain (stale tab content, a
// filter that does not narrow, a number key landing on the wrong panel, or a
// detail tab still showing the previous tab) routes users to data they never
// selected.
func TestAppWalk_TabsSearchPanelJumpsAndMRDetail(t *testing.T) {
	// Given: a freshly launched, resized demo model on the Favorites tab
	m := demoAppModel(t, projectTabFavorites)

	// Then: the Favorites tab advertises its empty state and lists no projects
	view := m.View()
	requireContains(t, view, "1 Projects", "No favorites yet (press f)")
	requireNotContains(t, view, "acme-corp/api-gateway")

	// When: t switches the projects tab and the returned auto-load commands
	// run against the demo service
	m, cmd := press(m, keyMsg("t"))
	m = drainCmd(t, m, cmd)

	// Then: the All tab lists the demo projects
	if m.projectTab != projectTabAll {
		t.Fatalf("projectTab = %d, want projectTabAll", m.projectTab)
	}
	requireContains(t, m.View(), "acme-corp/api-gateway", "acme-corp/auth-service")

	// When: / opens the search and "gateway" is typed
	m, _ = press(m, keyMsg("/"))
	if !m.search.active {
		t.Fatal("expected the project search to be active after /")
	}
	m = typeString(m, "gateway")

	// And: the pending query is applied by feeding the debounce message the
	// 150ms timer command would deliver, built from the model's own timer state
	if m.search.debounceTimer == nil {
		t.Fatal("expected a search debounce timer after typing")
	}
	m, _ = press(m, searchDebounceTickMsg{query: "gateway", timestamp: *m.search.debounceTimer})

	// Then: the list narrows to the one matching project
	view = m.View()
	requireContains(t, view, "acme-corp/api-gateway")
	requireNotContains(t, view, "acme-corp/auth-service")

	// When: esc clears the search
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: the full list is restored and the status confirms the reset
	requireContains(t, m.View(), "acme-corp/auth-service")
	if m.status != "Search cleared" {
		t.Fatalf("status = %q, want %q", m.status, "Search cleared")
	}

	// When: 2 jumps to the pipelines panel
	m, _ = press(m, keyMsg("2"))

	// Then: focus lands on the panel advertised as "2 Pipelines", which lists
	// the selected project's newest demo pipeline
	if m.focus.Active != PanelPipelines {
		t.Fatalf("focus = %d, want PanelPipelines", m.focus.Active)
	}
	requireContains(t, m.View(), "2 Pipelines", "#1001003", "feature/add-metrics")

	// When: 4 jumps to the MRs panel
	m, _ = press(m, keyMsg("4"))

	// Then: focus lands on "4 Merge Requests" and only the open demo MRs are
	// listed (the merged and closed ones stay out of the Open tab)
	if m.focus.Active != PanelMRs {
		t.Fatalf("focus = %d, want PanelMRs", m.focus.Active)
	}
	view = m.View()
	requireContains(t, view, "4 Merge Requests", "!100101", "!100102", "!100105", "add health check")
	requireNotContains(t, view, "!100103", "!100104")

	// When: enter opens the detail pane for the selected MR
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})

	// Then: the Info tab shows the MR's metadata
	if m.focus.Active != PanelDetail {
		t.Fatalf("focus = %d, want PanelDetail", m.focus.Active)
	}
	requireContains(t, m.View(), "readiness probe", "Author", "Alice Chen", "feature/health-check")

	// When: t cycles the detail to the Comments tab and the returned
	// discussion and diff fetches run against the demo service
	m, cmd = press(m, keyMsg("t"))
	m = drainCmd(t, m, cmd)

	// Then: the Comments tab replaces the Info fields with the demo thread
	if m.mrView.detailTab != mrDetailTabComments {
		t.Fatalf("detailTab = %d, want mrDetailTabComments", m.mrView.detailTab)
	}
	view = m.View()
	requireContains(t, view,
		"Bob Smith", "[resolved]", "internal/handler/handler.go:14",
		"Should we also add a", "Good idea",
	)
	requireNotContains(t, view, "feature/health-check")
}

// TestExplorerFlow_BrowsesTreeDescendsAndCloses: the explorer overlay opens on
// the selected project, descends into a directory, and closes cleanly.
// Given a demo-backed model with the projects panel focused, when e opens the
// explorer, enter descends into cmd/, and esc closes the overlay, then the
// root listing, the descended listing with its preview, and the restored
// multi-panel layout each render in turn.
// Why it matters: a stale navigation stack or preview shows users file content
// from a directory they already left, and a broken close traps them in the
// overlay.
func TestExplorerFlow_BrowsesTreeDescendsAndCloses(t *testing.T) {
	// Given: a demo model on the All tab with acme-corp/api-gateway selected
	m := demoAppModel(t, projectTabAll)

	// When: e opens the explorer and the returned tree fetches run against
	// the demo service (root listing, then the preview of the selection)
	m, cmd := press(m, keyMsg("e"))
	if cmd == nil {
		t.Fatal("expected a tree fetch command from e")
	}
	m = drainCmd(t, m, cmd)

	// Then: the overlay lists the demo root entries and previews the selected
	// cmd/ directory's children
	view := m.View()
	requireContains(t, view,
		"Explorer", "acme-corp/api-gateway @ main", "Path: /",
		"cmd/", "internal/", "README.md", "go.mod",
		"Preview", "server/",
	)

	// When: enter descends into cmd/ and the returned tree fetches run
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a tree fetch command from enter")
	}
	m = drainCmd(t, m, cmd)

	// Then: the listing shows cmd's children and the preview follows the
	// selection into cmd/server
	requireContains(t, m.View(), "Path: cmd", "server/", "main.go")

	// When: esc closes the overlay (the returned command restarts the 5s
	// refresh tick, so it is deliberately not executed)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: the explorer state is cleared and the overlay is gone from the
	// re-render, with the multi-panel layout back in place
	if m.explorer.project.ID != 0 {
		t.Fatal("expected explorer state to be cleared after esc")
	}
	view = m.View()
	requireNotContains(t, view, "Explorer", "Path: cmd")
	requireContains(t, view, "1 Projects", "4 Merge Requests")
	if m.status != "Back to projects" {
		t.Fatalf("status = %q, want %q", m.status, "Back to projects")
	}
}

// TestPipelineDrill_JobRowsAndLogPreview: selecting a pipeline loads its job
// rows and the log preview follows the job selection into the stages panel.
// Given the demo-populated pipelines panel, when j selects the next pipeline,
// enter focuses the stages panel, and j moves the job selection, then the job
// rows render with their statuses and the detail pane shows each selected
// job's demo trace in turn.
// Why it matters: a log preview pinned to the previous job makes users debug
// the wrong trace while believing they are reading the selected one.
func TestPipelineDrill_JobRowsAndLogPreview(t *testing.T) {
	// Given: the pipelines panel focused and populated from the demo service
	m := demoPipelinesPanelModel(t)

	// When: j selects the next pipeline and the returned stage/job fetches
	// run against the demo service
	m, cmd := press(m, keyMsg("j"))
	if cmd == nil {
		t.Fatal("expected stage/job fetch commands from j")
	}
	m = drainCmd(t, m, cmd)

	// Then: the stages panel renders pipeline #1001001's job rows with their
	// statuses, counting all five in the footer
	requireContains(t, m.View(), "#1001001", "build", "test:integration", "SUCCESS", "1 of 5")

	// And: the row model holds every demo job in stage order
	var gotJobs []string
	for _, job := range m.pipelineView.jobRows {
		gotJobs = append(gotJobs, job.Name)
	}
	if want := "build,test,test:integration,lint,deploy"; strings.Join(gotJobs, ",") != want {
		t.Fatalf("job rows = %q, want %q", strings.Join(gotJobs, ","), want)
	}

	// When: enter moves focus to the stages panel
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = drainCmd(t, m, cmd)

	// Then: focus is on stages and the detail pane previews the first job's
	// demo trace
	if m.focus.Active != PanelStages {
		t.Fatalf("focus = %d, want PanelStages", m.focus.Active)
	}
	requireContains(t, m.View(), "Job: build (#100100101)", "Running with gitlab-runner")

	// When: j moves the job selection and the returned trace fetch runs
	m, cmd = press(m, keyMsg("j"))
	if cmd == nil {
		t.Fatal("expected a log fetch command from j")
	}
	m = drainCmd(t, m, cmd)

	// And: the selection sits on the second job row
	if m.pipelineView.stageSelected != 1 {
		t.Fatalf("stageSelected = %d, want 1", m.pipelineView.stageSelected)
	}

	// Then: the log preview follows the newly selected job and renders its
	// own demo trace, naming the exact job ID
	requireContains(t, m.View(), "Job: test (#100100102)", "Job succeeded (job ID: 100100102)")
}

// mrsPanelModel builds a demo model with the MRs panel focused and the demo
// merge requests loaded through the panel's own focus-change fetch.
func mrsPanelModel(t *testing.T) Model {
	t.Helper()
	m := demoAppModel(t, projectTabAll)
	m, cmd := press(m, keyMsg("4"))
	if cmd == nil {
		t.Fatal("expected an MR fetch command from the panel jump")
	}
	m = drainCmd(t, m, cmd)
	if m.focus.Active != PanelMRs {
		t.Fatalf("focus = %d, want PanelMRs", m.focus.Active)
	}
	if len(m.mrView.mrs) == 0 {
		t.Fatal("no merge requests loaded from the demo service")
	}
	return m
}

// TestCreateMRModal_CancelAndSubmit: N opens the create-MR form, esc cancels
// it without side effects, and a filled form submits against the service.
// Given the MRs panel with demo data loaded, when N opens the modal, esc
// cancels it, and a reopened form is filled and submitted with ctrl+s, then
// the form renders its fields and hints, the cancel leaves no trace, and the
// submit toasts the created MR and closes the modal.
// Why it matters: a cancel that still submits, or a submit that drops the
// typed fields, creates merge requests the user never asked for.
func TestCreateMRModal_CancelAndSubmit(t *testing.T) {
	// Given: the MRs panel focused with demo MRs loaded
	m := mrsPanelModel(t)

	// When: N opens the create-MR modal
	m, _ = press(m, keyMsg("N"))

	// Then: the modal renders its title, field labels, and hint line
	requireContains(t, m.View(),
		"Create Merge Request", "Title", "Source Branch", "Target Branch",
		"Description", "Ctrl+S create", "Esc cancel",
	)

	// When: esc cancels the modal
	m, cancelCmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: no command fires (nothing reaches the service), the overlay is
	// gone from the re-render, and no submit toast appears
	if cancelCmd != nil {
		t.Fatal("esc should return no command from the create-MR modal")
	}
	if m.mrView.createMR.active {
		t.Fatal("expected the create-MR modal to close on esc")
	}
	requireNotContains(t, m.View(), "Create Merge Request")
	if strings.Contains(m.status, "Creating merge request") {
		t.Fatalf("status = %q, want no submit toast after cancel", m.status)
	}

	// When: the modal is reopened and the form is filled through Update
	m, _ = press(m, keyMsg("N"))
	m = typeString(m, "Add readiness endpoint")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	m = typeString(m, "feature/acceptance-probe")

	// And: the typed values render in the form fields
	requireContains(t, m.View(), "Add readiness endpoint", "feature/acceptance-probe")

	// When: ctrl+s submits and the returned command runs against the demo
	// service, feeding its result back
	m, submitCmd := press(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if submitCmd == nil {
		t.Fatal("expected a create-MR command from ctrl+s")
	}
	if m.status != "Creating merge request..." {
		t.Fatalf("status = %q, want %q", m.status, "Creating merge request...")
	}
	m = drainCmd(t, m, submitCmd)

	// Then: the success toast names the created MR and the modal is closed
	if want := "Created !999: Add readiness endpoint"; m.status != want {
		t.Fatalf("status = %q, want %q", m.status, want)
	}
	if m.mrView.createMR.active {
		t.Fatal("expected the create-MR modal to close after the MR is created")
	}
	requireNotContains(t, m.View(), "Create Merge Request")
}

// TestMRReplyModal_CancelAndSubmit: c opens the new-comment modal, esc cancels
// it without side effects, and a typed comment posts against the service.
// Given the MRs panel with demo data loaded, when c opens the modal, esc
// cancels it, and a reopened modal is filled and sent with ctrl+s, then the
// modal renders its title and hints, the cancel leaves no trace, and the send
// toasts "Comment posted" and closes the modal.
// Why it matters: a comment that posts after cancel, or silently never posts,
// leaves review threads saying something the author did not send.
func TestMRReplyModal_CancelAndSubmit(t *testing.T) {
	// Given: the MRs panel focused with demo MRs loaded
	m := mrsPanelModel(t)

	// When: c opens the new-comment modal for the selected MR
	m, _ = press(m, keyMsg("c"))

	// Then: the modal renders its title, input placeholder, and hint line
	requireContains(t, m.View(), "New Comment", "Type your comment...", "Ctrl+S to send", "Esc to cancel")

	// When: esc cancels the modal
	m, cancelCmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: no command fires, the overlay is gone, and no posting toast appears
	if cancelCmd != nil {
		t.Fatal("esc should return no command from the reply modal")
	}
	if m.mrView.reply.active {
		t.Fatal("expected the reply modal to close on esc")
	}
	requireNotContains(t, m.View(), "New Comment")
	if strings.Contains(m.status, "Posting comment") {
		t.Fatalf("status = %q, want no posting toast after cancel", m.status)
	}

	// When: the modal is reopened and a comment body is typed through Update
	m, _ = press(m, keyMsg("c"))
	m = typeString(m, "Thanks, the readiness probe reads well.")

	// And: the typed body renders in the textarea
	requireContains(t, m.View(), "Thanks, the readiness probe reads well.")

	// When: ctrl+s sends the comment and the returned command runs against
	// the demo service, feeding its result back
	m, submitCmd := press(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if submitCmd == nil {
		t.Fatal("expected a post-comment command from ctrl+s")
	}
	if m.status != "Posting comment..." {
		t.Fatalf("status = %q, want %q", m.status, "Posting comment...")
	}
	m = drainCmd(t, m, submitCmd)

	// Then: the success toast confirms the post and the modal is closed
	if m.status != "Comment posted" {
		t.Fatalf("status = %q, want %q", m.status, "Comment posted")
	}
	if m.mrView.reply.active {
		t.Fatal("expected the reply modal to close after posting")
	}
	requireNotContains(t, m.View(), "Type your comment...")
}

// TestHelpOverlay_ListsFocusedPanelBindings: ? renders the help overlay for
// the focused panel and esc dismisses it.
// Given the projects panel focused, when ? is pressed, then the overlay lists
// the panel's bindings including the glab yank and preview keys, and esc
// restores the multi-panel view.
// Why it matters: help that omits a panel's real bindings leaves features like
// the glab command emit undiscoverable.
func TestHelpOverlay_ListsFocusedPanelBindings(t *testing.T) {
	// Given: a demo model with the projects panel focused
	m := demoAppModel(t, projectTabAll)

	// When: ? opens the help overlay
	m, _ = press(m, keyMsg("?"))

	// Then: it lists the projects panel's bindings, naming the glab keys
	requireContains(t, m.View(),
		"Help - Press ? or Esc to close",
		"yank glab cmd", "glab cmd menu",
		"toggle favorite", "file explorer",
	)

	// When: esc closes the overlay
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: the overlay is gone and the panels are back
	if m.showHelp {
		t.Fatal("expected help to close on esc")
	}
	view := m.View()
	requireNotContains(t, view, "Help - Press ? or Esc to close")
	requireContains(t, view, "1 Projects")
}

// TestProjectSearch_SuppressesGlabYank: while the project search is typing, y
// is a search character, not the glab yank hotkey.
// Given an active project search with a captured clipboard, when y is pressed,
// then nothing is copied and the character lands in the search query, and once
// the search is cancelled the same key copies the selection's glab command.
// Why it matters: without the suppression guard, spelling a project name that
// contains y silently overwrites the user's clipboard mid-search.
func TestProjectSearch_SuppressesGlabYank(t *testing.T) {
	// Given: the projects panel with an active search and a captured clipboard
	m := demoAppModel(t, projectTabAll)
	capture := captureClipboard(t)
	m, _ = press(m, keyMsg("/"))

	// When: y is pressed while the search is typing
	m, _ = press(m, keyMsg("y"))

	// Then: nothing is written to the clipboard and the character lands in
	// the search query instead
	if capture.text != "" {
		t.Fatalf("clipboard = %q, want empty while search is active", capture.text)
	}
	if got := m.search.input.Value(); got != "y" {
		t.Fatalf("search input = %q, want %q", got, "y")
	}
	if strings.Contains(m.status, "Copied") {
		t.Fatalf("status = %q, want no copy toast while searching", m.status)
	}

	// When: esc leaves the search and y is pressed again
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, cmd := press(m, keyMsg("y"))
	if cmd == nil {
		t.Fatal("expected a clipboard command from y outside the search")
	}
	m = drainCmd(t, m, cmd)

	// Then: the selected project's default glab command is copied verbatim
	// and the status toasts it, proving the guard (not the capture) blocked
	// the in-search copy
	want := "glab repo view acme-corp/api-gateway"
	if capture.text != want {
		t.Fatalf("copied text = %q, want %q", capture.text, want)
	}
	if wantStatus := "Copied: " + want; m.status != wantStatus {
		t.Fatalf("status = %q, want %q", m.status, wantStatus)
	}
}
