// state_explorer.go manages the three-pane file browser state: navigation
// stack (push/pop directories), preview loading, and clipboard operations.
package ui

import (
	"fmt"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// openExplorer transitions to the three-pane file browser. It resets all
// explorer state (stack, preview, bubbles lists) and starts a root tree
// fetch for the project's default branch. The preview viewport is initialized
// here with proper dimensions — all subsequent preview resets must preserve
// this viewport instance to avoid zero-sized rendering (see queueExplorerPreview).
func (m Model) openExplorer(project gitlab.ProjectNode) (tea.Model, tea.Cmd) {
	ref := project.DefaultBranch
	if ref == "" {
		ref = "main"
	}
	m.mode = modeExplorer

	// Initialize bubbles lists for explorer panes
	delegate := treeEntryDelegate{}
	parentList := newBareList(nil, delegate, 0, 0)
	currentList := newBareList(nil, delegate, 0, 0)

	// Initialize preview viewport with proper dimensions
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
//
// The preview viewport is preserved across the state reset — see
// queueExplorerPreview for the rationale.
func (m Model) descendDirectory(entry gitlab.TreeNode) (tea.Model, tea.Cmd) {
	// Copy current list items to parent list before descending
	m.explorer.parentList.SetItems(m.explorer.currentList.Items())
	if cur := m.currentDirState(); cur != nil {
		m.explorer.parentList.Select(cur.selected)
	}
	newState := dirState{
		path:    entry.Path,
		loading: true,
	}
	m.explorer.stack = append(m.explorer.stack, newState)
	m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), entry.Path)
}

func (m Model) navigateExplorerUp() (tea.Model, tea.Cmd) {
	if len(m.explorer.stack) <= 1 {
		m.closeExplorer("Back to projects")
		return m, nil
	}
	m.explorer.stack = m.explorer.stack[:len(m.explorer.stack)-1]
	m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
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
	m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
	return m, fetchTreeCmd(m.ctx, m.client, m.opts.APITimeout, m.explorer.project.ID, displayRef(m.explorer), cur.path)
}

func (m *Model) closeExplorer(status string) {
	if m.mode != modeMultiPanel {
		m.mode = modeProjects
	}
	m.explorer = explorerState{}
	if status != "" {
		m.status = status
	}
}

// queueExplorerPreview starts an async fetch for the currently selected entry's
// preview (directory listing or file content). It skips redundant fetches if
// the preview is already loading or cached for the same path.
//
// IMPORTANT: Every previewState reset must preserve the viewport field.
// The viewport is initialized once in openExplorer with proper dimensions;
// replacing it with a zero-valued viewport causes View() to return empty
// content since Width/Height would be 0.
func (m *Model) queueExplorerPreview() tea.Cmd {
	entry := m.selectedEntry()
	if entry == nil {
		m.explorer.preview = previewState{viewport: m.explorer.preview.viewport}
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

// copyExplorerURL copies the GitLab web URL for the selected file or directory.
func (m *Model) copyExplorerURL() {
	entry := m.selectedEntry()
	if entry == nil {
		m.status = "No file selected"
		return
	}
	if m.explorer.project.WebURL == "" {
		m.status = "Project has no URL"
		return
	}
	ref := displayRef(m.explorer)
	var kind string
	if entry.IsDir() {
		kind = "tree"
	} else {
		kind = "blob"
	}
	url := fmt.Sprintf("%s/-/%s/%s/%s", m.explorer.project.WebURL, kind, ref, entry.Path)
	if err := clipboard.WriteAll(url); err != nil {
		m.status = "Failed to copy URL"
		m.logError("copy clipboard", "err", err)
		return
	}
	m.status = fmt.Sprintf("Copied %s URL", entry.Name)
}
