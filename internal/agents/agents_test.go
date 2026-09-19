// Tests mirror the OpenClaw contracts cited in roster.go, sessionkey.go and
// policy.go: roster load/unknown/default/allow matrix, session-key
// build/parse/main/subagent/primary, alias exact-wins + provider/alias, and
// allowlist exact/wildcard/deny.
package agents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testRosterYAML = `
# entries-form roster with explicit ownership and legacy main default
ownership: explicit
default: main
agents:
  main:
    name: Main
    emoji: "🤖"
    workspace: .
    model: openai/gpt-4
    fallbacks:
      - openai/gpt-4-mini
      - anthropic/claude-haiku
    delegation_mode: prefer
  research:
    name: Research
    model: anthropic/claude-sonnet
    allow_agents: [tools]
    delegation_mode: prefer
  tools:
    name: Tools
    model: openai/gpt-4-mini
`

func writeRoster(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agents.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadTestRoster(t *testing.T) *Roster {
	t.Helper()
	r, err := LoadFile(writeRoster(t, testRosterYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return r
}

func TestRosterLoadEntries(t *testing.T) {
	r := loadTestRoster(t)
	if r.Ownership != OwnershipExplicit {
		t.Fatalf("ownership = %q, want explicit", r.Ownership)
	}
	if len(r.Agents) != 3 {
		t.Fatalf("agents = %d, want 3", len(r.Agents))
	}
	main, err := r.Resolve("main")
	if err != nil {
		t.Fatalf("Resolve(main): %v", err)
	}
	if main.Model != "openai/gpt-4" || main.Name != "Main" || main.Emoji != "🤖" {
		t.Fatalf("main agent = %+v", main)
	}
	if len(main.Fallbacks) != 2 || main.Fallbacks[0] != "openai/gpt-4-mini" {
		t.Fatalf("main fallbacks = %v", main.Fallbacks)
	}
	research, err := r.Resolve("RESEARCH")
	if err != nil {
		t.Fatalf("Resolve case-insensitive: %v", err)
	}
	if research.Model != "anthropic/claude-sonnet" {
		t.Fatalf("research model = %q", research.Model)
	}
}

func TestRosterLoadListForm(t *testing.T) {
	path := writeRoster(t, `
default: solo
agents:
  - id: solo
    name: Solo
    model: openai/gpt-4-mini
`)
	r, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	a, err := r.Resolve("solo")
	if err != nil {
		t.Fatalf("Resolve(solo): %v", err)
	}
	if a.Name != "Solo" {
		t.Fatalf("solo = %+v", a)
	}
	if got := r.Default().ID; got != "solo" {
		t.Fatalf("default = %q, want solo", got)
	}
}

func TestRosterResolveUnknown(t *testing.T) {
	r := loadTestRoster(t)
	_, err := r.Resolve("ghost")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	var sel *AgentSelectionRequired
	if !errors.As(err, &sel) {
		t.Fatalf("error type = %T, want *AgentSelectionRequired", err)
	}
	if !IsAgentSelectionRequired(err) {
		t.Fatal("IsAgentSelectionRequired = false")
	}
	if len(sel.Available) != 3 {
		t.Fatalf("available = %v", sel.Available)
	}
}

func TestRosterDefault(t *testing.T) {
	r := loadTestRoster(t)
	if got := r.Default().ID; got != "main" {
		t.Fatalf("default = %q, want main", got)
	}
	// Env override wins when the target exists.
	t.Setenv(EnvDefaultAgent, "research")
	r.LoadEnv()
	if got := r.Default().ID; got != "research" {
		t.Fatalf("env default = %q, want research", got)
	}
	// Empty roster falls back to the legacy implicit main id.
	empty, err := ParseRoster("")
	if err != nil {
		t.Fatalf("ParseRoster empty: %v", err)
	}
	if got := empty.Default().ID; got != LegacyImplicitAgentID {
		t.Fatalf("empty default = %q, want main", got)
	}
}

func TestRosterIsAllowedMatrix(t *testing.T) {
	r := loadTestRoster(t)
	cases := []struct {
		from, to string
		want     bool
	}{
		{"main", "research", true},  // empty AllowAgents = all allowed
		{"main", "tools", true},     // empty AllowAgents = all allowed
		{"research", "tools", true}, // explicitly listed
		{"research", "main", false}, // specialists default deny unless listed
		{"research", "other", false},
		{"ghost", "main", false}, // unknown caller denied
		{"", "main", true},       // empty caller uses default (open) policy
	}
	for _, c := range cases {
		if got := r.IsAllowed(c.from, c.to); got != c.want {
			t.Errorf("IsAllowed(%q,%q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestSessionKeyBuildParse(t *testing.T) {
	if got := BuildMain("Main"); got != "agent:main:main" {
		t.Fatalf("BuildMain = %q", got)
	}
	id, rest, err := Parse("agent:research:main")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id != "research" || rest != "main" {
		t.Fatalf("Parse = %q %q", id, rest)
	}
	if _, _, err := Parse("direct:peer"); err == nil {
		t.Fatal("expected error for non-agent key")
	}
	if _, _, err := Parse("agent:main"); err == nil {
		t.Fatal("expected error for key without rest")
	}
	if _, _, err := Parse("agent::main"); err == nil {
		t.Fatal("expected error for empty agent id")
	}
	if _, _, err := Parse(""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestSessionKeyClassify(t *testing.T) {
	if !IsMain("agent:main:main") {
		t.Error("IsMain(agent:main:main) = false")
	}
	if !IsMain("agent:main:MAIN") {
		t.Error("IsMain case-insensitive = false")
	}
	if IsMain("agent:main:direct:peer") {
		t.Error("IsMain(direct) = true")
	}
	if IsMain("main") {
		t.Error("IsMain(bare main) = true")
	}
	sub := "agent:main:subagent:abc123"
	if !IsSubagent(sub) {
		t.Error("IsSubagent = false")
	}
	if IsSubagent("agent:main:main") {
		t.Error("IsSubagent(main) = true")
	}
	if !IsPrimary("agent:main:main") {
		t.Error("IsPrimary(main) = false")
	}
	if IsPrimary(sub) {
		t.Error("IsPrimary(subagent) = true")
	}
	if IsPrimary("agent:main:acp:session:1") {
		t.Error("IsPrimary(acp) = true")
	}
	if !IsAcp("agent:main:acp:session:1") {
		t.Error("IsAcp = false")
	}
	if NormalizeModel("  OpenAI/GPT-4 ") != "openai/gpt-4" {
		t.Error("NormalizeModel did not fold case/space")
	}
}

func TestAliasResolve(t *testing.T) {
	a := NewAliasIndex()
	a.Add("fast", "openai", "gpt-4o-mini")
	if p, m, ok := a.Resolve("fast"); !ok || p != "openai" || m != "gpt-4o-mini" {
		t.Fatalf("bare alias = %q %q %v", p, m, ok)
	}
	if p, m, ok := a.Resolve("openai/fast"); !ok || p != "openai" || m != "gpt-4o-mini" {
		t.Fatalf("provider/alias = %q %q %v", p, m, ok)
	}
	if _, _, ok := a.Resolve("unknown-alias"); ok {
		t.Fatal("unknown bare alias resolved")
	}
	// Direct provider/model rows pass through.
	if p, m, ok := a.Resolve("anthropic/claude-sonnet"); !ok || p != "anthropic" || m != "claude-sonnet" {
		t.Fatalf("passthrough = %q %q %v", p, m, ok)
	}
}

func TestAliasExactWins(t *testing.T) {
	a := NewAliasIndex()
	a.Add("pro", "openai", "pro-latest") // alias pro + scoped openai/pro → pro-latest
	a.Add("other", "openai", "pro")      // exact row openai/pro → pro
	p, m, ok := a.Resolve("openai/pro")
	if !ok || p != "openai" || m != "pro" {
		t.Fatalf("exact-wins = %q %q %v, want openai pro", p, m, ok)
	}
	// Bare alias still resolves through the alias table.
	if p, m, ok := a.Resolve("pro"); !ok || m != "pro-latest" {
		t.Fatalf("bare alias after exact = %q %q %v", p, m, ok)
	}
}

func TestAllowlist(t *testing.T) {
	l := NewAllowlist("openai/gpt-4", "anthropic/*")
	if !l.Allow("main", "openai", "gpt-4") {
		t.Error("exact allow = false")
	}
	if !l.Allow("main", "anthropic", "claude-sonnet") {
		t.Error("wildcard allow = false")
	}
	if !l.Allow("main", "anthropic", "other/x") {
		t.Error("provider/* should cover nested model paths")
	}
	if l.Allow("main", "openai", "gpt-4-mini") {
		t.Error("deny = true")
	}
	if l.Allow("main", "openai", "gpt-40") {
		t.Error("segment-boundary over-match = true")
	}
	empty := NewAllowlist()
	if empty.Allow("main", "openai", "gpt-4") {
		t.Error("empty allowlist = allow (want deny-all)")
	}
	if !AllowAll().Allow("main", "anything", "x/y") {
		t.Error("AllowAll = deny")
	}
}
