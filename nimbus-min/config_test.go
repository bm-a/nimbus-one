package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfigPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nimbus.json")
	t.Setenv("NIMBUS_MIN_CONFIG", p)
	return p
}

func TestConfigRoundTrip(t *testing.T) {
	testConfigPath(t)
	c := &Config{APIKey: "sk-test", Workspace: t.TempDir(), HTTPToken: "tok"}
	if err := saveConfig(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	// File must be owner-only (it may hold a key).
	fi, err := os.Stat(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("config perm = %o, want 600", fi.Mode().Perm())
	}
	back, err := loadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if back.APIKey != "sk-test" || back.Workspace != c.Workspace || back.HTTPToken != "tok" {
		t.Fatalf("roundtrip = %+v", back)
	}
}

func TestConfigMissingIsEmpty(t *testing.T) {
	testConfigPath(t)
	c, err := loadConfig()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if c.Workspace != "" || c.APIKey != "" {
		t.Fatalf("missing file must load empty: %+v", c)
	}
	if err := c.validate(); err == nil {
		t.Fatal("empty config must fail validation with onboard guidance")
	}
}

func TestConfigBrokenJSON(t *testing.T) {
	p := testConfigPath(t)
	if err := os.WriteFile(p, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("broken JSON must error")
	}
}

func TestConfigUnknownFieldRejected(t *testing.T) {
	p := testConfigPath(t)
	if err := os.WriteFile(p, []byte(`{"workspace":"x","teleport":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("unknown field must error (typo protection)")
	}
}

func TestAPIKeyResolution(t *testing.T) {
	testConfigPath(t)
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	p, _ := lookupProvider("anthropic")
	c := &Config{Workspace: t.TempDir()}
	if got := resolveKey(c, p); got != "env-key" {
		t.Fatalf("env fallback = %q", got)
	}
	c.APIKey = "file-key"
	if got := resolveKey(c, p); got != "file-key" {
		t.Fatalf("config key must win, got %q", got)
	}
	t.Setenv("NIMBUS_API_KEY", "generic-key")
	c2 := &Config{}
	if got := resolveKey(c2, p); got != "env-key" {
		t.Fatalf("provider env must beat generic, got %q", got)
	}
	p2, _ := lookupProvider("deepseek")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if got := resolveKey(c2, p2); got != "generic-key" {
		t.Fatalf("generic fallback = %q", got)
	}
}

func TestOnboardFlow(t *testing.T) {
	p := testConfigPath(t)
	ws := filepath.Join(t.TempDir(), "mywork")
	// consent, provider default, model default, empty key, workspace.
	stdin := strings.NewReader("yes\n\n\n\n" + ws + "\n")
	var stdout strings.Builder
	workspaceRoot = ""
	if err := onboard(stdin, &stdout); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Nimbus-One", "workspace", "yes", "Provider", "Model"} {
		if !strings.Contains(out, want) {
			t.Fatalf("onboard output missing %q:\n%s", want, out)
		}
	}
	// Config written with resolved workspace + fresh token.
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(raw), "mywork") {
		t.Fatalf("config missing workspace: %s", raw)
	}
	back, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if back.HTTPToken == "" {
		t.Fatal("onboard must mint an HTTP token")
	}
	if _, err := os.Stat(ws); err != nil {
		t.Fatalf("workspace not created: %v", err)
	}
}

func TestOnboardDecline(t *testing.T) {
	testConfigPath(t)
	workspaceRoot = ""
	if err := onboard(strings.NewReader("no\n"), &strings.Builder{}); err == nil {
		t.Fatal("declining consent must cancel")
	}
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Fatal("cancelled onboard must not write config")
	}
}

func TestOnboardProviderChoice(t *testing.T) {
	p := testConfigPath(t)
	ws := filepath.Join(t.TempDir(), "w")
	// Pick #4 (deepseek) by number, accept its default model, fake key.
	stdin := strings.NewReader("yes\n4\n\nk-deep\n" + ws + "\n")
	workspaceRoot = ""
	if err := onboard(stdin, &strings.Builder{}); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	back, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if back.Provider != "deepseek" || back.Model != "deepseek-v4-flash" || back.APIKey != "k-deep" {
		t.Fatalf("config = %+v", back)
	}
	_ = p
}

func TestOnboardLocalSkipsKey(t *testing.T) {
	testConfigPath(t)
	ws := filepath.Join(t.TempDir(), "w")
	// lmstudio has no default model: proves the required-model path too.
	stdin := strings.NewReader("yes\nlmstudio\nmymodel\n" + ws + "\n")
	workspaceRoot = ""
	var out strings.Builder
	if err := onboard(stdin, &out); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	if !strings.Contains(out.String(), "no key") {
		t.Fatalf("local provider must skip key step:\n%s", out.String())
	}
	back, _ := loadConfig()
	if back.Provider != "lmstudio" || back.Model != "mymodel" || back.APIKey != "" {
		t.Fatalf("config = %+v", back)
	}
}

func TestOnboardRequiresModel(t *testing.T) {
	testConfigPath(t)
	ws := filepath.Join(t.TempDir(), "w")
	// openrouter has no default; empty model must fail.
	stdin := strings.NewReader("yes\nopenrouter\n\nk\n" + ws + "\n")
	workspaceRoot = ""
	if err := onboard(stdin, &strings.Builder{}); err == nil {
		t.Fatal("empty model for default-less provider must fail")
	}
}

func TestParseProviderAnswer(t *testing.T) {
	if p, err := parseProviderAnswer(""); err != nil || p.ID != "anthropic" {
		t.Fatalf("default = %+v, %v", p, err)
	}
	if p, err := parseProviderAnswer("4"); err != nil || p.ID != "deepseek" {
		t.Fatalf("number = %+v, %v", p, err)
	}
	if p, err := parseProviderAnswer("xAI"); err != nil || p.ID != "xai" {
		t.Fatalf("id = %+v, %v", p, err)
	}
	if _, err := parseProviderAnswer("nope"); err == nil {
		t.Fatal("unknown must error")
	}
}
