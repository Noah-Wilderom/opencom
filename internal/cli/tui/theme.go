// internal/cli/tui/theme.go
package tui

import "github.com/charmbracelet/lipgloss"

// theme is the TUI's centralised palette + reusable styles. One
// dark-first palette for v1; lipgloss auto-adjusts colour rendering
// for the user's terminal background.
var theme = struct {
	accent   lipgloss.Style
	dim      lipgloss.Style
	ok       lipgloss.Style
	warn     lipgloss.Style
	err      lipgloss.Style
	key      lipgloss.Style
	head     lipgloss.Style
	box      lipgloss.Style
	modalBox lipgloss.Style
}{
	accent:   lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")),
	dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("#6e7681")),
	ok:       lipgloss.NewStyle().Foreground(lipgloss.Color("#56d364")),
	warn:     lipgloss.NewStyle().Foreground(lipgloss.Color("#f0883e")),
	err:      lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")),
	key:      lipgloss.NewStyle().Foreground(lipgloss.Color("#d2a8ff")),
	head:     lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3")).Bold(true),
	box:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#30363d")),
	modalBox: lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("#58a6ff")).Padding(1, 2),
}
