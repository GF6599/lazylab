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
}

type searchState struct {
	active bool
	query  string
	input  textinput.Model
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
		client: client,
		opts:   opts,
		page:   1,
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
	if m.cache != nil {
		return loadCacheCmd(m.cache)
	}
	return fetchProjectsCmd(m.client, m.opts.ProjectsPerPage, 1, false)
}

// Update reacts to Bubble Tea messages and returns the new model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case projectsLoadedMsg:
		return m.handleProjectsLoaded(msg)
	case cacheLoadedMsg:
		return m.handleCacheLoaded(msg)
	case cacheSavedMsg:
		if msg.err != nil && m.opts.Logger != nil {
			m.opts.Logger.Error("save cache", "err", msg.err)
		}
		return m, nil
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
	if m.cache != nil && len(m.allProjects) > 0 {
		cmds = append(cmds, saveCacheCmd(m.cache, m.allProjects))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.search.active {
		switch msg.Type {
		case tea.KeyEsc:
			m.search.active = false
			m.search.query = ""
			m.search.input.Reset()
			m.search.input.Blur()
			m.ensureSelectionBounds()
			m.status = "Search cleared"
			return m, nil
		case tea.KeyEnter:
			m.search.active = false
			m.search.query = m.search.input.Value()
			m.search.input.Blur()
			m.status = fmt.Sprintf("Search: %s", m.search.query)
			m.ensureSelectionBounds()
			return m, nil
		}
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.search.input, cmd = m.search.input.Update(msg)
		m.search.query = m.search.input.Value()
		m.ensureSelectionBounds()
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
	return m, nil
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

// View renders the UI to the terminal.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
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
	return lipgloss.NewStyle().Width(width).Render(b.String())
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

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
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
