package ui

import "github.com/charmbracelet/lipgloss"

var (
	baseStyle        = lipgloss.NewStyle().Padding(0, 1)
	titleStyle       = lipgloss.NewStyle().Bold(true)
	cursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	highlightStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F5F"))
	tabActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	tabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func columnStyle(width int) lipgloss.Style {
	return baseStyle.Copy().Width(width)
}
