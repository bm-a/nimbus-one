package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/perms"
	"nimbus-one/internal/session"
	"nimbus-one/internal/tools"
)

func TestRules_PlanHidesAndBlocks(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"ls"}`}}},
		{text: "planned"},
	}}
	reg := tools.NewRegistry()
	reg.Register(&reactUpperTool{})
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5, Mode: ModePlan}

	out, err := e.Run(context.Background(), "sys", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "planned" {
		t.Fatalf("out = %q", out)
	}
	// bash def must be hidden from the model in plan mode.
	for _, d := range prov.requests[0].Tools {
		if d.Name == "bash" {
			t.Fatalf("bash def visible in plan mode: %+v", prov.requests[0].Tools)
		}
	}
	// ...and the call must be blocked with the plan wording.
	found := false
	for _, m := range prov.requests[1].Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "plan mode is read-only") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no plan-mode block message: %+v", prov.requests[1].Messages)
	}
}

func TestRules_CustomRulesetOverridesMode(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "upper", Arguments: `{"text":"hi"}`}}},
		{text: "done"},
	}}
	ut := &reactUpperTool{}
	reg := tools.NewRegistry()
	reg.Register(ut)
	// Plan mode, but an explicit ruleset allowing "upper".
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5, Mode: ModePlan,
		Ruleset: perms.Ruleset{{Tool: "upper", Action: perms.Allow}}}
	if _, err := e.Run(context.Background(), "sys", "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ut.executed {
		t.Fatal("explicit ruleset allow did not override plan preset")
	}
}

func TestRules_AskHeadlessDenies(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "upper", Arguments: `{"text":"hi"}`}}},
		{text: "done"},
	}}
	ut := &reactUpperTool{}
	reg := tools.NewRegistry()
	reg.Register(ut)
	// No rule matches "upper" and no Ask handler: fail closed.
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5,
		Ruleset: perms.Ruleset{}}
	if _, err := e.Run(context.Background(), "sys", "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ut.executed {
		t.Fatal("ask-verdict executed without a handler")
	}
	found := false
	for _, m := range prov.requests[1].Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "requires approval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no approval guidance: %+v", prov.requests[1].Messages)
	}
}

func TestRules_AskApproveRemembers(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "upper", Arguments: `{"text":"hi"}`}}},
		{text: "", calls: []llm.ToolCall{{ID: "2", Name: "upper", Arguments: `{"text":"yo"}`}}},
		{text: "done"},
	}}
	ut := &reactUpperTool{}
	reg := tools.NewRegistry()
	reg.Register(ut)
	asks := 0
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5,
		Ruleset:   perms.Ruleset{},
		Ask:       func(tool, target string) bool { asks++; return true },
		Approvals: &perms.Approvals{},
	}
	if _, err := e.Run(context.Background(), "sys", "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if asks != 1 {
		t.Fatalf("asks = %d, want 1 (second call uses the recorded approval)", asks)
	}
	if ut.gotText != "yo" {
		t.Fatalf("second call text = %q, want approval path to execute", ut.gotText)
	}
}

func TestRules_SessionPersist(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "upper", Arguments: `{"text":"hi"}`}}},
		{text: "FINAL"},
	}}
	reg := tools.NewRegistry()
	reg.Register(&reactUpperTool{})
	st, err := session.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, err := st.CreateSession("", "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5, Session: st, SessionID: sid}
	if _, err := e.Run(context.Background(), "sys", "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	h, err := st.History(sid)
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, m := range h {
		roles = append(roles, m.Role)
	}
	want := []string{session.RoleUser, session.RoleAssistant, session.RoleTool, session.RoleAssistant}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("persisted roles = %v, want %v", roles, want)
	}
	if h[2].ToolCallID != "1" || h[2].Name != "upper" {
		t.Fatalf("tool row = %+v, want pairing preserved", h[2])
	}
}
