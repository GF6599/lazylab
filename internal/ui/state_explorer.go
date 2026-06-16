// state_explorer.go manages the three-pane file browser state: navigation
// stack (push/pop directories), preview loading, and clipboard operations.
package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// openExplorer initializes the three-pane file browser for a project. It resets
// all explorer state (stack, preview, bubbles lists) and starts a root tree
// fetch for the project's default branch. The preview viewport is initialized
// here with proper dimensions — all subsequent preview resets must preserve
// this viewport instance to avoid zero-sized rendering (see resetPreview).
//
// The caller's mode is not changed; in multi-panel mode the explorer appears
// as an overlay (detected via m.explorer.project.ID != 0), while standalone
// callers can set modeExplorer explicitly.
func (m Model) openExplorer(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	ref := project.DefaultBranch
	if ref == "" {
		ref = "main"
	}

	delegate := treeEntryDelegate{}
	parentList := newBareList(nil, delegate, 0, 0)
	currentList := newBareList(nil, delegate, 0, 0)

	previewVp := viewport.New(previewContentWidth(m.width), previewContentHeight(m.height))

	m.explorer = explorerState{
		project:     project,
		ref:         ref,
		stack:       []dirState{{path: "", loading: true}},
		parentList:  parentList,
		currentList: currentList,
		preview:     previewState{viewport: previewVp},
	}
	m.status = fmt.Sprintf("Browsing %s", project.PathWithNamespace)
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, project.ID, ref, "")
}

// descendDirectory pushes a new dirState onto the explorer stack and fetches
// its tree listing. Before descending, it copies the current list items into
// the parent list so the left pane shows the correct context.
func (m Model) descendDirectory(entry gitlab.TreeNode) (tea.Model, tea.Cmd) {
	m.explorer.parentList.SetItems(m.explorer.currentList.Items())
	if cur := m.currentDirState(); cur != nil {
		m.explorer.parentList.Select(cur.selected)
	}
	newState := dirState{
		path:    entry.Path,
		loading: true,
	}
	m.explorer.stack = append(m.explorer.stack, newState)
	m.explorer.resetPreview()
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m Model) navigateExplorerUp() (tea.Model, tea.Cmd) {
	if len(m.explorer.stack) <= 1 {
		m = m.closeExplorer("Back to projects")
		return m, ensurePipelineTickCmd(&m)
	}
	m.explorer.stack = m.explorer.stack[:len(m.explorer.stack)-1]
	m.explorer.resetPreview()
	return m, m.queueExplorerPreview()
}

// reloadExplorerPath re-fetches the current directory's tree listing. Preserves
// the stack depth and selection index so the user stays at the same location;
// only the entries and preview are reset.
func (m Model) reloadExplorerPath() (tea.Model, tea.Cmd) {
	cur := m.currentDirState()
	if cur == nil {
		return m, nil
	}
	cur.loading = true
	cur.err = nil
	cur.entries = nil
	m.explorer.resetPreview()
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), cur.path)
}

func (m Model) closeExplorer(status string) Model {
	if m.mode != modeMultiPanel {
		m.mode = modeProjects
	}
	m.explorer = explorerState{}
	if status != "" {
		m.status = status
	}
	return m
}

// resetPreview clears the preview content while preserving the viewport
// dimensions. The viewport is initialized once in openExplorer with proper
// dimensions; replacing it with a zero-valued viewport causes View() to
// return empty content since Width/Height would be 0.
func (e *explorerState) resetPreview() {
	e.preview = previewState{viewport: e.preview.viewport}
}

// queueExplorerPreview starts an async fetch for the currently selected entry's
// preview (directory listing or file content). It skips redundant fetches if
// the preview is already loading or cached for the same path.
func (m *Model) queueExplorerPreview() tea.Cmd {
	entry := m.selectedEntry()
	if entry == nil {
		m.explorer.resetPreview()
		return nil
	}
	if entry.IsDir() {
		m.explorer.preview = previewState{
			path:     entry.Path,
			loading:  true,
			viewport: m.explorer.preview.viewport,
		}
		return fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
	}
	if m.explorer.preview.loading && m.explorer.preview.path == entry.Path {
		return nil
	}
	if !m.explorer.preview.loading && m.explorer.preview.path == entry.Path && m.explorer.preview.content != "" && m.explorer.preview.err == nil {
		return nil
	}
	m.explorer.preview = previewState{path: entry.Path, loading: true, viewport: m.explorer.preview.viewport}
	return fetchFileCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m Model) currentDirState() *dirState {
	if len(m.explorer.stack) == 0 {
		return nil
	}
	return &m.explorer.stack[len(m.explorer.stack)-1]
}

func (m Model) parentDirState() *dirState {
	if len(m.explorer.stack) < 2 {
		return nil
	}
	return &m.explorer.stack[len(m.explorer.stack)-2]
}

func (m Model) selectedEntry() *gitlab.TreeNode {
	dir := m.currentDirState()
	if dir == nil || len(dir.entries) == 0 {
		return nil
	}
	if dir.selected < 0 || dir.selected >= len(dir.entries) {
		return nil
	}
	return &dir.entries[dir.selected]
}

// copyExplorerURL returns the model plus a Cmd that copies the GitLab web URL
// for the selected file or directory to the clipboard off the event loop.
// Guard paths still set m.status synchronously and return a nil Cmd.
func (m Model) copyExplorerURL() (Model, tea.Cmd) {
	entry := m.selectedEntry()
	if entry == nil {
		m.status = "No file selected"
		return m, nil
	}
	if m.explorer.project.WebURL == "" {
		m.status = "Project has no URL"
		return m, nil
	}
	ref := displayRef(m.explorer)
	kind := "blob"
	if entry.IsDir() {
		kind = "tree"
	}
	url := fmt.Sprintf("%s/-/%s/%s/%s", m.explorer.project.WebURL, kind, ref, entry.Path)
	return m, writeClipboardCmd(url, fmt.Sprintf("Copied %s URL", entry.Name))
}
