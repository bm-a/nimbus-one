// Package tui provides the zero-stress visual interface: setup wizard,
// key manager, health dashboard, and escalation modal. Built on
// Charmbracelet bubbletea/lipgloss/bubbles (pure Go, no Cgo) so the same
// code runs on Termux, Linux, macOS, and Windows terminals.
package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleOK     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	styleWarn   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	styleFail   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleCursor = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// badge renders a color-coded state badge for dashboards and modals.
func badge(state string) string {
	switch state {
	case "Healthy", "ok", "OK":
		return styleOK.Render("[Healthy]")
	case "CoolingDown", "Cooling Down", "warn":
		return styleWarn.Render("[Cooling Down]")
	case "Dead", "fail", "FAIL":
		return styleFail.Render("[Dead]")
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")).Render("[Standby]")
	}
}
