// Package ui contains the Bubble Tea models, views, and styles for the TUI.
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

	"lazylab/internal/gitlab"
)

// Mode represents the current UI state of the application.
type Mode int

const (
	modeProjects Mode = iota
	modeExplorer
	modeProjectActions
	modePipelines
)

// String returns the string representation of the mode for debugging and logging.
func (m Mode) String() string {
	return [...]string{"projects", "explorer", "project_actions", "pipelines"}[m]
}

const (
	pipelineRefreshInterval = 5 * time.Second
	pipelineDebounceDelay   = 300 * time.Millisecond
	pipelinePerPage         = 25
	pipelineAllRefsRef      = "__all__"
	pipelineAllRefsLabel    = "all refs"

	// Cache limits to prevent unbounded memory growth
	maxLogCacheEntries         = 10        // Keep last 10 job logs
	maxLogSizeBytes            = 1_000_000 // 1MB max per log
	maxPipelineStatusCacheSize = 100       // Keep last 100 pipeline statuses
)

var projectActionOptions = []string{
	"View pipelines",
	"Browse files",
}

// Options configures the model at creation time.
type Options struct {
	ProjectsPerPage int
	Logger          Logger
	Host            string
	APITimeout      time.Duration // Timeout for simple API calls (projects, tree, file)
	PipelineTimeout time.Duration // Timeout for pipeline operations (stages, jobs, logs)
}

// Logger is the subset of slog.Logger we care about.
type Logger interface {
	Debug(msg string, args ...any)
	Error(msg string, args ...any)
	Info(msg string, args ...any)
}

// projectItem wraps a GitLab project for use with bubbles/list
type projectItem struct {
	project gitlab.ProjectNode
	status  string // Pipeline status for this project
}

func (i projectItem) FilterValue() string {
	return i.project.PathWithNamespace
}

// projectDelegate renders project items in the list
type projectDelegate struct {
	pipelineStatus map[int]pipelineState
}

func (d projectDelegate) Height() int { return 1 }

func (d projectDelegate) Spacing() int { return 0 }

func (d projectDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d projectDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	proj, ok := item.(projectItem)
	if !ok {
		return
	}

	cursor := " "
	style := itemStyle
	if index == m.Index() {
		cursor = ">"
		style = selectedItemStyle
	}

	// Add pipeline status icon if available
	statusIcon := ""
	if state, ok := d.pipelineStatus[proj.project.ID]; ok && state.hasInfo {
		statusIcon = pipelineStatusIcon(state.info.Status) + " "
	}

	width := m.Width()
	line := clampLine(fmt.Sprintf("%s %s%s", cursor, statusIcon, proj.project.PathWithNamespace), width)
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

	cursor := " "
	style := itemStyle
	if index == m.Index() {
		cursor = ">"
		style = selectedItemStyle
	}

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
	timestamp := "unknown"
	if !i.summary.UpdatedAt.IsZero() {
		timestamp = i.summary.UpdatedAt.Local().Format("01-02 15:04")
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

	cursor := " "
	style := itemStyle
	if index == m.Index() {
		cursor = ">"
		style = selectedItemStyle
	}

	statusBadge := pipelineStatusBadgeWithWidth(pItem.summary.Status, 12)
	timestamp := "unknown"
	if !pItem.summary.UpdatedAt.IsZero() {
		timestamp = pItem.summary.UpdatedAt.Local().Format("01-02 15:04")
	}
	ref := pItem.summary.Ref
	if ref == "" {
		ref = "unknown-ref"
	}

	line := fmt.Sprintf("%s %s #%d %s %s", cursor, statusBadge, pItem.summary.ID, timestamp, ref)
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

	cursor := " "
	style := itemStyle
	if index == m.Index() {
		cursor = ">"
		style = selectedItemStyle
	}

	icon := explorerEntryIcon(eItem.entry)
	name := eItem.entry.Name
	if eItem.entry.IsDir() {
		name += "/"
	}

	line := fmt.Sprintf("%s%s %s", cursor, icon, name)
	fmt.Fprint(w, style.Render(line))
}

// Model shows a list of projects and metadata for the selected entry.
type Model struct {
	ctx               context.Context // Parent context for cancellation
	client            *gitlab.Client
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
	explorer          explorerState
	pipelineStatus    map[int]pipelineState
	actionMenu        actionMenuState
	pipelineView      pipelineViewState

	// Bubble components
	keys           keyMap
	help           help.Model
	spinner        spinner.Model
	paginator      paginator.Model
	projectList    list.Model
	showHelp       bool
	recentProjects []int // IDs of recently visited projects

	// visibleProjects cache
	visibleCache      []gitlab.ProjectNode
	visibleCacheQuery string // Last search query used
	visibleCachePage  int    // Last page used (when not searching)

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
}

type searchState struct {
	active bool
	query  string
	input  textinput.Model
}

type dirState struct {
	path     string
	entries  []gitlab.TreeNode
	selected int
	loading  bool
	err      error
}

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

type explorerState struct {
	project    gitlab.ProjectNode
	ref        string
	stack      []dirState
	preview    previewState
	parentList list.Model // Bubbles list for parent directory
	currentList list.Model // Bubbles list for current directory
}

type actionMenuState struct {
	project  gitlab.ProjectNode
	menuList list.Model
	selected int // Keep for backward compatibility
}

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
	stageCache           map[int][]gitlab.PipelineStage
	stageLoading         map[int]bool
	stageErr             map[int]error
	stageSelected        int
	stageTable           table.Model // Table for displaying stages
	jobsCache            map[int][]gitlab.PipelineJob
	jobsLoading          map[int]bool
	jobsErr              map[int]error
	logCache             map[int]string
	logLoading           map[int]bool
	logErr               map[int]error
	logPreview           previewState
	logViewport          viewport.Model
	logJobID             int
	pendingLogJobID      int
	logAutoFollow        bool
	focus                pipelineFocus
	confirmRetry         bool
	confirmRetryID       int
	confirmRetryRef      string
	confirmRetryIsJob    bool
	confirmRetryJobID    int
	confirmRetryJobName  string
	confirmRetryJobStage string
	retrying             bool
	retryErr             error
	pendingSelectID      int
	paginator            paginator.Model
}

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

type pipelineFocus int

const (
	pipelineFocusPipelines pipelineFocus = iota
	pipelineFocusStages
)

// NewModel returns a ready-to-run Bubble Tea model.
func NewModel(client *gitlab.Client, opts Options) Model {
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
	p.ActiveDot = lipgloss.NewStyle().Foreground(rosePineRose).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(rosePineMuted).Render("•")

	// Initialize pipeline status map (shared with delegate)
	pipelineStatus := make(map[int]pipelineState)

	// Initialize project list
	delegate := projectDelegate{pipelineStatus: pipelineStatus}
	projectList := list.New([]list.Item{}, delegate, 0, 0)
	projectList.Title = ""
	projectList.SetShowStatusBar(false)
	projectList.SetShowPagination(false)
	projectList.SetShowHelp(false)
	projectList.SetFilteringEnabled(false)
	projectList.Styles.Title = titleStyle

	m := Model{
		ctx:            context.Background(),
		client:         client,
		opts:           opts,
		page:           1,
		mode:           modeProjects,
		pipelineStatus: pipelineStatus,
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
		recentProjects: make([]int, 0, 10),
	}
	if cache, err := newProjectCache(opts.Host); err == nil {
		m.cache = cache
	} else if opts.Logger != nil {
		opts.Logger.Error("init cache", "err", err)
	}
	return m
}

// Init is invoked by Bubble Tea when the program starts.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.cache != nil {
		cmds = append(cmds, loadCacheCmd(m.cache))
	} else {
		cmds = append(cmds, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false))
	}
	cmds = append(cmds, pipelineTickCmd(), m.spinner.Tick)
	return tea.Batch(cmds...)
}

// Update reacts to Bubble Tea messages and returns the new model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Update spinner for loading animations
	var spinnerCmd tea.Cmd
	m.spinner, spinnerCmd = m.spinner.Update(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		m.refreshPreviewHighlight()
		m.updateViewportSizes()
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
		cmd := m.handlePipelineTick()
		if cmd == nil {
			return m, pipelineTickCmd()
		}
		return m, tea.Batch(cmd, pipelineTickCmd())
	case batchPipelineStatusMsg:
		return m.handleBatchPipelineStatus(msg)
	case pipelineDebounceTickMsg:
		return m.handlePipelineDebounceTickMsg(msg)
	}
	return m, nil
}
