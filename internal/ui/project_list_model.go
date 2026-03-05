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
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
	modeProjects       Mode = iota // Paginated project list with search
	modeExplorer                   // Three-pane file browser (parent / current / preview)
	modeProjectActions             // Modal overlay to choose "pipelines" or "browse files"
	modePipelines                  // Pipeline list with stages/jobs and log preview
	modeMultiPanel                 // Lazygit-style layout: projects + pipelines + detail side-by-side
)

const (
	pipelineRefreshInterval = 5 * time.Second
	pipelineDebounceDelay   = 300 * time.Millisecond
	searchDebounceDelay     = 150 * time.Millisecond
	pipelinePerPage         = 25
	pipelineAllRefsRef      = "__all__"
	pipelineAllRefsLabel    = "all refs"

	// Cache limits to prevent unbounded memory growth
	maxLogCacheEntries         = 10        // Keep last 10 job logs
	maxLogSizeBytes            = 1_000_000 // 1MB max per log
	maxPipelineStatusCacheSize = 100       // Keep last 100 pipeline statuses
	maxPreviewHighlightEntries = 25        // Keep last 25 syntax highlights
	maxPreviewHighlightBytes   = 200_000   // 200KB max per highlight
)

var projectActionOptions = []string{
	"View pipelines",
	"Browse files",
}

// projectTab selects the active tab in the projects panel.
type projectTab int

const (
	projectTabFavorites projectTab = iota
	projectTabAll
)

var projectTabLabels = []string{"★ Favorites", "All"}

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
type projectDelegate struct {
	pipelineStatus map[int]pipelineState
	favorites      map[int]bool
}

func (d projectDelegate) Height() int { return 1 }

func (d projectDelegate) Spacing() int { return 0 }

func (d projectDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d projectDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	proj, ok := item.(projectItem)
	if !ok {
		return
	}

	cursor, style := listCursorStyle(index, m.Index())

	// Add pipeline status icon if available
	statusIcon := ""
	if state, ok := d.pipelineStatus[proj.project.ID]; ok {
		switch {
		case state.hasInfo:
			statusIcon = pipelineStatusIcon(state.info.Status) + " "
		case state.empty:
			statusIcon = iconNoPipeline + " "
		case state.loading:
			statusIcon = iconLoading + " "
		case state.err != nil:
			statusIcon = iconUnknown + " "
		}
	}

	favIcon := ""
	if d.favorites[proj.project.ID] {
		favIcon = "★ "
	}

	width := m.Width()
	line := clampLine(fmt.Sprintf("%s %s%s%s", cursor, favIcon, statusIcon, proj.project.PathWithNamespace), width)
	fmt.Fprint(w, style.Render(line))
}

// actionMenuItem wraps an action menu option for use with bubbles/list
type actionMenuItem struct {
	label string
	index int
}

func (i actionMenuItem) Title() string       { return i.label }
func (i actionMenuItem) Description() string { return "" }
func (i actionMenuItem) FilterValue() string { return i.label }

// actionMenuDelegate renders action menu items in the list
type actionMenuDelegate struct{}

func (d actionMenuDelegate) Height() int                               { return 1 }
func (d actionMenuDelegate) Spacing() int                              { return 0 }
func (d actionMenuDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }
func (d actionMenuDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	aItem, ok := item.(actionMenuItem)
	if !ok {
		return
	}

	cursor, style := listCursorStyle(index, m.Index())

	line := fmt.Sprintf("%s %s", cursor, aItem.label)
	fmt.Fprint(w, style.Render(line))
}

// pipelineItem wraps a GitLab pipeline for use with bubbles/list
type pipelineItem struct {
	summary gitlab.PipelineSummary
}

func (i pipelineItem) Title() string {
	return fmt.Sprintf("#%d", i.summary.ID)
}

func (i pipelineItem) Description() string {
	timestamp := unknownStatus
	if !i.summary.UpdatedAt.IsZero() {
		timestamp = i.summary.UpdatedAt.Local().Format(timestampFormat)
	}
	return fmt.Sprintf("%s - %s - %s", i.summary.Status, timestamp, i.summary.Ref)
}

func (i pipelineItem) FilterValue() string {
	return fmt.Sprintf("%d %s %s", i.summary.ID, i.summary.Ref, i.summary.Status)
}

// pipelineDelegate renders pipeline items in the list
type pipelineDelegate struct{}

func (d pipelineDelegate) Height() int { return 1 }

func (d pipelineDelegate) Spacing() int { return 0 }

func (d pipelineDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d pipelineDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pItem, ok := item.(pipelineItem)
	if !ok {
		return
	}

	cursor, style := listCursorStyle(index, m.Index())

	icon := pipelineStatusStyle(pItem.summary.Status).Render(pipelineStatusIcon(pItem.summary.Status))
	timestamp := unknownStatus
	if !pItem.summary.UpdatedAt.IsZero() {
		timestamp = pItem.summary.UpdatedAt.Local().Format(timestampFormat)
	}
	ref := pItem.summary.Ref
	if ref == "" {
		ref = "unknown-ref"
	}

	line := fmt.Sprintf("%s %s #%d %s %s", cursor, icon, pItem.summary.ID, timestamp, ref)
	width := m.Width()
	fmt.Fprint(w, style.Render(clampLineANSI(line, width)))
}

// treeEntryItem wraps a GitLab tree entry for use with bubbles/list
type treeEntryItem struct {
	entry gitlab.TreeNode
}

func (i treeEntryItem) Title() string { return i.entry.Name }

func (i treeEntryItem) Description() string {
	if i.entry.IsDir() {
		return "directory"
	}
	return i.entry.Type
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

	cursor, style := listCursorStyle(index, m.Index())

	icon := explorerEntryIcon(eItem.entry)
	name := eItem.entry.Name
	if eItem.entry.IsDir() {
		name += "/"
	}

	line := fmt.Sprintf("%s%s %s", cursor, icon, name)
	fmt.Fprint(w, style.Render(line))
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
	ctx               context.Context // Parent context for cancellation
	client            gitlab.Service
	opts              Options
	allProjects       []gitlab.ProjectNode
	selected          int
	page              int
	totalPages        int
	width             int
	height            int
	loading           bool
	err               error
	status            string
	search            searchState
	pagesLoaded       int
	pagesReady        map[int]bool
	backgroundLoading bool
	cache             *projectCache
	mode              Mode
	focus             FocusState // Multi-panel focus tracking
	explorer          explorerState
	pipelineStatus    map[int]pipelineState
	actionMenu        actionMenuState
	pipelineView      pipelineViewState
	mrView            mrViewState
	// Bubble components
	keys           keyMap
	help           help.Model
	spinner        spinner.Model
	paginator      paginator.Model
	projectList    list.Model
	showHelp       bool
	recentProjects []int // IDs of recently visited projects
	favorites      map[int]bool
	favOrder       []int // User-defined ordering of favorite project IDs
	favStore       *favoritesStore
	projectTab     projectTab

	// visibleProjects cache — avoids recomputing the filtered/paged project
	// slice on every View call. Invalidated by search query changes, page
	// navigation, tab switches, and project list reloads. The cache key is
	// the triple (query, page, tab); a mismatch triggers recomputation.
	visibleCache      []gitlab.ProjectNode
	visibleCacheQuery string
	visibleCachePage  int
	visibleCacheTab   projectTab

	// Pipeline fetch debouncing
	pipelinePendingFetch  *gitlab.ProjectNode // Project awaiting fetch
	pipelineDebounceTimer *time.Time          // When to trigger fetch

	// Detail pane render cache
	detailCacheProjectID   int
	detailCachePipelineID  int
	detailCachePipelineHas bool
	detailCacheWidth       int
	detailCacheHeight      int
	detailCacheOutput      string

	previewHighlightCache map[string]previewHighlightEntry
	previewHighlightOrder []string

	// commitCache and commitLoading track per-project recent commits, keyed
	// by project ID. Fetched lazily when a project is selected in the multi-panel
	// layout and cached indefinitely (commits rarely change fast enough to matter
	// during a single session). Unlike pipeline status, there is no periodic refresh.
	commitCache   map[int][]gitlab.CommitSummary
	commitLoading map[int]bool
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

type actionMenuState struct {
	project  gitlab.ProjectNode
	menuList list.Model
	selected int // Keep for backward compatibility
}

// pipelineViewState holds all state for the pipeline browsing mode. Fields
// are grouped by concern:
//   - List/page: project, pipelines, selected, loading, err, page, totalPages, perPage
//   - Stages/matrix: stages, stageSelected, stageTable, jobRows, stageJobRows, matrixExpanded
//   - Logs: logs, logPreview, logViewport, logJobID, pendingLogJobID, logAutoFollow
//   - Retry: confirmRetry*, retrying, retryErr
//   - Bridges/children: bridges, childJobs
//   - Test reports: testReport*
//
// All caches are reset on page/selection changes via resetCaches, except
// matrixExpanded which is preserved across refreshes so bridge expand/collapse
// state survives auto-refresh ticks.
type pipelineViewState struct {
	project               gitlab.ProjectNode
	pipelines             []gitlab.PipelineSummary
	pipelineList          list.Model // Bubbles list for pipeline display
	selected              int
	loading               bool
	err                   error
	page                  int
	totalPages            int
	perPage               int
	stages                AsyncCache[int, []gitlab.PipelineStage]
	stageSelected         int
	stageTable            table.Model          // Table for displaying stages
	jobRows               []gitlab.PipelineJob // Maps table cursor index → job
	stageJobRows          []stageJobRow        // Rich row model with matrix grouping
	matrixExpanded        map[string]bool      // Expand/collapse state per matrix group key
	jobs                  AsyncCache[int, []gitlab.PipelineJob]
	logs                  AsyncCache[int, string]
	logPreview            previewState
	logViewport           viewport.Model
	logJobID              int
	pendingLogJobID       int
	logAutoFollow         bool
	focus                 pipelineFocus
	confirmRetry          bool
	confirmRetryID        int
	confirmRetryRef       string
	confirmRetryIsJob     bool
	confirmRetryJobID     int
	confirmRetryJobName   string
	confirmRetryJobStage  string
	confirmRetryProjectID int // Non-zero for bridge child jobs (downstream project)
	retrying              bool
	retryErr              error
	pendingSelectID       int
	bridges               AsyncCache[int, []gitlab.PipelineBridge]
	childJobs             AsyncCache[int, []gitlab.PipelineJob]
	testReport            *gitlab.TestReport
	testReportLoading     bool
	testReportErr         error
	testReportPipelineID  int
	detailTab             pipelineDetailTab
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
// help, paginator, project list), and sets up on-disk caches for projects and
// favorites. Cache initialization errors are logged but non-fatal — the app
// falls back to API-only mode.
//
// The returned Model starts in modeMultiPanel and begins loading on [Model.Init].
func NewModel(client gitlab.Service, opts Options) Model {
	if opts.ProjectsPerPage <= 0 {
		opts.ProjectsPerPage = 30
	}
	if opts.APITimeout <= 0 {
		opts.APITimeout = 15 * time.Second
	}
	if opts.PipelineTimeout <= 0 {
		opts.PipelineTimeout = 20 * time.Second
	}
	input := textinput.New()
	input.Placeholder = "Search projects"
	input.CharLimit = 128
	input.Prompt = "/ "
	input.TextStyle = lipgloss.NewStyle().Foreground(rosePineText)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(rosePineMuted)
	input.PromptStyle = lipgloss.NewStyle().Foreground(rosePineSubtle)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(rosePineFoam)
	input.Blur()

	// Initialize spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(rosePineFoam)

	// Initialize help
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(rosePineSubtle)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(rosePineMuted)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(rosePineSubtle)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(rosePineMuted)

	// Initialize paginator for projects
	p := paginator.New()
	p.Type = paginator.Dots
	p.PerPage = opts.ProjectsPerPage
	p.ActiveDot = lipgloss.NewStyle().Foreground(rosePineFoam).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(rosePineMuted).Render("•")

	// Initialize pipeline status map (shared with delegate)
	pipelineStatus := make(map[int]pipelineState)
	favorites := make(map[int]bool)

	// Initialize project list
	delegate := projectDelegate{pipelineStatus: pipelineStatus, favorites: favorites}
	projectList := newBareList(nil, delegate, 0, 0)
	projectList.Styles.Title = titleStyle

	m := Model{
		ctx:            context.Background(),
		client:         client,
		opts:           opts,
		page:           1,
		mode:           modeMultiPanel,
		focus:          FocusState{Active: PanelProjects},
		pipelineStatus: pipelineStatus,
		search: searchState{
			active: false,
			input:  input,
		},
		loading:               true,
		pagesReady:            make(map[int]bool),
		keys:                  newKeyMap(),
		help:                  h,
		spinner:               s,
		paginator:             p,
		projectList:           projectList,
		favorites:             favorites,
		projectTab:            projectTabFavorites,
		recentProjects:        make([]int, 0, 10),
		previewHighlightCache: make(map[string]previewHighlightEntry),
		previewHighlightOrder: make([]string, 0, maxPreviewHighlightEntries),
		commitCache:           make(map[int][]gitlab.CommitSummary),
		commitLoading:         make(map[int]bool),
	}
	if cache, err := newProjectCache(opts.Host); err == nil {
		m.cache = cache
	} else if opts.Logger != nil {
		opts.Logger.Error("init cache", "err", err)
	}
	if store, err := newFavoritesStore(opts.Host); err == nil {
		m.favStore = store
	} else if opts.Logger != nil {
		opts.Logger.Error("init favorites store", "err", err)
	}
	return m
}

// Init kicks off initial data loading. If an on-disk project cache exists, it
// is loaded first for instant startup; otherwise a foreground API fetch begins.
// Favorites are loaded in parallel. The spinner tick is always started so the
// loading indicator animates immediately.
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
	cmds = append(cmds, m.spinner.Tick)
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
// The spinner is only ticked when something is actively loading, to avoid
// unnecessary redraws in idle state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Only update spinner when actually loading something
	var spinnerCmd tea.Cmd
	if m.isLoading() {
		m.spinner, spinnerCmd = m.spinner.Update(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		m.refreshPreviewHighlight()
		m.updateViewportSizes()
		m.updateProjectListSize()
		return m, spinnerCmd
	case tea.KeyMsg:
		// Handle help toggle globally
		if key.Matches(msg, m.keys.Help) {
			m.showHelp = !m.showHelp
			return m, spinnerCmd
		}
		if m.showHelp && key.Matches(msg, m.keys.CloseHelp) {
			m.showHelp = false
			return m, spinnerCmd
		}
		if m.showHelp {
			return m, spinnerCmd
		}
		// Handle clear error
		if key.Matches(msg, m.keys.ClearError) {
			m.err = nil
			m.status = ""
			return m, spinnerCmd
		}
		switch m.mode {
		case modeMultiPanel:
			// Check if in explorer overlay
			if m.explorer.project.ID != 0 && len(m.explorer.stack) > 0 {
				newModel, cmd := m.handleExplorerKey(msg)
				return newModel, tea.Batch(spinnerCmd, cmd)
			}
			// Reply modal
			if m.mrView.reply.active {
				newModel, cmd := m.handleMRReplyKey(msg)
				return newModel, tea.Batch(spinnerCmd, cmd)
			}
			// Retry confirmation modal
			if m.pipelineView.confirmRetry {
				newModel, cmd := m.handlePipelineRetryConfirmKey(msg)
				return newModel, tea.Batch(spinnerCmd, cmd)
			}
			newModel, cmd := m.handleMultiPanelKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		case modeExplorer:
			newModel, cmd := m.handleExplorerKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		case modeProjectActions:
			newModel, cmd := m.handleProjectActionKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		case modePipelines:
			newModel, cmd := m.handlePipelineViewKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		default:
			newModel, cmd := m.handleProjectKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		}
	case projectsLoadedMsg:
		return m.handleProjectsLoaded(msg)
	case cacheLoadedMsg:
		return m.handleCacheLoaded(msg)
	case cacheSavedMsg:
		if msg.err != nil && m.opts.Logger != nil {
			m.opts.Logger.Error("save cache", "err", msg.err)
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
	case pipelineTickMsg:
		newModel, cmd := m.handlePipelineTick()
		m = newModel.(Model)
		if cmd == nil {
			return m, pipelineTickCmd()
		}
		return m, tea.Batch(cmd, pipelineTickCmd())
	case batchPipelineStatusMsg:
		return m.handleBatchPipelineStatus(msg)
	case pipelineDebounceTickMsg:
		return m.handlePipelineDebounceTickMsg(msg)
	case searchDebounceTickMsg:
		return m.handleSearchDebounceTickMsg(msg)
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
	case pipelineCanceledMsg:
		return m.handlePipelineCanceled(msg)
	case jobCanceledMsg:
		return m.handleJobCanceled(msg)
	case jobPlayedMsg:
		return m.handleJobPlayed(msg)
	case childPipelineJobsLoadedMsg:
		return m.handleChildPipelineJobsLoaded(msg)
	case bridgesLoadedMsg:
		return m.handleBridgesLoaded(msg)
	case testReportLoadedMsg:
		return m.handleTestReportLoaded(msg)
	case commitsLoadedMsg:
		return m.handleCommitsLoaded(msg)
	case favoritesLoadedMsg:
		if msg.err != nil {
			if m.opts.Logger != nil {
				m.opts.Logger.Error("load favorites", "err", msg.err)
			}
		} else {
			m.favOrder = msg.favOrder
			// Rebuild the map from the ordered slice
			m.favorites = make(map[int]bool, len(m.favOrder))
			for _, id := range m.favOrder {
				m.favorites[id] = true
			}
			m.projectList.SetDelegate(projectDelegate{
				pipelineStatus: m.pipelineStatus,
				favorites:      m.favorites,
			})
			m.invalidateVisibleCache()
			m.ensureSelectionBounds()
			m.updateProjectList()

			// Favorites may have changed the visible set; reload sidebar data
			// and batch-prefetch pipeline status for the new visible projects.
			if m.mode == modeMultiPanel && len(m.allProjects) > 0 {
				var cmds []tea.Cmd
				if cmd := (&m).queueBatchPrefetchPipelineStatus(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if cmd := (&m).autoLoadSelectedProjectData(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if len(cmds) > 0 {
					return m, tea.Batch(cmds...)
				}
			}
		}
		return m, nil
	case favoritesSavedMsg:
		if msg.err != nil && m.opts.Logger != nil {
			m.opts.Logger.Error("save favorites", "err", msg.err)
		}
		return m, nil
	}
	return m, nil
}
