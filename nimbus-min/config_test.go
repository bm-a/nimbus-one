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

func TestAPIKeyEnvFallback(t *testing.T) {
	testConfigPath(t)
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	c := &Config{Workspace: t.TempDir()}
	if c.apiKey() != "env-key" {
		t.Fatal("env fallback broken")
	}
	c.APIKey = "file-key"
	if c.apiKey() != "file-key" {
		t.Fatal("config key must win over env")
	}
}

func TestOnboardFlow(t *testing.T) {
	p := testConfigPath(t)
	ws := filepath.Join(t.TempDir(), "mywork")
	stdin := strings.NewReader("yes\n\n" + ws + "\n")
	var stdout strings.Builder
	workspaceRoot = ""
	if err := onboard(stdin, &stdout); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Nimbus-One", "workspace", "yes", "Anthropic"} {
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
