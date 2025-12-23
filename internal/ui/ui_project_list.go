package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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
	project         gitlab.ProjectNode
	pipelines       []gitlab.PipelineSummary
	selected        int
	loading         bool
	err             error
	stageCache      map[int][]gitlab.PipelineStage
	stageLoading    map[int]bool
	stageErr        map[int]error
	stageSelected   int
	jobsCache       map[int][]gitlab.PipelineJob
	jobsLoading     map[int]bool
	jobsErr         map[int]error
	logCache        map[int]string
	logLoading      map[int]bool
	logErr          map[int]error
	logPreview      previewState
	logJobID        int
	pendingLogJobID int
	logAutoFollow   bool
	focus           pipelineFocus
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
	prevSelectedID := 0
	if m.pipelineView.selected >= 0 && m.pipelineView.selected < len(m.pipelineView.pipelines) {
		prevSelectedID = m.pipelineView.pipelines[m.pipelineView.selected].ID
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
	selectedSame := false
	if prevSelectedID != 0 {
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
	m.pipelineView.logCache[msg.jobID] = msg.content
	if m.pipelineView.logErr != nil {
		delete(m.pipelineView.logErr, msg.jobID)
	}
	if msg.jobID != m.pipelineView.logJobID && msg.jobID != m.pipelineView.pendingLogJobID {
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
	m.pipelineView.logJobID = msg.jobID
	if m.pipelineView.logAutoFollow {
		m.tailPipelineLog()
	} else {
		m.clampPipelineLogOffset()
	}
	return m, nil
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
	case "ctrl+d":
		if m.scrollPreview(1) {
			return m, nil
		}
		if cur.selected < len(cur.entries)-1 {
			step := listPageStep(m.height)
			cur.selected = min(cur.selected+step, len(cur.entries)-1)
			return m, m.queueExplorerPreview()
		}
	case "ctrl+u":
		if m.scrollPreview(-1) {
			return m, nil
		}
		if cur.selected > 0 {
			step := listPageStep(m.height)
			cur.selected = max(cur.selected-step, 0)
			return m, m.queueExplorerPreview()
		}
	case "<":
		if m.scrollPreviewToStart() {
			return m, nil
		}
		if cur.selected > 0 {
			cur.selected = 0
			return m, m.queueExplorerPreview()
		}
	case ">":
		if m.scrollPreviewToEnd() {
			return m, nil
		}
		if cur.selected < len(cur.entries)-1 {
			cur.selected = len(cur.entries) - 1
			return m, m.queueExplorerPreview()
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
	case "ctrl+d":
		if m.scrollPipelineLog(1) {
			return m, nil
		}
		step := listPageStep(m.height)
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected < len(m.pipelineView.pipelines)-1 {
				m.pipelineView.selected = min(m.pipelineView.selected+step, len(m.pipelineView.pipelines)-1)
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			stages := m.selectedPipelineStages()
			if m.pipelineView.stageSelected < len(stages)-1 {
				m.pipelineView.stageSelected = min(m.pipelineView.stageSelected+step, len(stages)-1)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "ctrl+u":
		if m.scrollPipelineLog(-1) {
			return m, nil
		}
		step := listPageStep(m.height)
		if m.pipelineView.focus == pipelineFocusPipelines {
			if m.pipelineView.selected > 0 {
				m.pipelineView.selected = max(m.pipelineView.selected-step, 0)
				m.pipelineView.stageSelected = 0
				m.resetPipelineLogPreview()
				cmd := m.queuePipelineStagesForSelection()
				return m, tea.Batch(cmd, m.queuePipelineJobsForSelection())
			}
		} else {
			if m.pipelineView.stageSelected > 0 {
				m.pipelineView.stageSelected = max(m.pipelineView.stageSelected-step, 0)
				m.resetPipelineLogPreview()
				return m, m.queuePipelineLogPreview()
			}
		}
	case "<":
		if m.scrollPipelineLogToStart() {
			return m, nil
		}
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
			m.resetPipelineLogPreview()
			return m, m.queuePipelineLogPreview()
		}
	case ">":
		if m.scrollPipelineLogToEnd() {
			return m, nil
		}
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
		project:       project,
		loading:       true,
		stageCache:    make(map[int][]gitlab.PipelineStage),
		stageLoading:  make(map[int]bool),
		stageErr:      make(map[int]error),
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
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
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

func (m *Model) queuePipelineViewRefresh() tea.Cmd {
	if m.pipelineView.project.ID == 0 {
		return nil
	}
	var cmds []tea.Cmd
	if !m.pipelineView.loading {
		m.pipelineView.loading = true
		cmds = append(cmds, fetchPipelinesCmd(m.client, m.pipelineView.project.ID))
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
	return fetchPipelineStagesCmd(m.client, m.pipelineView.project.ID, pipeline.ID)
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
			}
			m.pipelineView.logJobID = job.ID
			if m.pipelineView.logAutoFollow {
				m.tailPipelineLog()
			} else {
				m.clampPipelineLogOffset()
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
	return fetchPipelineLogCmd(m.client, m.pipelineView.project.ID, job.ID)
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
	return fetchPipelineLogCmd(m.client, m.pipelineView.project.ID, job.ID)
}

func (m *Model) resetPipelineLogPreview() {
	m.pipelineView.logPreview = previewState{}
	m.pipelineView.logJobID = 0
	m.pipelineView.pendingLogJobID = 0
	m.pipelineView.logAutoFollow = true
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
