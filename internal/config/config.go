// Package config resolves cross-platform paths and loads configuration.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Config holds all runtime configuration.
type Config struct {
	DataDir        string
	WorkspaceDir   string
	SkillsDir      string
	ConfigFile     string
	HTTPPort       int
	HTTPBind       string
	HTTPToken      string
	TelegramToken  string
	TelegramAllow  []string
	DiscordToken   string
	HeartbeatEvery string
	PrimaryModel   string
	FallbackModels []string
	// Routes maps task kinds to models (e.g. explore=gpt-4o-mini).
	// User-controlled only: unset kinds inherit the primary model.
	// Never auto-populated, never silently switched.
	Routes       map[string]string
	APIKeys      map[string]string // provider -> key
	BaseURLs     map[string]string // provider -> base URL
	MaxSteps     int
	ContextLimit int
	LogLevel     string
}

// IsTermux reports whether we run inside Termux/Android.
func IsTermux() bool {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(home, "/data/data/com.termux/files/") {
		return true
	}
	if _, err := os.Stat("/data/data/com.termux/files/usr/bin"); err == nil {
		return true
	}
	return false
}

// DefaultDataDir returns the platform-appropriate data directory.
func DefaultDataDir() string {
	if IsTermux() {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".nimbus-one")
	}
	switch runtime.GOOS {
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "Nimbus-One")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming", "Nimbus-One")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "nimbus-one")
	default: // linux and others
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "nimbus-one")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "nimbus-one")
	}
}

// DefaultConfig returns sane defaults, overlaid with env vars.
func DefaultConfig() *Config {
	dataDir := DefaultDataDir()
	if env := os.Getenv("NIMBUS_DATA_DIR"); env != "" {
		dataDir = env
	}
	c := &Config{
		DataDir:        dataDir,
		WorkspaceDir:   filepath.Join(dataDir, "workspace"),
		SkillsDir:      filepath.Join(dataDir, "skills"),
		ConfigFile:     filepath.Join(dataDir, "config.yaml"),
		HTTPPort:       8787,
		HTTPBind:       "127.0.0.1",
		HTTPToken:      os.Getenv("NIMBUS_HTTP_TOKEN"),
		TelegramToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		DiscordToken:   os.Getenv("DISCORD_BOT_TOKEN"),
		HeartbeatEvery: "15m",
		PrimaryModel:   getenvOr("NIMBUS_MODEL", "gpt-4o-mini"),
		MaxSteps:       25,
		ContextLimit:   128000,
		LogLevel:       getenvOr("NIMBUS_LOG_LEVEL", "info"),
		APIKeys:        map[string]string{},
		BaseURLs:       map[string]string{},
	}
	if allow := os.Getenv("TELEGRAM_ALLOW_FROM"); allow != "" {
		c.TelegramAllow = strings.Split(allow, ",")
	}
	// Provider keys
	for _, kv := range [][2]string{
		{"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		{"deepseek", "DEEPSEEK_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
		{"ollama", ""},
	} {
		if kv[1] != "" {
			if v := os.Getenv(kv[1]); v != "" {
				c.APIKeys[kv[0]] = v
			}
		}
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		c.BaseURLs["openai"] = v
	}
	if v := os.Getenv("OLLAMA_BASE_URL"); v != "" {
		c.BaseURLs["ollama"] = v
	}
	if v := os.Getenv("NIMBUS_FALLBACK_MODELS"); v != "" {
		for _, m := range strings.Split(v, ",") {
			if m = strings.TrimSpace(m); m != "" {
				c.FallbackModels = append(c.FallbackModels, m)
			}
		}
	}
	return c
}

func getenvOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// WantsOllama reports whether the user opted into a local model: an ollama
// model in primary/fallbacks, or Ollama-specific env. Local-model errors
// and warnings surface only when this is true — unconfigured backends stay
// silent instead of spamming every run.
func (c *Config) WantsOllama() bool {
	if v := os.Getenv("OLLAMA_BASE_URL"); strings.TrimSpace(v) != "" {
		return true
	}
	if v := os.Getenv("OLLAMA_HOST"); strings.TrimSpace(v) != "" {
		return true
	}
	if v := os.Getenv("OLLAMA_MODEL"); strings.TrimSpace(v) != "" {
		return true
	}
	for _, m := range append([]string{c.PrimaryModel}, c.FallbackModels...) {
		l := strings.ToLower(strings.TrimSpace(m))
		if l == "ollama" || strings.HasPrefix(l, "ollama:") ||
			strings.HasPrefix(l, "llama") || strings.HasPrefix(l, "qwen") ||
			strings.HasPrefix(l, "mistral") || strings.HasPrefix(l, "mixtral") ||
			strings.HasPrefix(l, "phi") || strings.HasPrefix(l, "gemma") {
			return true
		}
	}
	return false
}

// EnsureDirs creates data/workspace/skills directories.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.WorkspaceDir, c.SkillsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Load reads config.yaml (simple key: value subset) over defaults.
func Load() (*Config, error) {
	c := DefaultConfig()
	data, err := os.ReadFile(c.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(strings.Trim(parts[1], `"' `))
		switch k {
		case "primary_model":
			c.PrimaryModel = v
		case "heartbeat_every":
			c.HeartbeatEvery = v
		case "http_port":
			var p int
			if _, err := strings.NewReader(v).Read(nil); err == nil {
				_ = p
			}
			// simple atoi
			n := 0
			for _, ch := range v {
				if ch >= '0' && ch <= '9' {
					n = n*10 + int(ch-'0')
				}
			}
			if n > 0 {
				c.HTTPPort = n
			}
		case "http_bind":
			c.HTTPBind = v
		case "telegram_token":
			c.TelegramToken = v
		case "fallback_models":
			// User-chosen fallback order, comma-separated. Empty = no
			// fallbacks (primary only). Never auto-populated.
			models := []string{}
			for _, m := range strings.Split(v, ",") {
				if m = strings.TrimSpace(m); m != "" {
					models = append(models, m)
				}
			}
			c.FallbackModels = models
		case "routes":
			// Per-kind model routing, comma-separated kind=model pairs.
			// Example: routes: explore=gpt-4o-mini, research=claude-sonnet-4
			if c.Routes == nil {
				c.Routes = map[string]string{}
			}
			for _, pair := range strings.Split(v, ",") {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) != 2 {
					continue
				}
				k := strings.ToLower(strings.TrimSpace(kv[0]))
				m := strings.TrimSpace(kv[1])
				if k != "" && m != "" {
					c.Routes[k] = m
				}
			}
		}
	}
	return c, nil
}

// Save writes the user-editable settings back to config.yaml. It only
// manages keys Nimbus-One itself owns; unknown keys and comments in the file
// are preserved by rewriting managed lines in place.
func (c *Config) Save() error {
	managed := map[string]string{
		"primary_model":   c.PrimaryModel,
		"heartbeat_every": c.HeartbeatEvery,
		"http_port":       itoa(c.HTTPPort),
		"http_bind":       c.HTTPBind,
		"fallback_models": strings.Join(c.FallbackModels, ", "),
		"routes":          encodeRoutes(c.Routes),
	}
	var lines []string
	seen := map[string]bool{}
	if data, err := os.ReadFile(c.ConfigFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				lines = append(lines, line)
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				lines = append(lines, line)
				continue
			}
			k := strings.TrimSpace(parts[0])
			if v, ok := managed[k]; ok {
				lines = append(lines, k+": "+v)
				seen[k] = true
				continue
			}
			lines = append(lines, line)
		}
	}
	for k, v := range managed {
		if !seen[k] {
			lines = append(lines, k+": "+v)
		}
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	tmp := c.ConfigFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.ConfigFile)
}

// encodeRoutes serializes kind=model pairs deterministically (sorted).
func encodeRoutes(routes map[string]string) string {
	if len(routes) == 0 {
		return ""
	}
	keys := make([]string, 0, len(routes))
	for k := range routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+routes[k])
	}
	return strings.Join(pairs, ", ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
