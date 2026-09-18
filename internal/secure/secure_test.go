package secure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearVaultEnv(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv("NIMBUS_VAULT_KEY"); ok {
		t.Setenv("NIMBUS_VAULT_KEY", v) // register cleanup
		if err := os.Unsetenv("NIMBUS_VAULT_KEY"); err != nil {
			t.Fatalf("Unsetenv: %v", err)
		}
	}
}

func TestLoadVaultCreatesKey0600(t *testing.T) {
	clearVaultEnv(t)
	dir := t.TempDir()
	v, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	if v == nil || !v.Enabled {
		t.Fatalf("expected enabled vault")
	}
	keyPath := filepath.Join(dir, "vault.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("vault.key missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected vault.key 0600, got %o", fi.Mode().Perm())
	}
	if fi.Size() == 0 {
		t.Fatalf("vault.key empty")
	}
}

func TestLoadVaultLegacyRawKeyStable(t *testing.T) {
	// Regression: raw 32-byte keys whose edge bytes are whitespace must
	// reload to the IDENTICAL key (TrimSpace-derivation flaked this).
	dir := t.TempDir()
	raw := []byte{'\n', 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, ' '}
	if len(raw) != 32 {
		t.Fatal("fixture must be 32 bytes")
	}
	if err := os.WriteFile(dir+"/vault.key", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	v1, err := LoadVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := LoadVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := v1.Encrypt([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Decrypt(ct); err != nil {
		t.Fatalf("key changed between loads: %v", err)
	}
}

func TestLoadVaultReloadSameKey(t *testing.T) {
	clearVaultEnv(t)
	dir := t.TempDir()
	v1, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	v2, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault reload: %v", err)
	}
	// Functional check: encrypt with one, decrypt with the other.
	ct, err := v1.Encrypt([]byte("same-key-check"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pt, err := v2.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt with reloaded vault: %v", err)
	}
	if string(pt) != "same-key-check" {
		t.Fatalf("key mismatch: got %q", pt)
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	clearVaultEnv(t)
	v, err := LoadVault(t.TempDir())
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	msg := []byte("hello secure world")
	ct, err := v.Encrypt(msg)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(ct) == 0 {
		t.Fatalf("empty ciphertext")
	}
	pt, err := v.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(pt) != string(msg) {
		t.Fatalf("roundtrip mismatch: got %q want %q", pt, msg)
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	clearVaultEnv(t)
	v, err := LoadVault(t.TempDir())
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	ct, err := v.Encrypt([]byte("tamper me"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	bad := append([]byte(nil), ct...)
	bad[len(bad)-1] ^= 0xFF
	if _, err := v.Decrypt(bad); err == nil {
		t.Fatalf("expected error for tampered blob")
	}
	// Truncated blob must also fail.
	if _, err := v.Decrypt(ct[:2]); err == nil {
		t.Fatalf("expected error for truncated blob")
	}
}

func TestWriteReadFileRoundtrip0600(t *testing.T) {
	clearVaultEnv(t)
	dir := t.TempDir()
	v, err := LoadVault(dir)
	if err != nil {
		t.Fatalf("LoadVault: %v", err)
	}
	p := filepath.Join(dir, "secret.dat")
	msg := []byte("file secret contents")
	if err := v.WriteFile(p, msg); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", fi.Mode().Perm())
	}
	pt, err := v.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(pt) != string(msg) {
		t.Fatalf("roundtrip mismatch: got %q want %q", pt, msg)
	}
}

func TestRedactMasksSecrets(t *testing.T) {
	sk := "sk-abc1234567890XYZsecret"
	out := Redact("my key is " + sk + " done")
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED], got %q", out)
	}
	if strings.Contains(out, sk) {
		t.Fatalf("raw sk secret leaked: %q", out)
	}

	raw := "secretvalue123"
	out2 := Redact("login with api_key=" + raw + " ok")
	if !strings.Contains(out2, "[REDACTED]") {
		t.Fatalf("expected [REDACTED], got %q", out2)
	}
	if strings.Contains(out2, raw) {
		t.Fatalf("raw api_key secret leaked: %q", out2)
	}
}

func TestHardenDataDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Loosen perms first to prove hardening tightens them.
	_ = os.Chmod(sub, 0o755)
	_ = os.Chmod(f, 0o644)
	if err := HardenDataDir(root); err != nil {
		t.Fatalf("HardenDataDir: %v", err)
	}
	fi, err := os.Stat(sub)
	if err != nil {
		t.Fatalf("Stat sub: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("expected dir 0700, got %o", fi.Mode().Perm())
	}
	ffi, err := os.Stat(f)
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if ffi.Mode().Perm() != 0o600 {
		t.Fatalf("expected file 0600, got %o", ffi.Mode().Perm())
	}
}

func TestScrubEnv(t *testing.T) {
	t.Setenv("NIMBUS_VAULT_KEY", "supersecretvaultkey")
	t.Setenv("MY_API_KEY", "should-be-scrubbed")
	got := ScrubEnv(nil)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "NIMBUS_VAULT_KEY") {
		t.Fatalf("ScrubEnv leaked NIMBUS_VAULT_KEY")
	}
	if strings.Contains(joined, "MY_API_KEY") {
		t.Fatalf("ScrubEnv leaked MY_API_KEY")
	}
	hasPath := false
	for _, kv := range got {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		t.Fatalf("ScrubEnv dropped PATH")
	}
}
