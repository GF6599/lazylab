package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
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
		// The animation never settles by design, so it is not work to drain.
		if _, ok := msg.(spinner.TickMsg); ok {
			return m
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
	requireContains(t, view, panelLabel(PanelProjects), "No favorites yet (press f)")
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

	// Then: focus lands on the panel advertised as "2 pipelines", which lists
	// the selected project's newest demo pipeline
	if m.focus.Active != PanelPipelines {
		t.Fatalf("focus = %d, want PanelPipelines", m.focus.Active)
	}
	requireContains(t, m.View(), panelLabel(PanelPipelines), "#1001003", "feature/add-metrics")

	// When: 4 jumps to the MRs panel
	m, _ = press(m, keyMsg("4"))

	// Then: focus lands on "4 merge requests" and only the open demo MRs are
	// listed (the merged and closed ones stay out of the Open tab)
	if m.focus.Active != PanelMRs {
		t.Fatalf("focus = %d, want PanelMRs", m.focus.Active)
	}
	view = m.View()
	requireContains(t, view, panelLabel(PanelMRs), "!100101", "!100102", "!100105", "add health check")
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
	requireContains(
		t, view,
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
	requireContains(
		t, view,
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
	requireContains(t, view, panelLabel(PanelProjects), panelLabel(PanelMRs))
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
	requireContains(
		t, m.View(),
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
	requireContains(t, m.View(), "new comment", "Type your comment...", "Ctrl+S to send", "Esc to cancel")

	// When: esc cancels the modal
	m, cancelCmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: no command fires, the overlay is gone, and no posting toast appears
	if cancelCmd != nil {
		t.Fatal("esc should return no command from the reply modal")
	}
	if m.mrView.reply.active {
		t.Fatal("expected the reply modal to close on esc")
	}
	requireNotContains(t, m.View(), "new comment")
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
	requireContains(
		t, m.View(),
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
	requireContains(t, view, panelLabel(PanelProjects))
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

// TestApp_HostileGitLabContentCannotDriveTheTerminal: content chosen by a remote party
// renders as text, never as terminal instructions.
// Given a project named with a clipboard-write sequence and a status line carrying a
// window-title forgery, when the app renders a frame, then the frame carries the visible
// text and no escape sequence other than colour.
// Why it matters: any person who can open a merge request chooses these bytes, and OSC 52
// would let them overwrite the clipboard the operator pastes glab commands from.
func TestApp_HostileGitLabContentCannotDriveTheTerminal(t *testing.T) {
	// Given: a running app whose project path carries a clipboard write and whose status
	// line carries a window-title forgery
	m := demoAppModel(t, projectTabAll)
	m.allProjects[0].PathWithNamespace = "\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\aacme/api"
	m.invalidateVisibleCache()
	m.updateProjectList()
	m.status = "\x1b]0;HIJACKED\aloaded 30 projects"

	// When: the app renders a frame
	frame := m.View()

	// Then: no sequence that acts on the terminal survives
	for _, banned := range []string{"\x1b]52", "\x1b]0;", "\a"} {
		if strings.Contains(frame, banned) {
			t.Errorf("frame carries %q, which the terminal would execute", banned)
		}
	}

	// And: the readable text of both survives, so the filter strips rather than blanks
	for _, want := range []string{"acme/api", "loaded 30 projects"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame dropped %q along with the escape sequence", want)
		}
	}
}

// TestApp_FilterLeavesABenignFrameUnchanged: filtering costs nothing on ordinary content.
// Given the app rendering demo data that carries no hostile sequence, when the filtered
// and unfiltered frames are compared, then they are byte-identical.
// Why it matters: the filter runs on every keystroke, so if it altered lipgloss colour,
// borders, or padding it would corrupt the whole interface rather than protect it.
func TestApp_FilterLeavesABenignFrameUnchanged(t *testing.T) {
	// Given: the app rendering ordinary demo content across every panel
	m := demoAppModel(t, projectTabAll)

	// When: the filtered and unfiltered frames are produced
	filtered := m.View()
	unfiltered := m.render()

	// Then: the filter changed nothing
	if filtered != unfiltered {
		t.Errorf("filter altered a benign frame\n before %q\n after  %q", unfiltered, filtered)
	}
	// And: the frame is a real one, not an empty string that would pass trivially
	if !strings.Contains(unfiltered, "acme-corp") {
		t.Fatal("frame did not render demo content, so the comparison proves nothing")
	}
}

// TestApp_HostileJobLogCannotDriveTheTerminal: a CI job's own output cannot act on the
// terminal that displays it.
// Given a pipeline job whose trace printed a clipboard-write sequence, when the stages
// panel renders the log, then the sequence is absent from the frame while the surrounding
// log text still reads normally.
// Why it matters: a job trace is the widest opening in the app, because anyone who can open
// a merge request can make a runner print any bytes they choose into it.
func TestApp_HostileJobLogCannotDriveTheTerminal(t *testing.T) {
	// Given: a stages panel showing a job trace that carries a clipboard write
	m := demoPipelinesPanelModel(t)
	m.focus.Active = PanelStages
	log := "Running with gitlab-runner 16.0\n" +
		"\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\a$ go build ./...\n" +
		"Job succeeded\n"
	m.pipelineView.logPreview.content = log
	m.setLogViewportContent(log)

	// When: the app renders a frame
	frame := m.View()

	// Then: the log is genuinely on screen, so the assertions below are not vacuous
	if !strings.Contains(frame, "gitlab-runner") {
		t.Fatal("log pane did not render, so this test proves nothing")
	}
	// And: the clipboard write did not survive into the frame
	if strings.Contains(frame, "\x1b]52") || strings.Contains(frame, "\a") {
		t.Error("frame carries the clipboard write from the job trace")
	}
	// And: the readable trace survives
	if !strings.Contains(frame, "go build ./...") {
		t.Error("frame dropped the log text along with the escape sequence")
	}
}

// findMsg runs cmd and returns the first message of type T it carries, unpacking a batch
// the way the runtime does. Update batches the animation tick alongside real work, so a
// command carrying one interesting message still arrives as a batch.
func findMsg[T tea.Msg](cmd tea.Cmd) (T, bool) {
	var zero T
	if cmd == nil {
		return zero, false
	}
	raw := cmd()
	if typed, ok := raw.(T); ok {
		return typed, true
	}
	if batch, ok := raw.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if found, ok := findMsg[T](sub); ok {
				return found, true
			}
		}
	}
	return zero, false
}

// triggerSpyService wraps the demo service to record what a play or a pipeline
// trigger actually sent, so a scenario asserts on the request that left the UI
// rather than on the form state behind it.
type triggerSpyService struct {
	*demo.DemoService
	playedJobID  int
	playedVars   []gitlab.PipelineVariable
	createdRef   string
	createdVars  []gitlab.PipelineVariable
	createCalled bool
}

func (s *triggerSpyService) PlayJob(ctx context.Context, projectID, jobID int, vars []gitlab.PipelineVariable) (gitlab.PipelineJob, error) {
	s.playedJobID = jobID
	s.playedVars = vars
	return s.DemoService.PlayJob(ctx, projectID, jobID, vars)
}

func (s *triggerSpyService) CreatePipeline(ctx context.Context, projectID int, ref string, vars []gitlab.PipelineVariable) (gitlab.PipelineSummary, error) {
	s.createCalled = true
	s.createdRef = ref
	s.createdVars = vars
	return s.DemoService.CreatePipeline(ctx, projectID, ref, vars)
}

// selectPipeline drives j until the named pipeline is selected, so a scenario
// names the run it wants instead of a key count that a change to the demo
// fixture would silently invalidate.
func selectPipeline(t *testing.T, m Model, pipelineID int) Model {
	t.Helper()
	for range len(m.pipelineView.pipelines) + 1 {
		if p := m.selectedPipeline(); p != nil && p.ID == pipelineID {
			return m
		}
		next, cmd := press(m, keyMsg("j"))
		m = drainCmd(t, next, cmd)
	}
	t.Fatalf("pipeline #%d never became selected", pipelineID)
	return m
}

func selectJobRow(t *testing.T, m Model, jobID int) Model {
	t.Helper()
	for range len(m.pipelineView.jobRows) + 1 {
		if job := m.selectedPipelineJob(); job != nil && job.ID == jobID {
			return m
		}
		next, cmd := press(m, keyMsg("j"))
		m = drainCmd(t, next, cmd)
	}
	t.Fatalf("job #%d never became selected", jobID)
	return m
}

// stagesPanelOverManualJob focuses the stages panel on the manual deploy job,
// the one job in the demo fixture a play applies to.
func stagesPanelOverManualJob(t *testing.T, svc gitlab.Service) Model {
	t.Helper()
	m := pipelinesPanelModelOver(t, svc)
	m = selectPipeline(t, m, demoManualPipelineID)
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = drainCmd(t, m, cmd)
	if m.focus.Active != PanelStages {
		t.Fatalf("focus = %d, want PanelStages", m.focus.Active)
	}
	return selectJobRow(t, m, demoManualJobID)
}

const (
	demoManualPipelineID = 1001006
	demoManualJobID      = 100100604
)

// TestPlayJobModal_CancelAndSubmitWithVariables: P opens the variables form, esc cancels it, and a
// filled form plays the job carrying what was typed.
// Given the stages panel over the demo project's manual deploy job, when P opens the modal, esc
// cancels it, and a reopened form is filled with a key and a value and submitted with enter, then
// the cancel sends no play and the submitted play carries the pair to the service.
// Why it matters: a manual job whose script requires a variable stays queued when the play omits
// it, and the status line reports a successful trigger either way, so a dropped pair reads to the
// user as a job that started and then quietly did nothing.
func TestPlayJobModal_CancelAndSubmitWithVariables(t *testing.T) {
	// Given: the stages panel over the manual deploy job
	svc := &triggerSpyService{DemoService: &demo.DemoService{}}
	m := stagesPanelOverManualJob(t, svc)

	// When: P opens the play-job modal
	m, _ = press(m, keyMsg("P"))

	// Then: the modal names the job and offers the variables editor
	requireContains(t, m.View(), "Play job: deploy", "Variables", "enter run", "esc cancel")

	// When: esc cancels it
	m, cancelCmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: nothing reached the service and the overlay is gone
	if cancelCmd != nil {
		t.Fatal("esc should return no command from the play-job modal")
	}
	if svc.playedJobID != 0 {
		t.Fatalf("play reached the service after cancel, job #%d", svc.playedJobID)
	}
	requireNotContains(t, m.View(), "Play job: deploy")

	// When: the modal is reopened and a variable is typed into it
	m, _ = press(m, keyMsg("P"))
	m = typeString(m, "DEPLOY_ENV")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	m = typeString(m, "staging")

	// And: the typed pair renders in the form
	requireContains(t, m.View(), "DEPLOY_ENV", "staging")

	// When: enter submits and the command runs against the service
	m, submitCmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if submitCmd == nil {
		t.Fatal("expected a play command from enter")
	}
	m = drainCmd(t, m, submitCmd)

	// Then: the play carried the typed pair to the manual job
	if svc.playedJobID != demoManualJobID {
		t.Errorf("played job #%d, want #%d", svc.playedJobID, demoManualJobID)
	}
	want := []gitlab.PipelineVariable{{Key: "DEPLOY_ENV", Value: "staging"}}
	if !slices.Equal(svc.playedVars, want) {
		t.Errorf("played variables = %+v, want %+v", svc.playedVars, want)
	}

	// And: the modal is closed and the status names the triggered job
	if m.pipelineView.playJob.active {
		t.Error("expected the play-job modal to close after the job is played")
	}
	if want := fmt.Sprintf("Triggered job #%d", demoManualJobID); m.status != want {
		t.Errorf("status = %q, want %q", m.status, want)
	}
}

// TestPlayJobModal_EmptyFormPlaysWithNoVariables: submitting an untouched form plays the job plainly.
// Given the play-job modal freshly opened over the manual deploy job, when enter submits it
// untouched, then the play reaches the service carrying no variables.
// Why it matters: every manual play before this modal existed took that path, so an untouched form
// must stay a plain play rather than start sending an empty-keyed variable GitLab rejects.
func TestPlayJobModal_EmptyFormPlaysWithNoVariables(t *testing.T) {
	// Given: the play-job modal open over the manual deploy job
	svc := &triggerSpyService{DemoService: &demo.DemoService{}}
	m := stagesPanelOverManualJob(t, svc)
	m, _ = press(m, keyMsg("P"))

	// When: enter submits the untouched form
	m, submitCmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if submitCmd == nil {
		t.Fatal("expected a play command from enter")
	}
	drainCmd(t, m, submitCmd)

	// Then: the job was played with no variables
	if svc.playedJobID != demoManualJobID {
		t.Errorf("played job #%d, want #%d", svc.playedJobID, demoManualJobID)
	}
	if len(svc.playedVars) != 0 {
		t.Errorf("played variables = %+v, want none", svc.playedVars)
	}
}

// TestRunPipelineModal_CancelAndSubmit: N opens the run-pipeline form, esc cancels it, and a filled
// form triggers a pipeline on the ref with its variables.
// Given the pipelines panel with demo pipelines loaded, when N opens the modal, esc cancels it, and
// a reopened form has its ref replaced and a variable added before enter, then the cancel triggers
// nothing and the submit sends the edited ref and the pair.
// Why it matters: a trigger that ignores the edited ref runs the wrong branch, and one that drops
// the variables runs a pipeline whose rules take a different shape than the user asked for.
func TestRunPipelineModal_CancelAndSubmit(t *testing.T) {
	// Given: the pipelines panel with demo pipelines loaded
	svc := &triggerSpyService{DemoService: &demo.DemoService{}}
	m := pipelinesPanelModelOver(t, svc)

	// When: N opens the run-pipeline modal
	m, _ = press(m, keyMsg("N"))

	// Then: the modal offers a ref field seeded from the selection, and the variables editor
	requireContains(t, m.View(), "Run pipeline", "Branch or tag", "Variables", "enter run")

	// When: esc cancels it
	m, cancelCmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})

	// Then: nothing was triggered and the overlay is gone
	if cancelCmd != nil {
		t.Fatal("esc should return no command from the run-pipeline modal")
	}
	if svc.createCalled {
		t.Fatal("a pipeline was triggered after cancel")
	}
	requireNotContains(t, m.View(), "Branch or tag")

	// When: the modal is reopened, the ref is replaced and a variable is added
	m, _ = press(m, keyMsg("N"))
	m.pipelineView.runPipeline.ref.SetValue("")
	m = typeString(m, "release/2.0")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	m = typeString(m, "DRY_RUN")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	m = typeString(m, "true")

	// When: enter submits and the command runs against the service
	m, submitCmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if submitCmd == nil {
		t.Fatal("expected a create-pipeline command from enter")
	}
	m = drainCmd(t, m, submitCmd)

	// Then: the trigger carried the edited ref and the typed pair
	if svc.createdRef != "release/2.0" {
		t.Errorf("triggered ref = %q, want %q", svc.createdRef, "release/2.0")
	}
	want := []gitlab.PipelineVariable{{Key: "DRY_RUN", Value: "true"}}
	if !slices.Equal(svc.createdVars, want) {
		t.Errorf("triggered variables = %+v, want %+v", svc.createdVars, want)
	}

	// And: the modal is closed and the status names the triggered run
	if m.pipelineView.runPipeline.active {
		t.Error("expected the run-pipeline modal to close after the pipeline is triggered")
	}
	if !strings.Contains(m.status, "release/2.0") {
		t.Errorf("status = %q, want it to name the ref", m.status)
	}
}

// TestTriggerModals_TildeTypesIntoTheFormRatherThanCyclingTheTheme: a tilde typed in a trigger
// modal is text, not the theme hotkey.
// Given the play-job modal and the run-pipeline modal each focused on a field, when a value
// containing a tilde is typed, then the field holds the tilde and the form renders it.
// Why it matters: ~ cycles the theme globally, and the guard that stands it down while a modal
// owns the keystrokes is a hand-kept list of modal names. A modal missing from that list compiles,
// passes every other test, and silently swallows a character out of a CI variable, so the
// pipeline runs with a value the user never typed.
func TestTriggerModals_TildeTypesIntoTheFormRatherThanCyclingTheTheme(t *testing.T) {
	// A cycled theme is package-global, so restore it whatever this test proves.
	saved := currentTheme
	t.Cleanup(func() { applyTheme(saved) })

	// Given: the play-job modal open on the manual deploy job, focused on a variable value
	play := stagesPanelOverManualJob(t, &triggerSpyService{DemoService: &demo.DemoService{}})
	play, _ = press(play, keyMsg("P"))
	play = typeString(play, "HOME_DIR")
	play, _ = press(play, tea.KeyMsg{Type: tea.KeyTab})

	// When: a value containing a tilde is typed
	play = typeString(play, "~/build")

	// Then: the variable holds the tilde verbatim
	requireContains(t, play.View(), "~/build")

	// Given: the run-pipeline modal open, focused on the ref field
	run := pipelinesPanelModelOver(t, &triggerSpyService{DemoService: &demo.DemoService{}})
	run, _ = press(run, keyMsg("N"))
	run.pipelineView.runPipeline.ref.SetValue("")

	// When: a ref containing a tilde is typed
	run = typeString(run, "wip~1")

	// Then: the ref holds the tilde verbatim
	requireContains(t, run.View(), "wip~1")
}
