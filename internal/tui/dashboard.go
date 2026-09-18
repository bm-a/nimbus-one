package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Stat is one key's dashboard snapshot (mirrors pool.KeyStats without
// importing the pool, keeping the TUI decoupled).
type Stat struct {
	ID        string
	State     string
	AvgMs     float64
	InFlight  int
	CooldownS int64
}

// tickMsg refreshes the dashboard clock.
type tickMsg time.Time

// Dashboard renders live key health badges.
type Dashboard struct {
	stats   []Stat
	updated time.Time
	width   int
}

// NewDashboard builds a dashboard from an initial snapshot.
func NewDashboard(stats []Stat) *Dashboard {
	return &Dashboard{stats: stats, updated: time.Now()}
}

// UpdateStats replaces the snapshot.
func (d *Dashboard) UpdateStats(stats []Stat) {
	d.stats = stats
	d.updated = time.Now()
}

// Init implements tea.Model.
func (d *Dashboard) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update implements tea.Model.
func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		d.width = m.Width
	case tickMsg:
		d.updated = time.Time(m)
		return d, tick()
	case tea.KeyMsg:
		if m.String() == "q" || m.String() == "esc" || m.String() == "ctrl+c" {
			return d, tea.Quit
		}
	}
	return d, nil
}

// View implements tea.Model.
func (d *Dashboard) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Nimbus One key health") + styleDim.Render("  (q to quit)") + "\n\n")
	if len(d.stats) == 0 {
		b.WriteString(styleWarn.Render("no keys configured — run `nimbus-one config`") + "\n")
		return b.String()
	}
	for _, s := range d.stats {
		line := fmt.Sprintf("%s %s  %.0fms avg  inflight=%d", badge(s.State), s.ID, s.AvgMs, s.InFlight)
		if s.State == "CoolingDown" || s.State == "Cooling Down" {
			line += fmt.Sprintf("  (%ds remaining)", s.CooldownS)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(styleDim.Render("\nupdated " + d.updated.Format("15:04:05")))
	return b.String()
}

// RunDashboard shows the dashboard until quit. Callers needing live data
// should run their own program loop and call UpdateStats on each tick;
// this helper renders the initial snapshot interactively.
func RunDashboard(initial []Stat) error {
	d := NewDashboard(initial)
	_, err := tea.NewProgram(d).Run()
	return err
}
