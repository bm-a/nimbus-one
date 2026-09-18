package secure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) (string, *Store) {
	t.Helper()
	clearVaultEnv(t)
	dir := t.TempDir()
	v, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	s, err := OpenSecrets(dir, v)
	if err != nil {
		t.Fatalf("OpenSecrets: %v", err)
	}
	return dir, s
}

func TestSecretsSetGetRoundtrip(t *testing.T) {
	_, s := openTestStore(t)
	if err := s.Set("openrouter", "sk-or-test-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get("openrouter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "sk-or-test-123" {
		t.Fatalf("roundtrip mismatch: got %q", got)
	}
}

func TestSecretsMissingErrors(t *testing.T) {
	_, s := openTestStore(t)
	if _, err := s.Get("does-not-exist"); err == nil {
		t.Fatalf("expected error for missing key")
	}
	// Get must never return empty silently.
	v, err := s.Get("also-missing")
	if err == nil {
		t.Fatalf("expected error, got value %q", v)
	}
	if v != "" {
		t.Fatalf("missing key must return empty string, got %q", v)
	}
}

func TestSecretsDelete(t *testing.T) {
	_, s := openTestStore(t)
	if err := s.Set("groq", "gsk-test"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete("groq"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("groq"); err == nil {
		t.Fatalf("expected error after delete")
	}
	// Deleting a missing key is a no-op, not an error.
	if err := s.Delete("nope"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestSecretsListExcludesValues(t *testing.T) {
	_, s := openTestStore(t)
	if err := s.Set("openai", "sk-super-secret-value-xyz"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("anthropic", "ant-secret-456"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 keys, got %v", list)
	}
	// Sorted.
	if list[0] != "anthropic" || list[1] != "openai" {
		t.Fatalf("expected sorted keys, got %v", list)
	}
	joined := strings.Join(list, "\n")
	if strings.Contains(joined, "sk-super-secret-value-xyz") || strings.Contains(joined, "ant-secret-456") {
		t.Fatalf("List leaked values: %v", list)
	}
}

func TestSecretsPersistenceAcrossReopen(t *testing.T) {
	clearVaultEnv(t)
	dir := t.TempDir()
	v, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	s, err := OpenSecrets(dir, v)
	if err != nil {
		t.Fatalf("OpenSecrets: %v", err)
	}
	if err := s.Set("gemini", "gemini-secret-abc"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Reopen with the same vault.
	s2, err := OpenSecrets(dir, v)
	if err != nil {
		t.Fatalf("OpenSecrets reopen: %v", err)
	}
	got, err := s2.Get("gemini")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got != "gemini-secret-abc" {
		t.Fatalf("persistence mismatch: got %q", got)
	}
	// Reopen with a freshly loaded vault (same key file).
	v3, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault reload: %v", err)
	}
	s3, err := OpenSecrets(dir, v3)
	if err != nil {
		t.Fatalf("OpenSecrets reload: %v", err)
	}
	got, err = s3.Get("gemini")
	if err != nil {
		t.Fatalf("Get after vault reload: %v", err)
	}
	if got != "gemini-secret-abc" {
		t.Fatalf("persistence mismatch after vault reload: got %q", got)
	}
}

func TestSecretsFileIsNotPlaintext(t *testing.T) {
	clearVaultEnv(t)
	dir := t.TempDir()
	v, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	s, err := OpenSecrets(dir, v)
	if err != nil {
		t.Fatalf("OpenSecrets: %v", err)
	}
	secret := "my-ultra-secret-not-plaintext-987654321"
	if err := s.Set("openrouter", secret); err != nil {
		t.Fatalf("Set: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatalf("ReadFile raw: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("secrets file contains plaintext secret")
	}
	if strings.Contains(string(raw), "openrouter") {
		t.Fatalf("secrets file leaks key material in plaintext")
	}
	// File must be 0600.
	fi, err := os.Stat(filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected secrets.enc 0600, got %o", fi.Mode().Perm())
	}
}

func TestKnownKeys(t *testing.T) {
	want := []string{"openrouter", "openai", "anthropic", "groq", "gemini", "deepseek", "telegram", "discord", "http_token"}
	got := KnownKeys()
	if len(got) != len(want) {
		t.Fatalf("KnownKeys length: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("KnownKeys mismatch: got %v want %v", got, want)
		}
	}
}
