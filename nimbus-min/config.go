package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config is the entire Nimbus-One configuration: provider, model, key
// reference, workspace, HTTP token. Anything else is out of scope.
type Config struct {
	// Provider selects a row from the providers table ("anthropic"
	// default). Empty = default. See `nimbus-min models`.
	Provider string `json:"provider,omitempty"`
	// Model overrides the provider's default. Empty = provider default;
	// providers with no default refuse until one is set.
	Model string `json:"model,omitempty"`
	// BaseURL overrides the provider's endpoint (self-hosted gateways
	// like vLLM/SGLite/LiteLLM, proxies, custom servers such as a key
	// in an unconventional env var). The wire shape stays the
	// provider's — pair the override with the matching provider id
	// (e.g. provider "ollama" shape for any OpenAI-compatible server).
	BaseURL string `json:"base_url,omitempty"`
	// APIKey holds the key directly, or "" when the key comes from the
	// provider's env vars (or NIMBUS_API_KEY) instead.
	APIKey    string `json:"api_key,omitempty"`
	Workspace string `json:"workspace"`
	HTTPToken string `json:"http_token,omitempty"`
}

// configDir returns the platform config directory.
func configDir() string {
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "nimbus-one")
		}
	}
	if runtime.GOOS == "android" || isTermux() {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".nimbus-one")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "nimbus-one")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nimbus-one")
}

func isTermux() bool {
	home, _ := os.UserHomeDir()
	return strings.HasPrefix(home, "/data/data/com.termux/files/")
}

// configPath is overridable in tests via NIMBUS_MIN_CONFIG.
func configPath() string {
	if p := os.Getenv("NIMBUS_MIN_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(configDir(), "nimbus.json")
}

// loadConfig reads the config file. Missing file is NOT an error here —
// callers decide whether to onboard (CLI) or fail with guidance.
func loadConfig() (*Config, error) {
	raw, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %v", err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config file is broken (%v) — run `nimbus-min onboard` to rebuild it", err)
	}
	return &c, nil
}

// saveConfig writes the config with 0600 permissions (it may hold a key).
func saveConfig(c *Config) error {
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o700); err != nil {
		return fmt.Errorf("create config dir: %v", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %v", err)
	}
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %v", err)
	}
	return os.Rename(tmp, configPath())
}

// validate checks the config the way the agent needs it: workspace must
// exist and lock cleanly. Key presence is checked separately so `onboard`
// can run before any key exists.
func (c *Config) validate() error {
	if strings.TrimSpace(c.Workspace) == "" {
		return fmt.Errorf("no workspace set — run `nimbus-min onboard` first")
	}
	return initJail(c.Workspace)
}
