// Package secure provides encryption-at-rest, secret redaction, and
// hardening helpers. All privacy-sensitive material stays local by default:
// no telemetry, minimal file permissions, redacted logs.
package secure

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Vault encrypts blobs with AES-256-GCM. Key material never leaves the host.
type Vault struct {
	key     []byte
	Enabled bool
}

// DeriveKey normalizes user-supplied key material to 32 bytes via SHA-256.
func DeriveKey(material string) []byte {
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

// ReadKeyFile loads key bytes WITHOUT creating anything (diagnostics and
// read-only callers). Format: env NIMBUS_VAULT_KEY (base64 32B or text),
// hex-encoded vault.key (64 chars), legacy raw 32 bytes, else SHA-256 of
// trimmed content. Same parsing as LoadVault — the single source of truth.
func ReadKeyFile(dataDir string) ([]byte, error) {
	if env := os.Getenv("NIMBUS_VAULT_KEY"); env != "" {
		if decoded, err := base64.StdEncoding.DecodeString(env); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
		return DeriveKey(env), nil
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "vault.key"))
	if err != nil {
		return nil, err
	}
	if hexed := strings.TrimSpace(string(data)); len(hexed) == 64 {
		if raw, err := hex.DecodeString(hexed); err == nil {
			return raw, nil
		}
	}
	if len(data) == 32 {
		return data, nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("vault: empty key file")
	}
	return DeriveKey(strings.TrimSpace(string(data))), nil
}

// LoadVault returns a vault backed by NIMBUS_VAULT_KEY or dataDir/vault.key
// (0600, created once). Missing/empty key files are (re)created, so
// encryption is on by default without any user action. A present but
// undecodable key file is surfaced as an error and NEVER silently replaced.
func LoadVault(dataDir string) (*Vault, error) {
	if key, err := ReadKeyFile(dataDir); err == nil {
		return &Vault{key: key, Enabled: true}, nil
	}
	keyPath := filepath.Join(dataDir, "vault.key")
	if info, serr := os.Stat(keyPath); serr == nil && info.Size() > 0 {
		if key, rerr := ReadKeyFile(dataDir); rerr == nil {
			return &Vault{key: key, Enabled: true}, nil
		} else {
			return nil, fmt.Errorf("vault: key file present but unusable: %w", rerr)
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("vault: rand: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return &Vault{key: key, Enabled: true}, nil
}

// Encrypt seals plaintext. Output is nonce|ciphertext.
func (v *Vault) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens nonce|ciphertext.
func (v *Vault) Decrypt(blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, fmt.Errorf("vault: blob too short")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// WriteFile atomically writes plaintext encrypted at rest (0600).
func (v *Vault) WriteFile(path string, plaintext []byte) error {
	enc, err := v.Encrypt(plaintext)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadFile decrypts a file written by WriteFile.
func (v *Vault) ReadFile(path string) ([]byte, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return v.Decrypt(blob)
}

// --- Secret redaction ---

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_-]{8,})\b`),
	regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|gho_[A-Za-z0-9]{8,})\b`),
	regexp.MustCompile(`\b(xox[bap]-[A-Za-z0-9-]{8,})\b`),
	regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*)(['"]?)([A-Za-z0-9_\-.~+/=]{12,})`),
	regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9_\-.~+/=]{12,})`),
	regexp.MustCompile(`(?i)(token\s*[:=]\s*)(['"]?)([A-Za-z0-9_\-.~+/=]{12,})`),
	regexp.MustCompile(`(?i)(password\s*[:=]\s*)(['"]?)([^\s'"]{6,})`),
}

const redacted = "[REDACTED]"

// Redact masks API keys, tokens, and passwords before logs/prompts ever see them.
func Redact(s string) string {
	out := s
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			parts := re.FindStringSubmatch(m)
			if len(parts) >= 4 && parts[1] != "" {
				// preserve the "key=" prefix, mask the value
				return parts[1] + parts[2] + redacted
			}
			if len(parts) == 3 && parts[1] != "" {
				return parts[1] + parts[2] + redacted
			}
			return redacted
		})
	}
	_ = strings.TrimSpace
	return out
}

// --- Hardening ---

// HardenDataDir forces 0700 on dirs and 0600 on files under root.
func HardenDataDir(root string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if info.IsDir() {
			_ = os.Chmod(p, 0o700)
			return nil
		}
		_ = os.Chmod(p, 0o600)
		return nil
	})
}

// SafeWriteFile atomically writes with 0600 perms (no world-readable secrets).
func SafeWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ScrubEnv returns env with sensitive values removed except allowlisted keys
// needed by child tools. Prevents accidental secret leakage to subprocesses.
func ScrubEnv(extra map[string]string) []string {
	deny := []string{"VAULT_KEY", "BOT_TOKEN", "API_KEY", "PASSWORD", "SECRET", "PRIVATE_KEY"}
	_ = deny
	out := []string{}
	for _, kv := range os.Environ() {
		up := strings.ToUpper(kv)
		bad := false
		for _, d := range deny {
			if strings.Contains(up, d) && !strings.HasPrefix(up, "NIMBUS_ALLOW_") {
				// keep provider keys only if explicitly re-passed via extra
				bad = true
				break
			}
		}
		if !bad {
			out = append(out, kv)
		}
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
