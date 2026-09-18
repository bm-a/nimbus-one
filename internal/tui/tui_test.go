package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	// Build a KeyMsg the same way the harness does: Type from string.
	var k tea.KeyMsg
	switch s {
	case "up":
		k = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		k = tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		if len(s) == 1 {
			k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		} else {
			k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
	}
	return k
}

func TestWizardSelectsProvider(t *testing.T) {
	w := NewWizard(nil, nil)
	m, _ := w.Update(keyMsg("down"))
	w = m.(*Wizard)
	m, _ = w.Update(keyMsg("enter"))
	w = m.(*Wizard)
	if w.Provider() != "ollama" {
		t.Fatalf("expected ollama after down+enter, got %q", w.Provider())
	}
	// ollama skips key entry straight to ordering
	if len(w.Models()) == 0 {
		t.Fatal("ollama should seed models")
	}
}

func TestWizardKeySaved(t *testing.T) {
	var gotP, gotK string
	save := func(p, k string) error { gotP, gotK = p, k; return nil }
	ping := func(p, k string) (string, error) { return "test-model", nil }
	w := NewWizard(save, ping)
	// select first provider (openrouter)
	m, _ := w.Update(keyMsg("enter"))
	w = m.(*Wizard)
	if w.Provider() != "openrouter" {
		t.Fatalf("provider: %q", w.Provider())
	}
	// type key via textinput update through wizard
	for _, ch := range "sk-test-123" {
		m, _ = w.Update(keyMsg(string(ch)))
		w = m.(*Wizard)
	}
	m, cmd := w.Update(keyMsg("enter"))
	w = m.(*Wizard)
	if cmd == nil {
		t.Fatal("expected ping command")
	}
	msg := cmd()
	m, _ = w.Update(msg)
	w = m.(*Wizard)
	if gotP != "openrouter" || gotK != "sk-test-123" {
		t.Fatalf("save got %q %q", gotP, gotK)
	}
	if w.Model() != "test-model" {
		t.Fatalf("model: %q", w.Model())
	}
}

func TestWizardPingReject(t *testing.T) {
	ping := func(p, k string) (string, error) { return "", errors.New("401 bad key") }
	w := NewWizard(nil, ping)
	m, _ := w.Update(keyMsg("enter"))
	w = m.(*Wizard)
	m, cmd := w.Update(keyMsg("enter")) // empty key → warning, no cmd
	_ = cmd
	// type then submit
	for _, ch := range "bad" {
		m, _ = w.Update(keyMsg(string(ch)))
		w = m.(*Wizard)
	}
	m, cmd = w.Update(keyMsg("enter"))
	w = m.(*Wizard)
	if cmd == nil {
		t.Fatal("expected ping cmd")
	}
	m, _ = w.Update(cmd())
	w = m.(*Wizard)
	if !strings.Contains(w.View(), "rejected") {
		t.Fatal("view should show rejection")
	}
}

func TestWizardReorder(t *testing.T) {
	w := NewWizard(nil, nil)
	w.step = 2
	w.models = []string{"a", "b", "c"}
	m, _ := w.Update(keyMsg("J")) // move a down
	w = m.(*Wizard)
	if w.models[0] != "b" || w.models[1] != "a" {
		t.Fatalf("reorder failed: %v", w.models)
	}
	m, _ = w.Update(keyMsg("enter"))
	w = m.(*Wizard)
	if w.Model() != "b" || !w.Done() {
		t.Fatalf("confirm: model=%q done=%v", w.Model(), w.Done())
	}
}

func TestDashboardRenders(t *testing.T) {
	d := NewDashboard([]Stat{
		{ID: "k1", State: "Healthy", AvgMs: 118},
		{ID: "k2", State: "CoolingDown", AvgMs: 900, CooldownS: 14},
		{ID: "k3", State: "Dead"},
	})
	v := d.View()
	for _, want := range []string{"k1", "k2", "k3", "118", "14"} {
		if !strings.Contains(v, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, v)
		}
	}
	d.UpdateStats([]Stat{{ID: "k9", State: "Healthy"}})
	if !strings.Contains(d.View(), "k9") {
		t.Fatal("UpdateStats not reflected")
	}
}

func TestEscalationChoices(t *testing.T) {
	e := NewEscalationText("2 keys dead", "")
	for i, k := range []string{"1", "2", "3"} {
		m, _ := e.Update(keyMsg(k))
		em := m.(*Escalation)
		if em.Choice() != i {
			t.Fatalf("key %s → choice %d", k, em.Choice())
		}
		e = NewEscalationText("x", "")
	}
	m, _ := e.Update(keyMsg("esc"))
	if m.(*Escalation).Choice() != -1 {
		t.Fatal("esc must cancel")
	}
	if !strings.Contains(strings.ToLower(e.View()), "muse") {
		t.Fatal("default recommendation must mention the Muse route")
	}
}
