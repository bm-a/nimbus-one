// Package setup auto-detects local dependencies and writes safe defaults.
// Every external dependency (ollama/opencode/network) degrades to a
// Warning plus a documented next-step Action — Detect and Apply never
// fail hard because a probe failed.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nimbus-one/internal/config"
	"nimbus-one/internal/secure"
)

// Report summarizes environment detection and actions taken.
type Report struct {
	DataDir       string
	IsTermux      bool
	OllamaFound   bool
	OllamaModels  []string
	OpenCodeFound bool
	OpenCodeAuth  bool
	OpenRouterKey bool
	TelegramToken bool
	LANIPs        []string
	Warnings      []string
	Actions       []string
}

// Detect probes the environment and returns a Report.
// It never fails hard: every probe failure becomes a Warning + fallback Action.
func Detect(ctx context.Context, cfg *config.Config, secrets *secure.Store) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	rep := &Report{
		DataDir:  cfg.DataDir,
		IsTermux: config.IsTermux(),
	}

	// Ollama probe. Reported factually, but install guidance appears only
	// when the user opted into local models or has no other backend —
	// unconfigured backends stay silent instead of spamming every run.
	models, found := probeOllama(ctx)
	rep.OllamaFound = found
	rep.OllamaModels = models
	if !found && (cfg.WantsOllama() || !hasAnyProviderKey(cfg, secrets)) {
		addWarning(rep, "Ollama not found — install or set OPENROUTER_API_KEY")
		addAction(rep, "install ollama from https://ollama.com or set OPENROUTER_API_KEY (run `nimbus-one models` to configure)")
	}

	// opencode probe.
	binFound, authFound := probeOpenCode()
	rep.OpenCodeFound = binFound
	rep.OpenCodeAuth = authFound
	if !binFound {
		addWarning(rep, "opencode binary not found — install opencode or configure an LLM provider directly")
		addAction(rep, "install opencode from https://opencode.ai or set OPENROUTER_API_KEY (run `nimbus-one models` to configure)")
	} else if !authFound {
		addWarning(rep, "opencode auth not found — run `opencode auth` or set provider API keys")
		addAction(rep, "run `opencode auth` or set OPENROUTER_API_KEY (run `nimbus-one models` to configure)")
	}

	// OpenRouter key.
	rep.OpenRouterKey = hasOpenRouterKey(cfg, secrets)
	if !rep.OpenRouterKey {
		addAction(rep, "set OPENROUTER_API_KEY or store key via `nimbus-one models` (fallback: install ollama)")
	}

	// Telegram token.
	rep.TelegramToken = hasTelegramToken(cfg, secrets)
	if !rep.TelegramToken {
		addAction(rep, "set TELEGRAM_BOT_TOKEN to enable the telegram gateway")
	}

	// LAN IPs.
	ips, err := lanIPs()
	if err != nil {
		addWarning(rep, "could not list network interfaces: "+err.Error())
		addAction(rep, "check network interfaces manually with `ip addr`")
	} else {
		rep.LANIPs = ips
	}

	ensureSharedWarnings(rep, cfg, secrets)

	return rep, nil
}

// Apply writes safe defaults: ensures dirs, generates an HTTP token when
// missing, writes minimal config.yaml without overwriting user values,
// and hardens the data dir. Each write is recorded in rep.Actions.
func Apply(ctx context.Context, cfg *config.Config, secrets *secure.Store, rep *Report) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = ctx
	if cfg == nil {
		return fmt.Errorf("setup: nil config")
	}
	if secrets == nil {
		return fmt.Errorf("setup: nil secrets store")
	}
	if rep == nil {
		rep = &Report{}
	}
	if rep.DataDir == "" {
		rep.DataDir = cfg.DataDir
	}

	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// Resolve effective HTTP token: config > secrets > env.
	token := strings.TrimSpace(cfg.HTTPToken)
	if token == "" {
		if v, err := secrets.Get("http_token"); err == nil && strings.TrimSpace(v) != "" {
			token = strings.TrimSpace(v)
			cfg.HTTPToken = token
		}
	}
	if token == "" {
		if env := strings.TrimSpace(os.Getenv("NIMBUS_HTTP_TOKEN")); env != "" {
			token = env
			cfg.HTTPToken = env
		}
	}

	nonLoopback := isNonLoopbackBind(cfg.HTTPBind)
	if nonLoopback && token == "" {
		addWarning(rep, "refusing to serve LAN without token — generated a token; set NIMBUS_HTTP_TOKEN or bind 127.0.0.1")
	}

	if token == "" {
		tok, err := generateToken()
		if err != nil {
			return err
		}
		token = tok
		cfg.HTTPToken = tok
		if err := secrets.Set("http_token", tok); err != nil {
			return err
		}
		addAction(rep, "generated http_token and stored in secrets")
	} else {
		// Mirror the effective token into secrets without overwriting a preset.
		if _, err := secrets.Get("http_token"); err != nil {
			if err := secrets.Set("http_token", token); err != nil {
				return err
			}
			addAction(rep, "stored existing http token in secrets")
		}
	}

	if err := writeMinimalConfig(cfg, rep); err != nil {
		return err
	}

	if err := secure.HardenDataDir(cfg.DataDir); err != nil {
		return err
	}
	addAction(rep, "hardened data dir "+cfg.DataDir)

	// Re-evaluate key presence after any writes.
	if hasOpenRouterKey(cfg, secrets) {
		rep.OpenRouterKey = true
	}
	if hasTelegramToken(cfg, secrets) {
		rep.TelegramToken = true
	}
	ensureSharedWarnings(rep, cfg, secrets)

	return nil
}

// --- probes ---

func probeOllama(ctx context.Context) ([]string, bool) {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, "http://localhost:11434/api/tags", nil)
	if err != nil {
		return nil, false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		// Server answered but body unparseable: ollama exists, models unknown.
		return nil, true
	}
	out := []string{}
	for _, m := range body.Models {
		if strings.TrimSpace(m.Name) != "" {
			out = append(out, m.Name)
		}
	}
	return out, true
}

func probeOpenCode() (bool, bool) {
	found := false
	if _, err := exec.LookPath("opencode"); err == nil {
		found = true
	}
	auth := false
	if home, err := os.UserHomeDir(); err == nil {
		candidates := []string{
			filepath.Join(home, ".local", "share", "opencode", "auth.json"),
			filepath.Join(home, ".config", "opencode", "opencode.json"),
		}
		for _, p := range candidates {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				auth = true
				break
			}
		}
	}
	return found, auth
}

func hasOpenRouterKey(cfg *config.Config, secrets *secure.Store) bool {
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); v != "" {
		return true
	}
	if cfg != nil && strings.TrimSpace(cfg.APIKeys["openrouter"]) != "" {
		return true
	}
	if secrets != nil {
		if v, err := secrets.Get("openrouter"); err == nil && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func hasTelegramToken(cfg *config.Config, secrets *secure.Store) bool {
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); v != "" {
		return true
	}
	if cfg != nil && strings.TrimSpace(cfg.TelegramToken) != "" {
		return true
	}
	if secrets != nil {
		if v, err := secrets.Get("telegram"); err == nil && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func hasAnyProviderKey(cfg *config.Config, secrets *secure.Store) bool {
	type kv struct{ provider, env string }
	providers := []kv{
		{"openrouter", "OPENROUTER_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		{"deepseek", "DEEPSEEK_API_KEY"},
	}
	for _, p := range providers {
		if v := strings.TrimSpace(os.Getenv(p.env)); v != "" {
			return true
		}
		if cfg != nil && strings.TrimSpace(cfg.APIKeys[p.provider]) != "" {
			return true
		}
		if secrets != nil {
			if v, err := secrets.Get(p.provider); err == nil && strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}

func lanIPs() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue // best-effort per interface
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out, nil
}

// --- shared warnings ---

func ensureSharedWarnings(rep *Report, cfg *config.Config, secrets *secure.Store) {
	// HTTP bound non-loopback without token.
	tok := ""
	if cfg != nil {
		tok = strings.TrimSpace(cfg.HTTPToken)
	}
	if tok == "" {
		if env := strings.TrimSpace(os.Getenv("NIMBUS_HTTP_TOKEN")); env != "" {
			tok = env
		} else if secrets != nil {
			if v, err := secrets.Get("http_token"); err == nil && strings.TrimSpace(v) != "" {
				tok = v
			}
		}
	}
	bind := ""
	if cfg != nil {
		bind = cfg.HTTPBind
	}
	if isNonLoopbackBind(bind) && tok == "" {
		addWarning(rep, "refusing to serve LAN without token — set NIMBUS_HTTP_TOKEN or bind 127.0.0.1")
		addAction(rep, "set NIMBUS_HTTP_TOKEN or bind http to 127.0.0.1")
	}

	// Telegram token without allowlist.
	if hasTelegramToken(cfg, secrets) {
		allow := []string{}
		if cfg != nil {
			allow = cfg.TelegramAllow
		}
		if len(allow) == 0 {
			addWarning(rep, "Telegram token set but allowlist empty — bot is open-to-anyone; set TELEGRAM_ALLOW_FROM")
			addAction(rep, "set TELEGRAM_ALLOW_FROM to restrict telegram access")
		}
	}

	// No LLM backend at all (ollama or any provider API key).
	// Note: opencode presence alone does not count — it is a separate
	// agent harness, not a guaranteed LLM backend for nimbus-one.
	backend := rep.OllamaFound
	if !backend && hasAnyProviderKey(cfg, secrets) {
		backend = true
	}
	if !backend {
		addWarning(rep, "No LLM backend found — run `nimbus-one models` or install ollama from https://ollama.com")
		addAction(rep, "run `nimbus-one models` to configure a provider or install ollama")
	}

	// Running as root.
	if isRoot() {
		addWarning(rep, "running as root is not recommended — prefer a dedicated user")
		addAction(rep, "re-run as a non-root user")
	}
}

func isNonLoopbackBind(bind string) bool {
	b := strings.TrimSpace(strings.ToLower(bind))
	if b == "" {
		return true // empty bind typically means all interfaces
	}
	if b == "127.0.0.1" || b == "::1" || b == "localhost" {
		return false
	}
	if strings.HasPrefix(b, "127.") {
		return false
	}
	return true
}

func isRoot() bool {
	return os.Geteuid() == 0
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("setup: rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func writeMinimalConfig(cfg *config.Config, rep *Report) error {
	path := cfg.ConfigFile
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(cfg.DataDir, "config.yaml")
		cfg.ConfigFile = path
	}
	existing := map[string]bool{}
	var raw []byte
	if data, err := os.ReadFile(path); err == nil {
		raw = data
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			parts := strings.SplitN(t, ":", 2)
			if len(parts) != 2 {
				continue
			}
			k := strings.TrimSpace(parts[0])
			if k != "" {
				existing[k] = true
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	type kv struct{ key, val string }
	want := []kv{}
	if !existing["http_port"] && cfg.HTTPPort != 0 {
		want = append(want, kv{"http_port", strconv.Itoa(cfg.HTTPPort)})
	}
	if !existing["http_bind"] && strings.TrimSpace(cfg.HTTPBind) != "" {
		want = append(want, kv{"http_bind", strings.TrimSpace(cfg.HTTPBind)})
	}
	if !existing["primary_model"] && strings.TrimSpace(cfg.PrimaryModel) != "" {
		want = append(want, kv{"primary_model", strings.TrimSpace(cfg.PrimaryModel)})
	}
	if !existing["heartbeat_every"] && strings.TrimSpace(cfg.HeartbeatEvery) != "" {
		want = append(want, kv{"heartbeat_every", strings.TrimSpace(cfg.HeartbeatEvery)})
	}
	if len(want) == 0 {
		return nil
	}

	var buf strings.Builder
	if len(raw) > 0 {
		buf.Write(raw)
		if !strings.HasSuffix(string(raw), "\n") {
			buf.WriteString("\n")
		}
	} else {
		buf.WriteString("# Nimbus-One config (auto-generated minimal defaults)\n")
	}
	for _, item := range want {
		buf.WriteString(item.key + ": " + item.val + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	for _, item := range want {
		addAction(rep, "wrote config.yaml "+item.key)
	}
	return nil
}

func addWarning(rep *Report, w string) {
	for _, e := range rep.Warnings {
		if e == w {
			return
		}
	}
	rep.Warnings = append(rep.Warnings, w)
}

func addAction(rep *Report, a string) {
	for _, e := range rep.Actions {
		if e == a {
			return
		}
	}
	rep.Actions = append(rep.Actions, a)
}
