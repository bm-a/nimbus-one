// Package selftest simulates real-life failures and proves the recovery
// paths work: poisoned keys, dead providers, context overflow, missing
// binaries, corrupt vaults, LAN weirdness. `nimbus-one selftest` runs it;
// CI runs the same scenarios hermetically (httptest + temp dirs only).
package selftest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nimbus-one/internal/doctor"
	"nimbus-one/internal/engine"
	"nimbus-one/internal/gateway"
	"nimbus-one/internal/llm"
	"nimbus-one/internal/llm/pool"
	"nimbus-one/internal/netdiscover"
	"nimbus-one/internal/secure"
	"nimbus-one/internal/skills"
	"nimbus-one/internal/state"
	"nimbus-one/internal/tools"
)

// Result is one scenario outcome.
type Result struct {
	ID     string
	Title  string
	Pass   bool
	Detail string
}

// Run executes every scenario with per-scenario panic recovery and timeout.
// It never touches the real data dir, network, or providers.
func Run(ctx context.Context) []Result {
	scenarios := []struct {
		id    string
		title string
		fn    func(ctx context.Context) (string, error)
	}{
		{"pool-429", "429 storm fails over to next key", scPool429},
		{"pool-401", "401 kills key, traffic moves on", scPool401},
		{"pool-down", "total outage escalates with recommendation", scPoolDown},
		{"net-flakes", "transient network heals with backoff", scNetFlakes},
		{"overflow", "context overflow compacts and resends", scOverflow},
		{"sidecar", "missing opencode guides file fallback", scSidecar},
		{"vault", "vault+secrets survive reload", scVault},
		{"memory", "memory recall finds stored facts", scMemory},
		{"skills", "skill loads and executes", scSkills},
		{"http", "HTTP chat/console/healthz answer", scHTTP},
		{"telegram", "allowlist gates senders", scTelegram},
		{"mdns", "LAN scan terminates cleanly", scMDNS},
		{"doctor", "diagnostics complete without panic", scDoctor},
		{"knowledge", "fix-it base answers 429", scKnowledge},
		{"switchback", "prober respects timers, traffic stays steady", scSwitchback},
	}
	out := make([]Result, 0, len(scenarios))
	for _, sc := range scenarios {
		out = append(out, runOne(ctx, sc.id, sc.title, sc.fn))
	}
	return out
}

func runOne(ctx context.Context, id, title string, fn func(context.Context) (string, error)) (r Result) {
	r = Result{ID: id, Title: title}
	defer func() {
		if rec := recover(); rec != nil {
			r.Pass = false
			r.Detail = fmt.Sprintf("panic: %v", rec)
		}
	}()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	detail, err := fn(c)
	if err != nil {
		r.Pass = false
		r.Detail = err.Error()
		return r
	}
	r.Pass = true
	r.Detail = detail
	return r
}

// --- stub providers ---

type scripted struct {
	calls int
	fn    func(call int) (string, []llm.ToolCall, error)
}

func (s *scripted) Name() string { return "scripted" }
func (s *scripted) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 1)
	close(ch)
	return ch, errors.New("unused")
}
func (s *scripted) Complete(ctx context.Context, req llm.ChatRequest) (string, []llm.ToolCall, error) {
	s.calls++
	return s.fn(s.calls)
}

func poolWith(stubs ...*scripted) *pool.Pool {
	cfgs := make([]pool.KeyConfig, len(stubs))
	for i := range stubs {
		// Distinct model per slot so the factory resolves the PICKED key,
		// exactly like production (client built from key credentials).
		cfgs[i] = pool.KeyConfig{ID: fmt.Sprintf("k%d", i), ProviderName: "t", Model: fmt.Sprintf("m%d", i)}
	}
	p := pool.NewPool(cfgs)
	byModel := map[string]*scripted{}
	for i, s := range stubs {
		byModel[fmt.Sprintf("m%d", i)] = s
	}
	p.SetClientFactory(func(apiKey, baseURL, model string) llm.Provider {
		if s, ok := byModel[model]; ok {
			return s
		}
		return stubs[0]
	})
	return p
}

// --- scenarios ---

func scPool429(ctx context.Context) (string, error) {
	bad := &scripted{fn: func(c int) (string, []llm.ToolCall, error) {
		return "", nil, &llm.StatusError{Code: 429, Body: "slow down"}
	}}
	good := &scripted{fn: func(c int) (string, []llm.ToolCall, error) { return "recovered", nil, nil }}
	p := poolWith(bad, good)
	text, _, err := p.Complete(ctx, llm.ChatRequest{})
	if err != nil || text != "recovered" {
		return "", fmt.Errorf("no failover (text=%q err=%v)", text, err)
	}
	stats := p.Stats()
	if stats[0].State != pool.StateCoolingDown && stats[1].State != pool.StateCoolingDown {
		return "", fmt.Errorf("429 key should cool: %+v", stats)
	}
	return "429 key cooled, traffic moved, reply delivered", nil
}

func scPool401(ctx context.Context) (string, error) {
	bad := &scripted{fn: func(c int) (string, []llm.ToolCall, error) {
		return "", nil, &llm.StatusError{Code: 401, Body: "bad key"}
	}}
	good := &scripted{fn: func(c int) (string, []llm.ToolCall, error) { return "ok", nil, nil }}
	p := poolWith(bad, good)
	if _, _, err := p.Complete(ctx, llm.ChatRequest{}); err != nil {
		return "", err
	}
	for _, s := range p.Stats() {
		if strings.HasPrefix(s.ID, "k0") && s.State != pool.StateDead {
			return "", fmt.Errorf("401 key must be Dead, got %s", s.State)
		}
	}
	// Second call must not touch the dead key.
	if _, _, err := p.Complete(ctx, llm.ChatRequest{}); err != nil {
		return "", err
	}
	if bad.calls != 1 {
		return "", fmt.Errorf("dead key reused (calls=%d)", bad.calls)
	}
	return "401 key exiled after 1 call, never retried", nil
}

func scPoolDown(ctx context.Context) (string, error) {
	down := &scripted{fn: func(c int) (string, []llm.ToolCall, error) {
		return "", nil, &llm.StatusError{Code: 503, Body: "down"}
	}}
	p := poolWith(down)
	_, _, err := p.Complete(ctx, llm.ChatRequest{})
	var ex *pool.ExhaustedError
	if !asExhausted(err, &ex) {
		return "", fmt.Errorf("want ExhaustedError, got %v", err)
	}
	rec := llm.Escalation{Provider: "t", KeysTotal: 1, LastErr: ex.Detail}.Recommend()
	if !strings.Contains(strings.ToLower(rec), "muse") {
		return "", fmt.Errorf("escalation lacks recommendation: %q", rec)
	}
	return "outage → typed exhaustion → actionable recommendation", nil
}

func asExhausted(err error, target **pool.ExhaustedError) bool {
	if err == nil {
		return false
	}
	for e := err; e != nil; {
		if ex, ok := e.(*pool.ExhaustedError); ok {
			*target = ex
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return strings.Contains(e.Error(), "all keys exhausted")
		}
		e = u.Unwrap()
	}
	return false
}

func scNetFlakes(ctx context.Context) (string, error) {
	n := 0
	err := llm.RetryTransient(ctx, 3, func() error {
		n++
		if n < 3 {
			return errors.New("connection reset by peer")
		}
		return nil
	})
	if err != nil || n != 3 {
		return "", fmt.Errorf("n=%d err=%v", n, err)
	}
	return "2 flakes absorbed by backoff+jitter, 3rd attempt won", nil
}

type echoTool struct{}

func (echoTool) Name() string                       { return "echo" }
func (echoTool) Description() string                { return "echo" }
func (echoTool) Parameters() map[string]tools.Param { return map[string]tools.Param{} }
func (e echoTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	return "echo-out", nil
}

func scOverflow(ctx context.Context) (string, error) {
	n := 0
	prov := &scripted{fn: func(c int) (string, []llm.ToolCall, error) {
		n = c
		switch c {
		case 1:
			return "w", []llm.ToolCall{{ID: "1", Name: "echo", Arguments: `{}`}}, nil
		case 2:
			return "", nil, &pool.ContextOverflowError{Msg: "context_length_exceeded"}
		default:
			return "healed", nil, nil
		}
	}}
	reg := tools.NewRegistry()
	reg.Register(echoTool{})
	eng := &engine.Engine{LLM: prov, Tools: reg, MaxSteps: 6}
	text, err := eng.Run(ctx, "sys", "go")
	if err != nil || text != "healed" || n != 3 {
		return "", fmt.Errorf("text=%q calls=%d err=%v", text, n, err)
	}
	return "overflow → oldest 30% compacted → resend won", nil
}

func scSidecar(ctx context.Context) (string, error) {
	// Empty Bin + scrubbed PATH: binary unresolvable → guided fallback.
	old := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent-path-xyz")
	defer os.Setenv("PATH", old)
	oldBin, hadBin := os.LookupEnv("OPENCODE_BIN")
	os.Unsetenv("OPENCODE_BIN")
	defer func() {
		if hadBin {
			os.Setenv("OPENCODE_BIN", oldBin)
		}
	}()
	tool := &tools.OpencodeTool{}
	out, err := tool.Execute(ctx, map[string]any{"task": "refactor"})
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, "pulling the files") {
		return "", fmt.Errorf("no file-fallback guidance: %q", out)
	}
	return "missing sidecar → guided file fallback, zero crash", nil
}

func scVault(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "selftest-vault")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	v, err := secure.LoadVault(dir)
	if err != nil {
		return "", err
	}
	s, err := secure.OpenSecrets(dir, v)
	if err != nil {
		return "", err
	}
	if err := s.Set("openrouter", "sk-selftest"); err != nil {
		return "", err
	}
	v2, err := secure.LoadVault(dir)
	if err != nil {
		return "", err
	}
	s2, err := secure.OpenSecrets(dir, v2)
	if err != nil {
		return "", err
	}
	got, err := s2.Get("openrouter")
	if err != nil || got != "sk-selftest" {
		return "", fmt.Errorf("reload mismatch: %q %v", got, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if bytes.Contains(raw, []byte("sk-selftest")) {
		return "", fmt.Errorf("secret in plaintext at rest")
	}
	return "encrypted at rest, stable across reloads", nil
}

func scMemory(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "selftest-mem")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	st, err := state.Open(dir)
	if err != nil {
		return "", err
	}
	mem := &state.Memory{Store: st, Dim: 64}
	if err := mem.IndexFact("user deploys nimbus-one on a sailboat with satellite wifi", "selftest"); err != nil {
		return "", err
	}
	if _, err := st.SaveTurn("s", "user", "hello"); err != nil {
		return "", err
	}
	got := mem.Recall("sailboat satellite deployment", 3)
	joined := strings.Join(got, "\n")
	if !strings.Contains(strings.ToLower(joined), "sailboat") {
		return "", fmt.Errorf("recall missed: %q", joined)
	}
	return "fact indexed + recalled over turn noise", nil
}

func scSkills(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "selftest-skill")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	skdir := filepath.Join(dir, "ping")
	if err := os.MkdirAll(filepath.Join(skdir, "scripts"), 0o755); err != nil {
		return "", err
	}
	sk := "---\nname: ping\ndescription: Replies pong for selftest.\n---\n\n# ping\n"
	if err := os.WriteFile(filepath.Join(skdir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		return "", err
	}
	sh := "#!/bin/sh\necho pong-$SKILL_ARG_WHO\n"
	if err := os.WriteFile(filepath.Join(skdir, "scripts", "run.sh"), []byte(sh), 0o755); err != nil {
		return "", err
	}
	reg := tools.NewRegistry()
	sr := skills.NewSkillRegistry(reg)
	if err := sr.LoadDir(dir); err != nil {
		return "", err
	}
	skills.RegisterAll(sr, reg)
	tool, ok := reg.Get("ping")
	if !ok {
		return "", fmt.Errorf("skill not registered (have %v)", reg.Names())
	}
	out, err := tool.Execute(ctx, map[string]any{"who": "selftest"})
	if err != nil || !strings.Contains(out, "pong-selftest") {
		return "", fmt.Errorf("out=%q err=%v", out, err)
	}
	return "SKILL.md parsed, registered, executed with args", nil
}

type stubEng struct{ reply string }

func (s *stubEng) Run(ctx context.Context, system, user string) (string, error) {
	return s.reply, nil
}

func scHTTP(ctx context.Context) (string, error) {
	b := gateway.New(&stubEng{reply: "stub-hi"}, "sys")
	srv := &gateway.Server{Addr: "127.0.0.1:0", Broker: b}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// healthz
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		return "", fmt.Errorf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()
	// chat
	body := strings.NewReader(`{"message":"hi","user":"t"}`)
	resp, err = http.Post(ts.URL+"/api/v1/chat", "application/json", body)
	if err != nil || resp.StatusCode != 200 {
		return "", fmt.Errorf("chat: %v %v", resp, err)
	}
	resp.Body.Close()
	// console
	resp, err = http.Get(ts.URL + "/")
	if err != nil || resp.StatusCode != 200 {
		return "", fmt.Errorf("console: %v %v", resp, err)
	}
	resp.Body.Close()
	return "chat + console + healthz all 200", nil
}

func scTelegram(ctx context.Context) (string, error) {
	open := &gateway.Telegram{}
	if !open.Allowed("anyone") {
		return "", fmt.Errorf("empty allowlist must allow (local dev)")
	}
	locked := &gateway.Telegram{Allow: []string{"123"}}
	if !locked.Allowed("123") || locked.Allowed("456") {
		return "", fmt.Errorf("allowlist misbehaves")
	}
	return "allowlist gates correctly (open + locked)", nil
}

func scMDNS(ctx context.Context) (string, error) {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	svcs, err := netdiscover.Browse(c, 300*time.Millisecond)
	if err != nil {
		return "", err
	}
	if time.Since(start) > 5*time.Second {
		return "", fmt.Errorf("scan hung")
	}
	return fmt.Sprintf("scan finished in-budget (%d peers nearby)", len(svcs)), nil
}

func scDoctor(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "selftest-doctor")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	checks := doctor.Run(ctx, dir)
	if len(checks) < 8 {
		return "", fmt.Errorf("only %d checks", len(checks))
	}
	for _, c := range checks {
		if c.Status != "ok" && c.Status != "warn" && c.Status != "fail" {
			return "", fmt.Errorf("bad status %q on %s", c.Status, c.ID)
		}
		if c.Status == "fail" && c.Fix == "" {
			return "", fmt.Errorf("fail without fix on %s", c.ID)
		}
	}
	return fmt.Sprintf("%d checks completed, every failure has a fix", len(checks)), nil
}

func scKnowledge(ctx context.Context) (string, error) {
	got := doctor.Find("429 rate limited", 1)
	if len(got) == 0 || got[0].ID != "llm-429" {
		return "", fmt.Errorf("KB missed 429: %+v", got)
	}
	return "fix-it base answers from symptoms", nil
}

func scSwitchback(ctx context.Context) (string, error) {
	// Cool k0 through the public failure path (429 stub + failover stub),
	// then prove the background prober respects the live timer: no
	// premature resurrection, and traffic stays on the healthy key.
	flaky := &scripted{fn: func(c int) (string, []llm.ToolCall, error) {
		return "", nil, &llm.StatusError{Code: 429, Body: "slow"}
	}}
	steady := &scripted{fn: func(c int) (string, []llm.ToolCall, error) { return "ok", nil, nil }}
	p := poolWith(flaky, steady)
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go p.StartProber(c, 20*time.Millisecond, func(ctx context.Context, tgt pool.ProbeTarget) error {
		return nil // would revive — but only after timer expiry
	})
	text, _, err := p.Complete(ctx, llm.ChatRequest{})
	if err != nil || text != "ok" {
		return "", fmt.Errorf("failover: text=%q err=%v", text, err)
	}
	time.Sleep(120 * time.Millisecond) // several prober ticks
	stats := p.Stats()
	if stats[0].State != pool.StateCoolingDown {
		return "", fmt.Errorf("prober resurrected an unexpired cooldown: %+v", stats)
	}
	if _, _, err := p.Complete(ctx, llm.ChatRequest{}); err != nil {
		return "", fmt.Errorf("healthy key must serve meanwhile: %v", err)
	}
	if flaky.calls != 1 {
		return "", fmt.Errorf("cooling key re-entered rotation early (calls=%d)", flaky.calls)
	}
	return "cooling respected, traffic steady, switch-back armed for timer expiry", nil
}
