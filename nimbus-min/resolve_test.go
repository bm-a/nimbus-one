package main

import (
	"strings"
	"testing"
)

func TestResolveDefaults(t *testing.T) {
	cfg := &Config{}
	t.Setenv("NIMBUS_PROVIDER", "")
	t.Setenv("NIMBUS_MODEL", "")
	t.Setenv("NIMBUS_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "k")
	r, err := resolveClient(cfg, "", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.provider.ID != "anthropic" || r.model != "claude-sonnet-4-5-20250929" || r.base != AnthropicBaseURL {
		t.Fatalf("defaults = %+v", r)
	}
	if _, ok := r.client.(*anthropicClient); !ok {
		t.Fatalf("anthropic must use native client, got %T", r.client)
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "envkey")
	t.Setenv("MISTRAL_API_KEY", "k")
	cfg := &Config{Provider: "groq", Model: "cfg-model"}
	t.Setenv("NIMBUS_PROVIDER", "deepseek")
	t.Setenv("NIMBUS_MODEL", "")
	// Env provider beats config; config model beats provider default.
	r, err := resolveClient(cfg, "", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.provider.ID != "deepseek" || r.model != "cfg-model" {
		t.Fatalf("env/config precedence = %s/%s", r.provider.ID, r.model)
	}
	// Flags beat everything.
	r, err = resolveClient(cfg, "mistral", "flag-model", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.provider.ID != "mistral" || r.model != "flag-model" {
		t.Fatalf("flag precedence = %s/%s", r.provider.ID, r.model)
	}
	if _, ok := r.client.(*openaiClient); !ok {
		t.Fatalf("openai-family must use compat client, got %T", r.client)
	}
	t.Setenv("NIMBUS_PROVIDER", "")
}

func TestResolveModelRequired(t *testing.T) {
	cfg := &Config{Provider: "openrouter"}
	t.Setenv("NIMBUS_PROVIDER", "")
	t.Setenv("OPENROUTER_API_KEY", "k")
	if _, err := resolveClient(cfg, "", "", ""); err == nil {
		t.Fatal("default-less provider without model must error")
	} else if !strings.Contains(err.Error(), "no default model") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestResolveKeyGuidance(t *testing.T) {
	cfg := &Config{Provider: "deepseek", Model: "m"}
	t.Setenv("NIMBUS_PROVIDER", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("NIMBUS_API_KEY", "")
	if _, err := resolveClient(cfg, "", "", ""); err == nil {
		t.Fatal("missing key must error")
	} else if !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Fatalf("error must name the env var: %v", err)
	}
}

func TestResolveBaseOverride(t *testing.T) {
	cfg := &Config{Provider: "ollama"}
	t.Setenv("NIMBUS_BASE_URL", "http://192.168.1.9:11434/v1")
	defer t.Setenv("NIMBUS_BASE_URL", "")
	r, err := resolveClient(cfg, "", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.base != "http://192.168.1.9:11434/v1" || r.provider.ID != "ollama" {
		t.Fatalf("base override = %+v", r)
	}
}

func TestResolveLocalNeedsNoKey(t *testing.T) {
	cfg := &Config{Provider: "ollama"}
	t.Setenv("NIMBUS_API_KEY", "")
	if _, err := resolveClient(cfg, "", "", ""); err != nil {
		t.Fatalf("local provider must not need a key: %v", err)
	}
}

func TestCmdModels(t *testing.T) {
	var out strings.Builder
	if err := cmdModels(&out); err != nil {
		t.Fatalf("models: %v", err)
	}
	text := out.String()
	for _, want := range []string{"anthropic", "deepseek", "ollama", "local", "Custom", "Not carried over"} {
		if !strings.Contains(text, want) {
			t.Fatalf("models output missing %q:\n%s", want, text)
		}
	}
}
