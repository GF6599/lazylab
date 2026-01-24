// Package ui contains the Bubble Tea models, views, and styles for the TUI.
package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
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
	maxLogCacheEntries = 10        // Keep last 10 job logs
	maxLogSizeBytes    = 1_000_000 // 1MB max per log
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
	project gitlab.ProjectNode
	ref     string
	stack   []dirState
	preview previewState
}

type actionMenuState struct {
	project  gitlab.ProjectNode
	selected int
}

type pipelineViewState struct {
	project              gitlab.ProjectNode
	pipelines            []gitlab.PipelineSummary
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
	info        gitlab.PipelineSummary
	hasInfo     bool
	loading     bool
	err         error
	empty       bool
	ref         string
	lastFetched time.Time
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
	case pipelineDebounceTickMsg:
		return m.handlePipelineDebounceTickMsg(msg)
	}
	return m, nil
}

func (m Model) handleCacheLoaded(msg cacheLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if m.opts.Logger != nil {
			m.opts.Logger.Error("load cache", "err", msg.err)
		}
	}
	if !msg.found || len(msg.projects) == 0 {
		m.loading = true
		m.status = "Cache empty, contacting GitLab..."
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	}
	m.loading = false
	m.err = nil
	m.backgroundLoading = false
	m.allProjects = msg.projects
	m.invalidateVisibleCache()
	totalProjects := len(msg.projects)
	perPage := m.opts.ProjectsPerPage
	if perPage <= 0 {
		perPage = 30
	}
	m.totalPages = (totalProjects + perPage - 1) / perPage
	if m.totalPages <= 0 {
		m.totalPages = 1
	}
	m.pagesReady = make(map[int]bool, m.totalPages)
	for p := 1; p <= m.totalPages; p++ {
		m.pagesReady[p] = true
	}
	m.pagesLoaded = m.totalPages
	m.page = 1
	m.selected = 0

	// Update paginator
	m.paginator.SetTotalPages(m.totalPages)
	m.paginator.Page = 0 // Paginator is 0-indexed
	if totalProjects == 0 {
		m.status = "Cache loaded (empty)"
	} else {
		m.status = fmt.Sprintf("Loaded %d cached projects", totalProjects)
	}
	m.ensureSelectionBounds()
	m.updateProjectList()
	return m, (&m).queuePipelineFetchForSelection(true)
}

func (m Model) handleTreeLoaded(msg treeLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeExplorer || m.explorer.project.ID != msg.projectID {
		return m, nil
	}
	// If this was triggered for directory preview (path matches preview.path), format preview.
	if m.explorer.preview.path != "" && m.explorer.preview.path == msg.path {
		if msg.err != nil {
			m.explorer.preview = previewState{path: msg.path, err: msg.err}
			m.status = "Failed to load directory preview"
			return m, nil
		}
		builder := &strings.Builder{}
		builder.WriteString(fmt.Sprintf("%s/\n", msg.path))
		for _, entry := range msg.entries {
			name := entry.Name
			if entry.IsDir() {
				name += "/"
			}
			builder.WriteString(fmt.Sprintf("%s %s", explorerEntryIcon(entry), name))
			builder.WriteString("\n")
		}
		m.explorer.preview = previewState{
			path:    msg.path,
			content: builder.String(),
			loading: false,
		}
		return m, nil
	}
	idx := m.findDirIndex(msg.path)
	if idx == -1 {
		return m, nil
	}
	dir := &m.explorer.stack[idx]
	if msg.err != nil {
		dir.loading = false
		dir.entries = nil
		dir.err = msg.err
		m.status = "Failed to load directory"
		return m, nil
	}
	dir.loading = false
	dir.err = nil
	dir.entries = msg.entries
	if dir.selected >= len(dir.entries) {
		dir.selected = max(0, len(dir.entries)-1)
	}
	if idx == len(m.explorer.stack)-1 {
		return m, m.queueExplorerPreview()
	}
	return m, nil
}

func (m Model) handleFileLoaded(msg fileLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeExplorer || m.explorer.project.ID != msg.projectID {
		return m, nil
	}
	if msg.path != m.explorer.preview.path {
		return m, nil
	}
	m.explorer.preview.loading = false
	if msg.err != nil {
		m.explorer.preview.err = msg.err
		m.explorer.preview.content = ""
		m.explorer.preview.raw = ""
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
		m.status = "Failed to load file"
		return m, nil
	}
	width := previewContentWidth(m.width)
	highlighted, isHighlighted, err := highlightPreviewContent(msg.path, msg.content, width)
	if err != nil {
		// Surface syntax highlighting errors to the user
		if m.opts.Logger != nil {
			m.opts.Logger.Debug("highlight preview", "err", err, "path", msg.path)
		}
		m.status = fmt.Sprintf("Syntax highlighting unavailable: %v", err)
		// Fall back to plain text
		m.explorer.preview.err = nil
		m.explorer.preview.raw = msg.content
		m.explorer.preview.content = msg.content
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
		m.explorer.preview.viewport.SetContent(msg.content)
		m.explorer.preview.viewport.GotoTop()
		return m, nil
	}
	m.explorer.preview.err = nil
	m.explorer.preview.raw = msg.content
	if isHighlighted {
		m.explorer.preview.content = highlighted
		m.explorer.preview.highlighted = true
		m.explorer.preview.highlightWidth = width
	} else {
		m.explorer.preview.content = msg.content
		m.explorer.preview.highlighted = false
		m.explorer.preview.highlightWidth = 0
	}
	m.explorer.preview.viewport.SetContent(m.explorer.preview.content)
	m.explorer.preview.viewport.GotoTop()
	return m, nil
}

func (m Model) handleProjectsLoaded(msg projectsLoadedMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if msg.err != nil {
		if msg.background {
			m.backgroundLoading = false
			m.status = "Background load failed"
		} else {
			m.loading = false
			m.status = "Failed to load projects"
		}
		m.err = msg.err
		if m.opts.Logger != nil {
			m.opts.Logger.Error("load projects", "err", msg.err, "background", msg.background)
		}
		return m, nil
	}

	if msg.background {
		m.appendPage(msg.page)
		m.updateProjectList()
		if m.totalPages > 0 {
			m.status = fmt.Sprintf("Caching %d/%d pages", m.pagesLoaded, m.totalPages)
		}
		if m.cache != nil && len(m.allProjects) > 0 {
			cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
		}
		if m.pagesLoaded >= m.totalPages && m.totalPages > 0 {
			m.backgroundLoading = false
			m.status = "All projects cached"
			if len(cmds) == 0 {
				return m, nil
			}
			return m, tea.Batch(cmds...)
		}
		if msg.page.NextPage > 0 {
			cmds = append(cmds, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, msg.page.NextPage, true))
		} else {
			m.backgroundLoading = false
			m.status = "All projects cached"
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	}

	// Foreground load resets project cache.
	m.loading = false
	m.err = nil
	m.page = msg.page.Page
	if m.page <= 0 {
		m.page = 1
	}
	m.totalPages = msg.page.TotalPages
	if m.totalPages <= 0 {
		m.totalPages = m.page
	}
	m.allProjects = append([]gitlab.ProjectNode(nil), msg.page.Projects...)
	m.invalidateVisibleCache()
	m.pagesReady = map[int]bool{m.page: true}
	m.pagesLoaded = len(m.pagesReady)
	m.selected = 0

	// Update paginator
	m.paginator.SetTotalPages(m.totalPages)
	m.paginator.Page = m.page - 1 // Paginator is 0-indexed
	if len(m.allProjects) == 0 {
		m.status = "No projects returned"
	} else {
		m.status = fmt.Sprintf("Loaded %d projects", len(m.allProjects))
	}
	if msg.page.NextPage > 0 {
		m.backgroundLoading = true
		cmds = append(cmds, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, msg.page.NextPage, true))
	} else {
		m.backgroundLoading = false
	}
	m.ensureSelectionBounds()
	m.updateProjectList()
	if pipelineCmd := (&m).queuePipelineFetchForSelection(true); pipelineCmd != nil {
		cmds = append(cmds, pipelineCmd)
	}
	if m.cache != nil && len(m.allProjects) > 0 {
		cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineStatus(msg pipelineStatusMsg) (tea.Model, tea.Cmd) {
	state := m.pipelineStatus[msg.projectID]
	state.loading = false
	state.ref = msg.ref
	state.lastFetched = time.Now()
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			state.empty = true
			state.err = nil
			state.hasInfo = false
			state.info = gitlab.PipelineSummary{}
		} else {
			state.err = msg.err
			state.empty = false
			state.hasInfo = false
		}
	} else {
		state.info = msg.pipeline
		state.hasInfo = true
		state.err = nil
		state.empty = false
	}
	m.pipelineStatus[msg.projectID] = state
	m.updateProjectList()
	return m, nil
}

func (m Model) handlePipelinesLoaded(msg pipelinesLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	prevSelectedID := 0
	if m.pipelineView.selected >= 0 && m.pipelineView.selected < len(m.pipelineView.pipelines) {
		prevSelectedID = m.pipelineView.pipelines[m.pipelineView.selected].ID
	}
	m.pipelineView.loading = false
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			m.pipelineView.err = nil
			m.pipelineView.pipelines = nil
			m.pipelineView.page = 1
			m.pipelineView.totalPages = 0
			return m, nil
		}
		m.pipelineView.err = msg.err
		return m, nil
	}
	m.pipelineView.err = nil
	if msg.page > 0 {
		m.pipelineView.page = msg.page
	}
	if msg.totalPages > 0 {
		m.pipelineView.totalPages = msg.totalPages
	}
	if m.pipelineView.totalPages > 0 && m.pipelineView.page > m.pipelineView.totalPages {
		m.pipelineView.page = m.pipelineView.totalPages
		m.pipelineView.loading = true
		perPage := m.pipelineView.perPage
		if perPage <= 0 {
			perPage = pipelinePerPage
		}
		return m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, m.pipelineView.page, perPage)
	}
	m.pipelineView.pipelines = msg.pipelines
	sort.SliceStable(m.pipelineView.pipelines, func(i, j int) bool {
		a := m.pipelineView.pipelines[i]
		b := m.pipelineView.pipelines[j]
		if !a.UpdatedAt.IsZero() && !b.UpdatedAt.IsZero() {
			if a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.ID > b.ID
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.ID != b.ID {
			return a.ID > b.ID
		}
		return a.Ref > b.Ref
	})
	selectedSame := false
	if m.pipelineView.pendingSelectID != 0 {
		for i, p := range m.pipelineView.pipelines {
			if p.ID == m.pipelineView.pendingSelectID {
				m.pipelineView.selected = i
				selectedSame = true
				m.pipelineView.pendingSelectID = 0
				break
			}
		}
	}
	if !selectedSame && prevSelectedID != 0 {
		for i, p := range m.pipelineView.pipelines {
			if p.ID == prevSelectedID {
				m.pipelineView.selected = i
				selectedSame = true
				break
			}
		}
	}
	if !selectedSame {
		if len(m.pipelineView.pipelines) == 0 {
			m.pipelineView.selected = 0
		} else if m.pipelineView.selected >= len(m.pipelineView.pipelines) {
			m.pipelineView.selected = max(0, len(m.pipelineView.pipelines)-1)
		}
		m.pipelineView.stageSelected = 0
		m.resetPipelineLogPreview()
	}
	cmds := []tea.Cmd{
		m.queuePipelineStagesForSelection(),
		m.queuePipelineJobsForSelection(),
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineStagesLoaded(msg pipelineStagesLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if m.pipelineView.stageLoading != nil {
		m.pipelineView.stageLoading[msg.pipelineID] = false
	}
	if msg.err != nil {
		if m.pipelineView.stageErr == nil {
			m.pipelineView.stageErr = make(map[int]error)
		}
		m.pipelineView.stageErr[msg.pipelineID] = msg.err
		return m, nil
	}
	if m.pipelineView.stageCache == nil {
		m.pipelineView.stageCache = make(map[int][]gitlab.PipelineStage)
	}
	m.pipelineView.stageCache[msg.pipelineID] = msg.stages
	if m.pipelineView.stageErr != nil {
		delete(m.pipelineView.stageErr, msg.pipelineID)
	}

	// Update stage table with new data
	m.updateStageTable()

	return m, m.queuePipelineLogPreview()
}

func (m Model) handlePipelineJobsLoaded(msg pipelineJobsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if m.pipelineView.jobsLoading != nil {
		m.pipelineView.jobsLoading[msg.pipelineID] = false
	}
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			m.pipelineView.jobsCache[msg.pipelineID] = nil
			return m, m.queuePipelineLogPreview()
		}
		if m.pipelineView.jobsErr == nil {
			m.pipelineView.jobsErr = make(map[int]error)
		}
		m.pipelineView.jobsErr[msg.pipelineID] = msg.err
		return m, nil
	}
	if m.pipelineView.jobsCache == nil {
		m.pipelineView.jobsCache = make(map[int][]gitlab.PipelineJob)
	}
	m.pipelineView.jobsCache[msg.pipelineID] = msg.jobs
	if m.pipelineView.jobsErr != nil {
		delete(m.pipelineView.jobsErr, msg.pipelineID)
	}

	// Update stage table with new job data
	m.updateStageTable()

	return m, m.queuePipelineLogPreview()
}

func (m Model) handlePipelineLogLoaded(msg pipelineLogLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	if m.pipelineView.logLoading != nil {
		m.pipelineView.logLoading[msg.jobID] = false
	}
	if msg.err != nil {
		if m.pipelineView.logErr == nil {
			m.pipelineView.logErr = make(map[int]error)
		}
		m.pipelineView.logErr[msg.jobID] = msg.err
		if msg.jobID == m.pipelineView.pendingLogJobID {
			m.pipelineView.pendingLogJobID = 0
		}
		if msg.jobID == m.pipelineView.logJobID && m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{err: msg.err}
		}
		return m, nil
	}
	if m.pipelineView.logCache == nil {
		m.pipelineView.logCache = make(map[int]string)
	}

	// Truncate oversized logs and evict old entries to prevent OOM
	truncated := truncateLogContent(msg.content)
	m.pipelineView.logCache[msg.jobID] = truncated
	m.evictOldLogs()
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, msg.jobID)
	}
	if msg.jobID != m.pipelineView.logJobID && msg.jobID != m.pipelineView.pendingLogJobID {
		return m, nil
	}
	if !m.pipelineView.logAutoFollow {
		return m, nil
	}
	if msg.jobID == m.pipelineView.pendingLogJobID {
		m.pipelineView.pendingLogJobID = 0
	}
	m.pipelineView.logPreview = previewState{
		path:    fmt.Sprintf("job-%d", msg.jobID),
		content: msg.content,
		raw:     msg.content,
		loading: false,
	}
	m.pipelineView.logViewport.SetContent(msg.content)
	m.pipelineView.logJobID = msg.jobID
	if m.pipelineView.logAutoFollow {
		m.pipelineView.logViewport.GotoBottom()
	}
	return m, nil
}

func (m Model) handlePipelineRetried(msg pipelineRetriedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.pipelineView.retrying = false
	m.pipelineView.confirmRetry = false
	m.pipelineView.confirmRetryID = 0
	m.pipelineView.confirmRetryRef = ""
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryJobID = 0
	m.pipelineView.confirmRetryJobName = ""
	m.pipelineView.confirmRetryJobStage = ""
	if msg.err != nil {
		m.pipelineView.retryErr = msg.err
		m.status = fmt.Sprintf("Failed to retry pipeline #%d", msg.pipelineID)
		return m, nil
	}
	m.pipelineView.retryErr = nil
	if msg.pipeline.ID != 0 {
		m.pipelineView.pendingSelectID = msg.pipeline.ID
		if msg.pipeline.ID == msg.pipelineID {
			m.status = fmt.Sprintf("Retried pipeline #%d", msg.pipeline.ID)
		} else {
			m.status = fmt.Sprintf("Triggered pipeline #%d", msg.pipeline.ID)
		}
	} else if msg.pipelineID != 0 {
		m.pipelineView.pendingSelectID = msg.pipelineID
		m.status = fmt.Sprintf("Retried pipeline #%d", msg.pipelineID)
	} else {
		m.status = "Pipeline retriggered"
	}
	m.pipelineView.page = 1
	return m.reloadPipelineView()
}

func (m Model) handlePipelineJobRetried(msg pipelineJobRetriedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.pipelineView.retrying = false
	m.pipelineView.confirmRetry = false
	m.pipelineView.confirmRetryID = 0
	m.pipelineView.confirmRetryRef = ""
	m.pipelineView.confirmRetryIsJob = false
	m.pipelineView.confirmRetryJobID = 0
	m.pipelineView.confirmRetryJobName = ""
	m.pipelineView.confirmRetryJobStage = ""
	if msg.err != nil {
		m.pipelineView.retryErr = msg.err
		m.status = fmt.Sprintf("Failed to retry job #%d", msg.jobID)
		return m, nil
	}
	m.pipelineView.retryErr = nil
	if msg.job.ID != 0 {
		if msg.job.Name != "" {
			m.status = fmt.Sprintf("Retried job %s (#%d)", msg.job.Name, msg.job.ID)
		} else {
			m.status = fmt.Sprintf("Retried job #%d", msg.job.ID)
		}
	} else if msg.jobID != 0 {
		m.status = fmt.Sprintf("Retried job #%d", msg.jobID)
	} else {
		m.status = "Job retried"
	}
	var cmds []tea.Cmd
	if cmd := m.queuePipelineStagesRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineJobsRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineLogRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handlePipelineTick() tea.Cmd {
	switch m.mode {
	case modeProjects:
		return (&m).queuePipelineFetchForSelection(false)
	case modePipelines:
		return (&m).queuePipelineViewRefresh()
	default:
		return nil
	}
}

func (m Model) handlePipelineDebounceTickMsg(msg pipelineDebounceTickMsg) (tea.Model, tea.Cmd) {
	// Ignore stale ticks
	if m.pipelineDebounceTimer == nil || !msg.timestamp.Equal(*m.pipelineDebounceTimer) {
		return m, nil
	}

	// Verify project still selected
	if m.pipelinePendingFetch == nil || m.pipelinePendingFetch.ID != msg.projectID {
		return m, nil
	}

	// Execute fetch
	m.pipelineDebounceTimer = nil
	project := *m.pipelinePendingFetch
	m.pipelinePendingFetch = nil

	return m, (&m).queuePipelineFetch(project, true)
}

func (m Model) handleProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prevID, prevOK := m.currentSelectedProjectID()
	key := msg.String()
	if m.search.active {
		var cmd tea.Cmd
		switch msg.Type {
		case tea.KeyEsc:
			m.search.active = false
			m.search.query = ""
			m.search.input.Reset()
			m.search.input.Blur()
			m.ensureSelectionBounds()
			m.updateProjectList()
			m.status = "Search cleared"
		case tea.KeyEnter:
			m.search.active = false
			m.search.query = m.search.input.Value()
			m.search.input.Blur()
			m.status = fmt.Sprintf("Search: %s", m.search.query)
			m.ensureSelectionBounds()
			m.updateProjectList()
		case tea.KeyCtrlC:
			return m, tea.Quit
		default:
			m.search.input, cmd = m.search.input.Update(msg)
			m.search.query = m.search.input.Value()
			m.ensureSelectionBounds()
			m.updateProjectList()
		}
		currID, currOK := m.currentSelectedProjectID()
		if prevID != currID || prevOK != currOK {
			if pipelineCmd := (&m).queuePipelineFetchForSelection(true); pipelineCmd != nil {
				if cmd != nil {
					return m, tea.Batch(cmd, pipelineCmd)
				}
				return m, pipelineCmd
			}
		}
		return m, cmd
	}

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "/":
		m.search.active = true
		m.search.input.SetValue(m.search.query)
		m.search.input.CursorEnd()
		m.search.input.Focus()
		return m, textinput.Blink
	case "enter":
		if project, ok := m.selectedProject(); ok {
			return m.openProjectActions(project)
		}
	case "down", "j":
		if m.selected < len(m.visibleProjects())-1 {
			m.selected++
		}
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "ctrl+d":
		visible := m.visibleProjects()
		if len(visible) > 0 {
			step := listPageStep(m.height)
			m.selected = min(m.selected+step, len(visible)-1)
		}
	case "ctrl+u":
		visible := m.visibleProjects()
		if len(visible) > 0 {
			step := listPageStep(m.height)
			m.selected = max(m.selected-step, 0)
		}
	case "<":
		if len(m.visibleProjects()) > 0 {
			m.selected = 0
		}
	case ">":
		visible := m.visibleProjects()
		if len(visible) > 0 {
			m.selected = len(visible) - 1
		}
	case "l", "right":
		m.movePage(1)
	case "h", "left":
		m.movePage(-1)
	case "r", "ctrl+r":
		m.loading = true
		m.err = nil
		m.status = "Refreshing projects..."
		m.backgroundLoading = false
		m.page = 1
		m.paginator.Page = 0 // Reset to first page (0-indexed)
		return m, fetchProjectsCmd(m.ctx, m.client, m.opts.APITimeout, m.opts.ProjectsPerPage, 1, false)
	case "ctrl+o":
		m.copyCloneCommand()
	}
	currID, currOK := m.currentSelectedProjectID()
	if prevID != currID || prevOK != currOK {
		// Queue debounced pipeline fetch instead of immediate fetch
		currProject, ok := m.selectedProject()
		if !ok {
			return m, nil
		}
		now := time.Now()
		m.pipelinePendingFetch = &currProject
		m.pipelineDebounceTimer = &now
		return m, pipelineDebounceTickCmd(currProject.ID, now, pipelineDebounceDelay)
	}
	return m, nil
}

func (m Model) handleProjectActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		m.closeActionMenu()
		return m, nil
	case "down", "j":
		if m.actionMenu.selected < len(projectActionOptions)-1 {
			m.actionMenu.selected++
		}
	case "up", "k":
		if m.actionMenu.selected > 0 {
			m.actionMenu.selected--
		}
	case "enter":
		switch m.actionMenu.selected {
		case 0:
			return m.openPipelineView(m.actionMenu.project)
		case 1:
			return m.openExplorer(m.actionMenu.project)
		}
	}
	return m, nil
}

func (m Model) handleExplorerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	cur := m.currentDirState()
	if cur == nil {
		m.closeExplorer("Back to projects")
		return m, nil
	}
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.closeExplorer("Back to projects")
		return m, nil
	case "J":
		m.explorer.preview.viewport.HalfViewDown()
		return m, nil
	case "K":
		m.explorer.preview.viewport.HalfViewUp()
		return m, nil
	case "ctrl+d":
		m.explorer.preview.viewport.HalfViewDown()
		if cur.selected < len(cur.entries)-1 {
			step := listPageStep(m.height)
			cur.selected = min(cur.selected+step, len(cur.entries)-1)
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "ctrl+u":
		m.explorer.preview.viewport.HalfViewUp()
		if cur.selected > 0 {
			step := listPageStep(m.height)
			cur.selected = max(cur.selected-step, 0)
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "<":
		m.explorer.preview.viewport.GotoTop()
		if cur.selected > 0 {
			cur.selected = 0
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case ">":
		m.explorer.preview.viewport.GotoBottom()
		if cur.selected < len(cur.entries)-1 {
			cur.selected = len(cur.entries) - 1
			return m, m.queueExplorerPreview()
		}
		return m, nil
	case "down", "j":
		if cur.selected < len(cur.entries)-1 {
			cur.selected++
			return m, m.queueExplorerPreview()
		}
	case "up", "k":
		if cur.selected > 0 {
			cur.selected--
			return m, m.queueExplorerPreview()
		}
	case "enter", "right", "l":
		entry := m.selectedEntry()
		if entry != nil && entry.IsDir() {
			return m.descendDirectory(*entry)
		}
	case "left", "h", "backspace":
		return m.navigateExplorerUp()
	case "r", "ctrl+r":
		return m.reloadExplorerPath()
	}
	return m, nil
}

func (m Model) handlePipelineViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pipelineView.confirmRetry {
		return m.handlePipelineRetryConfirmKey(msg)
	}
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		if m.pipelineView.focus == pipelineFocusStages {
			m.pipelineView.focus = pipelineFocusPipelines
			return m, nil
		}
		m.closePipelineView()
		return m, nil
	case "right", "l":
		m.pipelineView.focus = pipelineFocusStages
		return m, m.queuePipelineLogPreview()
	case "]":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if cmd := m.changePipelinePage(1); cmd != nil {
				return m, cmd
			}
		}
	case "[":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if cmd := m.changePipelinePage(-1); cmd != nil {
				return m, cmd
			}
		}
	case "J":
		m.pipelineView.logViewport.HalfViewDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		return m, nil
	case "K":
		m.pipelineView.logViewport.HalfViewUp()
		m.pipelineView.logAutoFollow = false
		return m, nil
	case "ctrl+d":
		m.pipelineView.logViewport.HalfViewDown()
		m.pipelineView.logAutoFollow = m.pipelineView.logViewport.AtBottom()
		step := listPageStep(m.height)
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
				m.pipelineView.selected = min(m.pipelineView.selected+step, len(m.pipelineView.pipelines)-1)
				m.pipelineView.stageSelected = 0
				m.pipelineView.stageTable.SetCursor(0)
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			stages := m.selectedPipelineStages()
			if m.pipelineView.stageSelected < len(stages)-1 {
				m.pipelineView.stageSelected = min(m.pipelineView.stageSelected+step, len(stages)-1)
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "ctrl+u":
		m.pipelineView.logViewport.HalfViewUp()
		m.pipelineView.logAutoFollow = false
		step := listPageStep(m.height)
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected > 0 {
				m.pipelineView.selected = max(m.pipelineView.selected-step, 0)
				m.pipelineView.stageSelected = 0
				m.pipelineView.stageTable.SetCursor(0)
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			if m.pipelineView.stageSelected > 0 {
				m.pipelineView.stageSelected = max(m.pipelineView.stageSelected-step, 0)
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "<":
		m.pipelineView.logViewport.GotoTop()
		m.pipelineView.logAutoFollow = false
		if m.pipelineView.focus == pipelineFocusPipelines {
			if len(m.pipelineView.pipelines) > 0 && m.pipelineView.selected != 0 {
				m.pipelineView.selected = 0
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else if m.pipelineView.stageSelected != 0 {
			m.pipelineView.stageSelected = 0
			m.pipelineView.stageTable.SetCursor(0)
			m.resetPipelineLogPreview()
			return m, m.queuePipelineLogPreview()
		}
	case ">":
		m.pipelineView.logViewport.GotoBottom()
		m.pipelineView.logAutoFollow = true
		if m.pipelineView.focus == pipelineFocusPipelines {
			if len(m.pipelineView.pipelines) > 0 {
				last := len(m.pipelineView.pipelines) - 1
				if m.pipelineView.selected != last {
					m.pipelineView.selected = last
					m.pipelineView.stageSelected = 0
					m.resetPipelineLogPreview()
					cmd := m.queuePipelineStagesForSelection()
					return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
				}
			}
		} else {
			stages := m.selectedPipelineStages()
			if len(stages) > 0 {
				last := len(stages) - 1
				if m.pipelineView.stageSelected != last {
					m.pipelineView.stageSelected = last
					m.pipelineView.stageTable.SetCursor(last)
					m.resetPipelineLogPreview()
					return m, m.queuePipelineLogPreview()
				}
			}
		}
	case "down", "j":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
				m.pipelineView.selected++
				m.pipelineView.stageSelected = 0
				m.pipelineView.stageTable.SetCursor(0)
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			stages := m.selectedPipelineStages()
			if m.pipelineView.stageSelected < len(stages)-1 {
				m.pipelineView.stageSelected++
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "up", "k":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected > 0 {
				m.pipelineView.selected--
				m.pipelineView.stageSelected = 0
				m.pipelineView.stageTable.SetCursor(0)
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			if m.pipelineView.stageSelected > 0 {
				m.pipelineView.stageSelected--
				m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "r", "ctrl+r":
		return m.reloadPipelineView()
	case "R":
		if m.pipelineView.retrying {
			m.status = "Retry already in progress"
			return m, nil
		}
		pipeline := m.selectedPipeline()
		if pipeline == nil {
			m.status = "No pipeline selected"
			return m, nil
		}
		if m.pipelineView.focus == pipelineFocusStages {
			job := m.selectedPipelineJob()
			if job == nil {
				var cmds []tea.Cmd
				if cmd := m.queuePipelineStagesForSelection(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if cmd := m.queuePipelineJobsForSelection(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if len(cmds) > 0 {
					m.status = "Loading pipeline jobs..."
					return m, tea.Batch(cmds...)
				}
				m.status = "No job selected"
				return m, nil
			}
			m.pipelineView.confirmRetry = true
			m.pipelineView.confirmRetryIsJob = true
			m.pipelineView.confirmRetryID = pipeline.ID
			m.pipelineView.confirmRetryRef = ""
			m.pipelineView.confirmRetryJobID = job.ID
			m.pipelineView.confirmRetryJobName = job.Name
			m.pipelineView.confirmRetryJobStage = job.Stage
			return m, nil
		}
		m.pipelineView.confirmRetry = true
		m.pipelineView.confirmRetryIsJob = false
		m.pipelineView.confirmRetryID = pipeline.ID
		m.pipelineView.confirmRetryRef = pipeline.Ref
		m.pipelineView.confirmRetryJobID = 0
		m.pipelineView.confirmRetryJobName = ""
		m.pipelineView.confirmRetryJobStage = ""
	}
	return m, nil
}

func (m Model) handlePipelineRetryConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "left", "h", "backspace":
		m.pipelineView.confirmRetry = false
		m.pipelineView.confirmRetryID = 0
		m.pipelineView.confirmRetryRef = ""
		m.pipelineView.confirmRetryIsJob = false
		m.pipelineView.confirmRetryJobID = 0
		m.pipelineView.confirmRetryJobName = ""
		m.pipelineView.confirmRetryJobStage = ""
		return m, nil
	case "enter":
		isJob := m.pipelineView.confirmRetryIsJob
		pipelineID := m.pipelineView.confirmRetryID
		ref := strings.TrimSpace(m.pipelineView.confirmRetryRef)
		jobID := m.pipelineView.confirmRetryJobID
		jobName := m.pipelineView.confirmRetryJobName
		m.pipelineView.confirmRetry = false
		m.pipelineView.confirmRetryID = 0
		m.pipelineView.confirmRetryRef = ""
		m.pipelineView.confirmRetryIsJob = false
		m.pipelineView.confirmRetryJobID = 0
		m.pipelineView.confirmRetryJobName = ""
		m.pipelineView.confirmRetryJobStage = ""
		if m.pipelineView.project.ID == 0 || m.pipelineView.retrying {
			return m, nil
		}
		if isJob {
			if jobID == 0 {
				return m, nil
			}
			if pipelineID == 0 {
				if pipeline := m.selectedPipeline(); pipeline != nil {
					pipelineID = pipeline.ID
				}
			}
			m.pipelineView.retrying = true
			m.pipelineView.retryErr = nil
			jobLabel := fmt.Sprintf("#%d", jobID)
			if jobName != "" {
				jobLabel = fmt.Sprintf("%s (#%d)", jobName, jobID)
			}
			m.status = fmt.Sprintf("Retrying job %s", jobLabel)
			return m, retryJobCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID, jobID)
		}
		if pipelineID == 0 {
			return m, nil
		}
		if ref == "" {
			ref = strings.TrimSpace(m.pipelineView.project.DefaultBranch)
		}
		m.pipelineView.retrying = true
		m.pipelineView.retryErr = nil
		m.status = fmt.Sprintf("Retrying pipeline #%d", pipelineID)
		return m, retryPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipelineID, ref)
	}
	return m, nil
}

func (m Model) openProjectActions(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modeProjectActions
	m.actionMenu = actionMenuState{
		project:  project,
		selected: 0,
	}
	m.status = fmt.Sprintf("Actions for %s", project.PathWithNamespace)
	return m, nil
}

func (m *Model) closeActionMenu() {
	m.mode = modeProjects
	m.actionMenu = actionMenuState{}
}

func (m Model) openExplorer(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	ref := project.DefaultBranch
	if ref == "" {
		ref = "main"
	}
	m.mode = modeExplorer
	m.explorer = explorerState{
		project: project,
		ref:     ref,
		stack: []dirState{
			{path: "", loading: true},
		},
	}
	m.status = fmt.Sprintf("Browsing %s", project.PathWithNamespace)
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, ref, "")
}

func (m Model) openPipelineView(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modePipelines

	// Initialize stage table
	columns := []table.Column{
		{Title: "Stage", Width: 20},
		{Title: "Status", Width: 12},
		{Title: "Jobs", Width: 30},
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(false),
		table.WithHeight(10),
	)

	// Style the table with Rose Pine colors
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(rosePineMuted).
		BorderBottom(true).
		Bold(false).
		Foreground(rosePineSubtle)
	s.Selected = s.Selected.
		Foreground(rosePineBase).
		Background(rosePineRose).
		Bold(false)
	t.SetStyles(s)

	m.pipelineView = pipelineViewState{
		project:       project,
		loading:       true,
		page:          1,
		totalPages:    1,
		perPage:       pipelinePerPage,
		stageCache:    make(map[int][]gitlab.PipelineStage),
		stageLoading:  make(map[int]bool),
		stageErr:      make(map[int]error),
		stageTable:    t,
		jobsCache:     make(map[int][]gitlab.PipelineJob),
		jobsLoading:   make(map[int]bool),
		jobsErr:       make(map[int]error),
		logCache:      make(map[int]string),
		logLoading:    make(map[int]bool),
		logErr:        make(map[int]error),
		logAutoFollow: true,
		focus:         pipelineFocusPipelines,
	}
	m.status = fmt.Sprintf("Pipelines for %s", project.PathWithNamespace)
	return m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, m.pipelineView.page, m.pipelineView.perPage)
}

func (m *Model) closePipelineView() {
	m.mode = modeProjects
	m.pipelineView = pipelineViewState{}
	m.actionMenu = actionMenuState{}
}

func (m *Model) reloadPipelineView() (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID == 0 {
		return *m, nil
	}
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	perPage := m.pipelineView.perPage
	if perPage <= 0 {
		perPage = pipelinePerPage
	}
	m.pipelineView.loading = true
	m.pipelineView.err = nil
	m.pipelineView.pipelines = nil
	m.pipelineView.selected = 0
	m.pipelineView.page = page
	m.pipelineView.perPage = perPage
	m.pipelineView.stageCache = make(map[int][]gitlab.PipelineStage)
	m.pipelineView.stageLoading = make(map[int]bool)
	m.pipelineView.stageErr = make(map[int]error)
	m.pipelineView.stageSelected = 0
	m.pipelineView.jobsCache = make(map[int][]gitlab.PipelineJob)
	m.pipelineView.jobsLoading = make(map[int]bool)
	m.pipelineView.jobsErr = make(map[int]error)
	m.pipelineView.logCache = make(map[int]string)
	m.pipelineView.logLoading = make(map[int]bool)
	m.pipelineView.logErr = make(map[int]error)
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
	m.pipelineView.focus = pipelineFocusPipelines
	return *m, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, page, perPage)
}

func (m *Model) changePipelinePage(delta int) tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	target := page + delta
	if target < 1 {
		target = 1
	}
	if m.pipelineView.totalPages > 0 && target > m.pipelineView.totalPages {
		target = m.pipelineView.totalPages
	}
	if target == page {
		return nil
	}
	perPage := m.pipelineView.perPage
	if perPage <= 0 {
		perPage = pipelinePerPage
	}
	m.pipelineView.loading = true
	m.pipelineView.err = nil
	m.pipelineView.pipelines = nil
	m.pipelineView.selected = 0
	m.pipelineView.page = target
	m.pipelineView.perPage = perPage
	m.pipelineView.totalPages = max(1, m.pipelineView.totalPages)
	m.pipelineView.stageCache = make(map[int][]gitlab.PipelineStage)
	m.pipelineView.stageLoading = make(map[int]bool)
	m.pipelineView.stageErr = make(map[int]error)
	m.pipelineView.stageSelected = 0
	m.pipelineView.jobsCache = make(map[int][]gitlab.PipelineJob)
	m.pipelineView.jobsLoading = make(map[int]bool)
	m.pipelineView.jobsErr = make(map[int]error)
	m.pipelineView.logCache = make(map[int]string)
	m.pipelineView.logLoading = make(map[int]bool)
	m.pipelineView.logErr = make(map[int]error)
	m.resetPipelineLogPreview()
	return fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, target, perPage)
}

func (m Model) descendDirectory(entry gitlab.TreeNode) (tea.Model, tea.Cmd) {
	newState := dirState{
		path:    entry.Path,
		loading: true,
	}
	m.explorer.stack = append(m.explorer.stack, newState)
	m.explorer.preview = previewState{}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m Model) navigateExplorerUp() (tea.Model, tea.Cmd) {
	if len(m.explorer.stack) <= 1 {
		m.closeExplorer("Back to projects")
		return m, nil
	}
	m.explorer.stack = m.explorer.stack[:len(m.explorer.stack)-1]
	m.explorer.preview = previewState{}
	return m, m.queueExplorerPreview()
}

func (m Model) reloadExplorerPath() (tea.Model, tea.Cmd) {
	cur := m.currentDirState()
	if cur == nil {
		return m, nil
	}
	cur.loading = true
	cur.err = nil
	cur.entries = nil
	m.explorer.preview = previewState{}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), cur.path)
}

func (m *Model) closeExplorer(status string) {
	m.mode = modeProjects
	m.explorer = explorerState{}
	if status != "" {
		m.status = status
	}
}

func (m *Model) movePage(delta int) {
	target := m.page + delta
	if target < 1 {
		target = 1
	}
	if m.totalPages > 0 && target > m.totalPages {
		target = m.totalPages
	}
	if target == m.page {
		return
	}
	m.page = target
	m.paginator.Page = m.page - 1 // Paginator is 0-indexed
	if !m.pagesReady[m.page] {
		m.status = fmt.Sprintf("Page %d is still caching (%d/%d)", m.page, m.pagesLoaded, m.totalPages)
	} else {
		m.status = fmt.Sprintf("Viewing page %d", m.page)
	}
	m.ensureSelectionBounds()
	m.updateProjectList()
}

func (m *Model) copyCloneCommand() {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return
	}
	if project.SSHURLToRepo == "" {
		m.status = "Project has no SSH URL"
		return
	}
	cmd := fmt.Sprintf("git clone %s", project.SSHURLToRepo)
	if err := clipboard.WriteAll(cmd); err != nil {
		m.status = "Failed to copy clone command"
		if m.opts.Logger != nil {
			m.opts.Logger.Error("copy clipboard", "err", err)
		}
		return
	}
	m.status = "Copied clone command to clipboard"
}

func (m Model) selectedProject() (gitlab.ProjectNode, bool) {
	projects := m.visibleProjects()
	if len(projects) == 0 || m.selected < 0 || m.selected >= len(projects) {
		return gitlab.ProjectNode{}, false
	}
	return projects[m.selected], true
}

func (m Model) currentSelectedProjectID() (int, bool) {
	project, ok := m.selectedProject()
	if !ok {
		return 0, false
	}
	return project.ID, true
}

func (m *Model) ensureSelectionBounds() {
	projects := m.visibleProjects()
	if len(projects) == 0 {
		m.selected = 0
		return
	}
	if m.selected >= len(projects) {
		m.selected = len(projects) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

func (m *Model) queuePipelineFetchForSelection(force bool) tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		return nil
	}
	return m.queuePipelineFetch(project, force)
}

func (m *Model) queuePipelineFetch(project gitlab.ProjectNode, force bool) tea.Cmd {
	if m.pipelineStatus == nil {
		m.pipelineStatus = make(map[int]pipelineState)
	}
	state := m.pipelineStatus[project.ID]
	if state.loading {
		return nil
	}
	if !force && !state.lastFetched.IsZero() && time.Since(state.lastFetched) < pipelineRefreshInterval {
		return nil
	}
	ref := pipelineAllRefsRef
	state.loading = true
	state.err = nil
	state.empty = false
	state.ref = ref
	m.pipelineStatus[project.ID] = state
	return fetchPipelineCmd(m.ctx, m.client, m.opts.PipelineTimeout, project.ID, ref)
}

func (m *Model) queuePipelineViewRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	var cmds []tea.Cmd
	page := m.pipelineView.page
	if page <= 0 {
		page = 1
	}
	perPage := m.pipelineView.perPage
	if perPage <= 0 {
		perPage = pipelinePerPage
	}
	if !m.pipelineView.loading {
		m.pipelineView.loading = true
		cmds = append(cmds, fetchPipelinesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, page, perPage))
	}
	if cmd := m.queuePipelineStagesRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineJobsRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.queuePipelineLogRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) selectedPipeline() *gitlab.PipelineSummary {
	if len(m.pipelineView.pipelines) == 0 {
		return nil
	}
	if m.pipelineView.selected < 0 || m.pipelineView.selected >= len(m.pipelineView.pipelines) {
		return nil
	}
	return &m.pipelineView.pipelines[m.pipelineView.selected]
}

func (m *Model) queuePipelineStagesForSelection() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.stageCache != nil {
		if _, ok := m.pipelineView.stageCache[pipeline.ID]; ok {
			return nil
		}
	}
	if m.pipelineView.stageLoading == nil {
		m.pipelineView.stageLoading = make(map[int]bool)
	}
	if m.pipelineView.stageLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.stageLoading[pipeline.ID] = true
	if m.pipelineView.stageErr != nil {
		delete(m.pipelineView.stageErr, pipeline.ID)
	}
	return fetchPipelineStagesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) queuePipelineJobsForSelection() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache != nil {
		if _, ok := m.pipelineView.jobsCache[pipeline.ID]; ok {
			return nil
		}
	}
	if m.pipelineView.jobsLoading == nil {
		m.pipelineView.jobsLoading = make(map[int]bool)
	}
	if m.pipelineView.jobsLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.jobsLoading[pipeline.ID] = true
	if m.pipelineView.jobsErr != nil {
		delete(m.pipelineView.jobsErr, pipeline.ID)
	}
	return fetchPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) queuePipelineStagesRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.stageLoading == nil {
		m.pipelineView.stageLoading = make(map[int]bool)
	}
	if m.pipelineView.stageLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.stageLoading[pipeline.ID] = true
	if m.pipelineView.stageErr != nil {
		delete(m.pipelineView.stageErr, pipeline.ID)
	}
	return fetchPipelineStagesCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) queuePipelineJobsRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsLoading == nil {
		m.pipelineView.jobsLoading = make(map[int]bool)
	}
	if m.pipelineView.jobsLoading[pipeline.ID] {
		return nil
	}
	m.pipelineView.jobsLoading[pipeline.ID] = true
	if m.pipelineView.jobsErr != nil {
		delete(m.pipelineView.jobsErr, pipeline.ID)
	}
	return fetchPipelineJobsCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, pipeline.ID)
}

func (m *Model) selectedPipelineStages() []gitlab.PipelineStage {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.stageCache == nil {
		return nil
	}
	return m.pipelineView.stageCache[pipeline.ID]
}

func (m *Model) selectedPipelineJob() *gitlab.PipelineJob {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache == nil {
		return nil
	}
	jobs, ok := m.pipelineView.jobsCache[pipeline.ID]
	if !ok {
		return nil
	}
	stages := m.selectedPipelineStages()
	if len(stages) == 0 {
		return nil
	}
	stageIndex := m.pipelineView.stageSelected
	if stageIndex < 0 || stageIndex >= len(stages) {
		stageIndex = max(0, len(stages)-1)
	}
	stageName := stages[stageIndex].Name
	return latestJobForStage(jobs, stageName)
}

func (m *Model) queuePipelineLogPreview() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache == nil {
		return m.queuePipelineJobsForSelection()
	}
	jobs, ok := m.pipelineView.jobsCache[pipeline.ID]
	if !ok {
		return m.queuePipelineJobsForSelection()
	}
	stages := m.selectedPipelineStages()
	if len(stages) == 0 {
		m.pipelineView.logPreview = previewState{content: "No stages available.", loading: false}
		return nil
	}
	if m.pipelineView.stageSelected >= len(stages) {
		m.pipelineView.stageSelected = max(0, len(stages)-1)
	}
	stageName := stages[m.pipelineView.stageSelected].Name
	job := latestJobForStage(jobs, stageName)
	if job == nil {
		m.pipelineView.logPreview = previewState{content: "No jobs available for stage.", loading: false}
		return nil
	}
	if m.pipelineView.logCache != nil {
		if content, ok := m.pipelineView.logCache[job.ID]; ok {
			prevJobID := m.pipelineView.logJobID
			m.pipelineView.logPreview = previewState{
				path:    job.Name,
				content: content,
				raw:     content,
				loading: false,
			}
			m.pipelineView.logViewport.SetContent(content)
			m.pipelineView.logJobID = job.ID
			if m.pipelineView.logAutoFollow {
				m.pipelineView.logViewport.GotoBottom()
			} else {
				if prevJobID != job.ID {
					m.pipelineView.logViewport.GotoTop()
				}
			}
			return nil
		}
	}
	if m.pipelineView.logLoading == nil {
		m.pipelineView.logLoading = make(map[int]bool)
	}
	if m.pipelineView.logLoading[job.ID] {
		return nil
	}
	m.pipelineView.logLoading[job.ID] = true
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, job.ID)
	}
	m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
	m.pipelineView.logJobID = job.ID
	return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}

func (m *Model) queuePipelineLogRefresh() tea.Cmd {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	if m.pipelineView.jobsCache == nil {
		return m.queuePipelineJobsForSelection()
	}
	jobs, ok := m.pipelineView.jobsCache[pipeline.ID]
	if !ok {
		return m.queuePipelineJobsForSelection()
	}
	stages := m.selectedPipelineStages()
	if len(stages) == 0 {
		if m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{content: "No stages available.", loading: false}
		}
		return nil
	}
	if m.pipelineView.stageSelected >= len(stages) {
		m.pipelineView.stageSelected = max(0, len(stages)-1)
	}
	stageName := stages[m.pipelineView.stageSelected].Name
	job := latestJobForStage(jobs, stageName)
	if job == nil {
		if m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{content: "No jobs available for stage.", loading: false}
		}
		return nil
	}
	if m.pipelineView.logLoading == nil {
		m.pipelineView.logLoading = make(map[int]bool)
	}
	if m.pipelineView.logLoading[job.ID] {
		return nil
	}
	m.pipelineView.logLoading[job.ID] = true
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, job.ID)
	}
	if job.ID != m.pipelineView.logJobID {
		m.pipelineView.pendingLogJobID = job.ID
		if m.pipelineView.logPreview.err != nil && m.pipelineView.logPreview.content == "" {
			m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
		}
	} else if m.pipelineView.logPreview.content == "" || m.pipelineView.logPreview.err != nil {
		m.pipelineView.logPreview = previewState{path: job.Name, loading: true}
	}
	return fetchPipelineLogCmd(m.ctx, m.client, m.opts.PipelineTimeout, m.pipelineView.project.ID, job.ID)
}

func (m *Model) refreshPipelineLogPreviewFromCache() bool {
	if m.pipelineView.logCache == nil {
		return false
	}
	if m.pipelineView.logAutoFollow && m.pipelineView.pendingLogJobID != 0 {
		m.pipelineView.logJobID = m.pipelineView.pendingLogJobID
		m.pipelineView.pendingLogJobID = 0
	}
	if m.pipelineView.logJobID == 0 {
		return false
	}
	content, ok := m.pipelineView.logCache[m.pipelineView.logJobID]
	if !ok || content == "" {
		return false
	}
	if content == m.pipelineView.logPreview.raw && m.pipelineView.logPreview.content != "" {
		return false
	}
	m.pipelineView.logPreview = previewState{
		path:    fmt.Sprintf("job-%d", m.pipelineView.logJobID),
		content: content,
		raw:     content,
		loading: false,
	}
	return true
}

func (m *Model) resetPipelineLogPreview() {
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
}

func (m *Model) visibleProjects() []gitlab.ProjectNode {
	// Check if cache is valid
	if m.search.query != "" {
		// Search mode: cache valid if query matches
		if m.search.query == m.visibleCacheQuery && m.visibleCache != nil {
			return m.visibleCache
		}

		// Recompute and cache
		filtered := make([]gitlab.ProjectNode, 0, len(m.allProjects))
		for _, p := range m.allProjects {
			if fuzzyMatch(p.PathWithNamespace, m.search.query) || fuzzyMatch(p.Name, m.search.query) {
				filtered = append(filtered, p)
			}
		}
		m.visibleCache = filtered
		m.visibleCacheQuery = m.search.query
		m.visibleCachePage = -1 // Invalid in search mode
		return filtered
	}

	// Pagination mode: cache valid if page matches
	if m.page == m.visibleCachePage && m.visibleCache != nil && m.visibleCacheQuery == "" {
		return m.visibleCache
	}

	// Recompute and cache
	pageData := m.pageSlice(m.page)
	m.visibleCache = pageData
	m.visibleCachePage = m.page
	m.visibleCacheQuery = ""
	return pageData
}

// invalidateVisibleCache clears the visibleProjects cache
func (m *Model) invalidateVisibleCache() {
	m.visibleCache = nil
	m.visibleCacheQuery = ""
	m.visibleCachePage = -1
}

func (m Model) pageSlice(page int) []gitlab.ProjectNode {
	if page <= 0 {
		page = 1
	}
	if len(m.allProjects) == 0 || !m.pagesReady[page] {
		return nil
	}
	start := (page - 1) * m.opts.ProjectsPerPage
	if start >= len(m.allProjects) {
		return nil
	}
	end := start + m.opts.ProjectsPerPage
	if end > len(m.allProjects) {
		end = len(m.allProjects)
	}
	return m.allProjects[start:end]
}

// updateProjectList syncs the bubbles list component with the current visible projects
func (m *Model) updateProjectList() {
	visible := m.visibleProjects()
	items := make([]list.Item, len(visible))
	for i, p := range visible {
		items[i] = projectItem{project: p}
	}
	m.projectList.SetItems(items)

	// Note: No need to call SetDelegate() - delegate holds reference to shared pipelineStatus map

	// Sync selection with list cursor
	if m.selected >= 0 && m.selected < len(items) {
		m.projectList.Select(m.selected)
	}
}

func (m *Model) appendPage(page gitlab.ProjectPage) {
	m.pagesReady[page.Page] = true
	m.pagesLoaded = len(m.pagesReady)
	m.allProjects = append(m.allProjects, page.Projects...)
	m.invalidateVisibleCache()
	if m.totalPages <= 0 {
		m.totalPages = page.TotalPages
	}
	if m.totalPages <= 0 {
		m.totalPages = m.pagesLoaded
	}
	m.ensureSelectionBounds()
}

// evictOldLogs removes oldest entries from logCache if it exceeds maxLogCacheEntries.
// This prevents unbounded memory growth when auto-refreshing pipeline logs.
func (m *Model) evictOldLogs() {
	if m.pipelineView.logCache == nil || len(m.pipelineView.logCache) <= maxLogCacheEntries {
		return
	}

	// Find jobs to evict (oldest by job ID - assumes increasing IDs over time)
	jobIDs := make([]int, 0, len(m.pipelineView.logCache))
	for jobID := range m.pipelineView.logCache {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Ints(jobIDs)

	// Keep the current job and the most recent ones, remove the oldest
	toRemove := len(jobIDs) - maxLogCacheEntries
	for i := 0; i < toRemove; i++ {
		jobID := jobIDs[i]
		// Don't evict currently displayed log
		if jobID == m.pipelineView.logJobID {
			continue
		}
		delete(m.pipelineView.logCache, jobID)
		if m.pipelineView.logLoading != nil {
			delete(m.pipelineView.logLoading, jobID)
		}
		if m.pipelineView.logErr != nil {
			delete(m.pipelineView.logErr, jobID)
		}
	}
}

// truncateLogContent truncates log content to maxLogSizeBytes if it exceeds the limit.
func truncateLogContent(content string) string {
	if len(content) <= maxLogSizeBytes {
		return content
	}
	truncated := content[:maxLogSizeBytes]
	return truncated + "\n\n... (log truncated at 1MB, full log available in GitLab web UI)"
}

func (m *Model) queueExplorerPreview() tea.Cmd {
	entry := m.selectedEntry()
	if entry == nil {
		m.explorer.preview = previewState{}
		return nil
	}
	if entry.IsDir() {
		m.explorer.preview = previewState{
			path:    entry.Path,
			loading: true,
		}
		return fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
	}
	if m.explorer.preview.loading && m.explorer.preview.path == entry.Path {
		return nil
	}
	if !m.explorer.preview.loading && m.explorer.preview.path == entry.Path && m.explorer.preview.content != "" && m.explorer.preview.err == nil {
		return nil
	}
	m.explorer.preview = previewState{path: entry.Path, loading: true}
	return fetchFileCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m *Model) currentDirState() *dirState {
	if len(m.explorer.stack) == 0 {
		return nil
	}
	return &m.explorer.stack[len(m.explorer.stack)-1]
}

func (m *Model) parentDirState() *dirState {
	if len(m.explorer.stack) < 2 {
		return nil
	}
	return &m.explorer.stack[len(m.explorer.stack)-2]
}

func (m *Model) selectedEntry() *gitlab.TreeNode {
	dir := m.currentDirState()
	if dir == nil || len(dir.entries) == 0 {
		return nil
	}
	if dir.selected < 0 || dir.selected >= len(dir.entries) {
		return nil
	}
	return &dir.entries[dir.selected]
}

// updateStageTable updates the stage table with current pipeline stages and jobs
func (m *Model) updateStageTable() {
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		m.pipelineView.stageTable.SetRows([]table.Row{})
		return
	}

	stages := m.pipelineView.stageCache[pipeline.ID]
	jobs := m.pipelineView.jobsCache[pipeline.ID]

	if len(stages) == 0 {
		m.pipelineView.stageTable.SetRows([]table.Row{})
		return
	}

	// Build rows for the table
	rows := make([]table.Row, len(stages))
	for i, stage := range stages {
		status := stage.Status
		if status == "" {
			status = "unknown"
		}
		summary := stageJobSummary(jobs, stage.Name)
		if summary != "" {
			summary = strings.TrimPrefix(summary, " ")
		}
		rows[i] = table.Row{
			stage.Name,
			pipelineStatusLabel(status),
			summary,
		}
	}

	m.pipelineView.stageTable.SetRows(rows)

	// Update cursor position to match stageSelected
	if m.pipelineView.stageSelected >= 0 && m.pipelineView.stageSelected < len(stages) {
		m.pipelineView.stageTable.SetCursor(m.pipelineView.stageSelected)
	}
}

// updateViewportSizes updates viewport dimensions when terminal resizes
func (m *Model) updateViewportSizes() {
	if m.mode == modeExplorer {
		width := previewContentWidth(m.width)
		height := previewContentHeight(m.height)
		if m.explorer.preview.viewport.Width != width || m.explorer.preview.viewport.Height != height {
			m.explorer.preview.viewport.Width = width
			m.explorer.preview.viewport.Height = height
		}
	}
	if m.mode == modePipelines {
		width := pipelineLogContentWidth(m.width)
		height := pipelineLogContentHeight(m.height)
		if m.pipelineView.logViewport.Width != width || m.pipelineView.logViewport.Height != height {
			m.pipelineView.logViewport.Width = width
			m.pipelineView.logViewport.Height = height
		}
	}
}
