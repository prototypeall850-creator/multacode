// Package tui styles: cyan-accent dark theme matching the
// counter-app prototype (compact top card, bordered panels).
package tui

import "github.com/charmbracelet/lipgloss"

var (
	cyan = lipgloss.Color("6")
	dimC = lipgloss.Color("8")
	redC = lipgloss.Color("9")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	dimStyle   = lipgloss.NewStyle().Bold(true).Foreground(dimC)
	hintStyle  = lipgloss.NewStyle().Foreground(dimC)
	errStyle   = lipgloss.NewStyle().Foreground(redC)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 2).
			Align(lipgloss.Center)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cyan).
			Padding(0, 1)

	inputFrameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	toolStyle      = lipgloss.NewStyle().Foreground(dimC)

	suggestSelStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(cyan)
)
