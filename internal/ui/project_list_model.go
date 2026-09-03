// Package ui implements the Bubble Tea TUI for browsing GitLab projects,
// pipelines, merge requests, and repository files.
//
// The UI is a mode-based state machine built on the Elm architecture. A single
// [Model] value owns all state and transitions between modes (projects,
// explorer, pipelines, multi-panel) via key events. All I/O is performed
// through [tea.Cmd] functions that return typed messages; the [Model.Update]
// method routes each message to the appropriate handler which produces the
// next state and optional follow-up commands.
//
// Rendering is pure: [Model.View] reads state and returns a string. No side
// effects occur during rendering, which makes the UI predictable and testable.
package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// Mode represents which top-level screen the user is viewing. Each mode has
// its own key handler, view function, and subset of Model state that it
// "owns". Transitioning between modes resets the destination mode's state
// (e.g., opening pipelines clears previous pipeline data) to avoid showing
// stale content from a prior visit.
type Mode int

const (
	modeProjects   Mode = iota // Paginated project list with search
	modeExplorer               // Three-pane file browser (parent / current / preview)
	modePipelines              // Pipeline list with stages/jobs and log preview
	modeMultiPanel             // Lazygit-style layout: projects + pipelines + detail side-by-side
)

const (
	pipelineRefreshInterval = 5 * time.Second
	pipelineDebounceDelay   = 300 * time.Millisecond
	searchDebounceDelay     = 150 * time.Millisecond
	// pipelinePerPage is the page size to use before there is a pane to measure.
	pipelinePerPage        = 25
	defaultProjectsPerPage = 30
	// maxAPIPerPage is the largest page GitLab serves. It caps a request rather than the pane, so a
	// pane taller than this still draws every row it is given.
	maxAPIPerPage        = 100
	pipelineAllRefsRef   = "__all__"
	pipelineAllRefsLabel = "all refs"

	// Cache limits to prevent unbounded memory growth
	maxLogCacheEntries         = 10        // Keep last 10 job logs
	maxLogSizeBytes            = 1_000_000 // 1MB max per log
	maxPipelineStatusCacheSize = 100       // Keep last 100 pipeline statuses
	maxPreviewHighlightEntries = 25        // Keep last 25 syntax highlights
	maxPreviewHighlightBytes   = 200_000   // 200KB max per highlight

	// UI layout constants
	stageTableDefaultHeight  = 10 // Default row count for the pipeline stage table
	projectTabCount          = 2  // Number of project tabs (Favorites, All)
	pipelineDetailTabCount   = 3  // Number of pipeline detail tabs (Log, Tests, Artifacts)
	pipelineLogHeaderReserve = 6  // Rows the log pane reserves above the viewport (title + KVs + divider)
)

// projectTab selects the active tab in the projects panel.
type projectTab int

const (
	projectTabFavorites projectTab = iota
	projectTabAll
)

var projectTabLabels = []string{"★ favorites", "all"}

// Options configures the model at creation time. Zero values are replaced with
// sensible defaults in [NewModel]:
//
//   - ProjectsPerPage defaults to 30.
//   - APITimeout defaults to 15s (used for project listing, tree, file fetches).
//   - PipelineTimeout defaults to 20s (used for stages, jobs, logs, retries).
//   - Host is the GitLab instance URL, used as the cache key for on-disk
//     project/favorites storage. An empty Host is valid (defaults apply).
//   - Logger may be nil; when set it receives structured debug/error output
//     without ever including the API token.
type Options struct {
	ProjectsPerPage int
	Logger          Logger
	Host            string
	APITimeout      time.Duration
	PipelineTimeout time.Duration
	// DiffContextLines controls how many unified-diff lines surround each
	// positioned MR comment in the Comments tab. Defaults to 10 if unset.
	// Set to 0 to disable inline diff context entirely.
	DiffContextLines int
}

// Logger abstracts structured logging so callers can inject slog.Logger (or a
// test spy) without coupling the UI package to a concrete implementation.
// In tests, a nil Logger is safe — all call sites check before logging.
type Logger interface {
	Debug(msg string, args ...any)
	Error(msg string, args ...any)
	Info(msg string, args ...any)
}

// projectItem satisfies bubbles/list.Item so projects can be rendered and
// filtered by the list widget. FilterValue returns the full path (org/repo)
// which drives the built-in fuzzy filter.
type projectItem struct {
	project gitlab.ProjectNode
}

func (i projectItem) FilterValue() string {
	return i.project.PathWithNamespace
}

// projectDelegate implements bubbles/list.ItemDelegate for custom single-line
// project rendering. It holds references (not copies) to the shared
// pipelineStatus and favorites maps so that status icons update in-place
// without rebuilding the delegate on every tick.
//
// Build one with [Model.rowDelegate], or the row draws no animation until the next frame
// arrives.
type projectDelegate struct {
	pipelineStatus *LRUCache[int, pipelineState]
	favorites      map[int]bool
	frames         statusFrames
}

func (m *Model) rowDelegate() projectDelegate {
	return projectDelegate{
		pipelineStatus: m.pipelineStatus,
		favorites:      m.favorites,
		frames:         m.statusFrames(),
	}
}

func (d projectDelegate) Height() int { return 1 }

func (d projectDelegate) Spacing() int { return 0 }

func (d projectDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d projectDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	proj, ok := item.(projectItem)
	if !ok {
		return
	}

	mk := markerFor(index, m.Index())

	// Add pipeline status icon if available
	statusIcon := ""
	if state, ok := d.pipelineStatus.Peek(proj.project.ID); ok {
		switch {
		case state.hasInfo:
			statusIcon = d.frames.icon(state.info.Status) + " "
		case state.empty:
			statusIcon = iconNoPipeline + " "
		case state.loading:
			statusIcon = d.frames.loadingIcon() + " "
		case state.err != nil:
			statusIcon = iconUnknown + " "
		}
	}

	favIcon := ""
	if d.favorites[proj.project.ID] {
		favIcon = "★ "
	}

	width := m.Width()
	line := fmt.Sprintf("%s%s%s", favIcon, statusIcon, proj.project.PathWithNamespace)
	indent := lipgloss.Width(fmt.Sprintf("%s%s", favIcon, statusIcon))
	renderListItem(w, mk, line, indent, width, index == m.Index(), false)
}

// pipelineItem wraps a GitLab pipeline for use with bubbles/list
type pipelineItem struct {
	summary gitlab.PipelineSummary
}

func (i pipelineItem) FilterValue() string {
	return fmt.Sprintf("%d %s %s", i.summary.ID, i.summary.Ref, i.summary.Status)
}

// pipelineDelegate renders pipeline items in the list. Build one with [Model.pipelineRowDelegate],
// for the reason [projectDelegate] gives.
type pipelineDelegate struct {
	frames statusFrames
}

func (m *Model) pipelineRowDelegate() pipelineDelegate {
	return pipelineDelegate{frames: m.statusFrames()}
}

func (d pipelineDelegate) Height() int { return 1 }

func (d pipelineDelegate) Spacing() int { return 0 }

func (d pipelineDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d pipelineDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pItem, ok := item.(pipelineItem)
	if !ok {
		return
	}

	mk := markerFor(index, m.Index())
	isSelected := index == m.Index()

	// The marked label takes one colour, so drop the per-status colour on the
	// current row and keep it elsewhere for at-a-glance status.
	icon := d.frames.icon(pItem.summary.Status)
	if !isSelected {
		icon = pipelineStatusStyle(pItem.summary.Status).Render(icon)
	}

	timestamp := unknownStatus
	if !pItem.summary.UpdatedAt.IsZero() {
		timestamp = pItem.summary.UpdatedAt.Local().Format(timestampFormat)
	}
	ref := pItem.summary.Ref
	if ref == "" {
		ref = "unknown-ref"
	}

	line := fmt.Sprintf("%s #%d %s %s", icon, pItem.summary.ID, timestamp, ref)
	width := m.Width()
	indent := lipgloss.Width(fmt.Sprintf("%s ", icon))
	renderListItem(w, mk, line, indent, width, isSelected, true)
}

// treeEntryItem wraps a GitLab tree entry for use with bubbles/list
type treeEntryItem struct {
	entry gitlab.TreeNode
}

func (i treeEntryItem) FilterValue() string { return i.entry.Name }

// treeEntryDelegate renders tree entry items in the list
type treeEntryDelegate struct{}

func (d treeEntryDelegate) Height() int                               { return 1 }
func (d treeEntryDelegate) Spacing() int                              { return 0 }
func (d treeEntryDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d treeEntryDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	eItem, ok := item.(treeEntryItem)
	if !ok {
		return
	}

	mk := markerFor(index, m.Index())

	icon := explorerEntryIcon(eItem.entry)
	name := eItem.entry.Name
	if eItem.entry.IsDir() {
		name += "/"
	}

	width := m.Width()
	line := fmt.Sprintf("%s %s", icon, name)
	indent := lipgloss.Width(fmt.Sprintf("%s ", icon))
	renderListItem(w, mk, line, indent, width, index == m.Index(), false)
}

// maxWrapLines is the maximum number of lines a selected list item can wrap to.
const maxWrapLines = 3

// renderListItem writes a list item to w, wrapping the selected item when it
// exceeds the available width. The bubbles list does not enforce Height() per
// delegate render call, so extra newlines push subsequent items down without
// corruption. When ansiClamp is true, unselected items use ANSI-aware clamping
// to preserve inline color sequences (e.g. pipeline status icons).
func renderListItem(w io.Writer, mk rowMarker, line string, indent, width int, isSelected, ansiClamp bool) {
	// The bracket pair replaces the row's horizontal padding rather than adding
	// to it, so the label gets the width less those two cells.
	inner := width - 2
	if inner < 1 {
		// clampLine reads a width of zero or less as "no limit" and hands back
		// the whole label, so below three cells draw the pair alone and cut it
		// to the space there is.
		fmt.Fprint(w, fitLine(mk.render(""), width))
		return
	}

	if isSelected && lipgloss.Width(line) > inner {
		wrapped := wrapSelectedItem(line, inner, indent, maxWrapLines)
		for i, wl := range wrapped {
			if i > 0 {
				fmt.Fprint(w, "\n")
			}
			// Every wrapped line is padded to the same width so the right-hand
			// pieces stack into one straight edge down the block.
			fmt.Fprint(w, mk.spanning(i, len(wrapped)).render(fitLine(wl, inner)))
		}
		return
	}
	if ansiClamp && !isSelected {
		fmt.Fprint(w, mk.render(clampLineANSI(line, inner)))
	} else {
		fmt.Fprint(w, mk.render(clampLine(line, inner)))
	}
}

// Model is the root Bubble Tea model. It is a value type following Bubble Tea
// conventions: Update returns a new Model by value, and View is a pure read.
//
// Internally it acts as a state machine whose current [Mode] determines which
// key handler, view function, and subset of state are active. The mode-specific
// state structs (explorerState, pipelineViewState, mrViewState, etc.) are only
// meaningful when Model.mode matches; transition functions (openExplorer,
// openPipelineView, etc.) reinitialize them from scratch.
//
// Shared mutable state (pipelineStatus map, favorites map) is referenced by
// both the Model and its list delegates. This is intentional: the delegate
// reads the same map instance so pipeline status icons refresh without
// reconstructing the delegate on every tick. The trade-off is that callers
// must not replace these maps after construction — only mutate them in place
// or call SetDelegate with the new map.
type Model struct {
	ctx         context.Context // Parent context for cancellation
	client      gitlab.Service
	opts        Options
	allProjects []gitlab.ProjectNode
	selected    int
	page        int
	totalPages  int
	width       int
	height      int
	loading     bool
	err         error
	status      string
	search      searchState
	pagesLoaded int
	// Zero means unknown, not empty.
	totalProjects     int
	pagesReady        map[int]bool
	backgroundLoading bool
	cache             *projectCache
	mode              Mode
	focus             FocusState // Multi-panel focus tracking
	explorer          explorerState
	pipelineStatus    *LRUCache[int, pipelineState]
	pipelineView      pipelineViewState
	mrView            mrViewState
	glabPreview       glabPreviewState // command-preview overlay (y yanks, Y opens)

	// pipelineTickAlive tracks whether a pipelineTickCmd is currently in flight.
	// The tick chain self-terminates when [handlePipelineTick] returns no work in
	// a non-refreshable mode (e.g. modeExplorer); ensurePipelineTickCmd restarts
	// it when the user transitions back to a mode that wants live refresh. The
	// flag also de-duplicates kick attempts so two concurrent tick chains can
	// never run, which would otherwise double the API refresh rate.
	pipelineTickAlive bool

	// spinnerTickAlive tracks whether a spinner tick is in flight. The chain is
	// self-terminating in the same way: a tick that arrives while nothing needs
	// animating produces no successor, so ensureSpinnerTickCmd restarts it when
	// something does. The flag also stops a second chain from starting, which would
	// otherwise spin the frames at a multiple of the intended rate.
	spinnerTickAlive bool

	// animationTick lets a second animation run at a fraction of the spinner's rate off the one
	// chain. A second spinner would need a second chain, a second flag, and a second restart
	// path for the sake of a slower glyph.
	animationTick int

	// Bubble components
	keys        keyMap
	help        help.Model
	spinner     spinner.Model
	projectList list.Model
	showHelp    bool
	favorites   map[int]bool
	favOrder    []int // User-defined ordering of favorite project IDs
	favStore    *favoritesStore
	prefStore   *preferencesStore
	projectTab  projectTab

	// visibleProjects cache — avoids recomputing the filtered/paged project
	// slice on every View call. Invalidated by search query changes, page
	// navigation, tab switches, and project list reloads. The cache key is
	// the triple (query, page, tab); a mismatch triggers recomputation.
	visibleCache        []gitlab.ProjectNode
	visibleCacheQuery   string
	visibleCachePage    int
	visibleCachePerPage int
	visibleCacheTab     projectTab

	// Selection debounce — delays all eager data loading (pipelines, commits,
	// MRs) until the user pauses navigation for pipelineDebounceDelay (300ms).
	// Without this, rapid j/k navigation spawns one full fetch batch per
	// keystroke, overwhelming the GitLab API rate limit.
	selectionPending  *gitlab.ProjectNode
	selectionDebounce *time.Time

	// batchInFlight prevents overlapping batch pipeline status fetches.
	// Shared via pointer so Bubble Tea value copies and the cmd goroutine
	// see the same flag. Must be accessed atomically.
	batchInFlight *atomic.Bool

	// detailCache memoizes the rendered detail pane for the currently selected
	// project. The pane is re-rendered only when any field of detailCacheKey
	// (project, pipeline, dimensions) changes, preventing redundant lipgloss
	// passes on every View() call.
	detailCache detailCacheState

	previewHighlightCache *LRUCache[string, previewHighlightEntry]

	// glamourRenderers pools glamour.TermRenderer instances by terminal width
	// to amortize their expensive style-compilation cost. Owned by Model
	// (rather than a package singleton) so parallel tests stay isolated and
	// theme changes can drop the cache without racing with another program
	// instance. Bubble Tea's serial Update loop makes a mutex unnecessary.
	glamourRenderers map[int]*glamour.TermRenderer

	// commitCache tracks per-project recent commits, keyed by project ID.
	// Fetched lazily when a project is selected in the multi-panel layout and
	// cached indefinitely (commits rarely change fast enough to matter during a
	// single session). Unlike pipeline status, there is no periodic refresh.
	commitCache AsyncCache[int, []gitlab.CommitSummary]

	lastLayoutKey layoutKey
}

// layoutKey holds every input the panel sizing reads. The sizing is a pure function of these, so an
// unchanged key means what is on screen is already the right size.
type layoutKey struct {
	mode              Mode
	explorerProjectID int
	width             int
	height            int
	focus             FocusState
}

// searchState implements debounced search: keystrokes update pendingQuery
// immediately, but the committed query (used by visibleProjects) only changes
// after debounceTimer expires. This prevents per-keystroke re-filtering of
// the full project list while still giving responsive feedback.
type searchState struct {
	active        bool
	query         string
	pendingQuery  string     // Query waiting for debounce
	debounceTimer *time.Time // When to apply pending query
	input         textinput.Model
}

// dirState represents a single directory level in the explorer's navigation
// stack. The explorer maintains a []dirState where stack[0] is the repository
// root (path ""); descending into a subdirectory pushes a new dirState, and
// ascending pops from the stack. Each level tracks its own selection cursor
// so that returning to a parent directory restores the previous position.
type dirState struct {
	path     string
	entries  []gitlab.TreeNode
	selected int
	loading  bool
	err      error
}

// previewState holds the content and viewport for a file or directory preview
// pane. The viewport field must be preserved across state resets — it is
// initialized once with proper dimensions, and a zero-valued viewport causes
// empty renders because Width/Height would be 0.
type previewState struct {
	path           string
	content        string
	raw            string
	loading        bool
	err            error
	highlighted    bool
	highlightWidth int
	viewport       viewport.Model
}

// explorerState models the three-pane file browser as a stack of directories.
// stack[0] is always the repository root (path ""); descending into a directory
// pushes a new dirState, and ascending pops it. The stack depth encodes the
// current path implicitly, so the breadcrumb trail is always available.
type explorerState struct {
	project     gitlab.ProjectNode
	ref         string
	stack       []dirState
	preview     previewState
	parentList  list.Model // Bubbles list for parent directory
	currentList list.Model // Bubbles list for current directory
}

// pipelineViewState holds all state for the pipeline browsing mode. Fields
// are grouped by concern:
//   - List/page: project, pipelines, selected, loading, err, page, totalPages, perPage
//   - Stages/matrix: stages, stageSelected, stageTable, jobRows, stageJobRows, matrixExpanded
//   - Logs: logs, logPreview, logViewport, logJobID, pendingLogJobID, logAutoFollow
//   - Retry: retryConfirm, retrying, retryErr
//   - Bridges/children: bridges, childJobs
//   - Test reports: testReport*
//
// All caches are reset on page/selection changes via resetCaches, except
// matrixExpanded which is preserved across refreshes so bridge expand/collapse
// state survives auto-refresh ticks.
type pipelineViewState struct {
	project         gitlab.ProjectNode
	pipelines       []gitlab.PipelineSummary
	pipelineList    list.Model // Bubbles list for pipeline display
	selected        int
	loading         bool
	err             error
	page            int
	totalPages      int
	perPage         int
	stages          AsyncCache[int, []gitlab.PipelineStage]
	stageSelected   int
	stageTable      table.Model          // Table for displaying stages
	jobRows         []gitlab.PipelineJob // Maps table cursor index → job
	stageJobRows    []stageJobRow        // Rich row model with matrix grouping
	matrixExpanded  map[string]bool      // Expand/collapse state per matrix group key
	jobs            AsyncCache[int, []gitlab.PipelineJob]
	logs            AsyncCache[int, string]
	logPreview      previewState
	logViewport     viewport.Model
	logJobID        int
	pendingLogJobID int
	logAutoFollow   bool
	focus           pipelineFocus
	retryConfirm    retryConfirmState
	retrying        bool
	retryErr        error
	pendingSelectID int
	// Zero means unknown, not empty.
	totalItems int
	bridges    AsyncCache[int, []gitlab.PipelineBridge]
	// Fetched on its own, because no list endpoint carries a start time.
	pipelineStarts       AsyncCache[int, time.Time]
	childJobs            AsyncCache[int, []gitlab.PipelineJob]
	testReport           *gitlab.TestReport
	testReportLoading    bool
	testReportErr        error
	testReportPipelineID int
	detailTab            pipelineDetailTab
	playJob              playJobState
	runPipeline          runPipelineState
}

// retryConfirmState groups the retry confirmation modal fields into a single
// struct so that dismissing the modal is a zero-value assignment rather than
// clearing 8 individual fields. Zero value means no confirmation is active.
type retryConfirmState struct {
	active    bool
	id        int    // Pipeline ID
	ref       string // Pipeline ref (for new pipeline runs)
	isJob     bool   // True if retrying a specific job, false for whole pipeline
	jobID     int
	jobName   string
	jobStage  string
	projectID int // Non-zero for bridge child jobs (downstream project)
}

// playJobState backs the play-job modal. A manual job's script can require
// variables that no earlier stage sets, so the modal collects them before the
// play instead of firing straight away. Zero value means the modal is closed.
type playJobState struct {
	active        bool
	projectID     int // Downstream project for a bridge child, else the view project
	viewProjectID int
	jobID         int
	jobName       string
	vars          variablesForm
	sending       bool
	err           error
}

// runPipelineState backs the run-pipeline modal: the ref to run on plus the
// variables the run needs. focus is 0 for the ref field and 1..n for variable
// field n-1, so one wrapping increment walks the whole form. Zero value means
// the modal is closed.
type runPipelineState struct {
	active    bool
	projectID int
	ref       textinput.Model
	vars      variablesForm
	focus     int
	sending   bool
	err       error
}

// pipelineState is a tri-state model for a project's latest pipeline status:
// loading (fetch in progress), error (fetch failed), or ready (hasInfo=true or
// empty=true). lastFetched prevents re-fetching within pipelineRefreshInterval;
// lastAccessed is used for LRU eviction when the cache exceeds its size limit.
type pipelineState struct {
	info         gitlab.PipelineSummary
	hasInfo      bool
	loading      bool
	err          error
	empty        bool
	ref          string
	lastFetched  time.Time
	lastAccessed time.Time
}

// pipelineFocus tracks which column has keyboard focus in the dual-focus
// pipeline view: pipelines on the left (navigated with j/k) or stages in
// the middle (navigated with j/k after pressing l). Press h to return focus
// to pipelines.
type pipelineFocus int

const (
	pipelineFocusPipelines pipelineFocus = iota
	pipelineFocusStages
)

// NewModel returns a ready-to-run Bubble Tea model. It applies defaults to
// zero-valued [Options] fields, initializes Bubble Tea sub-components (spinner,
// help, project list), and sets up on-disk caches for projects and
// favorites. Cache initialization errors are logged but non-fatal — the app
// falls back to API-only mode.
//
// The provided ctx is stored in the Model and used as the base context for all
// subsequent API calls. Callers should pass a context derived from
// [signal.NotifyContext] or similar so that in-flight requests are cancelled
// on shutdown.
//
// The returned Model starts in modeMultiPanel and begins loading on [Model.Init].
func NewModel(ctx context.Context, client gitlab.Service, opts Options) Model {
	opts = applyOptionDefaults(opts)
	pipelineStatus := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	previewHighlight := NewLRUCache[string, previewHighlightEntry](maxPreviewHighlightEntries)
	favorites := make(map[int]bool)

	m := Model{
		ctx:                   ctx,
		client:                client,
		opts:                  opts,
		page:                  1,
		mode:                  modeMultiPanel,
		focus:                 FocusState{Active: PanelProjects},
		pipelineStatus:        &pipelineStatus,
		search:                searchState{input: newSearchInput()},
		loading:               true,
		pagesReady:            make(map[int]bool),
		keys:                  newKeyMap(),
		help:                  newAppHelp(),
		spinner:               newAppSpinner(),
		pipelineView:          newPipelineViewState(),
		favorites:             favorites,
		projectTab:            projectTabFavorites,
		previewHighlightCache: &previewHighlight,
		commitCache:           NewAsyncCache[int, []gitlab.CommitSummary](),
		batchInFlight:         &atomic.Bool{},
	}
	m.projectList = newProjectListModel(m.rowDelegate())
	m = m.attachPersistentStores()
	return m
}

// applyOptionDefaults fills zero-valued Options fields with sensible defaults.
func applyOptionDefaults(opts Options) Options {
	if opts.ProjectsPerPage <= 0 {
		opts.ProjectsPerPage = 30
	}
	if opts.APITimeout <= 0 {
		opts.APITimeout = 15 * time.Second
	}
	if opts.PipelineTimeout <= 0 {
		opts.PipelineTimeout = 20 * time.Second
	}
	if opts.DiffContextLines <= 0 {
		opts.DiffContextLines = 10
	}
	return opts
}

func newSearchInput() textinput.Model {
	input := textinput.New()
	input.Placeholder = "Search projects"
	input.CharLimit = 128
	input.Prompt = "/ "
	input.TextStyle = lipgloss.NewStyle().Foreground(colorText)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorMuted)
	input.PromptStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(colorActive)
	input.Blur()
	return input
}

// Named rather than set inline so the icon width contract can measure the frames the UI
// actually draws. appPulse lights one to three dots against appSpinner's seven, so a queued
// pipeline reads as dimmer than a running one without taking a colour of its own.
var (
	appSpinner = spinner.Dot
	appPulse   = spinner.Jump
)

const pulseDivisor = 3

func newAppSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = appSpinner
	s.Style = lipgloss.NewStyle().Foreground(colorActive)
	return s
}

// A delegate holds a copy of these because the list hands out no way to reach the delegate it
// holds, so Update pushes the frames in through SetDelegate.
type statusFrames struct {
	spin  string
	pulse string
}

func (m *Model) statusFrames() statusFrames {
	frames := appPulse.Frames
	return statusFrames{
		spin:  animationFrame(m.spinner),
		pulse: strings.TrimSpace(frames[(m.animationTick/pulseDivisor)%len(frames)]),
	}
}

// Waiting is not working, so a status GitLab has not started yet takes the slower pulse.
//
// A moving frame is returned only for a status isLivePipelineStatus also claims, because that is
// what keeps the tick chain alive. Normalise a status any differently here and a row draws a frame
// of an animation nothing is redrawing, which holds one frame for good.
//
// The zero value falls back to the fixed glyph throughout: a delegate reaching a render with no
// frames should draw a still row rather than an empty column that tears the pane.
func (f statusFrames) icon(status string) string {
	switch {
	case f.spin == "" || f.pulse == "":
		return pipelineStatusIcon(status)
	case !isLivePipelineStatus(status):
		return pipelineStatusIcon(status)
	case strings.EqualFold(status, "running"):
		return f.spin
	default:
		return f.pulse
	}
}

// loadingIcon falls back for the reason [statusFrames.icon] gives, to the glyph for work that has
// not started, since a fetch that has not answered has told the row nothing yet.
func (f statusFrames) loadingIcon() string {
	if f.spin == "" {
		return iconPending
	}
	return f.spin
}

// animationFrame is for a caller drawing the frame inside a span it already owns: a styled
// frame closes with a reset that clears every attribute, stripping the colour from the rest
// of that span. The frames carry a trailing space, dropped here so one measures a single
// cell like every other icon a row draws.
func animationFrame(s spinner.Model) string {
	s.Style = lipgloss.Style{}
	return strings.TrimSpace(s.View())
}

func newAppHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorSubtle)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(colorSubtle)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMuted)
	return h
}

func newProjectListModel(delegate projectDelegate) list.Model {
	pl := newBareList(nil, delegate, 0, 0)
	pl.Styles.Title = titleStyle
	return pl
}

// attachPersistentStores opens the on-disk caches for projects, favorites,
// and preferences. Failures are logged but non-fatal; the app falls back to
// API-only mode for the affected store.
func (m Model) attachPersistentStores() Model {
	if cache, err := newProjectCache(m.opts.Host); err == nil {
		m.cache = cache
	} else {
		m.logError("init cache", "err", err)
	}
	if store, err := newFavoritesStore(m.opts.Host); err == nil {
		m.favStore = store
	} else {
		m.logError("init favorites store", "err", err)
	}
	if store, err := newPreferencesStore(m.opts.Host); err == nil {
		m.prefStore = store
	} else {
		m.logError("init preferences store", "err", err)
	}
	return m
}

// refreshThemeSubComponents re-applies theme colors to Bubble Tea sub-components
// that store their own style copies (search input, spinner, help, stage
// table). Called after applyTheme() on theme changes.
func (m Model) refreshThemeSubComponents() Model {
	m.search.input.TextStyle = lipgloss.NewStyle().Foreground(colorText)
	m.search.input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorMuted)
	m.search.input.PromptStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	m.search.input.Cursor.Style = lipgloss.NewStyle().Foreground(colorActive)

	m.spinner.Style = lipgloss.NewStyle().Foreground(colorActive)

	m.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorSubtle)
	m.help.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMuted)
	m.help.Styles.FullKey = lipgloss.NewStyle().Foreground(colorSubtle)
	m.help.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMuted)

	m.pipelineView.stageTable.SetStyles(stageTableStyles())
	return m
}

// isTextInputActive returns true when a text input has focus and keystrokes
// should be routed to it rather than interpreted as global hotkeys.
func (m Model) isTextInputActive() bool {
	return m.search.active || m.mrView.reply.active || m.mrView.createMR.active ||
		m.pipelineView.playJob.active || m.pipelineView.runPipeline.active
}

// toggleTheme cycles to the next theme, applies it, invalidates all caches
// that hold pre-rendered styled content, and persists the choice.
func (m Model) toggleTheme() (tea.Model, tea.Cmd) {
	next := NextTheme(currentTheme)
	applyTheme(next)
	m = m.refreshThemeSubComponents()
	m.invalidateDetailCache()
	m.populateDetailCache()
	m = m.clearPreviewHighlightCache()
	m = m.clearGlamourRenderers()
	m.refreshExplorerPreview()
	m.refreshMRViewportContent()
	m.status = "Theme: " + ThemeLabel(next)
	var cmd tea.Cmd
	if m.prefStore != nil {
		cmd = savePreferencesCmd(m.prefStore, m.focus.LayoutMode, m.focus.ScreenMode, currentTheme)
	}
	return m, cmd
}

// clearPreviewHighlightCache discards all cached syntax-highlighted preview
// strings so they are re-generated with the current theme's environment.
func (m Model) clearPreviewHighlightCache() Model {
	if m.previewHighlightCache != nil {
		c := NewLRUCache[string, previewHighlightEntry](maxPreviewHighlightEntries)
		m.previewHighlightCache = &c
	}
	return m
}

// clearGlamourRenderers drops cached glamour renderers so they are rebuilt
// with options that match the freshly applied theme.
func (m Model) clearGlamourRenderers() Model {
	m.glamourRenderers = nil
	return m
}

// refreshExplorerPreview re-highlights the explorer preview content after a
// theme change. If no explorer preview is active or highlighted, this is a no-op.
func (m *Model) refreshExplorerPreview() {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || !preview.highlighted {
		return
	}
	width := previewContentWidth(m.width)
	preview.highlightWidth = 0 // force cache miss
	highlighted, isHighlighted, err := m.highlightPreview(preview.path, preview.raw, width)
	if err != nil {
		return
	}
	if isHighlighted {
		preview.content = highlighted
		preview.highlighted = true
		preview.highlightWidth = width
		preview.viewport.SetContent(highlighted)
	}
}

// Init kicks off initial data loading. If an on-disk project cache exists, it
// is loaded first for instant startup; otherwise a foreground API fetch begins.
// Favorites are loaded in parallel. The spinner tick is not started here: Update owns
// ignition through ensureSpinnerTickCmd, so there is one path that can start a chain.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.cache != nil {
		cmds = append(cmds, loadCacheCmd(m.cache))
	} else {
		cmds = append(cmds, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false))
	}
	if m.favStore != nil {
		cmds = append(cmds, loadFavoritesCmd(m.favStore))
	}
	if m.prefStore != nil {
		cmds = append(cmds, loadPreferencesCmd(m.prefStore))
	}
	return tea.Batch(cmds...)
}

// Update is the central message router. It handles three categories of messages:
//
//  1. System messages (WindowSizeMsg) — resize all sub-components.
//  2. Key messages — dispatched to the mode-specific key handler. Help toggle
//     and error clearing are handled globally before mode dispatch.
//  3. Async result messages — each typed message is routed to its handler
//     (e.g., projectsLoadedMsg -> handleProjectsLoaded). Handlers update state
//     and may return follow-up commands for cascading fetches.
//
// The spinner ticks only while something on screen needs animating, so an idle app
// does not redraw. The tick is batched here rather than inside each handler, because a
// handler that forgets it drops the follow-up tick and the animation stops for good.
// needsAnimation is re-read after routing, since routing is what turns it true.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, isSpinnerTick := msg.(spinner.TickMsg); isSpinnerTick {
		m.spinnerTickAlive = false
	}
	var spinnerCmd tea.Cmd
	if m.needsAnimation() {
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		if spinnerCmd != nil {
			m.spinnerTickAlive = true
			m.animationTick++
		}
	}
	updated, cmd := m.routeMsg(msg)
	next := updated.(Model)
	// Sits after routing because a handler can move the focus, the screen mode or the terminal
	// size, and every one of those changes what each panel is given room for.
	layoutCmd := (&next).syncPanelSizes()
	if spinnerCmd != nil {
		// The spinner only answers a tick, so a command here means the frame just moved.
		// This sits after routing to reach whichever list a handler left behind.
		next.projectList.SetDelegate((&next).rowDelegate())
		next.pipelineView.pipelineList.SetDelegate((&next).pipelineRowDelegate())
		(&next).refreshStageTableFrames()
	}
	return next, tea.Batch(cmd, layoutCmd, spinnerCmd, ensureSpinnerTickCmd(&next))
}

func (m Model) routeMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case pipelineTickMsg:
		return m.handlePipelineTickMsg()
	case favoritesLoadedMsg:
		return m.handleFavoritesLoaded(msg)
	case preferencesLoadedMsg:
		return m.handlePreferencesLoaded(msg)
	}
	return m.routeAsyncMsg(msg)
}

// handleWindowSize updates layout-dependent state when the terminal is resized.
func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.help.Width = msg.Width
	m.refreshPreviewHighlight()
	return m, nil
}

// handleKeyMsg processes global key bindings (quit on too-small terminal, help
// toggle, error clear, theme toggle) before falling through to the mode-aware
// dispatcher.
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.width < MinTerminalWidth || m.height < MinTerminalHeight {
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Help) {
		m.showHelp = !m.showHelp
		return m, nil
	}
	if m.showHelp && key.Matches(msg, m.keys.CloseHelp) {
		m.showHelp = false
		return m, nil
	}
	if m.showHelp {
		return m, nil
	}
	if key.Matches(msg, m.keys.ClearError) {
		m.err = nil
		m.status = ""
		return m, nil
	}
	if key.Matches(msg, m.keys.Theme) && !m.isTextInputActive() {
		newModel, cmd := m.toggleTheme()
		return newModel, cmd
	}
	newModel, cmd := m.dispatchKey(msg)
	return newModel, cmd
}

// handlePipelineTickMsg drives the periodic refresh chain. The chain
// self-terminates in non-refreshable modes and is restarted via
// ensurePipelineTickCmd when the user transitions back into one.
func (m Model) handlePipelineTickMsg() (tea.Model, tea.Cmd) {
	newModel, cmd := m.handlePipelineTick()
	m = newModel.(Model)
	next := continuePipelineTickCmd(&m)
	switch {
	case cmd == nil:
		return m, next
	case next == nil:
		return m, cmd
	}
	return m, tea.Batch(cmd, next)
}

// handleFavoritesLoaded applies a freshly loaded favorites snapshot and kicks
// off the dependent reloads (visible-set prefetch, selected-project change).
func (m Model) handleFavoritesLoaded(msg favoritesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.logError("load favorites", "err", msg.err)
		return m, nil
	}
	prevID, prevOK := m.currentSelectedProjectID()
	m.favOrder = msg.favOrder
	m.favorites = make(map[int]bool, len(m.favOrder))
	for _, id := range m.favOrder {
		m.favorites[id] = true
	}
	m.projectList.SetDelegate((&m).rowDelegate())
	m.invalidateVisibleCache()
	m.ensureSelectionBounds()
	m.updateProjectList()

	var cmds []tea.Cmd
	if cmd := (&m).queueBatchPrefetchPipelineStatus(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := (&m).handleSelectedProjectChange(prevID, prevOK); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// handlePreferencesLoaded applies persisted layout/screen/theme preferences.
func (m Model) handlePreferencesLoaded(msg preferencesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.logError("load preferences", "err", msg.err)
		return m, nil
	}
	m.focus.LayoutMode = msg.layoutMode
	m.focus.ScreenMode = msg.screenMode
	applyTheme(msg.theme)
	m = m.refreshThemeSubComponents()
	m = m.clearGlamourRenderers()
	m.invalidateDetailCache()
	m.updateViewportSizes()
	m.populateDetailCache()
	m.refreshMRViewportContent()
	return m, nil
}

// routeAsyncMsg dispatches the remaining async result messages to their typed
// handlers. Kept separate from [Model.Update] so the central switch stays small.
func (m Model) routeAsyncMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsLoadedMsg:
		return m.handleProjectsLoaded(msg)
	case cacheLoadedMsg:
		return m.handleCacheLoaded(msg)
	case cacheSavedMsg:
		if msg.err != nil {
			m.logError("save cache", "err", msg.err)
		}
		return m, nil
	case treeLoadedMsg:
		return m.handleTreeLoaded(msg)
	case fileLoadedMsg:
		return m.handleFileLoaded(msg)
	case pipelineStatusMsg:
		return m.handlePipelineStatus(msg)
	case pipelinesLoadedMsg:
		return m.handlePipelinesLoaded(msg)
	case pipelineStartLoadedMsg:
		return m.handlePipelineStartLoaded(msg)
	case pipelineStagesLoadedMsg:
		return m.handlePipelineStagesLoaded(msg)
	case pipelineJobsLoadedMsg:
		return m.handlePipelineJobsLoaded(msg)
	case pipelineLogLoadedMsg:
		return m.handlePipelineLogLoaded(msg)
	case pipelineRetriedMsg:
		return m.handlePipelineRetried(msg)
	case pipelineJobRetriedMsg:
		return m.handlePipelineJobRetried(msg)
	case batchPipelineStatusMsg:
		return m.handleBatchPipelineStatus(msg)
	case selectionDebounceTickMsg:
		return m.handleSelectionDebounce(msg)
	case searchDebounceTickMsg:
		return m.handleSearchDebounceTickMsg(msg)
	case pipelineSelectionTickMsg:
		return m.handlePipelineSelectionDebounce(msg)
	case pageSizeTickMsg:
		return m.handlePageSizeSettled(msg)
	case mrsLoadedMsg:
		return m.handleMRsLoaded(msg)
	case mrDiscussionsLoadedMsg:
		return m.handleMRDiscussionsLoaded(msg)
	case mrDiffsLoadedMsg:
		return m.handleMRDiffsLoaded(msg)
	case mrDiscussionResolvedMsg:
		return m.handleMRDiscussionResolved(msg)
	case mrDiscussionReplyMsg:
		return m.handleMRDiscussionReply(msg)
	case mrDiscussionCreatedMsg:
		return m.handleMRDiscussionCreated(msg)
	case mrDiffRefsLoadedMsg:
		return m.handleMRDiffRefsLoaded(msg)
	case mrCreatedMsg:
		return m.handleMRCreated(msg)
	case branchesLoadedMsg:
		return m.handleBranchesLoaded(msg)
	case pipelineCanceledMsg:
		return m.handlePipelineCanceled(msg)
	case jobCanceledMsg:
		return m.handleJobCanceled(msg)
	case jobPlayedMsg:
		return m.handleJobPlayed(msg)
	case pipelineCreatedMsg:
		return m.handlePipelineCreated(msg)
	case childPipelineJobsLoadedMsg:
		return m.handleChildPipelineJobsLoaded(msg)
	case bridgesLoadedMsg:
		return m.handleBridgesLoaded(msg)
	case testReportLoadedMsg:
		return m.handleTestReportLoaded(msg)
	case commitsLoadedMsg:
		return m.handleCommitsLoaded(msg)
	case favoritesSavedMsg:
		if msg.err != nil {
			m.logError("save favorites", "err", msg.err)
		}
		return m, nil
	case preferencesSavedMsg:
		if msg.err != nil {
			m.logError("save preferences", "err", msg.err)
		}
		return m, nil
	case clipboardWroteMsg:
		return m.handleClipboardWrote(msg)
	}
	return m, nil
}

// handleClipboardWrote applies the result of an async clipboard write to the
// status line. The error is logged with the same "copy clipboard" key the
// pre-refactor synchronous code used, so log scrapers don't break.
func (m Model) handleClipboardWrote(msg clipboardWroteMsg) (tea.Model, tea.Cmd) {
	m.status = msg.status
	if msg.err != nil {
		m.logError("copy clipboard", "err", msg.err)
	}
	return m, nil
}

// dispatchKey routes a key event to the appropriate handler. In modeMultiPanel
// it walks an explicit modal-overlay stack (explorer > createMR > reply >
// retryConfirm > glabPreview) before falling through to the panel router. The
// order encodes precedence: an active overlay always shadows lower-priority
// handlers.
func (m Model) dispatchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeMultiPanel:
		for _, ovr := range m.modalOverlays() {
			if ovr.active {
				return ovr.handle(msg)
			}
		}
		return m.handleMultiPanelKey(msg)
	case modeExplorer:
		return m.handleExplorerKey(msg)
	case modePipelines:
		return m.handlePipelineViewKey(msg)
	}
	return m, nil
}

// modalOverlay describes one entry in the modal stack: the predicate that
// activates it and the handler to invoke. Order matters — overlays earlier
// in the slice shadow later ones.
type modalOverlay struct {
	active bool
	handle func(tea.KeyMsg) (tea.Model, tea.Cmd)
}

// modalOverlays returns the modal stack in priority order. Building this each
// keystroke is fine: it's a small slice of closures over receiver state,
// which is cheap relative to the work the handlers themselves do.
func (m Model) modalOverlays() []modalOverlay {
	return []modalOverlay{
		{active: m.explorer.project.ID != 0 && len(m.explorer.stack) > 0, handle: m.handleExplorerKey},
		{active: m.mrView.createMR.active, handle: m.handleCreateMRKey},
		{active: m.mrView.reply.active, handle: m.handleMRReplyKey},
		{active: m.pipelineView.retryConfirm.active, handle: m.handlePipelineRetryConfirmKey},
		{active: m.pipelineView.playJob.active, handle: m.handlePlayJobKey},
		{active: m.pipelineView.runPipeline.active, handle: m.handleRunPipelineKey},
		{active: m.glabPreview.active, handle: m.handleGlabPreviewKey},
	}
}
