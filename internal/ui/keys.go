package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap defines all keybindings for the application
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
	Enter   key.Binding
	Back    key.Binding
	Quit    key.Binding
	Search  key.Binding
	Refresh key.Binding
	Retry   key.Binding
	Copy    key.Binding
	Yank    key.Binding

	// Pagination
	NextPage key.Binding
	PrevPage key.Binding
	Jump     key.Binding

	// Preview scrolling
	ScrollUp   key.Binding
	ScrollDown key.Binding

	// Help
	Help       key.Binding
	CloseHelp  key.Binding
	ClearError key.Binding
}

// newKeyMap creates a new keyMap with all bindings
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
			key.WithKeys("c"),
			key.WithHelp("c", "copy URL"),
		),
		Yank: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "yank/copy"),
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
	}
}

// ShortHelp returns bindings to show in the short help view
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Up, k.Down, k.Enter, k.Search, k.Quit}
}

// FullHelp returns bindings to show in the full help view
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right},
		{k.HalfUp, k.HalfDown, k.Top, k.Bottom},
		{k.Enter, k.Back, k.Refresh, k.Retry},
		{k.Search, k.Copy, k.NextPage, k.PrevPage},
		{k.ScrollUp, k.ScrollDown, k.ClearError, k.Quit},
	}
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
		k.ScrollUp, k.ScrollDown, k.Back,
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
		k.NextPage, k.PrevPage, k.Back,
		k.Help, k.Quit,
	}
}
