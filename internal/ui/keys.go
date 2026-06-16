// keys.go defines the canonical set of keybindings for the application.
//
// All bindings are declared in one place so that help text, key matching,
// and mode-specific subsets stay in sync. The keyMap struct implements
// help.KeyMap (via ShortHelp/FullHelp) for integration with the Bubbles
// help component.
//
// Mode-specific functions (projectsKeyMap, explorerKeyMap, pipelinesKeyMap)
// return subsets for contextual help rendering — they don't create new
// bindings, just select which ones to display.

package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap holds every keybinding the TUI recognizes. Bindings use vim-style
// mnemonics (j/k/h/l, g/G, Ctrl-D/U) to feel natural for terminal users.
type keyMap struct {
	// Navigation
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	HalfUp   key.Binding
	HalfDown key.Binding
	Top      key.Binding
	Bottom   key.Binding

	// Actions
	Enter      key.Binding
	Back       key.Binding
	Quit       key.Binding
	Search     key.Binding
	Refresh    key.Binding
	Retry      key.Binding
	Copy       key.Binding
	Favorite   key.Binding
	Explorer   key.Binding
	Cancel     key.Binding
	Play       key.Binding
	Comment    key.Binding
	CycleTab   key.Binding
	CycleTabRv key.Binding
	MoveFavUp  key.Binding
	MoveFavDn  key.Binding
	Theme      key.Binding

	// Pagination
	NextPage key.Binding
	PrevPage key.Binding
	Jump     key.Binding

	// Preview scrolling
	ScrollUp   key.Binding
	ScrollDown key.Binding

	// MR actions
	CreateMR          key.Binding
	ResolveDiscussion key.Binding

	// Help
	Help       key.Binding
	CloseHelp  key.Binding
	ClearError key.Binding

	// Multi-panel layout / routing
	NextPanel      key.Binding
	PrevPanel      key.Binding
	ToggleLayout   key.Binding
	NextScreenMode key.Binding
	JumpPanel      key.Binding
}

// newKeyMap returns the default keybinding set. Each binding carries its own
// help text so that ShortHelp/FullHelp can render contextual hints.
func newKeyMap() keyMap {
	return keyMap{
		// Navigation
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "left/back"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "right/enter"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdn", "page down"),
		),
		HalfUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "half page up"),
		),
		HalfDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "half page down"),
		),
		Top: key.NewBinding(
			key.WithKeys("<", "g"),
			key.WithHelp("</g", "jump to top"),
		),
		Bottom: key.NewBinding(
			key.WithKeys(">", "G"),
			key.WithHelp(">/G", "jump to bottom"),
		),

		// Actions
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select/open"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc", "backspace"),
			key.WithHelp("esc", "back"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r", "ctrl+r"),
			key.WithHelp("r", "refresh"),
		),
		Retry: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "retry pipeline/job"),
		),
		Copy: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("Ctrl+O", "copy URL"),
		),
		Favorite: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "toggle favorite"),
		),
		Explorer: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "file explorer"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("C"),
			key.WithHelp("C", "cancel pipeline/job"),
		),
		Play: key.NewBinding(
			key.WithKeys("P"),
			key.WithHelp("P", "play manual job"),
		),
		Comment: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "new comment"),
		),
		CreateMR: key.NewBinding(
			key.WithKeys("N"),
			key.WithHelp("N", "new MR"),
		),
		ResolveDiscussion: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "resolve discussion"),
		),
		CycleTab: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "next tab"),
		),
		CycleTabRv: key.NewBinding(
			key.WithKeys("T"),
			key.WithHelp("T", "prev tab"),
		),
		MoveFavUp: key.NewBinding(
			key.WithKeys("{"),
			key.WithHelp("{", "move fav up"),
		),
		MoveFavDn: key.NewBinding(
			key.WithKeys("}"),
			key.WithHelp("}", "move fav down"),
		),
		Theme: key.NewBinding(
			key.WithKeys("~"),
			key.WithHelp("~", "cycle theme"),
		),

		// Pagination
		NextPage: key.NewBinding(
			key.WithKeys("]"),
			key.WithHelp("]", "next page"),
		),
		PrevPage: key.NewBinding(
			key.WithKeys("["),
			key.WithHelp("[", "prev page"),
		),
		Jump: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"),
			key.WithHelp("1-9", "jump to recent"),
		),

		// Preview scrolling
		ScrollUp: key.NewBinding(
			key.WithKeys("K"),
			key.WithHelp("K", "scroll preview up"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("J"),
			key.WithHelp("J", "scroll preview down"),
		),

		// Help
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		CloseHelp: key.NewBinding(
			key.WithKeys("?", "esc"),
			key.WithHelp("?/esc", "close help"),
		),
		ClearError: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("ctrl+l", "clear status"),
		),

		// Multi-panel layout / routing
		NextPanel: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next panel"),
		),
		PrevPanel: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev panel"),
		),
		ToggleLayout: key.NewBinding(
			key.WithKeys("+", "-"),
			key.WithHelp("+/-", "toggle layout"),
		),
		NextScreenMode: key.NewBinding(
			key.WithKeys("="),
			key.WithHelp("=", "next screen mode"),
		),
		JumpPanel: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5"),
			key.WithHelp("1-5", "jump to panel"),
		),
	}
}

// ShortHelp returns the minimal set of bindings for the collapsed help bar.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Up, k.Down, k.Enter, k.Search, k.Quit}
}

// FullHelp returns grouped bindings for the expanded help overlay.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right},
		{k.HalfUp, k.HalfDown, k.Top, k.Bottom},
		{k.Enter, k.Back, k.Refresh, k.Retry},
		{k.Search, k.Copy, k.NextPage, k.PrevPage},
		{k.ScrollUp, k.ScrollDown, k.ClearError, k.Quit},
		{k.Favorite, k.Explorer, k.CycleTab, k.Comment},
		{k.Cancel, k.Play, k.MoveFavUp, k.MoveFavDn},
		{k.Theme},
	}
}

// projectsShortHelp returns the minimal binding set for the projects mode help bar.
func projectsShortHelp(k keyMap) []key.Binding {
	return []key.Binding{k.Search, k.Enter, k.Refresh, k.Copy, k.Help, k.Quit}
}

// explorerShortHelp returns the minimal binding set for the explorer mode help bar.
func explorerShortHelp(k keyMap) []key.Binding {
	return []key.Binding{k.Enter, k.Back, k.ScrollDown, k.Refresh, k.Copy, k.Help, k.Quit}
}

// pipelinesShortHelp returns the minimal binding set for the pipelines mode help bar.
func pipelinesShortHelp(k keyMap) []key.Binding {
	return []key.Binding{k.Retry, k.Refresh, k.ScrollDown, k.NextPage, k.Copy, k.Help, k.Quit}
}

// projectsKeyMap returns keybindings for projects mode
func projectsKeyMap() []key.Binding {
	k := newKeyMap()
	return []key.Binding{
		k.Up, k.Down, k.HalfUp, k.HalfDown,
		k.Top, k.Bottom, k.Enter, k.Search,
		k.Refresh, k.Left, k.Right,
		k.Copy, k.Help, k.Quit,
	}
}

// explorerKeyMap returns keybindings for explorer mode
func explorerKeyMap() []key.Binding {
	k := newKeyMap()
	return []key.Binding{
		k.Up, k.Down, k.Left, k.Right,
		k.HalfUp, k.HalfDown, k.Top, k.Bottom,
		k.ScrollUp, k.ScrollDown, k.Refresh, k.Back,
		k.Copy, k.Help, k.Quit,
	}
}

// pipelinesKeyMap returns keybindings for pipelines mode
func pipelinesKeyMap() []key.Binding {
	k := newKeyMap()
	return []key.Binding{
		k.Up, k.Down, k.Left, k.Right,
		k.HalfUp, k.HalfDown, k.Top, k.Bottom,
		k.ScrollUp, k.ScrollDown, k.Refresh, k.Retry,
		k.Cancel, k.Play, k.NextPage, k.PrevPage, k.Copy, k.Back,
		k.Help, k.Quit,
	}
}

// multiPanelKeyMap returns the full binding list for the ? help overlay
// in multi-panel mode, tailored to the currently focused panel.
func multiPanelKeyMap(panel PanelID, prevActive PanelID, m *Model) []key.Binding {
	k := newKeyMap()
	resolve := k.ResolveDiscussion
	switch panel {
	case PanelProjects:
		return []key.Binding{
			k.Up, k.Down, k.HalfUp, k.HalfDown,
			k.Top, k.Bottom, k.Enter, k.Search,
			k.Favorite, k.Explorer, k.CycleTab,
			k.NextPage, k.PrevPage, k.Refresh,
			k.MoveFavUp, k.MoveFavDn,
			k.Copy, k.Theme, k.Help, k.Quit,
		}
	case PanelPipelines:
		return []key.Binding{
			k.Up, k.Down, k.HalfUp, k.HalfDown,
			k.Top, k.Bottom, k.Left, k.Right,
			k.ScrollUp, k.ScrollDown, k.Retry, k.Cancel,
			k.CycleTab, k.NextPage, k.PrevPage,
			k.Refresh, k.Copy, k.Theme, k.Help, k.Quit,
		}
	case PanelStages:
		return []key.Binding{
			k.Up, k.Down, k.HalfUp, k.HalfDown,
			k.Top, k.Bottom, k.Left, k.Right,
			k.ScrollUp, k.ScrollDown, k.Retry, k.Cancel,
			k.Play, k.CycleTab, k.Refresh, k.Copy,
			k.Theme, k.Help, k.Quit,
		}
	case PanelMRs:
		return []key.Binding{
			k.Up, k.Down, k.HalfUp, k.HalfDown,
			k.Top, k.Bottom, k.Left, k.Right,
			k.ScrollUp, k.ScrollDown, k.Comment,
			k.CreateMR, k.CycleTab, k.NextPage, k.PrevPage,
			k.Copy, k.Theme, k.Help, k.Quit,
		}
	case PanelDetail:
		isMR := prevActive == PanelMRs
		if isMR && m != nil {
			switch m.mrView.detailTab {
			case mrDetailTabComments:
				return []key.Binding{
					k.Up, k.Down, k.ScrollUp, k.ScrollDown,
					k.Top, k.Bottom, resolve, k.Enter,
					k.Comment, k.CycleTab, k.Left, k.Copy,
					k.Theme, k.Help, k.Quit,
				}
			case mrDetailTabDiff:
				return []key.Binding{
					k.Up, k.Down, k.ScrollUp, k.ScrollDown,
					k.Top, k.Bottom, k.Comment, k.CycleTab,
					k.Left, k.Copy, k.Theme, k.Help, k.Quit,
				}
			default:
				return []key.Binding{
					k.ScrollUp, k.ScrollDown, k.Top, k.Bottom,
					k.Comment, k.CycleTab, k.Left, k.Copy,
					k.Theme, k.Help, k.Quit,
				}
			}
		}
		return []key.Binding{
			k.ScrollUp, k.ScrollDown, k.Top, k.Bottom,
			k.Retry, k.CycleTab, k.Left, k.Copy,
			k.Theme, k.Help, k.Quit,
		}
	default:
		return []key.Binding{k.Theme, k.Help, k.Quit}
	}
}
