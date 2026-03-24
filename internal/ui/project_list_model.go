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
	"sync/atomic"
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
	modeProjects   Mode = iota // Paginated project list with search
	modeExplorer               // Three-pane file browser (parent / current / preview)
	modePipelines              // Pipeline list with stages/jobs and log preview
	modeMultiPanel             // Lazygit-style layout: projects + pipelines + detail side-by-side
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

	// UI layout constants
	stageTableDefaultHeight = 10 // Default row count for the pipeline stage table
	projectTabCount         = 2  // Number of project tabs (Favorites, All)
	pipelineDetailTabCount  = 3  // Number of pipeline detail tabs (Log, Info, Tests)
)

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
type projectDelegate struct {
	pipelineStatus *LRUCache[int, pipelineState]
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
	if state, ok := d.pipelineStatus.Get(proj.project.ID); ok {
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
	line := fmt.Sprintf("%s %s%s%s", cursor, favIcon, statusIcon, proj.project.PathWithNamespace)
	indent := lipgloss.Width(fmt.Sprintf("%s %s%s", cursor, favIcon, statusIcon))
	renderListItem(w, style, line, indent, width, index == m.Index(), false)
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
	isSelected := index == m.Index()

	// Use plain icon when selected so the selection background applies
	// uniformly; colored icon otherwise for at-a-glance status.
	icon := pipelineStatusIcon(pItem.summary.Status)
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

	line := fmt.Sprintf("%s %s #%d %s %s", cursor, icon, pItem.summary.ID, timestamp, ref)
	width := m.Width()
	indent := lipgloss.Width(fmt.Sprintf("%s %s ", cursor, icon))
	renderListItem(w, style, line, indent, width, isSelected, true)
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

	width := m.Width()
	line := fmt.Sprintf("%s%s %s", cursor, icon, name)
	indent := lipgloss.Width(fmt.Sprintf("%s%s ", cursor, icon))
	renderListItem(w, style, line, indent, width, index == m.Index(), false)
}

// maxWrapLines is the maximum number of lines a selected list item can wrap to.
const maxWrapLines = 3

// renderListItem writes a list item to w, wrapping the selected item when it
// exceeds the available width. The bubbles list does not enforce Height() per
// delegate render call, so extra newlines push subsequent items down without
// corruption. When ansiClamp is true, unselected items use ANSI-aware clamping
// to preserve inline color sequences (e.g. pipeline status icons).
func renderListItem(w io.Writer, style lipgloss.Style, line string, indent, width int, isSelected, ansiClamp bool) {
	if isSelected && lipgloss.Width(line) > width {
		for i, wl := range wrapSelectedItem(line, width, indent, maxWrapLines) {
			if i > 0 {
				fmt.Fprint(w, "\n")
			}
			fmt.Fprint(w, style.Render(fitLine(wl, width)))
		}
		return
	}
	if ansiClamp && !isSelected {
		fmt.Fprint(w, style.Render(clampLineANSI(line, width)))
	} else {
		fmt.Fprint(w, style.Render(clampLine(line, width)))
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
	pipelineStatus    *LRUCache[int, pipelineState]
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
	prefStore      *preferencesStore
	projectTab     projectTab

	// visibleProjects cache — avoids recomputing the filtered/paged project
	// slice on every View call. Invalidated by search query changes, page
	// navigation, tab switches, and project list reloads. The cache key is
	// the triple (query, page, tab); a mismatch triggers recomputation.
	visibleCache      []gitlab.ProjectNode
	visibleCacheQuery string
	visibleCachePage  int
	visibleCacheTab   projectTab

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

	// Detail pane render cache
	detailCacheProjectID   int
	detailCachePipelineID  int
	detailCachePipelineHas bool
	detailCacheWidth       int
	detailCacheHeight      int
	detailCacheOutput      string

	previewHighlightCache *LRUCache[string, previewHighlightEntry]

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
	project              gitlab.ProjectNode
	pipelines            []gitlab.PipelineSummary
	pipelineList         list.Model // Bubbles list for pipeline display
	selected             int
	loading              bool
	err                  error
	page                 int
	totalPages           int
	perPage              int
	stages               AsyncCache[int, []gitlab.PipelineStage]
	stageSelected        int
	stageTable           table.Model          // Table for displaying stages
	jobRows              []gitlab.PipelineJob // Maps table cursor index → job
	stageJobRows         []stageJobRow        // Rich row model with matrix grouping
	matrixExpanded       map[string]bool      // Expand/collapse state per matrix group key
	jobs                 AsyncCache[int, []gitlab.PipelineJob]
	logs                 AsyncCache[int, string]
	logPreview           previewState
	logViewport          viewport.Model
	logJobID             int
	pendingLogJobID      int
	logAutoFollow        bool
	focus                pipelineFocus
	retryConfirm         retryConfirmState
	retrying             bool
	retryErr             error
	pendingSelectID      int
	bridges              AsyncCache[int, []gitlab.PipelineBridge]
	childJobs            AsyncCache[int, []gitlab.PipelineJob]
	testReport           *gitlab.TestReport
	testReportLoading    bool
	testReportErr        error
	testReportPipelineID int
	detailTab            pipelineDetailTab
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
// The provided ctx is stored in the Model and used as the base context for all
// subsequent API calls. Callers should pass a context derived from
// [signal.NotifyContext] or similar so that in-flight requests are cancelled
// on shutdown.
//
// The returned Model starts in modeMultiPanel and begins loading on [Model.Init].
func NewModel(ctx context.Context, client gitlab.Service, opts Options) Model {
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
	input := textinput.New()
	input.Placeholder = "Search projects"
	input.CharLimit = 128
	input.Prompt = "/ "
	input.TextStyle = lipgloss.NewStyle().Foreground(colorText)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorMuted)
	input.PromptStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(colorActive)
	input.Blur()

	// Initialize spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorActive)

	// Initialize help
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorSubtle)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(colorSubtle)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMuted)

	// Initialize paginator for projects
	p := paginator.New()
	p.Type = paginator.Dots
	p.PerPage = opts.ProjectsPerPage
	p.ActiveDot = lipgloss.NewStyle().Foreground(colorActive).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(colorMuted).Render("•")

	// Initialize pipeline status cache (shared with delegate)
	pipelineStatus := NewLRUCache[int, pipelineState](maxPipelineStatusCacheSize)
	favorites := make(map[int]bool)

	// Initialize project list
	delegate := projectDelegate{pipelineStatus: &pipelineStatus, favorites: favorites}
	projectList := newBareList(nil, delegate, 0, 0)
	projectList.Styles.Title = titleStyle

	m := Model{
		ctx:            ctx,
		client:         client,
		opts:           opts,
		page:           1,
		mode:           modeMultiPanel,
		focus:          FocusState{Active: PanelProjects},
		pipelineStatus: &pipelineStatus,
		search: searchState{
			active: false,
			input:  input,
		},
		loading:        true,
		pagesReady:     make(map[int]bool),
		keys:           newKeyMap(),
		help:           h,
		spinner:        s,
		paginator:      p,
		projectList:    projectList,
		favorites:      favorites,
		projectTab:     projectTabFavorites,
		recentProjects: make([]int, 0, 10),
		previewHighlightCache: func() *LRUCache[string, previewHighlightEntry] {
			c := NewLRUCache[string, previewHighlightEntry](maxPreviewHighlightEntries)
			return &c
		}(),
		commitCache:   make(map[int][]gitlab.CommitSummary),
		commitLoading: make(map[int]bool),
		batchInFlight: &atomic.Bool{},
	}
	if cache, err := newProjectCache(opts.Host); err == nil {
		m.cache = cache
	} else {
		m.logError("init cache", "err", err)
	}
	if store, err := newFavoritesStore(opts.Host); err == nil {
		m.favStore = store
	} else {
		m.logError("init favorites store", "err", err)
	}
	if store, err := newPreferencesStore(opts.Host); err == nil {
		m.prefStore = store
	} else {
		m.logError("init preferences store", "err", err)
	}
	return m
}

// refreshThemeSubComponents re-applies theme colors to Bubble Tea sub-components
// that store their own style copies (search input, spinner, help, paginator,
// stage table). Called after applyTheme() on theme changes.
func (m *Model) refreshThemeSubComponents() {
	m.search.input.TextStyle = lipgloss.NewStyle().Foreground(colorText)
	m.search.input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorMuted)
	m.search.input.PromptStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	m.search.input.Cursor.Style = lipgloss.NewStyle().Foreground(colorActive)

	m.spinner.Style = lipgloss.NewStyle().Foreground(colorActive)

	m.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorSubtle)
	m.help.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMuted)
	m.help.Styles.FullKey = lipgloss.NewStyle().Foreground(colorSubtle)
	m.help.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMuted)

	m.paginator.ActiveDot = lipgloss.NewStyle().Foreground(colorActive).Render("•")
	m.paginator.InactiveDot = lipgloss.NewStyle().Foreground(colorMuted).Render("•")

	// Refresh stage table styles. Selected is built from scratch to avoid
	// inheriting the default Color("212") (bright pink) from DefaultStyles().
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorSubtle).
		BorderBottom(true).
		Bold(false).
		Foreground(colorSubtle)
	s.Selected = lipgloss.NewStyle().
		Foreground(colorText).
		Background(colorHighlightMed)
	s.Cell = s.Cell.
		Foreground(colorText)
	m.pipelineView.stageTable.SetStyles(s)
}

// isTextInputActive returns true when a text input has focus and keystrokes
// should be routed to it rather than interpreted as global hotkeys.
func (m *Model) isTextInputActive() bool {
	return m.search.active || m.mrView.reply.active || m.mrView.createMR.active
}

// toggleTheme cycles to the next theme, applies it, invalidates all caches
// that hold pre-rendered styled content, and persists the choice.
func (m Model) toggleTheme() (tea.Model, tea.Cmd) {
	next := NextTheme(currentTheme)
	applyTheme(next)
	m.refreshThemeSubComponents()
	m.invalidateDetailCache()
	m.clearPreviewHighlightCache()
	m.refreshExplorerPreview()
	m.status = "Theme: " + ThemeLabel(next)
	var cmd tea.Cmd
	if m.prefStore != nil {
		cmd = savePreferencesCmd(m.prefStore, m.focus.LayoutMode, m.focus.ScreenMode, currentTheme)
	}
	return m, cmd
}

// clearPreviewHighlightCache discards all cached syntax-highlighted preview
// strings so they are re-generated with the current theme's environment.
func (m *Model) clearPreviewHighlightCache() {
	if m.previewHighlightCache != nil {
		c := NewLRUCache[string, previewHighlightEntry](maxPreviewHighlightEntries)
		m.previewHighlightCache = &c
	}
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
	if m.prefStore != nil {
		cmds = append(cmds, loadPreferencesCmd(m.prefStore))
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
		// Block all keys except quit when terminal is too small
		if m.width < MinTerminalWidth || m.height < MinTerminalHeight {
			if msg.String() == "ctrl+c" || msg.String() == "q" {
				return m, tea.Quit
			}
			return m, nil
		}
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
		// Handle theme toggle globally so it works in all panes and overlays.
		// Skip when a text input is active so ~ can be typed as a character.
		if key.Matches(msg, m.keys.Theme) && !m.isTextInputActive() {
			newModel, cmd := m.toggleTheme()
			return newModel, tea.Batch(spinnerCmd, cmd)
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
			if m.pipelineView.retryConfirm.active {
				newModel, cmd := m.handlePipelineRetryConfirmKey(msg)
				return newModel, tea.Batch(spinnerCmd, cmd)
			}
			newModel, cmd := m.handleMultiPanelKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		case modeExplorer:
			newModel, cmd := m.handleExplorerKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		case modePipelines:
			newModel, cmd := m.handlePipelineViewKey(msg)
			return newModel, tea.Batch(spinnerCmd, cmd)
		default:
			return m, spinnerCmd
		}
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
	case selectionDebounceTickMsg:
		return m.handleSelectionDebounce(msg)
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
	case mrDiscussionCreatedMsg:
		return m.handleMRDiscussionCreated(msg)
	case mrDiffRefsLoadedMsg:
		return m.handleMRDiffRefsLoaded(msg)
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
			m.logError("load favorites", "err", msg.err)
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
		if msg.err != nil {
			m.logError("save favorites", "err", msg.err)
		}
		return m, nil
	case preferencesLoadedMsg:
		if msg.err != nil {
			m.logError("load preferences", "err", msg.err)
		} else {
			m.focus.LayoutMode = msg.layoutMode
			m.focus.ScreenMode = msg.screenMode
			applyTheme(msg.theme)
			m.refreshThemeSubComponents()
			m.invalidateDetailCache()
			m.updateViewportSizes()
		}
		return m, nil
	case preferencesSavedMsg:
		if msg.err != nil {
			m.logError("save preferences", "err", msg.err)
		}
		return m, nil
	}
	return m, nil
}
