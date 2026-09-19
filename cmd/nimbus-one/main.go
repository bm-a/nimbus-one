// Command nimbus-one is the single-binary autonomous agent runtime: setup,
// chat, serve, and diagnostics. Every subcommand explains itself —
// run `nimbus-one help <command>`. Nothing here requires editing config files
// by hand; `nimbus-one config` (visual) and `nimbus-one auto` (automatic) cover setup.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nimbus-one/internal/config"
	"nimbus-one/internal/daemon"
	"nimbus-one/internal/doctor"
	"nimbus-one/internal/engine"
	"nimbus-one/internal/gateway"
	"nimbus-one/internal/llm"
	"nimbus-one/internal/llm/pool"
	"nimbus-one/internal/log"
	"nimbus-one/internal/media"
	"nimbus-one/internal/netdiscover"
	"nimbus-one/internal/perms"
	"nimbus-one/internal/prompt"
	"nimbus-one/internal/secure"
	"nimbus-one/internal/selftest"
	"nimbus-one/internal/session"
	"nimbus-one/internal/setup"
	"nimbus-one/internal/skills"
	"nimbus-one/internal/state"
	"nimbus-one/internal/tools"
	"nimbus-one/internal/tui"
	"nimbus-one/internal/update"
	"nimbus-one/internal/vcs"
)

const version = "0.1.0-beta"

func main() {
	// Code-level safety net: any panic becomes a redacted report pointing
	// at doctor --bundle and self-update — never a raw stack trace leak.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "nimbus-one crashed: %v\n", secure.Redact(fmt.Sprint(r)))
			fmt.Fprintln(os.Stderr, "Run `nimbus-one doctor --bundle support.zip` and file it; `nimbus-one update` pulls the latest fixes (asks first, preserves your data).")
			os.Exit(2)
		}
	}()
	log.Init(os.Getenv("NIMBUS_LOG_LEVEL"))
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp("")
		return
	}
	cmd, rest := args[0], args[1:]
	var code int
	switch cmd {
	case "init":
		code = cmdInit(rest)
	case "auto":
		code = cmdAuto(rest)
	case "config":
		code = cmdConfig(rest)
	case "run", "exec":
		code = cmdRun(cmd, rest)
	case "serve":
		code = cmdServe(rest)
	case "skills":
		code = cmdSkills(rest)
	case "secrets":
		code = cmdSecrets(rest)
	case "models":
		code = cmdModels(rest)
	case "discover":
		code = cmdDiscover(rest)
	case "doctor":
		code = cmdDoctor(rest)
	case "update":
		code = cmdUpdate(rest)
	case "fix":
		code = cmdFix(rest)
	case "selftest":
		code = cmdSelftest(rest)
	case "speak":
		code = cmdSpeak(rest)
	case "transcribe":
		code = cmdTranscribe(rest)
	case "status", "dashboard":
		code = cmdStatus(cmd, rest)
	case "version", "--version", "-v":
		fmt.Printf("nimbus-one %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	case "help", "--help", "-h":
		printHelp(strings.Join(rest, " "))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q — try `nimbus-one help`\n", cmd)
		code = 2
	}
	os.Exit(code)
}

// ---------- help ----------

var helpText = map[string]string{
	"": `nimbus-one — lightweight autonomous agent. One binary, runs on any Android, laptop, or server.

USAGE
  nimbus-one <command> [flags]

SETUP (pick one — everything is your choice)
  init [--auto]        Scaffold workspace + keys. --auto detects everything.
  auto                 Detect network, APIs, Ollama, OpenCode; configure; warn.
  config               Visual setup wizard (falls back to guided prompts).

RUN
  run [--mode plan|build] [--model M] <message...>
                       One-shot task. Default mode is build (full tools);
                       plan is read-only (describes changes instead).
  exec ...             Same as run (explicit non-interactive).

SERVE
  serve [--mode ...]   HTTP :8787 + Telegram/Discord (if configured) +
                       heartbeat daemon + LAN discovery. Ctrl-C stops.

MANAGE
  skills [list]        List loaded skills and their triggers.
  secrets set|get|del|list <key>
                       Encrypted secret manager (values never listed).
  models               List OpenRouter models (works offline via fallback).
  discover             Find Nimbus-One peers on your LAN (mDNS, 5s scan).
  doctor [--bundle FILE]
                       Diagnose setup; --bundle writes a redacted support zip.
  update [--yes] [--repo OWNER/NAME]
                       Pull the latest release from GitHub. ALWAYS asks first
                       (unless --yes); backs up vault+secrets+workspace+memory
                       before touching the binary. Refuses on metered guesses:
                       shows exact version delta before you confirm.
  fix <symptom...>      Search the built-in fix-it knowledge base.
                       Try: 'nimbus-one fix 429', 'nimbus-one fix telegram',
                       'nimbus-one fix context too long'.
  selftest             Simulate real-life failures (dead keys, 429 storms,
                       outages, overflow, missing binaries, corrupt vaults)
                       and prove every recovery path works. Hermetic.
  speak <text...>      Speak text aloud (termux-tts-speak / say / espeak).
  transcribe <file>    Transcribe audio (.ogg voice notes as-is, no ffmpeg)
                       via whisper.cpp, NIMBUS_STT_URL, or OpenAI Whisper.
  status               Configured providers, channels, workspace summary.
  dashboard            Live key-health dashboard (q to quit).
  version              Print version.
  help [command]       This text, or per-command detail.

EXAMPLES
  nimbus-one init --auto
  nimbus-one run --mode plan "what would you change in main.go?"
  nimbus-one serve
  curl -H "Authorization: Bearer $TOKEN" localhost:8787/api/v1/chat \
    -d '{"message":"hello"}'
`,
	"init":   "init [--auto]: create data dir, workspace (SOUL/USER/MEMORY/HEARTBEAT), vault key, default secrets. --auto also probes Ollama/OpenCode/network and writes what it finds (never overwrites your values).",
	"auto":   "auto: full automatic configuration. Detects Termux, Ollama models, OpenCode auth, provider keys, LAN IPs. Generates an HTTP token when serving the LAN without one. Prints WARNINGS for everything risky and ACTIONS for everything changed.",
	"config": "config: visual Charmbracelet wizard — pick provider, paste key (hidden, validated live), order fallback models. Without a terminal it uses guided text prompts instead. Nothing to edit by hand.",
	"run":    "run [--mode plan|build] [--model MODEL] <message...>: one-shot agent task with the ReAct loop (max 25 steps, 3 retries per tool). plan = read-only inspection; build = full tools. On total key failure an escalation card offers: new key / switch provider / retry.",
	"serve":  "serve [--mode ...] [--no-mdns]: start HTTP API (bind+port from config, default 127.0.0.1:8787), Telegram polling (needs token+allowlist), Discord REST, heartbeat ticker, skill hot-reload, Termux wake-lock. Advertises _nimbus._tcp on the LAN unless --no-mdns.",
	"doctor": "doctor [--bundle out.zip]: 13 checks (dirs, vault, config, secrets, Ollama, OpenCode, Telegram, port, disk, Termux, memory, skills). --bundle writes a REDACTED diagnostics zip safe to share when asking for help.",
}

func printHelp(cmd string) {
	if t, ok := helpText[cmd]; ok {
		fmt.Println(t)
		return
	}
	if cmd != "" {
		fmt.Printf("no help for %q\n\n", cmd)
	}
	fmt.Println(helpText[""])
}

func flagVal(args []string, name, def string) (string, []string) {
	prefix := "--" + name + "="
	for i, a := range args {
		if a == "--"+name && i+1 < len(args) {
			return args[i+1], append(args[:i], args[i+2:]...)
		}
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix), append(args[:i], args[i+1:]...)
		}
	}
	return def, args
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
	}
	return false
}

// ---------- bootstrap ----------

type app struct {
	cfg     *config.Config
	vault   *secure.Vault
	secrets *secure.Store
	ws      *state.Workspace
	store   *state.JSONLStore
	memory  *state.Memory
	tools   *tools.Registry
	skillz  *skills.SkillRegistry
	eng     *engine.Engine
	tasks   *engine.Tasks
	prov    llm.Provider
	system  string
}

// bootstrap loads everything non-interactive. noisy controls whether
// key/fallback guidance goes to stderr (true for run/serve, false for
// management commands where it would just be noise).
func bootstrap(ctx context.Context, noisy bool) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	vault, err := secure.LoadVault(cfg.DataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: vault unavailable (%v) — secrets stay in env only.\n", secure.Redact(err.Error()))
		vault = nil
	} else {
		_ = secure.HardenDataDir(cfg.DataDir)
	}
	var sec *secure.Store
	if vault != nil {
		sec, err = secure.OpenSecrets(cfg.DataDir, vault)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: secrets store unreadable (%v) — continuing without it.\n", secure.Redact(err.Error()))
		}
	}
	ws := &state.Workspace{Dir: cfg.WorkspaceDir}
	defaults, err := workspaceDefaults()
	if err != nil {
		return nil, err
	}
	if err := ws.EnsureDefaults(defaults["SOUL.md"], defaults["USER.md"], defaults["MEMORY.md"], defaults["HEARTBEAT.md"]); err != nil {
		return nil, err
	}
	// Seed starter skill into user skills dir (never overwrite).
	seedSkill(cfg.SkillsDir)
	st, err := state.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	mem := &state.Memory{Store: st, Dim: 128}
	toolsReg := tools.NewRegistry()
	tools.RegisterBuiltins(toolsReg, cfg.WorkspaceDir)
	skillReg := skills.NewSkillRegistry(toolsReg)
	for _, dir := range []string{cfg.SkillsDir, filepath.Join(cfg.WorkspaceDir, "skills")} {
		if err := skillReg.LoadDir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: skills in %s: %v\n", dir, secure.Redact(err.Error()))
		}
	}
	skills.RegisterAll(skillReg, toolsReg)
	prov := buildProvider(ctx, cfg, sec, noisy)
	eng := &engine.Engine{LLM: prov, Tools: toolsReg, MaxSteps: cfg.MaxSteps}
	// Multi-agent primitives share the engine: subagents fan out with a
	// depth cap, background tasks poll via tasks_poll. Same registry, so
	// subagents see exactly what the user gave the top agent — nothing more.
	taskReg := engine.NewTasks()
	toolsReg.Register(&engine.DelegateTool{Eng: eng, Tasks: taskReg})
	toolsReg.Register(&engine.TasksTool{Tasks: taskReg})
	a := &app{cfg: cfg, vault: vault, secrets: sec, ws: ws, store: st, memory: mem, tools: toolsReg, skillz: skillReg, eng: eng, tasks: taskReg, prov: prov}
	a.system = a.buildSystem()
	return a, nil
}

func workspaceDefaults() (map[string]string, error) {
	return setup.DefaultWorkspace(), nil
}

func seedSkill(skillsDir string) {
	dst := filepath.Join(skillsDir, "hello", "SKILL.md")
	if _, err := os.Stat(dst); err == nil {
		return // never overwrite user skills
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	_ = os.WriteFile(dst, []byte(setup.DefaultHelloSkill), 0o644)
}

// secretOrEnv reads a provider key: secrets store > config > env. Never logs values.
func secretOrEnv(sec *secure.Store, cfg *config.Config, provider, env string) string {
	if sec != nil {
		if v, err := sec.Get(provider); err == nil && v != "" {
			return v
		}
	}
	if v := cfg.APIKeys[provider]; v != "" {
		return v
	}
	return os.Getenv(env)
}

// buildProvider assembles the deterministic pool from the user's own
// choices and nothing else:
//
//  1. Primary model (config `primary_model` / --model / NIMBUS_MODEL).
//  2. The user's fallback list (`fallback_models` / NIMBUS_FALLBACK_MODELS /
//     wizard-confirmed order). Empty = primary only. Suggestions are shown
//     in the wizard and docs but NEVER applied silently.
//  3. Local Ollama last — free, on-device, no key — unless the user already
//     listed it.
//
// Each entry reuses the key of the provider that serves that model
// (matched by name); entries without a usable key are skipped with a
// warning so the user sees exactly what was chosen and why.
func buildProvider(ctx context.Context, cfg *config.Config, sec *secure.Store, noisy bool) llm.Provider {
	_ = ctx
	known := []struct {
		name string
		env  string
		url  string
	}{
		{"openrouter", "OPENROUTER_API_KEY", llm.OpenRouterBaseURL},
		{"openai", "OPENAI_API_KEY", llm.OpenAIBaseURL},
		{"anthropic", "ANTHROPIC_API_KEY", ""},
		{"groq", "GROQ_API_KEY", llm.GroqBaseURL},
		{"gemini", "GEMINI_API_KEY", llm.GeminiBaseURL},
		{"deepseek", "DEEPSEEK_API_KEY", llm.DeepSeekBaseURL},
		{"meta", "META_API_KEY", llm.MetaBaseURL},
	}
	keys := map[string]string{} // provider -> key
	bases := map[string]string{}
	for _, p := range known {
		if key := secretOrEnv(sec, cfg, p.name, p.env); key != "" {
			keys[p.name] = key
			bases[p.name] = p.url
		}
	}
	// User's model order: primary first, then THEIR fallbacks.
	wanted := []string{}
	if strings.TrimSpace(cfg.PrimaryModel) != "" {
		wanted = append(wanted, strings.TrimSpace(cfg.PrimaryModel))
	}
	for _, m := range cfg.FallbackModels {
		if m = strings.TrimSpace(m); m != "" {
			wanted = append(wanted, m)
		}
	}
	var cfgs []pool.KeyConfig
	seen := map[string]bool{}
	for _, m := range wanted {
		prov := inferProviderName(m, keys)
		if prov == "" {
			fmt.Fprintf(os.Stderr, "WARNING: no key for model %q — skipped (add a key or remove it from fallback_models).\n", m)
			continue
		}
		id := prov + "/" + m
		if seen[id] {
			continue
		}
		seen[id] = true
		cfgs = append(cfgs, pool.KeyConfig{ID: id, ProviderName: prov, Model: m, APIKey: keys[prov], BaseURL: bases[prov]})
	}
	// Local Ollama joins only when the user opted in (config models or
	// OLLAMA_* env). Unconfigured backends stay silent: no entries, no
	// warnings, no error spam — just the no-backend guidance below.
	hasOllama := false
	for _, c := range cfgs {
		if c.ProviderName == "ollama" {
			hasOllama = true
		}
	}
	if !hasOllama && cfg.WantsOllama() {
		model := "llama3.1"
		if env := strings.TrimSpace(os.Getenv("OLLAMA_MODEL")); env != "" {
			model = env
		} else {
			for _, m := range append([]string{cfg.PrimaryModel}, cfg.FallbackModels...) {
				if l := strings.ToLower(strings.TrimSpace(m)); l != "" && l != "ollama" && !strings.Contains(l, "/") {
					model = m
					break
				}
			}
		}
		cfgs = append(cfgs, pool.KeyConfig{ID: "ollama", ProviderName: "ollama", Model: model})
	}
	if len(keys) == 0 && noisy {
		if cfg.WantsOllama() {
			fmt.Fprintln(os.Stderr, "WARNING: no API keys found — local Ollama only. Run `nimbus-one config` to choose more.")
		} else {
			fmt.Fprintln(os.Stderr, "WARNING: no backends configured — `nimbus-one config` for keys (recommended: OpenRouter), or name a local model to use Ollama.")
		}
	} else if len(cfg.FallbackModels) == 0 && noisy {
		fmt.Fprintln(os.Stderr, "note: no fallback_models configured — primary only. `nimbus-one config` to choose your fallback order.")
	}
	return pool.NewPool(cfgs)
}

// inferProviderName maps a user-chosen model to the provider that serves it,
// restricted to providers the user actually has keys for. "" = none.
func inferProviderName(model string, keys map[string]string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	has := func(p string) bool { _, ok := keys[p]; return ok }
	// Explicit provider/model form: "openrouter/..." or bare provider names.
	if strings.Contains(m, "/") {
		first := strings.Split(m, "/")[0]
		if has(first) {
			return first
		}
		// OpenRouter serves any provider/model pair.
		if has("openrouter") {
			return "openrouter"
		}
		return ""
	}
	switch {
	case m == "ollama" || strings.HasPrefix(m, "ollama:"):
		return "ollama" // keyless local
	case strings.HasPrefix(m, "claude"):
		if has("anthropic") {
			return "anthropic"
		}
	case strings.HasPrefix(m, "muse"):
		if has("openrouter") {
			return "openrouter" // recommended route
		}
		if has("meta") {
			return "meta"
		}
	case strings.HasPrefix(m, "gemini"):
		if has("gemini") {
			return "gemini"
		}
	case strings.HasPrefix(m, "llama"), strings.HasPrefix(m, "mixtral"), strings.HasPrefix(m, "qwen"), strings.HasPrefix(m, "mistral"):
		if has("groq") {
			return "groq"
		}
	case strings.HasPrefix(m, "deepseek"):
		if has("deepseek") {
			return "deepseek"
		}
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		if has("openai") {
			return "openai"
		}
	}
	// Any keyed OpenAI-compatible provider can attempt an unknown model id;
	// prefer openrouter, then openai. Still the user's key, user's model.
	for _, p := range []string{"openrouter", "openai", "groq", "deepseek", "gemini", "meta", "anthropic"} {
		if has(p) {
			return p
		}
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (a *app) buildSystem() string {
	soul, _ := a.ws.Load("SOUL.md")
	user, _ := a.ws.Load("USER.md")
	memory, _ := a.ws.Load("MEMORY.md")
	return a.assemblePrompt(engine.BuildPrompt(soul, user, memory, nil, a.skillz.CapabilitiesPrompt()))
}

// assemblePrompt appends the model-variant addendum and environment facts
// (internal/prompt) to the identity sections.
func (a *app) assemblePrompt(base string) string {
	prof := llm.MatchModelProfile(a.cfg.PrimaryModel)
	env := prompt.Env{
		Workdir:   a.cfg.WorkspaceDir,
		SkillsDir: a.cfg.SkillsDir,
		ToolNames: a.tools.Names(),
		IsGitRepo: vcs.Detect(a.cfg.WorkspaceDir).IsRepo,
	}
	return prompt.Assemble(base, prompt.Addendum(prompt.VariantFor(prof.PromptVariant)), env.Block())
}

// refreshMemory injects top recalled facts into the system prompt.
func (a *app) refreshMemory(query string) {
	facts := a.memory.Recall(query, 5)
	a.system = a.assemblePrompt(engine.BuildPrompt(mustLoad(a.ws, "SOUL.md"), mustLoad(a.ws, "USER.md"), mustLoad(a.ws, "MEMORY.md"), facts, a.skillz.CapabilitiesPrompt()))
}

func mustLoad(ws *state.Workspace, name string) string {
	s, _ := ws.Load(name)
	return s
}

// ---------- init / auto / config ----------

func cmdInit(args []string) int {
	auto := hasFlag(args, "auto")
	cfg := config.DefaultConfig()
	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}
	vault, err := secure.LoadVault(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "WARNING: vault:", secure.Redact(err.Error()))
	} else {
		_ = secure.HardenDataDir(cfg.DataDir)
	}
	ctx := context.Background()
	var sec *secure.Store
	if vault != nil {
		sec, _ = secure.OpenSecrets(cfg.DataDir, vault)
	}
	rep, err := setup.Detect(ctx, cfg, sec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}
	if auto {
		if err := setup.Apply(ctx, cfg, sec, rep); err != nil {
			fmt.Fprintln(os.Stderr, "init --auto:", err)
			return 1
		}
	}
	// Seed workspace templates + starter skill (never overwrites).
	ws := &state.Workspace{Dir: cfg.WorkspaceDir}
	dw := setup.DefaultWorkspace()
	if err := ws.EnsureDefaults(dw["SOUL.md"], dw["USER.md"], dw["MEMORY.md"], dw["HEARTBEAT.md"]); err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}
	seedSkill(cfg.SkillsDir)
	printReport(rep)
	fmt.Printf("\nWorkspace ready at %s\n", cfg.WorkspaceDir)
	fmt.Println("Next: `nimbus-one config` (visual keys) or `nimbus-one run \"hello\"`.")
	return 0
}

func cmdAuto(args []string) int {
	_ = args
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	a, err := bootstrap(ctx, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "auto:", err)
		return 1
	}
	rep, err := setup.Detect(ctx, a.cfg, a.secrets)
	if err != nil {
		fmt.Fprintln(os.Stderr, "auto:", err)
		return 1
	}
	if err := setup.Apply(ctx, a.cfg, a.secrets, rep); err != nil {
		fmt.Fprintln(os.Stderr, "auto:", err)
		return 1
	}
	printReport(rep)
	return 0
}

func printReport(rep *setup.Report) {
	if rep == nil {
		return
	}
	fmt.Printf("data: %s   termux: %v\n", rep.DataDir, rep.IsTermux)
	if len(rep.LANIPs) > 0 {
		fmt.Printf("lan: %s\n", strings.Join(rep.LANIPs, ", "))
	}
	status := func(b bool) string {
		if b {
			return "found"
		}
		return "missing"
	}
	fmt.Printf("ollama: %s  opencode: %s (auth %s)  openrouter-key: %s  telegram: %s\n",
		status(rep.OllamaFound), status(rep.OpenCodeFound), status(rep.OpenCodeAuth),
		status(rep.OpenRouterKey), status(rep.TelegramToken))
	if len(rep.OllamaModels) > 0 {
		fmt.Printf("ollama models: %s\n", strings.Join(rep.OllamaModels, ", "))
	}
	for _, w := range rep.Warnings {
		fmt.Printf("WARNING: %s\n", w)
	}
	for _, a := range rep.Actions {
		fmt.Printf("did: %s\n", a)
	}
}

// cmdConfig runs the visual wizard, or guided prompts without a TTY.
func cmdConfig(args []string) int {
	_ = args
	ctx := context.Background()
	a, err := bootstrap(ctx, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	save := func(provider, key string) error {
		if a.secrets == nil {
			return fmt.Errorf("secrets store unavailable (vault failed) — set %s env var instead", "XXX_API_KEY")
		}
		return a.secrets.Set(provider, key)
	}
	ping := func(provider, key string) (string, error) {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		switch provider {
		case "openrouter":
			return llm.PingOpenRouter(c, key)
		case "ollama":
			names, err := ollamaModels(c)
			if err != nil {
				return "", err
			}
			if len(names) > 0 {
				return names[0], nil
			}
			return "llama3.1", nil
		default:
			if strings.TrimSpace(key) == "" {
				return "", fmt.Errorf("empty key")
			}
			return provider + "/default", nil // stored; validated on first real call
		}
	}
	w, err := tui.RunWizard(save, ping)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No terminal for the visual wizard — guided text setup instead.")
		return textConfig(a)
	}
	if w.Provider() != "" {
		fmt.Printf("Saved %s key. Primary model: %s\n", w.Provider(), w.Model())
	}
	// Persist exactly what the user confirmed: primary + THEIR fallback
	// order (possibly empty = primary only). Suggestions never persist
	// unless confirmed here.
	if w.Done() {
		ms := w.Models()
		if len(ms) > 0 {
			a.cfg.PrimaryModel = ms[0]
			a.cfg.FallbackModels = append([]string{}, ms[1:]...)
		} else if w.Model() != "" {
			// User removed every suggestion: primary stands alone.
			a.cfg.PrimaryModel = w.Model()
			a.cfg.FallbackModels = nil
		}
		if err := a.cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not save config (%v) — choices apply to this session only.\n", err)
		} else if len(a.cfg.FallbackModels) == 0 {
			fmt.Println("No fallbacks kept — primary only. Re-run `nimbus-one config` any time to choose more.")
		} else {
			fmt.Printf("Fallback order saved: %s\n", strings.Join(a.cfg.FallbackModels, ", "))
		}
	}
	fmt.Println("Done. `nimbus-one run \"hello\"` to try it, `nimbus-one doctor` to verify.")
	return 0
}

func textConfig(a *app) int {
	in := bufio.NewReader(os.Stdin)
	fmt.Println("Guided setup (nothing to edit by hand). Providers: openrouter (recommended), ollama (local), openai, anthropic, groq, gemini, deepseek, meta.")
	fmt.Print("provider [openrouter]: ")
	prov, _ := in.ReadString('\n')
	prov = strings.TrimSpace(prov)
	if prov == "" {
		prov = "openrouter"
	}
	if prov == "ollama" {
		fmt.Println("Ollama needs no key. Done — `nimbus-one run \"hello\"` to try it.")
		return 0
	}
	key, err := readSecret("API key (hidden where possible): ")
	if err != nil || key == "" {
		fmt.Fprintln(os.Stderr, "no key entered — aborted, nothing changed.")
		return 1
	}
	if a.secrets == nil {
		fmt.Fprintln(os.Stderr, "secrets store unavailable — export the key as env instead.")
		return 1
	}
	if err := a.secrets.Set(prov, key); err != nil {
		fmt.Fprintln(os.Stderr, "save:", err)
		return 1
	}
	fmt.Printf("Saved %s key (encrypted). `nimbus-one run \"hello\"` to try it.\n", prov)
	return 0
}

func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	if runtime.GOOS != "windows" {
		// Best-effort echo suppression; falls back to plain read.
		if stty("-echo") == nil {
			defer stty("echo")
		} else {
			fmt.Print(" (warning: terminal will echo) ")
		}
	} else {
		fmt.Print(" (warning: terminal will echo on Windows) ")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ---------- run / exec ----------

func cmdRun(cmd string, args []string) int {
	mode, args := flagVal(args, "mode", "build")
	model, args := flagVal(args, "model", "")
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: nimbus-one %s [--mode plan|build] [--model M] <message...>\n", cmd)
		return 2
	}
	prompt := strings.Join(args, " ")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	a, err := bootstrap(ctx, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 1
	}
	a.eng.SetMode(mode)
	attachSession(a, cmd)
	a.eng.ModelID = a.cfg.PrimaryModel
	if model != "" {
		a.cfg.PrimaryModel = model
		a.prov = buildProvider(ctx, a.cfg, a.secrets, true)
		a.eng.LLM = a.prov
		a.eng.ModelID = model
	}
	a.refreshMemory(prompt)
	reply, err := a.eng.Run(ctx, a.system, prompt)
	if err != nil {
		if ex, ok := asExhausted(err); ok {
			return escalate(ctx, a, prompt, ex.Detail)
		}
		fmt.Fprintln(os.Stderr, "error:", secure.Redact(err.Error()))
		return 1
	}
	fmt.Println(reply)
	a.remember("repl", "user", prompt)
	a.remember("repl", "assistant", reply)
	a.appendMemory(prompt, reply)
	return 0
}

// attachSession wires SQLite turn persistence into the engine for run/exec.
// Best-effort by design: a store failure warns on stderr and the run
// continues unpersisted — the answer matters more than the archive.
func attachSession(a *app, cmd string) {
	st, err := session.Open(filepath.Join(a.cfg.DataDir, "sessions.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: session store unavailable (%v) — turns will not be persisted.\n", secure.Redact(err.Error()))
		return
	}
	sid, err := st.CreateSession("", cmd, a.cfg.WorkspaceDir, a.cfg.PrimaryModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: session create failed (%v) — turns will not be persisted.\n", secure.Redact(err.Error()))
		_ = st.Close()
		return
	}
	a.eng.Session = st
	a.eng.SessionID = sid
	a.eng.Approvals = &perms.Approvals{}
}

func asExhausted(err error) (*pool.ExhaustedError, bool) {
	for err != nil {
		if ex, ok := err.(*pool.ExhaustedError); ok {
			return ex, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			// engine wraps with %w into fmt.wrapError — try string match fallback
			if strings.Contains(err.Error(), "all keys exhausted") {
				return &pool.ExhaustedError{Detail: err.Error()}, true
			}
			return nil, false
		}
		err = u.Unwrap()
	}
	return nil, false
}

// escalate is the last-resort path: every key failed. TTY users get the
// interactive card (new key / switch provider / retry); everyone else gets
// diagnostics plus the Meta Muse 1.3 recommendation.
func escalate(ctx context.Context, a *app, prompt, detail string) int {
	esc := llm.Escalation{Provider: a.cfg.PrimaryModel, LastErr: detail}
	if p, ok := a.prov.(*pool.Pool); ok && p != nil {
		for _, s := range p.Stats() {
			esc.KeysTotal++
			switch s.State {
			case pool.StateCoolingDown:
				esc.KeysCooling++
			case pool.StateDead:
				esc.KeysDead++
			}
		}
	}
	fmt.Fprintln(os.Stderr, "All keys exhausted: "+esc.Summary())
	if !isTTY() {
		fmt.Fprintln(os.Stderr, "Recommendation: "+esc.Recommend())
		return 1
	}
	choice := tui.RunEscalation(esc)
	switch choice {
	case 0:
		key, err := readSecret("New key for " + a.cfg.PrimaryModel + ": ")
		if err != nil || key == "" || a.secrets == nil {
			fmt.Fprintln(os.Stderr, "no key saved.")
			return 1
		}
		prov := providerOf(a.cfg.PrimaryModel)
		if err := a.secrets.Set(prov, key); err != nil {
			fmt.Fprintln(os.Stderr, "save:", err)
			return 1
		}
		a.prov = buildProvider(ctx, a.cfg, a.secrets, false)
		a.eng.LLM = a.prov
		reply, err := a.eng.Run(ctx, a.system, prompt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", secure.Redact(err.Error()))
			return 1
		}
		fmt.Println(reply)
		return 0
	case 1:
		fmt.Println("Run `nimbus-one config` to switch provider, then retry your prompt.")
		return 0
	case 2:
		reply, err := a.eng.Run(ctx, a.system, prompt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", secure.Redact(err.Error()))
			return 1
		}
		fmt.Println(reply)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "No choice made. Recommendation: "+esc.Recommend())
		return 1
	}
}

func providerOf(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "/") {
		return strings.Split(m, "/")[0]
	}
	return "openrouter"
}

func (a *app) remember(session, role, content string) {
	if a.store == nil {
		return
	}
	_, _ = a.store.SaveTurn(session, role, content)
}

func (a *app) appendMemory(prompt, reply string) {
	snip := strings.TrimSpace(prompt)
	if len(snip) > 200 {
		snip = snip[:200] + "…"
	}
	out := strings.TrimSpace(reply)
	if len(out) > 200 {
		out = out[:200] + "…"
	}
	_ = a.ws.Append("MEMORY.md", "Q: "+snip+"\nA: "+out)
}

// ---------- serve ----------

func cmdServe(args []string) int {
	mode, args := flagVal(args, "mode", "build")
	noMDNS := hasFlag(args, "no-mdns")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a, err := bootstrap(ctx, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		return 1
	}
	a.eng.SetMode(mode)
	broker := gateway.New(a.eng, a.system)
	// Skill hot-reload without restarts.
	watcher := &skills.Watcher{Dir: a.cfg.SkillsDir, OnChange: func() {
		r := skills.NewSkillRegistry(a.tools)
		for _, dir := range []string{a.cfg.SkillsDir, filepath.Join(a.cfg.WorkspaceDir, "skills")} {
			_ = r.LoadDir(dir)
		}
		fmt.Fprintln(os.Stderr, "skills reloaded")
	}}
	go func() { _ = watcher.Start(ctx) }()
	// Termux wake-lock so Android doesn't suspend us.
	if daemon.IsTermux() {
		if err := daemon.WakeLock(); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: wake-lock: %v\n", err)
		} else {
			defer daemon.WakeUnlock()
		}
	}
	// Heartbeat with battery-aware throttling.
	every := daemon.ParseEvery(a.cfg.HeartbeatEvery)
	if pct, err := daemon.BatteryPct(ctx); err == nil {
		every = daemon.ThrottleForBattery(pct, every)
		fmt.Fprintf(os.Stderr, "battery %d%% — heartbeat every %s\n", pct, every)
	}
	hb := &daemon.Heartbeat{WorkspaceDir: a.cfg.WorkspaceDir, Broker: broker, Eng: a.eng}
	sched := &daemon.Scheduler{Every: every, Fn: func(c context.Context) { hb.Tick(c) }}
	go sched.Start(ctx)
	defer sched.Stop()
	// Background housekeeping: drop finished subagent tasks older than a day.
	prune := &daemon.Scheduler{Every: time.Hour, Fn: func(c context.Context) {
		_ = c
		a.tasks.Prune(24 * time.Hour)
	}}
	go prune.Start(ctx)
	defer prune.Stop()
	// Background switch-back: when a key's cooldown timer runs out, probe
	// it token-free and return healthy keys to rotation automatically.
	if p, ok := a.prov.(*pool.Pool); ok && p != nil {
		go p.StartProber(ctx, 60*time.Second, func(c context.Context, tgt pool.ProbeTarget) error {
			return pool.ModelsProbe(c, tgt.BaseURL, tgt.APIKey)
		})
	}
	// LAN discovery (best effort — serve works via direct IP regardless).
	if !noMDNS {
		ips, _ := netdiscover.LocalIPs()
		host, _ := os.Hostname()
		go func() {
			svc := netdiscover.Service{Instance: firstNonEmpty(host, "nimbus-one"), Host: firstNonEmpty(host, "nimbus-one") + ".local", Port: a.cfg.HTTPPort, TXT: map[string]string{"mode": mode, "v": version}}
			for _, ip := range ips {
				svc.IPs = append(svc.IPs, ip)
			}
			if err := netdiscover.Advertise(ctx, svc); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: mDNS unavailable (%v) — use direct IP.\n", err)
			}
		}()
	}
	// HTTP API.
	addr := fmt.Sprintf("%s:%d", a.cfg.HTTPBind, a.cfg.HTTPPort)
	if isNonLoopback(a.cfg.HTTPBind) && a.cfg.HTTPToken == "" {
		tok := ""
		if a.secrets != nil {
			tok, _ = a.secrets.Get("http_token")
		}
		if tok == "" {
			fmt.Fprintln(os.Stderr, "REFUSING to serve LAN without a token — set one via `nimbus-one secrets set http_token <v>` or bind 127.0.0.1.")
			return 1
		}
		a.cfg.HTTPToken = tok
	}
	srv := &gateway.Server{Addr: addr, Token: a.cfg.HTTPToken, Broker: broker}
	// Voice engines: STT for .ogg notes + web uploads, TTS for /speak +
	// web playback. OpenAI key optional — whisper.cpp / local engines win
	// when present; endpoints stay live with 501 guidance otherwise.
	voiceKey := secretOrEnv(a.secrets, a.cfg, "openai", "OPENAI_API_KEY")
	srv.Transcribe = func(ctx context.Context, oggPath string) (string, error) {
		return media.Transcribe(ctx, oggPath, media.STTConfig{APIKey: voiceKey})
	}
	srv.Synthesize = func(ctx context.Context, text string) (string, error) {
		return media.Synthesize(ctx, text, media.TTSConfig{APIKey: voiceKey})
	}
	go func() {
		if err := srv.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "http: %v\n", err)
		}
	}()
	fmt.Fprintf(os.Stderr, "serving http://%s (mode=%s)\n", addr, mode)
	// Telegram polling (allowlist enforced inside).
	if tok := telegramToken(a); tok != "" {
		tg := &gateway.Telegram{Token: tok, Broker: broker, Allow: a.cfg.TelegramAllow}
		tg.Policy = &gateway.PolicyStore{}
		tg.Health = &gateway.HealthMonitor{Notify: func(msg string) {
			fmt.Fprintf(os.Stderr, "WARNING: %s\n", secure.Redact(msg))
		}}
		tg.Transcriber = func(ctx context.Context, oggPath string) (string, error) {
			return media.Transcribe(ctx, oggPath, media.STTConfig{APIKey: voiceKey})
		}
		tg.Speaker = func(ctx context.Context, text string) (string, error) {
			return media.Synthesize(ctx, text, media.TTSConfig{APIKey: voiceKey})
		}
		go func() {
			if err := tg.RunPolling(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: telegram: %v\n", secure.Redact(err.Error()))
			}
		}()
		fmt.Fprintln(os.Stderr, "telegram polling on")
	} else {
		fmt.Fprintln(os.Stderr, "telegram off (no token — `nimbus-one secrets set telegram <token>`)")
	}
	// Discord: full WS gateway (inbound) + REST sender. Falls back to
	// REST-only idle when the gateway can't connect (logged, never fatal).
	if tok := discordToken(a); tok != "" {
		gw := &gateway.Gateway{Token: tok, Broker: broker}
		go func() {
			if err := gw.Connect(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "WARNING: discord gateway: %v (REST send still works)\n", secure.Redact(err.Error()))
			}
		}()
		fmt.Fprintln(os.Stderr, "discord gateway on")
	}
	// REPL attached to the same broker when interactive.
	if isTTY() {
		go gateway.RunREPL(ctx, broker, "local")
	}
	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "\nshutting down.")
	return 0
}

func isNonLoopback(bind string) bool {
	b := strings.TrimSpace(bind)
	return b != "" && b != "127.0.0.1" && b != "localhost" && b != "::1"
}

func telegramToken(a *app) string {
	if a.secrets != nil {
		if v, err := a.secrets.Get("telegram"); err == nil && v != "" {
			return v
		}
	}
	if a.cfg.TelegramToken != "" {
		return a.cfg.TelegramToken
	}
	return os.Getenv("TELEGRAM_BOT_TOKEN")
}

func discordToken(a *app) string {
	if a.secrets != nil {
		if v, err := a.secrets.Get("discord"); err == nil && v != "" {
			return v
		}
	}
	return os.Getenv("DISCORD_BOT_TOKEN")
}

// ---------- skills / secrets / models / discover / doctor / status ----------

func cmdSkills(args []string) int {
	_ = args
	ctx := context.Background()
	a, err := bootstrap(ctx, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skills:", err)
		return 1
	}
	fmt.Print(a.skillz.CapabilitiesPrompt())
	return 0
}

func cmdSecrets(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: nimbus-one secrets set|get|del|list [key] [value]")
		return 2
	}
	ctx := context.Background()
	a, err := bootstrap(ctx, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secrets:", err)
		return 1
	}
	if a.secrets == nil {
		fmt.Fprintln(os.Stderr, "secrets store unavailable (vault failed).")
		return 1
	}
	switch args[0] {
	case "set":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nimbus-one secrets set <key> [value]  (no value → prompt)")
			return 2
		}
		val := ""
		if len(args) > 2 {
			val = args[2]
		} else {
			v, err := readSecret("value (hidden where possible): ")
			if err != nil || v == "" {
				fmt.Fprintln(os.Stderr, "aborted, nothing changed.")
				return 1
			}
			val = v
		}
		if err := a.secrets.Set(args[1], val); err != nil {
			fmt.Fprintln(os.Stderr, "set:", err)
			return 1
		}
		fmt.Printf("saved %q (encrypted). Known keys: %s\n", args[1], strings.Join(secure.KnownKeys(), ", "))
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nimbus-one secrets get <key>")
			return 2
		}
		v, err := a.secrets.Get(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "get:", err)
			return 1
		}
		fmt.Println(v) // user explicitly asked; their terminal, their secret
	case "del", "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nimbus-one secrets del <key>")
			return 2
		}
		if err := a.secrets.Delete(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "del:", err)
			return 1
		}
		fmt.Printf("deleted %q.\n", args[1])
	case "list":
		keys := a.secrets.List()
		if len(keys) == 0 {
			fmt.Println("(no secrets stored — values are never shown here)")
			return 0
		}
		fmt.Println("stored keys (values never shown):")
		for _, k := range keys {
			fmt.Println("  " + k)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: nimbus-one secrets set|get|del|list")
		return 2
	}
	return 0
}

func cmdModels(args []string) int {
	_ = args
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	models, err := llm.ListOpenRouterModels(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: catalog unreachable (%v) — showing built-in fallback chain.\n", secure.Redact(err.Error()))
	}
	fmt.Println("Models (OpenRouter; offline suggestions when unreachable):")
	for _, m := range models {
		mark := ""
		if m.ID == llm.SuggestedModels[0] {
			mark = "  ← suggested (Meta Muse 1.3) — your choice, never auto-applied"
		}
		fmt.Printf("  %s%s\n", m.ID, mark)
	}
	fmt.Println("\nTo choose fallbacks: `nimbus-one config`, or set fallback_models in config.yaml, or NIMBUS_FALLBACK_MODELS.")
	return 0
}

func cmdDiscover(args []string) int {
	_ = args
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	fmt.Println("Scanning LAN for _nimbus._tcp (5s)…")
	svcs, err := netdiscover.Browse(ctx, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover:", err)
		return 1
	}
	if len(svcs) == 0 {
		fmt.Println("No peers found. (mDNS needs Wi-Fi multicast; serve still works via direct IP.)")
		return 0
	}
	for _, s := range svcs {
		fmt.Printf("  %s:%d  %v  %v\n", s.Host, s.Port, s.IPs, s.TXT)
	}
	return 0
}

func cmdDoctor(args []string) int {
	bundle := ""
	for i, a := range args {
		if a == "--bundle" && i+1 < len(args) {
			bundle = args[i+1]
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := config.DefaultConfig()
	checks := doctor.Run(ctx, cfg.DataDir)
	fails, warns := 0, 0
	for _, c := range checks {
		mark := "✓"
		switch c.Status {
		case "warn":
			mark = "!"
			warns++
		case "fail":
			mark = "✗"
			fails++
		}
		fmt.Printf("[%s] %s: %s\n", mark, c.Title, c.Detail)
		if c.Status != "ok" && c.Fix != "" {
			fmt.Printf("    fix: %s\n", c.Fix)
		}
	}
	fmt.Printf("\n%d ok, %d warnings, %d failures\n", len(checks)-warns-fails, warns, fails)
	if bundle != "" {
		zb, err := doctor.Bundle(ctx, cfg.DataDir, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bundle:", err)
			return 1
		}
		if err := os.WriteFile(bundle, zb, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "bundle:", err)
			return 1
		}
		fmt.Printf("Redacted support bundle → %s (safe to share).\nAttach it at %s\n", bundle, doctor.SupportEndpoint())
	}
	if fails > 0 {
		return 1
	}
	return 0
}

// cmdFix searches the built-in fix-it knowledge base — the same database
// the doctor uses, mirrored in docs/TROUBLESHOOTING.md for AI agents.
func cmdFix(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nimbus-one fix <symptom...>  (e.g. `nimbus-one fix 429`, `nimbus-one fix telegram token`)")
		fmt.Fprintln(os.Stderr, "known topics:")
		for _, e := range doctor.Knowledge {
			fmt.Fprintf(os.Stderr, "  %-14s %s\n", e.ID, e.Title)
		}
		return 2
	}
	found := doctor.Find(strings.Join(args, " "), 3)
	if len(found) == 0 {
		fmt.Println("No match in the knowledge base. Run `nimbus-one doctor`, then attach `nimbus-one doctor --bundle support.zip` when asking for help at:")
		fmt.Println("  " + doctor.SupportEndpoint())
		return 1
	}
	for i, e := range found {
		if i > 0 {
			fmt.Println("---")
		}
		fmt.Printf("[%s] %s\ncause: %s\nfix: %s\n", e.ID, e.Title, e.Cause, e.Fix)
		for _, c := range e.Commands {
			fmt.Printf("  $ %s\n", c)
		}
	}
	return 0
}

// cmdUpdate pulls the latest GitHub release ONLY with user consent and a
// full data backup first. Code-level fixes arrive this way; user data and
// memories are preserved by construction (backup + atomic replace).
func cmdUpdate(args []string) int {
	yes := hasFlag(args, "--yes")
	repo, _ := flagVal(args, "repo", "")
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("NIMBUS_REPO"))
	}
	if repo == "" {
		repo = "bm-a/nimbus-one"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg := config.DefaultConfig()
	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "update: cannot locate running binary:", err)
		return 1
	}
	ucfg := update.Config{Repo: repo, Current: version, DataDir: cfg.DataDir, BinPath: bin}
	latest, _, err := update.LatestRelease(ctx, ucfg)
	if err != nil {
		if strings.Contains(err.Error(), " 404") {
			fmt.Fprintln(os.Stderr, "update: no releases published yet — build from source: git pull && make build.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "update: release check failed (%v) — offline? Try again on Wi-Fi, or build from source: git pull && make build.\n", secure.Redact(err.Error()))
		return 1
	}
	if !update.NeedsUpdate(latest, version) {
		fmt.Printf("Already current (local %s, latest %s).\n", version, latest)
		return 0
	}
	fmt.Printf("Update available: %s → %s (repo %s).\n", version, latest, repo)
	fmt.Printf("Backup first, then atomic binary replace. Your vault, secrets, workspace, and memories are preserved.\n")
	if !yes {
		if !isTTY() {
			fmt.Fprintln(os.Stderr, "Non-interactive shell: re-run with --yes to consent explicitly.")
			return 2
		}
		fmt.Print("Proceed? [y/N]: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
			fmt.Println("Aborted — nothing changed.")
			return 0
		}
	}
	backup, err := update.BackupDataDir(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "update: backup failed — refusing to continue:", err)
		return 1
	}
	fmt.Println("Backed up to " + backup)
	res, err := update.Run(ctx, ucfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed (%v) — your data is intact at %s.\n", secure.Redact(err.Error()), backup)
		return 1
	}
	fmt.Println(res + ". Restart the binary to run the new version.")
	return 0
}

// cmdSelftest simulates real-life failures and proves recovery: poisoned
// keys, outages, flakes, overflow, missing sidecars, vault reloads, skill
// runs, HTTP paths, telegram gates, LAN scans, doctor, knowledge base.
func cmdSelftest(args []string) int {
	_ = args
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	results := selftest.Run(ctx)
	fails := 0
	for _, r := range results {
		mark := "PASS"
		if !r.Pass {
			mark = "FAIL"
			fails++
		}
		fmt.Printf("[%s] %-10s %s\n", mark, r.ID, r.Title)
		if !r.Pass || strings.TrimSpace(r.Detail) != "" {
			fmt.Printf("       %s\n", r.Detail)
		}
	}
	fmt.Printf("\n%d/%d scenarios pass\n", len(results)-fails, len(results))
	if fails > 0 {
		return 1
	}
	return 0
}

// cmdSpeak plays text via the local speech engine (live on phones).
func cmdSpeak(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nimbus-one speak <text...>")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := media.Speak(ctx, strings.Join(args, " ")); err != nil {
		fmt.Fprintln(os.Stderr, "speak:", err)
		return 1
	}
	return 0
}

// cmdTranscribe prints the transcript of an audio file.
func cmdTranscribe(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nimbus-one transcribe <audio-file>")
		return 2
	}
	cfg := media.STTConfig{}
	if m, rest := flagVal(args, "model", ""); m != "" {
		cfg.Model = m
		args = rest
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	text, err := media.Transcribe(ctx, args[0], cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "transcribe:", err)
		return 1
	}
	fmt.Println(text)
	return 0
}

func cmdStatus(cmd string, args []string) int {
	_ = args
	ctx := context.Background()
	a, err := bootstrap(ctx, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "status:", err)
		return 1
	}
	if cmd == "dashboard" {
		if !isTTY() {
			fmt.Fprintln(os.Stderr, "dashboard needs a terminal — use `nimbus-one status` instead.")
			return 1
		}
		var stats []tui.Stat
		if p, ok := a.prov.(*pool.Pool); ok && p != nil {
			for _, s := range p.Stats() {
				stats = append(stats, tui.Stat{ID: s.ID, State: s.State, AvgMs: s.AvgMs, InFlight: s.InFlight, CooldownS: s.CooldownRemainS})
			}
		}
		if err := tui.RunDashboard(stats); err != nil {
			fmt.Fprintln(os.Stderr, "dashboard:", err)
			return 1
		}
		return 0
	}
	fmt.Printf("nimbus-one %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("data: %s   model: %s   mode default: build\n", a.cfg.DataDir, a.cfg.PrimaryModel)
	fmt.Printf("http: %s:%d   heartbeat: %s   skills: %d tools: %d\n",
		a.cfg.HTTPBind, a.cfg.HTTPPort, a.cfg.HeartbeatEvery, len(a.skillz.Skills), a.tools.Count())
	configured := []string{}
	for _, p := range []struct{ name, env string }{
		{"openrouter", "OPENROUTER_API_KEY"}, {"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"}, {"groq", "GROQ_API_KEY"},
		{"gemini", "GEMINI_API_KEY"}, {"deepseek", "DEEPSEEK_API_KEY"},
		{"meta", "META_API_KEY"},
	} {
		if secretOrEnv(a.secrets, a.cfg, p.name, p.env) != "" {
			configured = append(configured, p.name)
		}
	}
	fmt.Printf("providers with keys: %s\n", strings.Join(configured, ", "))
	if len(configured) == 0 {
		fmt.Println("WARNING: no provider keys — `nimbus-one config` (recommended: OpenRouter + Meta Muse 1.3) or local Ollama.")
	}
	fmt.Printf("telegram: %s   discord: %s\n", onOff(telegramToken(a) != ""), onOff(discordToken(a) != ""))
	if telegramToken(a) != "" && len(a.cfg.TelegramAllow) == 0 {
		fmt.Println("WARNING: telegram token set but allowlist empty — anyone can talk to your bot. Set TELEGRAM_ALLOW_FROM.")
	}
	return 0
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ---------- small platform helpers ----------

func isTTY() bool {
	if runtime.GOOS == "windows" {
		// Best effort: CON exists on consoles.
		f, err := os.OpenFile("CON", os.O_RDWR, 0)
		if err != nil {
			return false
		}
		_ = f.Close()
		return true
	}
	if _, err := os.Stat("/dev/tty"); err != nil {
		return false
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func ollamaModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/api/tags", nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama not reachable: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(b, &out)
	names := []string{}
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

func localIPs() []string {
	ips := []string{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, inf := range ifaces {
		addrs, err := inf.Addrs()
		if err != nil {
			continue
		}
		for _, ad := range addrs {
			var ip net.IP
			switch v := ad.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	return ips
}
