package ui

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	gclient "gitlab-tui-codex/internal/gitlab"
	"gitlab-tui-codex/pkg/logging"
)

type viewMode int

const (
	viewPipelines viewMode = iota
	viewFiles
)

type focusArea int

const (
	focusProjects focusArea = iota
	focusContent
)

// Model drives the Bubble Tea program.
type model struct {
	client *gclient.Client
	logger *slog.Logger

	width        int
	height       int
	ready        bool
	view         viewMode
	focus        focusArea
	status       string
	err          error
	pathStack    []string
	filePreview  string
	previewPath  string
	previewBusy  bool
	projectBusy  bool
	pipelineBusy bool
	treeBusy     bool

	projects      []gclient.Project
	projectCursor int

	pipelines      []gclient.Pipeline
	pipelineCursor int

	treeNodes  []gclient.TreeNode
	fileCursor int
}

// NewModel constructs a Bubble Tea model backed by a GitLab client.
func NewModel(client *gclient.Client) tea.Model {
	return model{
		client:      client,
		logger:      logging.Logger().With("component", "ui"),
		view:        viewFiles,
		status:      "Loading projects...",
		projectBusy: true,
	}
}

// Init triggers the initial project load.
func (m model) Init() tea.Cmd {
	m.log().Debug("initializing bubble tea model")
	return fetchProjectsCmd(m.client)
}

// Update reacts to UI events and API results.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.log().Debug("window resized", "width", msg.Width, "height", msg.Height)
	case tea.KeyMsg:
		key := msg.String()
		m.log().Debug("key pressed", "key", key)
		switch key {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "left", "h":
			m.focus = focusProjects
		case "right", "l":
			m.focus = focusContent
		case "tab":
			m.log().Debug("toggling tab via tab key")
			m.toggleView()
		case "up", "k":
			if m.focus == focusProjects {
				cmds = append(cmds, m.moveProjectCursor(-1))
			} else {
				cmds = append(cmds, m.moveContentCursor(-1))
			}
		case "down", "j":
			if m.focus == focusProjects {
				cmds = append(cmds, m.moveProjectCursor(1))
			} else {
				cmds = append(cmds, m.moveContentCursor(1))
			}
		case "enter":
			if m.focus == focusProjects {
				m.focus = focusContent
				m.log().Debug("moved focus to content pane via enter")
			} else {
				cmds = append(cmds, m.handleEnter())
			}
		case "p":
			m.view = viewPipelines
			m.focus = focusContent
			m.log().Debug("switched to pipelines view")
		case "f":
			m.view = viewFiles
			m.focus = focusContent
			m.log().Debug("switched to files view")
		case "ctrl+o":
			cmds = append(cmds, m.copyCloneCommand())
		}
	case projectsLoadedMsg:
		m.projectBusy = false
		m.projects = msg
		sortProjectsByName(m.projects)
		m.log().Info("projects loaded", "count", len(msg))
		if len(m.projects) == 0 {
			m.status = "No projects available. Check your GitLab token's permissions."
		} else {
			m.status = fmt.Sprintf("Loaded %d projects", len(m.projects))
			cmds = append(cmds, m.refreshProjectData())
		}
	case pipelinesLoadedMsg:
		if m.currentProjectID() != msg.ProjectID {
			break
		}
		m.pipelineBusy = false
		m.pipelines = msg.Pipelines
		m.log().Debug("pipelines loaded", "project_id", msg.ProjectID, "count", len(msg.Pipelines))
		if m.pipelineCursor >= len(m.pipelines) {
			m.pipelineCursor = max(0, len(m.pipelines)-1)
		}
	case treeLoadedMsg:
		if m.currentProjectID() != msg.ProjectID || m.currentPath() != msg.Path {
			break
		}
		m.treeBusy = false
		m.treeNodes = msg.Nodes
		m.log().Debug("repository tree loaded", "project_id", msg.ProjectID, "path", msg.Path, "count", len(msg.Nodes))
		if m.fileCursor >= len(m.fileEntries()) {
			m.fileCursor = max(0, len(m.fileEntries())-1)
		}
	case fileContentLoadedMsg:
		if m.currentProjectID() != msg.ProjectID || msg.Path != m.previewPath {
			break
		}
		m.previewBusy = false
		m.filePreview = msg.Content
		m.log().Debug("file content loaded", "project_id", msg.ProjectID, "path", msg.Path, "length", len(msg.Content))
	case errMsg:
		m.err = msg
		m.status = fmt.Sprintf("Error: %v", msg)
		m.previewBusy = false
		m.pipelineBusy = false
		m.treeBusy = false
		m.projectBusy = false
		m.log().Error("ui encountered error", "err", msg)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) moveProjectCursor(offset int) tea.Cmd {
	if len(m.projects) == 0 {
		return nil
	}
	prev := m.projectCursor
	m.projectCursor = clamp(m.projectCursor+offset, 0, len(m.projects)-1)
	if prev == m.projectCursor {
		return nil
	}
	m.log().Debug("project selection changed", "new_index", m.projectCursor)
	m.pipelineCursor = 0
	m.fileCursor = 0
	m.pathStack = nil
	m.filePreview = ""
	m.previewPath = ""
	return m.refreshProjectData()
}

func (m *model) refreshProjectData() tea.Cmd {
	if len(m.projects) == 0 {
		return nil
	}
	m.pipelineBusy = true
	m.treeBusy = true
	m.status = fmt.Sprintf("Fetching data for %s...", m.activeProject().Name)
	m.log().Info("refreshing project data", "project_id", m.currentProjectID(), "project_name", m.activeProject().Name)

	return tea.Batch(
		fetchPipelinesCmd(m.client, m.currentProjectID()),
		fetchTreeCmd(m.client, m.currentProjectID(), m.activeBranch(), m.currentPath()),
	)
}

func (m *model) moveContentCursor(offset int) tea.Cmd {
	switch m.view {
	case viewPipelines:
		if len(m.pipelines) == 0 {
			return nil
		}
		m.pipelineCursor = clamp(m.pipelineCursor+offset, 0, len(m.pipelines)-1)
	case viewFiles:
		entries := m.fileEntries()
		if len(entries) == 0 {
			return nil
		}
		m.fileCursor = clamp(m.fileCursor+offset, 0, len(entries)-1)
	}
	return nil
}

func (m *model) handleEnter() tea.Cmd {
	switch m.view {
	case viewPipelines:
		// Nothing to fetch on enter for pipelines yet.
	case viewFiles:
		entries := m.fileEntries()
		if len(entries) == 0 {
			return nil
		}
		entry := entries[m.fileCursor]
		switch entry.Type {
		case "tree":
			m.pathStack = append(m.pathStack, entry.Name)
			m.fileCursor = 0
			m.treeBusy = true
			m.log().Debug("entering directory", "path", m.currentPath())
			return fetchTreeCmd(m.client, m.currentProjectID(), m.activeBranch(), m.currentPath())
		case "up":
			if len(m.pathStack) > 0 {
				m.pathStack = m.pathStack[:len(m.pathStack)-1]
				m.fileCursor = 0
				m.treeBusy = true
				m.log().Debug("moving up directory", "path", m.currentPath())
				return fetchTreeCmd(m.client, m.currentProjectID(), m.activeBranch(), m.currentPath())
			}
		case "blob":
			m.previewBusy = true
			m.previewPath = entry.Path
			m.log().Info("loading file preview", "path", entry.Path)
			return fetchFileCmd(m.client, m.currentProjectID(), m.activeBranch(), entry.Path)
		}
	}
	return nil
}

func (m *model) toggleView() {
	if m.view == viewPipelines {
		m.view = viewFiles
		m.log().Debug("toggled to files view")
	} else {
		m.view = viewPipelines
		m.log().Debug("toggled to pipelines view")
	}
}

func (m *model) copyCloneCommand() tea.Cmd {
	if len(m.projects) == 0 {
		m.status = "No project selected to copy."
		return nil
	}
	project := m.activeProject()
	if project.SSHURL == "" {
		m.status = "Selected project has no SSH clone URL."
		return nil
	}

	cloneCmd := fmt.Sprintf("git clone %s", project.SSHURL)
	if err := clipboard.WriteAll(cloneCmd); err != nil {
		m.log().Error("failed to copy clone command", "err", err)
		m.status = "Unable to copy clone command. Check logs for details."
		return nil
	}

	m.log().Info("copied clone command", "project_id", project.ID, "project_name", project.Name)
	m.status = fmt.Sprintf("Copied '%s' to clipboard", cloneCmd)
	return nil
}

func (m model) View() string {
	if !m.ready {
		return "Loading..."
	}

	leftWidth, middleWidth, rightWidth := m.columnWidths()
	left := columnStyle(leftWidth).Render(m.renderProjects())
	middle := columnStyle(middleWidth).Render(m.renderContentColumn())
	right := columnStyle(rightWidth).Render(m.renderDetailsColumn())

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, middle, right)
	status := mutedStyle.Render(m.status)
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}

	return fmt.Sprintf("%s\n\n%s", body, status)
}

func (m model) columnWidths() (int, int, int) {
	width := m.width
	if width <= 0 {
		width = 100
	}
	left := 28
	right := 40
	if width-left-right < 20 {
		right = max(30, width-left-20)
	}
	middle := max(20, width-left-right)
	return left, middle, right
}

func (m model) renderProjects() string {
	if len(m.projects) == 0 {
		if m.projectBusy {
			return "Loading projects..."
		}
		if m.err != nil {
			return m.err.Error()
		}
		return "No projects found."
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("Projects\n\n"))
	for i, proj := range m.projects {
		line := fmt.Sprintf("%s\n%s\n", proj.Name, mutedStyle.Render(proj.PathWithNamespace))
		if i == m.projectCursor {
			if m.focus == focusProjects {
				line = cursorStyle.Render(line)
			} else {
				line = highlightStyle.Render(line)
			}
		}
		b.WriteString(line)
		b.WriteRune('\n')
	}
	return b.String()
}

func (m model) renderContentColumn() string {
	header := m.renderTabs()
	var body string
	switch m.view {
	case viewPipelines:
		body = m.renderPipelines()
	case viewFiles:
		body = m.renderFiles()
	}
	return fmt.Sprintf("%s\n\n%s", header, body)
}

func (m model) renderTabs() string {
	tabs := []string{
		tabInactiveStyle.Render("Pipelines"),
		tabInactiveStyle.Render("Files"),
	}
	if m.view == viewPipelines {
		tabs[0] = tabActiveStyle.Render("Pipelines")
	} else {
		tabs[1] = tabActiveStyle.Render("Files")
	}
	return strings.Join(tabs, "  ")
}

func (m model) renderPipelines() string {
	if m.pipelineBusy {
		return "Loading pipelines..."
	}
	if len(m.pipelines) == 0 {
		return "No pipelines found."
	}

	var b strings.Builder
	for i, pipe := range m.pipelines {
		line := fmt.Sprintf("#%d %s [%s]\n", pipe.IID, strings.ToUpper(pipe.Status), pipe.Ref)
		if i == m.pipelineCursor && m.focus == focusContent && m.view == viewPipelines {
			line = cursorStyle.Render(line)
		} else if i == m.pipelineCursor {
			line = highlightStyle.Render(line)
		}
		b.WriteString(line)
	}
	return b.String()
}

func (m model) renderFiles() string {
	if m.treeBusy {
		return "Loading files..."
	}
	entries := m.fileEntries()
	if len(entries) == 0 {
		return "No files found for branch " + m.activeBranch()
	}
	var b strings.Builder
	for i, entry := range entries {
		icon := "   "
		switch entry.Type {
		case "tree":
			icon = "[D]"
		case "blob":
			icon = "[F]"
		case "up":
			icon = "[^]"
		}
		line := fmt.Sprintf("%s %s\n", icon, entry.Name)
		if i == m.fileCursor && m.focus == focusContent && m.view == viewFiles {
			line = cursorStyle.Render(line)
		} else if i == m.fileCursor {
			line = highlightStyle.Render(line)
		}
		b.WriteString(line)
	}
	return b.String()
}

func (m model) renderDetailsColumn() string {
	switch m.view {
	case viewPipelines:
		return m.renderPipelineDetails()
	case viewFiles:
		return m.renderFilePreview()
	default:
		return ""
	}
}

func (m model) renderPipelineDetails() string {
	if len(m.pipelines) == 0 {
		return "Select a pipeline to view details."
	}
	if m.pipelineCursor >= len(m.pipelines) {
		return ""
	}
	pipe := m.pipelines[m.pipelineCursor]
	lines := []string{
		titleStyle.Render(fmt.Sprintf("Pipeline #%d", pipe.IID)),
		fmt.Sprintf("Status: %s", strings.ToUpper(pipe.Status)),
		fmt.Sprintf("Ref: %s", pipe.Ref),
		fmt.Sprintf("SHA: %s", truncate(pipe.SHA, 12)),
		fmt.Sprintf("Updated: %s", pipe.UpdatedAt.Format(time.RFC822)),
		fmt.Sprintf("URL: %s", pipe.WebURL),
	}
	return strings.Join(lines, "\n")
}

func (m model) renderFilePreview() string {
	if m.previewBusy {
		return "Loading file..."
	}
	if m.filePreview == "" {
		return "Select a file to preview."
	}
	header := fmt.Sprintf("%s\n\n", titleStyle.Render(m.previewPath))
	return header + m.filePreview
}

func (m model) activeProject() gclient.Project {
	if len(m.projects) == 0 {
		return gclient.Project{}
	}
	return m.projects[m.projectCursor]
}

func (m model) currentProjectID() int {
	if len(m.projects) == 0 {
		return 0
	}
	return m.projects[m.projectCursor].ID
}

func (m model) activeBranch() string {
	if proj := m.activeProject(); proj.DefaultBranch != "" {
		return proj.DefaultBranch
	}
	return "main"
}

func (m model) currentPath() string {
	if len(m.pathStack) == 0 {
		return ""
	}
	return path.Join(m.pathStack...)
}

func (m model) fileEntries() []gclient.TreeNode {
	nodes := make([]gclient.TreeNode, 0, len(m.treeNodes)+1)
	if len(m.pathStack) > 0 {
		nodes = append(nodes, gclient.TreeNode{
			Name: "..",
			Path: strings.Join(m.pathStack[:len(m.pathStack)-1], "/"),
			Type: "up",
		})
	}
	nodes = append(nodes, m.treeNodes...)
	return nodes
}

func sortProjectsByName(projects []gclient.Project) {
	sort.SliceStable(projects, func(i, j int) bool {
		left := strings.ToLower(projects[i].Name)
		right := strings.ToLower(projects[j].Name)
		if left == right {
			return strings.ToLower(projects[i].PathWithNamespace) < strings.ToLower(projects[j].PathWithNamespace)
		}
		return left < right
	})
}

type projectsLoadedMsg []gclient.Project

type pipelinesLoadedMsg struct {
	ProjectID int
	Pipelines []gclient.Pipeline
}

type treeLoadedMsg struct {
	ProjectID int
	Path      string
	Nodes     []gclient.TreeNode
}

type fileContentLoadedMsg struct {
	ProjectID int
	Path      string
	Content   string
}

type errMsg struct{ error }

func fetchProjectsCmd(client *gclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		logging.Logger().Debug("fetchProjectsCmd executing")
		projects, err := client.ListProjects(ctx)
		if err != nil {
			return errMsg{err}
		}
		return projectsLoadedMsg(projects)
	}
}

func fetchPipelinesCmd(client *gclient.Client, projectID int) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		logging.Logger().Debug("fetchPipelinesCmd executing", "project_id", projectID)
		pipelines, err := client.ListPipelines(ctx, projectID)
		if err != nil {
			return errMsg{err}
		}
		return pipelinesLoadedMsg{ProjectID: projectID, Pipelines: pipelines}
	}
}

func fetchTreeCmd(client *gclient.Client, projectID int, ref, path string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		logging.Logger().Debug("fetchTreeCmd executing", "project_id", projectID, "path", path, "ref", ref)
		nodes, err := client.ListTree(ctx, projectID, ref, path)
		if err != nil {
			return errMsg{err}
		}
		return treeLoadedMsg{ProjectID: projectID, Path: path, Nodes: nodes}
	}
}

func fetchFileCmd(client *gclient.Client, projectID int, ref, filePath string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		logging.Logger().Debug("fetchFileCmd executing", "project_id", projectID, "path", filePath, "ref", ref)
		content, err := client.GetFileContent(ctx, projectID, ref, filePath)
		if err != nil {
			return errMsg{err}
		}
		return fileContentLoadedMsg{ProjectID: projectID, Path: filePath, Content: content}
	}
}

func clamp(val, minVal, maxVal int) int {
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

func (m model) log() *slog.Logger {
	if m.logger != nil {
		return m.logger
	}
	return logging.Logger().With("component", "ui")
}
