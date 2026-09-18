package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nimbus-one/internal/config"
	"nimbus-one/internal/secure"
)

func testVaultAndStore(t *testing.T, dir string) *secure.Store {
	t.Helper()
	if err := os.Unsetenv("NIMBUS_VAULT_KEY"); err != nil {
		// best-effort; Setenv-based cleanup in subtests may still apply
	}
	v, err := secure.LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	s, err := secure.OpenSecrets(dir, v)
	if err != nil {
		t.Fatalf("OpenSecrets: %v", err)
	}
	return s
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY",
		"GROQ_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY",
		"TELEGRAM_BOT_TOKEN", "NIMBUS_HTTP_TOKEN",
	} {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v) // registers restore
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("Unsetenv %s: %v", k, err)
			}
		}
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		t.Fatalf("clearEnv failed: OPENROUTER_API_KEY still set")
	}
}

// isolateHomeAndPath hides the host's opencode binary and auth files so
// detection tests are hermetic regardless of the dev machine.
func isolateHomeAndPath(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
}

func TestDetectCleanTempDirReturnsWarningsNoError(t *testing.T) {
	clearEnv(t)
	isolateHomeAndPath(t)
	dir := t.TempDir()
	s := testVaultAndStore(t, dir)
	cfg := &config.Config{
		DataDir:        dir,
		WorkspaceDir:   filepath.Join(dir, "workspace"),
		SkillsDir:      filepath.Join(dir, "skills"),
		ConfigFile:     filepath.Join(dir, "config.yaml"),
		HTTPPort:       8787,
		HTTPBind:       "127.0.0.1",
		PrimaryModel:   "gpt-4o-mini",
		HeartbeatEvery: "15m",
		APIKeys:        map[string]string{},
		BaseURLs:       map[string]string{},
	}
	rep, err := Detect(context.Background(), cfg, s)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if rep == nil {
		t.Fatalf("Detect returned nil report")
	}
	if len(rep.Warnings) == 0 {
		t.Fatalf("expected warnings on clean temp dir, got none")
	}
	// Probe failures must be warnings, not errors: Detect succeeds even
	// when ollama/opencode/network are absent.
}

func TestDetectProbeFailuresAreWarningsNotErrors(t *testing.T) {
	clearEnv(t)
	isolateHomeAndPath(t)
	dir := t.TempDir()
	s := testVaultAndStore(t, dir)
	cfg := &config.Config{
		DataDir:      dir,
		WorkspaceDir: filepath.Join(dir, "workspace"),
		SkillsDir:    filepath.Join(dir, "skills"),
		ConfigFile:   filepath.Join(dir, "config.yaml"),
		HTTPPort:     8787,
		HTTPBind:     "127.0.0.1",
		APIKeys:      map[string]string{},
		BaseURLs:     map[string]string{},
	}
	// PATH/HOME already isolated above so opencode lookup fails; ollama on
	// localhost:11434 is almost certainly absent in CI. Both must degrade
	// to warnings.
	rep, err := Detect(context.Background(), cfg, s)
	if err != nil {
		t.Fatalf("Detect must not fail hard: %v", err)
	}
	if len(rep.Warnings) == 0 {
		t.Fatalf("expected probe-failure warnings, got none")
	}
	if len(rep.Actions) == 0 {
		t.Fatalf("expected fallback actions, got none")
	}
}

func TestApplyGeneratesTokenAndWritesConfig(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	s := testVaultAndStore(t, dir)
	cfg := &config.Config{
		DataDir:        dir,
		WorkspaceDir:   filepath.Join(dir, "workspace"),
		SkillsDir:      filepath.Join(dir, "skills"),
		ConfigFile:     filepath.Join(dir, "config.yaml"),
		HTTPPort:       8787,
		HTTPBind:       "127.0.0.1",
		PrimaryModel:   "gpt-4o-mini",
		HeartbeatEvery: "15m",
		APIKeys:        map[string]string{},
		BaseURLs:       map[string]string{},
	}
	rep, err := Detect(context.Background(), cfg, s)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if err := Apply(context.Background(), cfg, s, rep); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.TrimSpace(cfg.HTTPToken) == "" {
		t.Fatalf("expected HTTP token generated")
	}
	if len(cfg.HTTPToken) != 64 {
		t.Fatalf("expected 32-byte hex token (64 chars), got %d", len(cfg.HTTPToken))
	}
	stored, err := s.Get("http_token")
	if err != nil {
		t.Fatalf("http_token not in secrets: %v", err)
	}
	if stored != cfg.HTTPToken {
		t.Fatalf("secrets token mismatch")
	}
	if len(rep.Actions) == 0 {
		t.Fatalf("expected actions recorded")
	}
	if _, err := os.Stat(cfg.ConfigFile); err != nil {
		t.Fatalf("config.yaml not written: %v", err)
	}
}

func TestApplyDoesNotOverwritePresetToken(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	s := testVaultAndStore(t, dir)
	const preset = "preset-token-abc123-preset-token-abc123-preset!!"
	if err := s.Set("http_token", preset); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cfg := &config.Config{
		DataDir:        dir,
		WorkspaceDir:   filepath.Join(dir, "workspace"),
		SkillsDir:      filepath.Join(dir, "skills"),
		ConfigFile:     filepath.Join(dir, "config.yaml"),
		HTTPPort:       8787,
		HTTPBind:       "127.0.0.1",
		HTTPToken:      preset,
		PrimaryModel:   "gpt-4o-mini",
		HeartbeatEvery: "15m",
		APIKeys:        map[string]string{},
		BaseURLs:       map[string]string{},
	}
	rep := &Report{DataDir: dir}
	if err := Apply(context.Background(), cfg, s, rep); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if cfg.HTTPToken != preset {
		t.Fatalf("preset token overwritten: got %q", cfg.HTTPToken)
	}
	got, err := s.Get("http_token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != preset {
		t.Fatalf("secrets preset overwritten: got %q", got)
	}
}

func TestApplyWritesConfigWithoutOverwritingUserValues(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	s := testVaultAndStore(t, dir)
	// Pre-existing user config with a custom primary_model.
	userCfg := "primary_model: my-custom-model\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(userCfg), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := &config.Config{
		DataDir:        dir,
		WorkspaceDir:   filepath.Join(dir, "workspace"),
		SkillsDir:      filepath.Join(dir, "skills"),
		ConfigFile:     filepath.Join(dir, "config.yaml"),
		HTTPPort:       8787,
		HTTPBind:       "127.0.0.1",
		PrimaryModel:   "gpt-4o-mini",
		HeartbeatEvery: "15m",
		APIKeys:        map[string]string{},
		BaseURLs:       map[string]string{},
	}
	rep := &Report{DataDir: dir}
	if err := Apply(context.Background(), cfg, s, rep); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "my-custom-model") {
		t.Fatalf("user-set primary_model overwritten: %q", data)
	}
	if strings.Contains(string(data), "gpt-4o-mini") {
		t.Fatalf("default overwrote user value: %q", data)
	}
}

func TestWarningsRequired(t *testing.T) {
	clearEnv(t)
	isolateHomeAndPath(t)
	dir := t.TempDir()
	s := testVaultAndStore(t, dir)

	// LAN bind without token → refusing to serve LAN without token.
	cfg := &config.Config{
		DataDir:      dir,
		WorkspaceDir: filepath.Join(dir, "workspace"),
		SkillsDir:    filepath.Join(dir, "skills"),
		ConfigFile:   filepath.Join(dir, "config.yaml"),
		HTTPPort:     8787,
		HTTPBind:     "0.0.0.0",
		APIKeys:      map[string]string{},
		BaseURLs:     map[string]string{},
	}
	rep, err := Detect(context.Background(), cfg, s)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "refusing to serve LAN without token") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected LAN-without-token warning, got %v", rep.Warnings)
	}
	// Apply must force-generate a token in that case.
	if err := Apply(context.Background(), cfg, s, rep); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.TrimSpace(cfg.HTTPToken) == "" {
		t.Fatalf("expected forced token generation for LAN bind")
	}

	// Telegram token without allowlist → open-to-anyone.
	clearEnv(t)
	dir2 := t.TempDir()
	s2 := testVaultAndStore(t, dir2)
	cfg2 := &config.Config{
		DataDir:       dir2,
		WorkspaceDir:  filepath.Join(dir2, "workspace"),
		SkillsDir:     filepath.Join(dir2, "skills"),
		ConfigFile:    filepath.Join(dir2, "config.yaml"),
		HTTPPort:      8787,
		HTTPBind:      "127.0.0.1",
		TelegramToken: "tg-test-token",
		APIKeys:       map[string]string{},
		BaseURLs:      map[string]string{},
	}
	rep2, _ := Detect(context.Background(), cfg2, s2)
	found = false
	for _, w := range rep2.Warnings {
		if strings.Contains(strings.ToLower(w), "open-to-anyone") || strings.Contains(strings.ToLower(w), "open to anyone") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected telegram open-to-anyone warning, got %v", rep2.Warnings)
	}

	// No LLM backend → suggest nimbus-one models / ollama.
	rep3, _ := Detect(context.Background(), cfg2, s2)
	found = false
	for _, w := range rep3.Warnings {
		lw := strings.ToLower(w)
		if strings.Contains(w, "nimbus-one models") || (strings.Contains(lw, "no llm backend") && strings.Contains(lw, "ollama")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected no-backend warning with `nimbus-one models`/ollama, got %v", rep3.Warnings)
	}

	// Running as root → warn (only enforced when actually root).
	if os.Geteuid() == 0 {
		found = false
		for _, w := range rep3.Warnings {
			if strings.Contains(strings.ToLower(w), "root") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected root warning when running as root, got %v", rep3.Warnings)
		}
	}
}
