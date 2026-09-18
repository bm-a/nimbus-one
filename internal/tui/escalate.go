package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"nimbus-one/internal/llm"
)

// Escalation options (last resort only, after every key/tier failed).
var escalationOptions = []string{"Enter New Key", "Switch Provider", "Retry Now"}

// Escalation is the in-session modal card. It interrupts CLI execution to
// accept a new key or switch configuration without killing the agent.
type Escalation struct {
	diagnostics    string
	recommendation string
	cursor         int
	choice         int // -1 = undecided/cancelled
	done           bool
}

// NewEscalation builds the modal from an llm.Escalation payload.
func NewEscalation(e llm.Escalation) *Escalation {
	rec := e.Recommend()
	diag := e.Summary()
	if strings.TrimSpace(e.LastErr) != "" {
		diag += "\n" + e.LastErr
	}
	return &Escalation{diagnostics: diag, recommendation: rec, choice: -1}
}

// NewEscalationText builds the modal from raw strings (tests/callers).
func NewEscalationText(diagnostics, recommendation string) *Escalation {
	if strings.TrimSpace(recommendation) == "" {
		recommendation = llm.DefaultRecommendation
	}
	return &Escalation{diagnostics: diagnostics, recommendation: recommendation, choice: -1}
}

// Init implements tea.Model.
func (e *Escalation) Init() tea.Cmd { return nil }

// Choice returns 0/1/2 for the chosen option, -1 when cancelled/undecided.
func (e *Escalation) Choice() int { return e.choice }

// Done reports the modal resolved.
func (e *Escalation) Done() bool { return e.done }

// Update implements tea.Model.
func (e *Escalation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	switch m.String() {
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < len(escalationOptions)-1 {
			e.cursor++
		}
	case "1", "2", "3":
		e.choice = int(m.String()[0] - '1')
		e.done = true
		return e, tea.Quit
	case "enter":
		e.choice = e.cursor
		e.done = true
		return e, tea.Quit
	case "esc", "ctrl+c", "q":
		e.choice = -1
		e.done = true
		return e, tea.Quit
	}
	return e, nil
}

// View implements tea.Model.
func (e *Escalation) View() string {
	var b strings.Builder
	b.WriteString(styleBox.Render(
		styleFail.Render("All keys exhausted")+"\n\n"+
			e.diagnostics+"\n\n"+
			styleTitle.Render("Recommendation: ")+e.recommendation+"\n",
	) + "\n")
	for i, opt := range escalationOptions {
		marker := "  "
		if i == e.cursor {
			marker = styleCursor.Render("▸ ")
		}
		b.WriteString(marker + opt + "\n")
	}
	b.WriteString(styleDim.Render("\n1/2/3 or ↑/↓+enter • esc cancels"))
	return b.String()
}

// RunEscalation shows the modal and returns the choice (0/1/2, -1 cancel).
func RunEscalation(e llm.Escalation) int {
	m := NewEscalation(e)
	p := tea.NewProgram(m)
	out, err := p.Run()
	if err != nil {
		return -1
	}
	if nm, ok := out.(*Escalation); ok {
		return nm.Choice()
	}
	return -1
}
