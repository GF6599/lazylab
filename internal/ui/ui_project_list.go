package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gitlab-tui-codex/internal/gitlab"
)

const (
	modeProjects = "projects"
	modeExplorer = "explorer"
)

const pipelineRefreshInterval = 5 * time.Second

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
	path    string
	content string
	loading bool
	err     error
}

type explorerState struct {
	project gitlab.ProjectNode
	ref     string
	stack   []dirState
	preview previewState
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
	case tea.KeyMsg:
		if m.mode == modeExplorer {
			return m.handleExplorerKey(msg)
		}
		return m.handleProjectKey(msg)
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
			builder.WriteString(name)
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
		m.status = "Failed to load file"
		return m, nil
	}
	m.explorer.preview.err = nil
	m.explorer.preview.content = clipPreview(msg.content)
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
			return m.openExplorer(project)
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
		}
		return fetchTreeCmd(m.client, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
	}
	if m.explorer.preview.loading && m.explorer.preview.path == entry.Path {
		return nil
	}
	if !m.explorer.preview.loading && m.explorer.preview.path == entry.Path && m.explorer.preview.content != "" && m.explorer.preview.err == nil {
		return nil
	}
	m.explorer.preview = previewState{path: entry.Path, loading: true}
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
	if m.mode == modeExplorer {
		return renderExplorerView(m, width)
	}
	listWidth := width / 2
	detailWidth := width - listWidth

	left := renderListPane(m, listWidth)
	right := renderDetailPane(m, detailWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func renderListPane(m Model, width int) string {
	b := &strings.Builder{}
	title := titleStyle.Render(renderListTitle(m))
	b.WriteString(title)
	b.WriteString("\n")
	if m.loading {
		b.WriteString(" Loading projects...\n")
	}
	if m.err != nil {
		b.WriteString(errorStyle.Render(" " + m.err.Error()))
		b.WriteString("\n")
	}
	if len(m.allProjects) == 0 && !m.loading && m.err == nil {
		b.WriteString(" No projects found.\n")
	}
	visible := m.visibleProjects()
	if m.search.query == "" && !m.pagesReady[m.page] && !m.loading {
		b.WriteString(fmt.Sprintf(" Page %d is still loading...\n", m.page))
	}
	for i, p := range visible {
		cursor := " "
		style := itemStyle
		if i == m.selected {
			cursor = ">"
			style = selectedItemStyle
		}
		line := fmt.Sprintf("%s %s", cursor, truncate(p.PathWithNamespace, width-3))
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	progress := renderProgressBar(m, width)
	if progress != "" {
		b.WriteString(progress)
		b.WriteString("\n")
	}
	b.WriteString(renderSearchBar(m))
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(statusStyle.Render(" " + m.status))
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func renderDetailPane(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString(titleStyle.Render("Details"))
	b.WriteString("\n")
	visible := m.visibleProjects()
	if len(visible) == 0 {
		b.WriteString(" Select a project to see more information.\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	project := visible[m.selected]
	fmt.Fprintf(b, " Name: %s\n", project.Name)
	fmt.Fprintf(b, " Path: %s\n", project.PathWithNamespace)
	fmt.Fprintf(b, " Visibility: %s\n", project.Visibility)
	fmt.Fprintf(b, " Stars: %d\n", project.StarCount)
	if !project.LastActivityAt.IsZero() {
		fmt.Fprintf(b, " Last Activity: %s\n", project.LastActivityAt.Format(time.RFC1123))
	}
	fmt.Fprintf(b, " URL: %s\n", project.WebURL)
	if project.DefaultBranch != "" {
		fmt.Fprintf(b, " Default Branch: %s\n", project.DefaultBranch)
	}
	if project.SSHURLToRepo != "" {
		fmt.Fprintf(b, " Clone: git clone %s\n", project.SSHURLToRepo)
	}
	if project.Description != "" {
		b.WriteString("\n")
		b.WriteString(wrapText(project.Description, width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(renderPipelineSection(m, project, width))
	b.WriteString("\n")
	return lipgloss.NewStyle().Width(width).Render(b.String())
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
	if width < 80 {
		width = 80
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
	previewLines := normalizeColumn(renderExplorerPreview(m, previewWidth-2), previewWidth-2, contentHeight)

	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", parentWidth-2) + "┬" + strings.Repeat("─", currentWidth-2) + "┬" + strings.Repeat("─", previewWidth-2) + "┐\n")
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&b, "│%s│%s│%s│\n", parentLines[i], currentLines[i], previewLines[i])
	}
	b.WriteString("└" + strings.Repeat("─", parentWidth-2) + "┴" + strings.Repeat("─", currentWidth-2) + "┴" + strings.Repeat("─", previewWidth-2) + "┘")
	return b.String()
}

func renderExplorerParents(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString("Parents\n")
	parent := m.parentDirState()
	if parent == nil {
		b.WriteString(" (root)\n")
		return b.String()
	}
	pathLabel := parent.path
	if pathLabel == "" {
		pathLabel = "/"
	}
	fmt.Fprintf(b, "Path: %s\n", pathLabel)
	if parent.loading {
		b.WriteString(" Loading...\n")
	}
	if parent.err != nil {
		b.WriteString(" " + parent.err.Error() + "\n")
		return b.String()
	}
	if len(parent.entries) == 0 {
		b.WriteString(" (empty)\n")
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
		line := fmt.Sprintf("%s%s", cursor, truncate(name, width-1))
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func renderExplorerCurrent(m Model, width int) string {
	b := &strings.Builder{}
	title := fmt.Sprintf("Explorer · %s @ %s", m.explorer.project.PathWithNamespace, displayRef(m.explorer))
	b.WriteString(title)
	b.WriteString("\n")
	cur := m.currentDirState()
	if cur == nil {
		b.WriteString(" No directory selected.\n")
		return b.String()
	}
	pathLabel := cur.path
	if pathLabel == "" {
		pathLabel = "/"
	}
	fmt.Fprintf(b, "Path: %s\n", pathLabel)
	if cur.loading {
		b.WriteString(" Loading directory...\n")
	}
	if cur.err != nil {
		b.WriteString(" " + cur.err.Error() + "\n")
		return b.String()
	}
	if len(cur.entries) == 0 && !cur.loading && cur.err == nil {
		b.WriteString(" Directory is empty.\n")
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
		line := fmt.Sprintf("%s%s", cursor, truncate(name, width-1))
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("Enter/→ descend · ←/Esc up\n")
	return b.String()
}

func renderExplorerPreview(m Model, width int) string {
	b := &strings.Builder{}
	b.WriteString("Preview\n")
	preview := m.explorer.preview
	if preview.loading {
		b.WriteString(" Loading file preview...\n")
		return b.String()
	}
	if preview.err != nil {
		b.WriteString(" " + preview.err.Error() + "\n")
		return b.String()
	}
	if preview.content == "" {
		b.WriteString(" Select a file to preview.\n")
		return b.String()
	}
	lines := strings.Split(preview.content, "\n")
	maxLines := 200
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "… (truncated) …")
	}
	for _, line := range lines {
		wrapped := wrapPreviewLine(line, width)
		for _, segment := range wrapped {
			b.WriteString(segment)
			b.WriteString("\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

var (
	titleStyle        = lipgloss.NewStyle().Bold(true)
	itemStyle         = lipgloss.NewStyle()
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	statusStyle       = lipgloss.NewStyle().Faint(true)
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	searchStyle       = lipgloss.NewStyle().Faint(true)
	progressStyle     = lipgloss.NewStyle().Faint(true)
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

func renderSearchBar(m Model) string {
	if m.search.active {
		return searchStyle.Render(m.search.input.View())
	}
	if m.search.query != "" {
		return searchStyle.Render(fmt.Sprintf("/ %s", m.search.query))
	}
	return searchStyle.Render("/ (press / to search)")
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
	barWidth := width - 10
	if barWidth < 10 {
		barWidth = 10
	}
	ratio := float64(loaded) / float64(total)
	filled := int(ratio * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return progressStyle.Render(fmt.Sprintf("Caching %d/%d pages [%s]", loaded, total, bar))
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
