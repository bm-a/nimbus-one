package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nimbus-one/internal/secure"
)

func TestSecretsCheckAgreesWithVault(t *testing.T) {
	// Regression: doctor must use the same key parsing as the vault or a
	// fresh install reports its own secrets as corrupt.
	dir := t.TempDir()
	v, err := secure.LoadVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := secure.OpenSecrets(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("probe", "value"); err != nil {
		t.Fatal(err)
	}
	for _, c := range Run(testCtx(t), dir) {
		if c.ID == "secrets" && c.Status == StatusFail {
			t.Fatalf("fresh vault reported corrupt: %s", c.Detail)
		}
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func unzip(t *testing.T, bz []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(bz), int64(len(bz)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("zip entry %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			_ = rc.Close()
			t.Fatalf("zip read %s: %v", f.Name, err)
		}
		_ = rc.Close()
		out[f.Name] = buf.String()
	}
	return out
}

func TestRunReturnsChecks(t *testing.T) {
	dir := t.TempDir()
	checks := Run(testCtx(t), dir)
	if len(checks) < 8 {
		t.Fatalf("expected >=8 checks, got %d", len(checks))
	}
	seen := map[string]bool{}
	for _, c := range checks {
		if c.ID == "" || c.Title == "" {
			t.Errorf("check missing ID/Title: %+v", c)
		}
		switch c.Status {
		case StatusOK, StatusWarn, StatusFail:
		default:
			t.Errorf("check %s has invalid status %q", c.ID, c.Status)
		}
		if seen[c.ID] {
			t.Errorf("duplicate check ID %q", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestBundleRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	const fakeToken = "fake-telegram-token-ABCDEF1234567890"
	cfg := "primary_model: test-model\ntelegram_token: " + fakeToken + "\nhttp_port: 8787\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	bz, err := Bundle(testCtx(t), dir, false)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	files := unzip(t, bz)
	for _, want := range []string{"checks.json", "config.yaml", "versions.txt", "bundle.json"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("bundle missing %s (has %v)", want, keys(files))
		}
	}
	gotCfg := files["config.yaml"]
	if !strings.Contains(gotCfg, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in bundled config, got:\n%s", gotCfg)
	}
	if strings.Contains(gotCfg, fakeToken) {
		t.Errorf("bundled config leaks raw token")
	}
	for name, body := range files {
		if strings.Contains(body, fakeToken) {
			t.Errorf("bundle file %s contains raw token", name)
		}
	}
}

func TestBundleIncludeSecretsWarns(t *testing.T) {
	dir := t.TempDir()
	const fakeToken = "include-me-token-ZZTOP1234567890"
	cfg := "telegram_token: " + fakeToken + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	bz, err := Bundle(testCtx(t), dir, true)
	if err != nil {
		t.Fatalf("Bundle includeSecrets: %v", err)
	}
	files := unzip(t, bz)
	if _, ok := files["WARNING.txt"]; !ok {
		t.Errorf("expected WARNING.txt when includeSecrets=true")
	}
	if !strings.Contains(files["config.yaml"], fakeToken) {
		t.Errorf("expected raw value present when includeSecrets=true")
	}
}

func TestSystemPromptSecretsSafety(t *testing.T) {
	p := SystemPrompt()
	if strings.TrimSpace(p) == "" {
		t.Fatalf("SystemPrompt empty")
	}
	lower := strings.ToLower(p)
	for _, want := range []string{"secret", "never ask", "nimbus-one secrets set", "bundle"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("SystemPrompt missing %q", want)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
