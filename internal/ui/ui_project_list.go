package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"gitlab-tui-codex/internal/gitlab"
)

const (
	modeProjects       = "projects"
	modeExplorer       = "explorer"
	modeProjectActions = "project_actions"
	modePipelines      = "pipelines"
)

const pipelineRefreshInterval = 5 * time.Second

var projectActionOptions = []string{
	"Browse files",
	"View pipelines",
}

// Options configures the model at creation time.
type Options struct {
	ProjectsPerPage int
	Logger          Logger
	Host            string
}

// Logger is the subset of slog.Logger we care about.
type Logger interface {
	Debug(msg string, args ...any)
	Error(msg string, args ...any)
	Info(msg string, args ...any)
}

// Model shows a list of projects and metadata for the selected entry.
type Model struct {
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
	mode              string
	explorer          explorerState
	pipelineStatus    map[int]pipelineState
	actionMenu        actionMenuState
	pipelineView      pipelineViewState
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
	offset         int
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
	project       gitlab.ProjectNode
	pipelines     []gitlab.PipelineSummary
	selected      int
	loading       bool
	err           error
	stageCache    map[int][]gitlab.PipelineStage
	stageLoading  map[int]bool
	stageErr      map[int]error
	stageSelected int
	jobsCache     map[int][]gitlab.PipelineJob
	jobsLoading   map[int]bool
	jobsErr       map[int]error
	logCache      map[int]string
	logLoading    map[int]bool
	logErr        map[int]error
	logPreview    previewState
	logJobID      int
	focus         pipelineFocus
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
	input := textinput.New()
	input.Placeholder = "Search projects"
	input.CharLimit = 128
	input.Prompt = "/ "
	input.Blur()
	m := Model{
		client:         client,
		opts:           opts,
		page:           1,
		mode:           modeProjects,
		pipelineStatus: make(map[int]pipelineState),
		search: searchState{
			active: false,
			input:  input,
		},
		loading:    true,
		pagesReady: make(map[int]bool),
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
		cmds = append(cmds, fetchProjectsCmd(m.client, m.opts.ProjectsPerPage, 1, false))
	}
	cmds = append(cmds, pipelineTickCmd())
	return tea.Batch(cmds...)
}

// Update reacts to Bubble Tea messages and returns the new model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.refreshPreviewHighlight()
		m.clampPreviewOffset()
		m.clampPipelineLogOffset()
	case tea.KeyMsg:
		switch m.mode {
		case modeExplorer:
			return m.handleExplorerKey(msg)
		case modeProjectActions:
			return m.handleProjectActionKey(msg)
		case modePipelines:
			return m.handlePipelineViewKey(msg)
		default:
			return m.handleProjectKey(msg)
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
	case pipelineTickMsg:
		cmd := m.handlePipelineTick()
		if cmd == nil {
			return m, pipelineTickCmd()
		}
		return m, tea.Batch(cmd, pipelineTickCmd())
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
		return m, fetchProjectsCmd(m.client, m.opts.ProjectsPerPage, 1, false)
	}
	m.loading = false
	m.err = nil
	m.backgroundLoading = false
	m.allProjects = msg.projects
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
	if totalProjects == 0 {
		m.status = "Cache loaded (empty)"
	} else {
		m.status = fmt.Sprintf("Loaded %d cached projects", totalProjects)
	}
	m.ensureSelectionBounds()
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
			offset:  0,
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
	if err != nil && m.opts.Logger != nil {
		m.opts.Logger.Debug("highlight preview", "err", err)
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
	m.explorer.preview.offset = 0
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
			cmds = append(cmds, fetchProjectsCmd(m.client, m.opts.ProjectsPerPage, msg.page.NextPage, true))
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
	m.pagesReady = map[int]bool{m.page: true}
	m.pagesLoaded = len(m.pagesReady)
	m.selected = 0
	if len(m.allProjects) == 0 {
		m.status = "No projects returned"
	} else {
		m.status = fmt.Sprintf("Loaded %d projects", len(m.allProjects))
	}
	if msg.page.NextPage > 0 {
		m.backgroundLoading = true
		cmds = append(cmds, fetchProjectsCmd(m.client, m.opts.ProjectsPerPage, msg.page.NextPage, true))
	} else {
		m.backgroundLoading = false
	}
	m.ensureSelectionBounds()
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
	return m, nil
}

func (m Model) handlePipelinesLoaded(msg pipelinesLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePipelines || m.pipelineView.project.ID != msg.projectID {
		return m, nil
	}
	m.pipelineView.loading = false
	if msg.err != nil {
		if errors.Is(msg.err, gitlab.ErrNoPipelines) {
			m.pipelineView.err = nil
			m.pipelineView.pipelines = nil
			return m, nil
		}
		m.pipelineView.err = msg.err
		return m, nil
	}
	m.pipelineView.err = nil
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
	if m.pipelineView.selected >= len(m.pipelineView.pipelines) {
		m.pipelineView.selected = max(0, len(m.pipelineView.pipelines)-1)
	}
	m.pipelineView.stageSelected = 0
	m.resetPipelineLogPreview()
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
		m.pipelineView.logPreview = previewState{err: msg.err}
		return m, nil
	}
	if m.pipelineView.logCache == nil {
		m.pipelineView.logCache = make(map[int]string)
	}
	m.pipelineView.logCache[msg.jobID] = msg.content
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, msg.jobID)
	}
	m.pipelineView.logPreview = previewState{
		path:    fmt.Sprintf("job-%d", msg.jobID),
		content: msg.content,
		raw:     msg.content,
		loading: false,
		offset:  0,
	}
	m.pipelineView.logJobID = msg.jobID
	return m, nil
}

func (m Model) handlePipelineTick() tea.Cmd {
	if m.mode != modeProjects {
		return nil
	}
	return (&m).queuePipelineFetchForSelection(false)
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
			m.status = "Search cleared"
		case tea.KeyEnter:
			m.search.active = false
			m.search.query = m.search.input.Value()
			m.search.input.Blur()
			m.status = fmt.Sprintf("Search: %s", m.search.query)
			m.ensureSelectionBounds()
		case tea.KeyCtrlC:
			return m, tea.Quit
		default:
			m.search.input, cmd = m.search.input.Update(msg)
			m.search.query = m.search.input.Value()
			m.ensureSelectionBounds()
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
		return m, fetchProjectsCmd(m.client, m.opts.ProjectsPerPage, 1, false)
	case "ctrl+o":
		m.copyCloneCommand()
	}
	currID, currOK := m.currentSelectedProjectID()
	if prevID != currID || prevOK != currOK {
		if pipelineCmd := (&m).queuePipelineFetchForSelection(true); pipelineCmd != nil {
			return m, pipelineCmd
		}
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
			return m.openExplorer(m.actionMenu.project)
		case 1:
			return m.openPipelineView(m.actionMenu.project)
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
		if m.scrollPreview(1) {
			return m, nil
		}
	case "K":
		if m.scrollPreview(-1) {
			return m, nil
		}
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
	case "J":
		if m.scrollPipelineLog(1) {
			return m, nil
		}
	case "K":
		if m.scrollPipelineLog(-1) {
			return m, nil
		}
	case "down", "j":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
				m.pipelineView.selected++
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			stages := m.selectedPipelineStages()
			if m.pipelineView.stageSelected < len(stages)-1 {
				m.pipelineView.stageSelected++
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "up", "k":
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected > 0 {
				m.pipelineView.selected--
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			if m.pipelineView.stageSelected > 0 {
				m.pipelineView.stageSelected--
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "r", "ctrl+r":
		return m.reloadPipelineView()
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
	return m, fetchTreeCmd(m.client, project.ID, ref, "")
}

func (m Model) openPipelineView(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	m.mode = modePipelines
	m.pipelineView = pipelineViewState{
		project:      project,
		loading:      true,
		stageCache:   make(map[int][]gitlab.PipelineStage),
		stageLoading: make(map[int]bool),
		stageErr:     make(map[int]error),
		jobsCache:    make(map[int][]gitlab.PipelineJob),
		jobsLoading:  make(map[int]bool),
		jobsErr:      make(map[int]error),
		logCache:     make(map[int]string),
		logLoading:   make(map[int]bool),
		logErr:       make(map[int]error),
		focus:        pipelineFocusPipelines,
	}
	m.status = fmt.Sprintf("Pipelines for %s", project.PathWithNamespace)
	return m, fetchPipelinesCmd(m.client, project.ID)
}

func (m *Model) closePipelineView() {
	m.mode = modeProjectActions
	m.pipelineView = pipelineViewState{}
	m.actionMenu.selected = 1
}

func (m *Model) reloadPipelineView() (tea.Model, tea.Cmd) {
	if m.pipelineView.project.ID == 0 {
		return *m, nil
	}
	m.pipelineView.loading = true
	m.pipelineView.err = nil
	m.pipelineView.pipelines = nil
	m.pipelineView.selected = 0
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
	m.pipelineView.focus = pipelineFocusPipelines
	return *m, fetchPipelinesCmd(m.client, m.pipelineView.project.ID)
}

func (m Model) descendDirectory(entry gitlab.TreeNode) (tea.Model, tea.Cmd) {
	newState := dirState{
		path:    entry.Path,
		loading: true,
	}
	m.explorer.stack = append(m.explorer.stack, newState)
	m.explorer.preview = previewState{}
	return m, fetchTreeCmd(m.client, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
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
	return m, fetchTreeCmd(m.client, m.explorer.project.ID, displayRef(m.explorer), cur.path)
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
	if !m.pagesReady[m.page] {
		m.status = fmt.Sprintf("Page %d is still caching (%d/%d)", m.page, m.pagesLoaded, m.totalPages)
	} else {
		m.status = fmt.Sprintf("Viewing page %d", m.page)
	}
	m.ensureSelectionBounds()
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
	ref := strings.TrimSpace(project.DefaultBranch)
	state.loading = true
	state.err = nil
	state.empty = false
	state.ref = ref
	m.pipelineStatus[project.ID] = state
	return fetchPipelineCmd(m.client, project.ID, ref)
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
	return fetchPipelineStagesCmd(m.client, m.pipelineView.project.ID, pipeline.ID)
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
	return fetchPipelineJobsCmd(m.client, m.pipelineView.project.ID, pipeline.ID)
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
			m.pipelineView.logPreview = previewState{
				path:    job.Name,
				content: content,
				raw:     content,
				loading: false,
				offset:  0,
			}
			m.pipelineView.logJobID = job.ID
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
	return fetchPipelineLogCmd(m.client, m.pipelineView.project.ID, job.ID)
}

func (m *Model) resetPipelineLogPreview() {
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
}

func (m Model) visibleProjects() []gitlab.ProjectNode {
	if m.search.query != "" {
		filtered := make([]gitlab.ProjectNode, 0, len(m.allProjects))
		for _, p := range m.allProjects {
			if fuzzyMatch(p.PathWithNamespace, m.search.query) || fuzzyMatch(p.Name, m.search.query) {
				filtered = append(filtered, p)
			}
		}
		return filtered
	}
	return m.pageSlice(m.page)
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

func (m *Model) appendPage(page gitlab.ProjectPage) {
	m.pagesReady[page.Page] = true
	m.pagesLoaded = len(m.pagesReady)
	m.allProjects = append(m.allProjects, page.Projects...)
	if m.totalPages <= 0 {
		m.totalPages = page.TotalPages
	}
	if m.totalPages <= 0 {
		m.totalPages = m.pagesLoaded
	}
	m.ensureSelectionBounds()
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
			offset:  0,
		}
		return fetchTreeCmd(m.client, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
	}
	if m.explorer.preview.loading && m.explorer.preview.path == entry.Path {
		return nil
	}
	if !m.explorer.preview.loading && m.explorer.preview.path == entry.Path && m.explorer.preview.content != "" && m.explorer.preview.err == nil {
		return nil
	}
	m.explorer.preview = previewState{path: entry.Path, loading: true, offset: 0}
	return fetchFileCmd(m.client, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
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

// View renders the UI to the terminal.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	switch m.mode {
	case modeExplorer:
		return renderExplorerView(m, width)
	case modeProjectActions:
		return renderProjectActionView(m, width)
	case modePipelines:
		return renderPipelineView(m, width)
	}
	listWidth := width / 2
	detailWidth := width - listWidth

	left := renderListPane(m, listWidth)
	right := renderDetailPane(m, detailWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func renderListPane(m Model, width int) string {
	b := &strings.Builder{}
	title := titleStyle.Render(clampLine(renderListTitle(m), width))
	b.WriteString(title)
	b.WriteString("\n")
	if m.loading {
		b.WriteString(clampLine(" Loading projects...", width))
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString(errorStyle.Render(clampLine(" "+m.err.Error(), width)))
		b.WriteString("\n")
	}
	if len(m.allProjects) == 0 && !m.loading && m.err == nil {
		b.WriteString(clampLine(" No projects found.", width))
		b.WriteString("\n")
	}
	visible := m.visibleProjects()
	if m.search.query == "" && !m.pagesReady[m.page] && !m.loading {
		b.WriteString(clampLine(fmt.Sprintf(" Page %d is still loading...", m.page), width))
		b.WriteString("\n")
	}
	for i, p := range visible {
		cursor := " "
		style := itemStyle
		if i == m.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(fmt.Sprintf("%s %s", cursor, p.PathWithNamespace), width)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	progress := renderProgressBar(m, width)
	if progress != "" {
		b.WriteString(progress)
		b.WriteString("\n")
	}
	b.WriteString(renderSearchBar(m, width))
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(statusStyle.Render(clampLine(" "+m.status, width)))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderDetailPane(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString(titleStyle.Render(clampLine("Details", width)))
	b.WriteString("\n")
	visible := m.visibleProjects()
	if len(visible) == 0 {
		b.WriteString(clampLine(" Select a project to see more information.", width))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	project := visible[m.selected]
	writeDetailLine(b, fmt.Sprintf(" Name: %s", project.Name), width)
	writeDetailLine(b, fmt.Sprintf(" Path: %s", project.PathWithNamespace), width)
	writeDetailLine(b, fmt.Sprintf(" Visibility: %s", project.Visibility), width)
	writeDetailLine(b, fmt.Sprintf(" Stars: %d", project.StarCount), width)
	if !project.LastActivityAt.IsZero() {
		writeDetailLine(b, fmt.Sprintf(" Last Activity: %s", project.LastActivityAt.Format(time.RFC1123)), width)
	}
	writeDetailLine(b, fmt.Sprintf(" URL: %s", project.WebURL), width)
	if project.DefaultBranch != "" {
		writeDetailLine(b, fmt.Sprintf(" Default Branch: %s", project.DefaultBranch), width)
	}
	if project.SSHURLToRepo != "" {
		writeDetailLine(b, fmt.Sprintf(" Clone: git clone %s", project.SSHURLToRepo), width)
	}
	if project.Description != "" {
		b.WriteString("\n")
		b.WriteString(clampLines(wrapText(project.Description, width), width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(clampLines(renderPipelineSection(m, project, width), width))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineListPane(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Pipelines · %s", m.pipelineView.project.PathWithNamespace)
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusPipelines {
		header = explorerHeaderStyle.Bold(true)
	}
	b.WriteString(header.Render(clampLine(title, width)))
	b.WriteString("\n")
	if m.pipelineView.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading pipelines...", width)))
		b.WriteString("\n")
	}
	if m.pipelineView.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+m.pipelineView.err.Error(), width)))
		b.WriteString("\n")
	}
	if len(m.pipelineView.pipelines) == 0 && !m.pipelineView.loading && m.pipelineView.err == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" No pipelines found.", width)))
		b.WriteString("\n")
	}
	for i, p := range m.pipelineView.pipelines {
		cursor := " "
		if i == m.pipelineView.selected {
			cursor = ">"
		}
		statusBadge := pipelineStatusBadge(p.Status)
		ref := p.Ref
		if ref == "" {
			ref = "unknown-ref"
		}
		line := clampLine(fmt.Sprintf("%s %s #%d %s", cursor, statusBadge, p.ID, ref), width)
		b.WriteString(renderPipelineEntryLine(line, i == m.pipelineView.selected, m.pipelineView.focus == pipelineFocusPipelines))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(explorerHintStyle.Render(clampLine(" ← back · → stages · r refresh", width)))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineStagesPane(m Model, width int) string {
	b := &strings.Builder{}
	header := explorerHeaderStyle
	if m.pipelineView.focus == pipelineFocusStages {
		header = explorerHeaderStyle.Bold(true)
	}
	b.WriteString(header.Render(clampLine("Stages", width)))
	b.WriteString("\n")
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a pipeline to see stages.", width)))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Pipeline: #%d", pipeline.ID), width)))
	b.WriteString("\n")
	if pipeline.Ref != "" {
		b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Ref: %s", pipeline.Ref), width)))
		b.WriteString("\n")
	}
	if m.pipelineView.stageLoading[pipeline.ID] {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading stages...", width)))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.pipelineView.jobsLoading[pipeline.ID] {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading jobs...", width)))
		b.WriteString("\n")
	}
	if err := m.pipelineView.jobsErr[pipeline.ID]; err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+err.Error(), width)))
		b.WriteString("\n")
	}
	if err := m.pipelineView.stageErr[pipeline.ID]; err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+err.Error(), width)))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	stages := m.pipelineView.stageCache[pipeline.ID]
	if len(stages) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" No stage data available.", width)))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	jobs := m.pipelineView.jobsCache[pipeline.ID]
	for i, stage := range stages {
		cursor := " "
		if i == m.pipelineView.stageSelected {
			cursor = ">"
		}
		status := stage.Status
		if status == "" {
			status = "unknown"
		}
		summary := stageJobSummary(jobs, stage.Name)
		stageLine := fmt.Sprintf("%s %s %s%s", cursor, pipelineStatusBadge(status), stage.Name, summary)
		b.WriteString(renderPipelineEntryLine(clampLine(stageLine, width), i == m.pipelineView.stageSelected, m.pipelineView.focus == pipelineFocusStages))
		b.WriteString("\n")
	}
	b.WriteString(explorerHintStyle.Render(clampLine(" ↑/↓ stages · ← pipelines · J/K scroll logs", width)))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineLogPane(m Model, width, height int) string {
	b := &strings.Builder{}
	title := "Log Preview"
	if job := m.pipelineLogJob(); job != nil {
		title = fmt.Sprintf("Log · %s", job.Name)
	}
	b.WriteString(explorerHeaderStyle.Render(clampLine(title, width)))
	b.WriteString("\n")
	preview := m.pipelineView.logPreview
	if preview.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading job log...", width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+preview.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a stage to preview logs.", width)))
		b.WriteString("\n")
		return b.String()
	}
	contentLines := previewContentLines(preview, width)
	visibleHeight := max(0, height-1)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	offset := preview.offset
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if visibleHeight > 0 && len(contentLines) > visibleHeight {
		contentLines = contentLines[offset:min(offset+visibleHeight, len(contentLines))]
	}
	for _, line := range contentLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderPipelineSection(m Model, project gitlab.ProjectNode, width int) string {
	state, ok := m.pipelineStatus[project.ID]
	refLabel := pipelineRefLabel(project, state)
	if refLabel == "" {
		refLabel = "all refs"
	}
	var b strings.Builder
	fmt.Fprintf(&b, " Pipeline (%s):\n", refLabel)
	switch {
	case state.loading && !state.hasInfo:
		b.WriteString("  Loading latest pipeline...\n")
	case state.err != nil:
		b.WriteString("  Error: " + state.err.Error() + "\n")
	case state.empty:
		fmt.Fprintf(&b, "  No pipelines found for %s.\n", refLabel)
	case state.hasInfo:
		fmt.Fprintf(&b, "  Status: %s (#%d)\n", state.info.Status, state.info.ID)
		if state.info.SHA != "" {
			fmt.Fprintf(&b, "  SHA: %s\n", truncate(state.info.SHA, 12))
		}
		if !state.info.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, "  Updated: %s\n", state.info.UpdatedAt.Format(time.RFC1123))
		}
		if state.info.WebURL != "" {
			urlWidth := width - 4
			if urlWidth < 4 {
				urlWidth = width
			}
			fmt.Fprintf(&b, "  URL: %s\n", truncate(state.info.WebURL, urlWidth))
		}
		if len(state.info.Stages) > 0 {
			stageWidth := width - 8
			if stageWidth < 8 {
				stageWidth = width
			}
			b.WriteString("  Stages:\n")
			for _, stage := range state.info.Stages {
				stageName := truncate(stage.Name, stageWidth)
				stageStatus := truncate(stage.Status, stageWidth)
				fmt.Fprintf(&b, "   - %s: %s\n", stageName, stageStatus)
			}
		}
		if state.loading {
			b.WriteString("  Refreshing...\n")
		}
	default:
		if !ok {
			b.WriteString("  Pipeline status pending...\n")
		} else if state.loading {
			b.WriteString("  Refreshing pipeline status...\n")
		} else {
			b.WriteString("  Pipeline status pending...\n")
		}
	}
	if !state.lastFetched.IsZero() {
		fmt.Fprintf(&b, "  Checked: %s\n", state.lastFetched.Format(time.RFC1123))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderExplorerView(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	if width < 18 {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for explorer view.")
	}
	parentWidth := max(6, width*20/100)
	currentWidth := max(6, width*40/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 6 {
		previewWidth = 6
		currentWidth = max(6, width-parentWidth-previewWidth)
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	parentLines := normalizeColumn(renderExplorerParents(m, parentWidth-2), parentWidth-2, contentHeight)
	currentLines := normalizeColumn(renderExplorerCurrent(m, currentWidth-2), currentWidth-2, contentHeight)
	previewLines := normalizeColumn(renderExplorerPreview(m, previewWidth-2, contentHeight), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("┌" + strings.Repeat("─", parentWidth-2) + "┬" + strings.Repeat("─", currentWidth-2) + "┬" + strings.Repeat("─", previewWidth-2) + "┐"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s%s%s\n",
			explorerBorderStyle.Render("│"),
			parentLines[i],
			explorerBorderStyle.Render("│"),
			currentLines[i],
			explorerBorderStyle.Render("│"),
			previewLines[i],
			explorerBorderStyle.Render("│"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("└" + strings.Repeat("─", parentWidth-2) + "┴" + strings.Repeat("─", currentWidth-2) + "┴" + strings.Repeat("─", previewWidth-2) + "┘"))
	return b.String()
}

func renderProjectActionView(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Project Actions · %s", m.actionMenu.project.PathWithNamespace)
	b.WriteString(titleStyle.Render(clampLine(title, width)))
	b.WriteString("\n")
	b.WriteString(clampLine("Choose what to open:", width))
	b.WriteString("\n\n")
	for i, option := range projectActionOptions {
		cursor := " "
		style := itemStyle
		if i == m.actionMenu.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := clampLine(fmt.Sprintf("%s %s", cursor, option), width)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(statusStyle.Render(clampLine(" Enter to select · Esc to cancel", width)))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderPipelineView(m Model, width int) string {
	if width <= 0 {
		width = 80
	}
	if width < 24 {
		return lipgloss.NewStyle().Width(width).Render(" Terminal too narrow for pipeline view.")
	}
	parentWidth := max(12, width*20/100)
	currentWidth := max(12, width*40/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 12 {
		previewWidth = 12
		currentWidth = max(12, width-parentWidth-previewWidth)
	}
	height := m.height
	if height <= 5 {
		height = 5
	}
	contentHeight := height - 2
	parentLines := normalizeColumn(renderPipelineListPane(m, parentWidth-2), parentWidth-2, contentHeight)
	currentLines := normalizeColumn(renderPipelineStagesPane(m, currentWidth-2), currentWidth-2, contentHeight)
	previewLines := normalizeColumn(renderPipelineLogPane(m, previewWidth-2, contentHeight), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString(explorerBorderStyle.Render("┌" + strings.Repeat("─", parentWidth-2) + "┬" + strings.Repeat("─", currentWidth-2) + "┬" + strings.Repeat("─", previewWidth-2) + "┐"))
	b.WriteString("\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "%s%s%s%s%s%s%s\n",
			explorerBorderStyle.Render("│"),
			parentLines[i],
			explorerBorderStyle.Render("│"),
			currentLines[i],
			explorerBorderStyle.Render("│"),
			previewLines[i],
			explorerBorderStyle.Render("│"),
		)
	}
	b.WriteString(explorerBorderStyle.Render("└" + strings.Repeat("─", parentWidth-2) + "┴" + strings.Repeat("─", currentWidth-2) + "┴" + strings.Repeat("─", previewWidth-2) + "┘"))
	return b.String()
}

func renderExplorerParents(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString(explorerHeaderStyle.Render(clampLine("Parents", width)))
	b.WriteString("\n")
	parent := m.parentDirState()
	if parent == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" (root)", width)))
		b.WriteString("\n")
		return b.String()
	}
	pathLabel := parent.path
	if pathLabel == "" {
		pathLabel = "/"
	}
	b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Path: %s", pathLabel), width)))
	b.WriteString("\n")
	if parent.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading...", width)))
		b.WriteString("\n")
	}
	if parent.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+parent.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if len(parent.entries) == 0 {
		b.WriteString(explorerHintStyle.Render(clampLine(" (empty)", width)))
		b.WriteString("\n")
		return b.String()
	}
	for i, entry := range parent.entries {
		cursor := " "
		if i == parent.selected {
			cursor = ">"
		}
		name := entry.Name
		if entry.IsDir() {
			name += "/"
		}
		line := clampLine(fmt.Sprintf("%s%s %s", cursor, explorerEntryIcon(entry), name), width)
		b.WriteString(renderExplorerEntryLine(line, entry.IsDir(), i == parent.selected))
		b.WriteString("\n")
	}
	return b.String()
}

func renderExplorerCurrent(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Explorer · %s @ %s", m.explorer.project.PathWithNamespace, displayRef(m.explorer))
	b.WriteString(explorerHeaderStyle.Render(clampLine(title, width)))
	b.WriteString("\n")
	cur := m.currentDirState()
	if cur == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" No directory selected.", width)))
		b.WriteString("\n")
		return b.String()
	}
	pathLabel := cur.path
	if pathLabel == "" {
		pathLabel = "/"
	}
	b.WriteString(explorerPathStyle.Render(clampLine(fmt.Sprintf("Path: %s", pathLabel), width)))
	b.WriteString("\n")
	if cur.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading directory...", width)))
		b.WriteString("\n")
	}
	if cur.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+cur.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if len(cur.entries) == 0 && !cur.loading && cur.err == nil {
		b.WriteString(explorerHintStyle.Render(clampLine(" Directory is empty.", width)))
		b.WriteString("\n")
	}
	for i, entry := range cur.entries {
		cursor := " "
		if i == cur.selected {
			cursor = ">"
		}
		name := entry.Name
		if entry.IsDir() {
			name += "/"
		}
		line := clampLine(fmt.Sprintf("%s%s %s", cursor, explorerEntryIcon(entry), name), width)
		b.WriteString(renderExplorerEntryLine(line, entry.IsDir(), i == cur.selected))
		b.WriteString("\n")
	}
	b.WriteString(explorerHintStyle.Render(clampLine("Enter/→ descend · ←/Esc up", width)))
	b.WriteString("\n")
	return b.String()
}

func renderExplorerPreview(m Model, width, height int) string {
	b := &strings.Builder{}
	b.WriteString(explorerHeaderStyle.Render(clampLine("Preview", width)))
	b.WriteString("\n")
	preview := m.explorer.preview
	if preview.loading {
		b.WriteString(explorerHintStyle.Render(clampLine(" Loading file preview...", width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.err != nil {
		b.WriteString(explorerErrorStyle.Render(clampLine(" "+preview.err.Error(), width)))
		b.WriteString("\n")
		return b.String()
	}
	if preview.content == "" {
		b.WriteString(explorerHintStyle.Render(clampLine(" Select a file to preview.", width)))
		b.WriteString("\n")
		return b.String()
	}
	contentLines := previewContentLines(preview, width)
	visibleHeight := max(0, height-1)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	offset := preview.offset
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if visibleHeight > 0 && len(contentLines) > visibleHeight {
		contentLines = contentLines[offset:min(offset+visibleHeight, len(contentLines))]
	}
	for _, line := range contentLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderExplorerEntryLine(line string, isDir, selected bool) string {
	style := explorerFileStyle
	if isDir {
		style = explorerDirStyle
	}
	if selected {
		style = explorerSelectedStyle
	}
	return style.Render(line)
}

func explorerEntryIcon(entry gitlab.TreeNode) string {
	switch entry.Type {
	case "tree":
		return "📁"
	case "commit":
		return "🔗"
	case "blob":
		return "📄"
	default:
		return "•"
	}
}

var (
	titleStyle        = lipgloss.NewStyle().Bold(true)
	itemStyle         = lipgloss.NewStyle()
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	statusStyle       = lipgloss.NewStyle().Faint(true)
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	searchStyle       = lipgloss.NewStyle().Faint(true)
	progressStyle     = lipgloss.NewStyle().Faint(true)
	pipelineSuccess   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	pipelineFailed    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	pipelineRunning   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("208"))
	pipelinePending   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("179"))
	pipelineCanceled  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	pipelineSkipped   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("244"))
	pipelineUnknown   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("250"))
	rosePineText      = lipgloss.Color("#e0def4")
	rosePineMuted     = lipgloss.Color("#6e6a86")
	rosePineSubtle    = lipgloss.Color("#908caa")
	rosePineOverlay   = lipgloss.Color("#26233a")
	rosePineRose      = lipgloss.Color("#eb6f92")
	rosePinePine      = lipgloss.Color("#31748f")
	rosePineIris      = lipgloss.Color("#c4a7e7")
	rosePineLove      = lipgloss.Color("#eb6f92")
)

var (
	explorerBorderStyle   = lipgloss.NewStyle().Foreground(rosePineOverlay)
	explorerHeaderStyle   = lipgloss.NewStyle().Bold(true).Foreground(rosePineIris)
	explorerPathStyle     = lipgloss.NewStyle().Foreground(rosePineMuted)
	explorerHintStyle     = lipgloss.NewStyle().Foreground(rosePineSubtle)
	explorerErrorStyle    = lipgloss.NewStyle().Foreground(rosePineLove)
	explorerDirStyle      = lipgloss.NewStyle().Foreground(rosePinePine)
	explorerFileStyle     = lipgloss.NewStyle().Foreground(rosePineText)
	explorerSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(rosePineRose)
)

const maxPreviewLen = 8000

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func pipelineRefLabel(project gitlab.ProjectNode, state pipelineState) string {
	if strings.TrimSpace(state.ref) != "" {
		return state.ref
	}
	if strings.TrimSpace(project.DefaultBranch) != "" {
		return strings.TrimSpace(project.DefaultBranch)
	}
	return "all refs"
}

func pipelineStatusStyle(status string) lipgloss.Style {
	switch strings.ToLower(status) {
	case "success":
		return pipelineSuccess
	case "failed":
		return pipelineFailed
	case "running":
		return pipelineRunning
	case "pending", "created", "waiting_for_resource", "scheduled":
		return pipelinePending
	case "canceled", "canceled?":
		return pipelineCanceled
	case "skipped":
		return pipelineSkipped
	default:
		return pipelineUnknown
	}
}

func pipelineStatusBadge(status string) string {
	label := strings.ToUpper(strings.TrimSpace(status))
	if label == "" {
		label = "UNKNOWN"
	}
	return pipelineStatusStyle(status).Render(fmt.Sprintf("[%s]", label))
}

func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

func renderPipelineEntryLine(line string, selected, focused bool) string {
	if selected && focused {
		return explorerSelectedStyle.Render(line)
	}
	if selected {
		return explorerPathStyle.Render(line)
	}
	return explorerFileStyle.Render(line)
}

func latestJobForStage(jobs []gitlab.PipelineJob, stage string) *gitlab.PipelineJob {
	var selected *gitlab.PipelineJob
	for i := range jobs {
		job := &jobs[i]
		if job.Stage != stage {
			continue
		}
		if selected == nil || job.ID > selected.ID {
			selected = job
		}
	}
	return selected
}

func stageJobSummary(jobs []gitlab.PipelineJob, stage string) string {
	if len(jobs) == 0 {
		return ""
	}
	total := 0
	counts := map[string]int{}
	for _, job := range jobs {
		if job.Stage != stage {
			continue
		}
		total++
		status := strings.ToLower(job.Status)
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	if total == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, status := range []string{"success", "failed", "running", "pending", "canceled", "skipped", "manual", "unknown"} {
		if count := counts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, status))
		}
		if len(parts) >= 2 {
			break
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf(" (%d jobs)", total)
	}
	return fmt.Sprintf(" (%d jobs: %s)", total, strings.Join(parts, ", "))
}

func (m *Model) pipelineLogJob() *gitlab.PipelineJob {
	if m.pipelineView.logJobID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	jobs := m.pipelineView.jobsCache[pipeline.ID]
	for i := range jobs {
		if jobs[i].ID == m.pipelineView.logJobID {
			return &jobs[i]
		}
	}
	return nil
}

func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
		} else {
			line += " " + word
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

func clipPreview(s string) string {
	if len(s) <= maxPreviewLen {
		return s
	}
	return s[:maxPreviewLen] + "\n… truncated …"
}

func highlightPreviewContent(path, content string, width int) (string, bool, error) {
	if content == "" {
		return "", false, nil
	}
	if width <= 0 {
		width = 80
	}
	if highlighted, err := highlightWithBat(path, content, width); err == nil {
		return highlighted, true, nil
	}
	if highlighted, err := highlightWithGlamour(path, content, width); err == nil {
		return highlighted, true, nil
	}
	return content, false, nil
}

func highlightWithBat(path, content string, width int) (string, error) {
	batPath, err := exec.LookPath("bat")
	if err != nil {
		batPath, err = exec.LookPath("batcat")
		if err != nil {
			return "", err
		}
	}
	args := []string{
		"--color=always",
		"--style=plain",
		"--paging=never",
		"--wrap=character",
		"--terminal-width",
		strconv.Itoa(width),
	}
	if path != "" {
		args = append(args, "--file-name", path)
	}
	cmd := exec.Command(batPath, args...)
	cmd.Stdin = strings.NewReader(content)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

func highlightWithGlamour(path, content string, width int) (string, error) {
	lang := languageFromPath(path)
	fence := "```"
	for strings.Contains(content, fence) {
		fence += "`"
	}
	header := fence
	if lang != "" {
		header += lang
	}
	markdown := header + "\n" + content + "\n" + fence + "\n"
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(markdown)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

func languageFromPath(path string) string {
	base := filepath.Base(path)
	switch base {
	case "Dockerfile":
		return "dockerfile"
	case "Makefile":
		return "makefile"
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	return ext
}

func wrapPreviewLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if line == "" {
		return []string{""}
	}
	var segments []string
	var b strings.Builder
	currentWidth := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > width && b.Len() > 0 {
			segments = append(segments, b.String())
			b.Reset()
			currentWidth = 0
		}
		if rw > width {
			segments = append(segments, string(r))
			continue
		}
		b.WriteRune(r)
		currentWidth += rw
		if currentWidth == width {
			segments = append(segments, b.String())
			b.Reset()
			currentWidth = 0
		}
	}
	if b.Len() > 0 {
		segments = append(segments, b.String())
	}
	if len(segments) == 0 {
		return []string{""}
	}
	return segments
}

type projectsLoadedMsg struct {
	page       gitlab.ProjectPage
	err        error
	background bool
}

type cacheLoadedMsg struct {
	projects []gitlab.ProjectNode
	err      error
	found    bool
}

type cacheSavedMsg struct {
	err error
}

type treeLoadedMsg struct {
	projectID int
	path      string
	entries   []gitlab.TreeNode
	err       error
}

type fileLoadedMsg struct {
	projectID int
	path      string
	content   string
	err       error
}

type pipelineStatusMsg struct {
	projectID int
	ref       string
	pipeline  gitlab.PipelineSummary
	err       error
}

type pipelinesLoadedMsg struct {
	projectID int
	pipelines []gitlab.PipelineSummary
	err       error
}

type pipelineStagesLoadedMsg struct {
	projectID  int
	pipelineID int
	stages     []gitlab.PipelineStage
	err        error
}

type pipelineJobsLoadedMsg struct {
	projectID  int
	pipelineID int
	jobs       []gitlab.PipelineJob
	err        error
}

type pipelineLogLoadedMsg struct {
	projectID int
	jobID     int
	content   string
	err       error
}

type pipelineTickMsg struct{}

func fetchProjectsCmd(client *gitlab.Client, perPage, page int, background bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		pageData, err := client.ListProjects(ctx, gitlab.ProjectListOptions{PerPage: perPage, Page: page})
		return projectsLoadedMsg{page: pageData, err: err, background: background}
	}
}

func loadCacheCmd(cache *projectCache) tea.Cmd {
	return func() tea.Msg {
		projects, err := cache.Load()
		if err != nil {
			if errors.Is(err, errCacheNotFound) {
				return cacheLoadedMsg{found: false}
			}
			return cacheLoadedMsg{err: err}
		}
		return cacheLoadedMsg{projects: projects, found: true}
	}
}

func saveCacheCmd(cache *projectCache, projects []gitlab.ProjectNode) tea.Cmd {
	return func() tea.Msg {
		if err := cache.Save(projects); err != nil {
			return cacheSavedMsg{err: err}
		}
		return cacheSavedMsg{}
	}
}

func fetchTreeCmd(client *gitlab.Client, projectID int, ref, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		nodes, err := client.ListTree(ctx, projectID, gitlab.TreeListOptions{Ref: ref, Path: path})
		return treeLoadedMsg{projectID: projectID, path: path, entries: nodes, err: err}
	}
}

func fetchFileCmd(client *gitlab.Client, projectID int, ref, filePath string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		content, err := client.GetFileContent(ctx, projectID, filePath, ref)
		if err != nil {
			return fileLoadedMsg{projectID: projectID, path: filePath, err: err}
		}
		return fileLoadedMsg{projectID: projectID, path: filePath, content: clipPreview(content)}
	}
}

func fetchPipelineCmd(client *gitlab.Client, projectID int, ref string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		summary, err := client.LatestPipeline(ctx, projectID, ref)
		return pipelineStatusMsg{projectID: projectID, ref: ref, pipeline: summary, err: err}
	}
}

func fetchPipelinesCmd(client *gitlab.Client, projectID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pipelines, err := client.ListPipelines(ctx, projectID)
		return pipelinesLoadedMsg{projectID: projectID, pipelines: pipelines, err: err}
	}
}

func fetchPipelineStagesCmd(client *gitlab.Client, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		stages, err := client.PipelineStages(ctx, projectID, pipelineID)
		return pipelineStagesLoadedMsg{projectID: projectID, pipelineID: pipelineID, stages: stages, err: err}
	}
}

func fetchPipelineJobsCmd(client *gitlab.Client, projectID, pipelineID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		jobs, err := client.ListPipelineJobs(ctx, projectID, pipelineID)
		return pipelineJobsLoadedMsg{projectID: projectID, pipelineID: pipelineID, jobs: jobs, err: err}
	}
}

func fetchPipelineLogCmd(client *gitlab.Client, projectID, jobID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		content, err := client.GetJobTrace(ctx, projectID, jobID)
		if err != nil {
			return pipelineLogLoadedMsg{projectID: projectID, jobID: jobID, err: err}
		}
		return pipelineLogLoadedMsg{projectID: projectID, jobID: jobID, content: clipPreview(content)}
	}
}

func pipelineTickCmd() tea.Cmd {
	return tea.Tick(pipelineRefreshInterval, func(time.Time) tea.Msg {
		return pipelineTickMsg{}
	})
}

func renderListTitle(m Model) string {
	if m.search.query != "" {
		return fmt.Sprintf("Projects · Search “%s” (%d matches)", truncate(m.search.query, 20), len(m.visibleProjects()))
	}
	total := max(1, m.totalPages)
	page := max(1, m.page)
	return fmt.Sprintf("Projects · Page %d/%d · Cached %d/%d pages", page, total, m.pagesLoaded, total)
}

func renderSearchBar(m Model, width int) string {
	var line string
	if m.search.active {
		line = m.search.input.View()
	} else if m.search.query != "" {
		line = fmt.Sprintf("/ %s", m.search.query)
	} else {
		line = "/ (press / to search)"
	}
	return searchStyle.Render(clampLine(line, width))
}

func renderProgressBar(m Model, width int) string {
	if m.totalPages <= 1 {
		return ""
	}
	if !m.backgroundLoading && m.pagesLoaded >= m.totalPages {
		return progressStyle.Render("Cache warm")
	}
	total := max(1, m.totalPages)
	loaded := m.pagesLoaded
	if loaded > total {
		loaded = total
	}
	label := fmt.Sprintf("Caching %d/%d pages ", loaded, total)
	barWidth := width - lipgloss.Width(label) - 2
	if barWidth < 6 {
		return progressStyle.Render(clampLine(fmt.Sprintf("Caching %d/%d", loaded, total), width))
	}
	ratio := float64(loaded) / float64(total)
	filled := int(ratio * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return progressStyle.Render(clampLine(fmt.Sprintf("Caching %d/%d pages [%s]", loaded, total, bar), width))
}

func fuzzyMatch(target, pattern string) bool {
	targetRunes := []rune(strings.ToLower(target))
	patternRunes := []rune(strings.ToLower(pattern))
	if len(patternRunes) == 0 {
		return true
	}
	tIdx := 0
	for _, r := range patternRunes {
		found := false
		for tIdx < len(targetRunes) {
			if targetRunes[tIdx] == r {
				found = true
				tIdx++
				break
			}
			tIdx++
		}
		if !found {
			return false
		}
	}
	return true
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func displayRef(ex explorerState) string {
	if ex.ref == "" {
		return "main"
	}
	return ex.ref
}

func parentDir(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		return ""
	}
	return path[:idx]
}

func (m *Model) findDirIndex(path string) int {
	for i := range m.explorer.stack {
		if m.explorer.stack[i].path == path {
			return i
		}
	}
	return -1
}

func normalizeColumn(content string, width, height int) []string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	result := make([]string, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		result[i] = fitLine(line, width)
	}
	return result
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) > width {
		var b strings.Builder
		for _, r := range line {
			if lipgloss.Width(b.String()+string(r)) > width {
				break
			}
			b.WriteRune(r)
		}
		line = b.String()
	}
	pad := width - lipgloss.Width(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func clampLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	currentWidth := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > width-1 {
			break
		}
		b.WriteRune(r)
		currentWidth += rw
	}
	return b.String() + "…"
}

func clampLines(text string, width int) string {
	if width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = clampLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func writeDetailLine(b *strings.Builder, line string, width int) {
	b.WriteString(clampLine(line, width))
	b.WriteString("\n")
}

func previewContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(6, width*20/100)
	currentWidth := max(6, width*40/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 6 {
		previewWidth = 6
		currentWidth = max(6, width-parentWidth-previewWidth)
	}
	contentWidth := previewWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	return contentWidth
}

func previewContentHeight(height int) int {
	if height <= 5 {
		height = 5
	}
	return height - 2
}

func pipelineLogContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(12, width*20/100)
	currentWidth := max(12, width*40/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 12 {
		previewWidth = 12
		currentWidth = max(12, width-parentWidth-previewWidth)
	}
	contentWidth := previewWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	return contentWidth
}

func pipelineLogContentHeight(height int) int {
	if height <= 5 {
		height = 5
	}
	return height - 2
}

func previewContentLines(preview previewState, width int) []string {
	if preview.content == "" {
		return nil
	}
	lines := strings.Split(preview.content, "\n")
	maxLines := 200
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "… (truncated) …")
	}
	if preview.highlighted {
		return lines
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		segments := wrapPreviewLine(line, width)
		wrapped = append(wrapped, segments...)
	}
	return wrapped
}

func (m *Model) scrollPreview(delta int) bool {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || preview.err != nil || preview.content == "" {
		return false
	}
	height := previewContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		return false
	}
	width := previewContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if maxOffset == 0 {
		preview.offset = 0
		return false
	}
	step := max(1, visibleHeight/2)
	next := preview.offset + (delta * step)
	if next < 0 {
		next = 0
	}
	if next > maxOffset {
		next = maxOffset
	}
	if next == preview.offset {
		return false
	}
	preview.offset = next
	return true
}

func (m *Model) refreshPreviewHighlight() {
	if m.mode != modeExplorer {
		return
	}
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || !preview.highlighted {
		return
	}
	width := previewContentWidth(m.width)
	if preview.highlightWidth == width {
		return
	}
	highlighted, isHighlighted, err := highlightPreviewContent(preview.path, preview.raw, width)
	if err != nil && m.opts.Logger != nil {
		m.opts.Logger.Debug("rehighlight preview", "err", err)
		return
	}
	if isHighlighted {
		preview.content = highlighted
		preview.highlighted = true
		preview.highlightWidth = width
	}
	m.clampPreviewOffset()
}

func (m *Model) clampPreviewOffset() {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || preview.content == "" {
		preview.offset = 0
		return
	}
	height := previewContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		preview.offset = 0
		return
	}
	width := previewContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if preview.offset < 0 {
		preview.offset = 0
		return
	}
	if preview.offset > maxOffset {
		preview.offset = maxOffset
	}
}

func (m *Model) scrollPipelineLog(delta int) bool {
	if m.mode != modePipelines {
		return false
	}
	preview := &m.pipelineView.logPreview
	if preview.raw == "" && preview.content == "" {
		return false
	}
	if preview.loading || preview.err != nil {
		return false
	}
	height := pipelineLogContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		return false
	}
	width := pipelineLogContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if maxOffset == 0 {
		preview.offset = 0
		return false
	}
	step := max(1, visibleHeight/2)
	next := preview.offset + (delta * step)
	if next < 0 {
		next = 0
	}
	if next > maxOffset {
		next = maxOffset
	}
	if next == preview.offset {
		return false
	}
	preview.offset = next
	return true
}

func (m *Model) clampPipelineLogOffset() {
	if m.mode != modePipelines {
		return
	}
	preview := &m.pipelineView.logPreview
	if preview.content == "" || preview.loading {
		preview.offset = 0
		return
	}
	height := pipelineLogContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		preview.offset = 0
		return
	}
	width := pipelineLogContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if preview.offset < 0 {
		preview.offset = 0
		return
	}
	if preview.offset > maxOffset {
		preview.offset = maxOffset
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
