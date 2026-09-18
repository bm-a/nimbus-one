package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Providers offered by the wizard, cheapest/mobile-friendly first.
var wizardProviders = []string{"openrouter", "ollama", "groq", "deepseek", "gemini", "openai", "anthropic"}

// DefaultModels seeds the fallback-priority list per provider.
// These are SUGGESTIONS shown for the user to confirm, reorder, or remove
// — nothing is applied until the user presses enter.
func DefaultModels(provider string) []string {
	switch provider {
	case "openrouter":
		return []string{
			"meta/muse-spark-1.3-contributor",
			"openrouter/free-tier-fallback",
			"anthropic/claude-sonnet-4",
		}
	case "ollama":
		return []string{"llama3.1", "qwen2.5", "mistral"}
	default:
		return []string{provider + "/default", provider + "/fallback"}
	}
}

// pingResultMsg carries live key-validation output.
type pingResultMsg struct {
	model string
	err   error
}

// Wizard is the interactive setup flow:
//  0. provider select (up/down + enter)
//  1. key entry (masked, live ping against /v1/models)
//  2. fallback order (J/K or up/down move; enter confirms)
//  3. summary
type Wizard struct {
	step      int
	providers []string
	cursor    int
	input     textinput.Model
	provider  string
	key       string
	model     string
	pingMsg   string
	pinging   bool
	models    []string
	modelCur  int
	done      bool
	errMsg    string

	save func(provider, key string) error
	ping func(provider, key string) (string, error)
}

// NewWizard builds the wizard. save persists the key (e.g. secrets store),
// ping validates it live (e.g. GET /v1/models). Both are injected so tests
// and headless callers can stub them.
func NewWizard(save func(provider, key string) error, ping func(provider, key string) (string, error)) *Wizard {
	ti := textinput.New()
	ti.Placeholder = "paste API key (input hidden)"
	ti.EchoMode = textinput.EchoPassword
	ti.CharLimit = 256
	ti.Prompt = "key> "
	return &Wizard{
		providers: append([]string{}, wizardProviders...),
		input:     ti,
		save:      save,
		ping:      ping,
	}
}

// Init implements tea.Model.
func (w *Wizard) Init() tea.Cmd { return nil }

// Provider returns the chosen provider.
func (w *Wizard) Provider() string { return w.provider }

// Key returns the entered key.
func (w *Wizard) Key() string { return w.key }

// Model returns the confirmed primary model (first in priority order).
func (w *Wizard) Model() string { return w.model }

// Models returns the ordered fallback list.
func (w *Wizard) Models() []string { return append([]string{}, w.models...) }

// Done reports the wizard finished (saved or skipped).
func (w *Wizard) Done() bool { return w.done }

// Update implements tea.Model.
func (w *Wizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case pingResultMsg:
		w.pinging = false
		if m.err != nil {
			w.pingMsg = styleFail.Render("✗ key rejected: " + m.err.Error())
			return w, nil
		}
		w.pingMsg = styleOK.Render("✓ live — model: " + m.model)
		w.model = m.model
		if w.save != nil {
			if err := w.save(w.provider, w.key); err != nil {
				w.errMsg = "save failed: " + err.Error()
				return w, nil
			}
		}
		w.models = DefaultModels(w.provider)
		if w.model != "" {
			w.models = append([]string{w.model}, w.models...)
		}
		w.step = 2
		return w, nil
	case tea.KeyMsg:
		return w.updateKey(m)
	}
	if w.step == 1 {
		var cmd tea.Cmd
		w.input, cmd = w.input.Update(msg)
		return w, cmd
	}
	return w, nil
}

func (w *Wizard) updateKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := m.String()
	switch w.step {
	case 0:
		switch key {
		case "up", "k":
			if w.cursor > 0 {
				w.cursor--
			}
		case "down", "j":
			if w.cursor < len(w.providers)-1 {
				w.cursor++
			}
		case "enter":
			w.provider = w.providers[w.cursor]
			if w.provider == "ollama" {
				// local — no key needed
				w.models = DefaultModels(w.provider)
				w.model = w.models[0]
				w.step = 2
				return w, nil
			}
			w.step = 1
			return w, w.input.Focus()
		case "esc", "ctrl+c", "q":
			w.done = true
		}
	case 1:
		switch key {
		case "enter":
			w.key = strings.TrimSpace(w.input.Value())
			if w.key == "" {
				w.pingMsg = styleWarn.Render("paste a key first, or esc to skip")
				return w, nil
			}
			if w.ping == nil {
				w.models = DefaultModels(w.provider)
				w.step = 2
				return w, nil
			}
			w.pinging = true
			w.pingMsg = styleDim.Render("… validating against /v1/models")
			prov, k := w.provider, w.key
			ping := w.ping
			return w, func() tea.Msg {
				model, err := ping(prov, k)
				return pingResultMsg{model: model, err: err}
			}
		case "esc":
			w.step = 0
		default:
			var cmd tea.Cmd
			w.input, cmd = w.input.Update(m)
			return w, cmd
		}
	case 2:
		switch key {
		case "up", "k":
			if w.modelCur > 0 {
				w.modelCur--
			}
		case "down", "j":
			if w.modelCur < len(w.models)-1 {
				w.modelCur++
			}
		case "K", "J":
			// Shift-move: reorder priority
			i := w.modelCur
			n := i - 1
			if key == "J" {
				n = i + 1
			}
			if n >= 0 && n < len(w.models) {
				w.models[i], w.models[n] = w.models[n], w.models[i]
				w.modelCur = n
			}
		case "x", "X", "d":
			// Remove a suggestion — your fallbacks, your choice.
			if len(w.models) > 0 && w.modelCur < len(w.models) {
				w.models = append(w.models[:w.modelCur], w.models[w.modelCur+1:]...)
				if w.modelCur >= len(w.models) && w.modelCur > 0 {
					w.modelCur--
				}
			}
		case "enter":
			if len(w.models) > 0 {
				w.model = w.models[0]
			}
			w.step = 3
			w.done = true
		case "esc":
			w.step = 1
		}
	}
	return w, nil
}

// View implements tea.Model.
func (w *Wizard) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Nimbus-One setup") + "\n\n")
	switch w.step {
	case 0:
		b.WriteString("Choose a provider (↑/↓ + enter):\n\n")
		for i, p := range w.providers {
			marker := "  "
			if i == w.cursor {
				marker = styleCursor.Render("▸ ")
			}
			line := marker + p
			if p == "openrouter" {
				line += styleDim.Render("  — recommended, free tier, many models")
			}
			if p == "ollama" {
				line += styleDim.Render("  — local, no key, runs on-device")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString(styleDim.Render("\nq to quit"))
	case 1:
		b.WriteString("Provider: " + styleOK.Render(w.provider) + "\n")
		b.WriteString(w.input.View() + "\n")
		if w.pingMsg != "" {
			b.WriteString(w.pingMsg + "\n")
		}
		b.WriteString(styleDim.Render("\nenter validates live • esc back"))
	case 2:
		b.WriteString("Your fallback order — suggestions only, nothing applied until you confirm.\n")
		b.WriteString(styleDim.Render("j/k move • Shift+J/K reorder • x removes • enter confirms (empty list = primary only)\n\n"))
		for i, m := range w.models {
			marker := "  "
			if i == w.modelCur {
				marker = styleCursor.Render("▸ ")
			}
			b.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, m))
		}
	case 3:
		b.WriteString(styleOK.Render("✓ configured") + "\n")
		b.WriteString(fmt.Sprintf("provider=%s model=%s\n", w.provider, w.model))
		if w.errMsg != "" {
			b.WriteString(styleFail.Render(w.errMsg) + "\n")
		}
	}
	return b.String()
}

// RunWizard launches the interactive wizard on the terminal.
// It returns an error when no TTY is available — callers fall back to
// headless `nimbus-one init --auto` in that case.
func RunWizard(save func(provider, key string) error, ping func(provider, key string) (string, error)) (*Wizard, error) {
	w := NewWizard(save, ping)
	p := tea.NewProgram(w)
	m, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("wizard: %w (hint: use `nimbus-one init --auto` without a terminal)", err)
	}
	nw, ok := m.(*Wizard)
	if !ok {
		return nil, fmt.Errorf("wizard: unexpected model type")
	}
	return nw, nil
}
