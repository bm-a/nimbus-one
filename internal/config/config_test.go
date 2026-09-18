package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDirNonEmpty(t *testing.T) {
	d := DefaultDataDir()
	if d == "" {
		t.Fatalf("DefaultDataDir empty")
	}
}

func TestDefaultConfigDefaults(t *testing.T) {
	c := DefaultConfig()
	if c == nil {
		t.Fatalf("DefaultConfig nil")
	}
	if c.HTTPPort != 8787 {
		t.Fatalf("expected HTTPPort 8787, got %d", c.HTTPPort)
	}
	if c.MaxSteps != 25 {
		t.Fatalf("expected MaxSteps 25, got %d", c.MaxSteps)
	}
	if c.DataDir == "" || c.WorkspaceDir == "" || c.ConfigFile == "" {
		t.Fatalf("expected dirs set: %+v", c)
	}
}

func TestEnsureDirsCreatesDirs(t *testing.T) {
	base := t.TempDir()
	c := &Config{
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: filepath.Join(base, "data", "workspace"),
		SkillsDir:    filepath.Join(base, "data", "skills"),
		ConfigFile:   filepath.Join(base, "data", "config.yaml"),
	}
	if err := c.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{c.DataDir, c.WorkspaceDir, c.SkillsDir} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatalf("dir %s missing: %v", d, err)
		}
		if !fi.IsDir() {
			t.Fatalf("%s not a dir", d)
		}
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NIMBUS_DATA_DIR", dir)
	// Ensure no config.yaml exists.
	_ = os.Remove(filepath.Join(dir, "config.yaml"))
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPPort != 8787 {
		t.Fatalf("expected default HTTPPort 8787, got %d", c.HTTPPort)
	}
	if c.MaxSteps != 25 {
		t.Fatalf("expected default MaxSteps 25, got %d", c.MaxSteps)
	}
	if c.DataDir != dir {
		t.Fatalf("expected DataDir %q, got %q", dir, c.DataDir)
	}
}

func TestWantsOllamaExplicit(t *testing.T) {
	c := DefaultConfig()
	c.PrimaryModel = "llama3.1"
	if !c.WantsOllama() {
		t.Fatal("llama primary should opt into ollama")
	}
	c.PrimaryModel = "gpt-4o-mini"
	c.FallbackModels = []string{"ollama:qwen2.5"}
	if !c.WantsOllama() {
		t.Fatal("ollama: fallback should opt in")
	}
}

func TestWantsOllamaSilentByDefault(t *testing.T) {
	c := DefaultConfig()
	c.PrimaryModel = "gpt-4o-mini"
	c.FallbackModels = nil
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_MODEL", "")
	if c.WantsOllama() {
		t.Fatal("cloud-only config must not opt into ollama errors")
	}
	t.Setenv("OLLAMA_HOST", "localhost:11434")
	if !c.WantsOllama() {
		t.Fatal("OLLAMA_HOST should opt in")
	}
}

func TestLoadConfigOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NIMBUS_DATA_DIR", dir)
	cfg := "primary_model: test-model-override\nheartbeat_every: 5m\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PrimaryModel != "test-model-override" {
		t.Fatalf("expected primary_model override, got %q", c.PrimaryModel)
	}
	if c.HeartbeatEvery != "5m" {
		t.Fatalf("expected heartbeat_every override, got %q", c.HeartbeatEvery)
	}
}
