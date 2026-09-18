// Package doctor implements a built-in troubleshoot/doctor agent using only
// the Go standard library (plus sibling internal packages; no third-party
// dependencies).
//
// Run executes a suite of fast, never-panicking health checks covering the
// binary, data directory, vault key, config, encrypted secrets, Ollama,
// the opencode sidecar binary, Telegram, the HTTP port, disk space, Termux
// APIs, MEMORY.md and the skills directory.
//
// Bundle builds a redacted-by-default diagnostics ZIP for support requests,
// and SystemPrompt provides the persona for the local support-troubleshooter.
package doctor

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"nimbus-one/internal/config"
)

// Check statuses.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Check is a single doctor result. Fix is a concrete command or config edit
// the user can run to resolve the issue; it is empty when Status is "ok".
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// perCheckTimeout bounds every individual check.
const perCheckTimeout = 5 * time.Second

// Run executes the full check suite against dataDir (defaulting to
// config.DefaultDataDir when empty) and returns one Check per probe.
// It never panics and never aborts early: a panicking or slow check becomes
// a failing Check and the rest of the suite still runs.
func Run(ctx context.Context, dataDir string) []Check {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = config.DefaultDataDir()
	}
	suite := []struct {
		id    string
		title string
		fn    func(context.Context, string) Check
	}{
		{"go_version", "Go/binary version", checkGoVersion},
		{"data_dir", "Data directory writable", checkDataDir},
		{"vault_key", "Vault key present with 0600", checkVaultKey},
		{"config", "config.yaml parseable", checkConfig},
		{"secrets", "secrets.enc decryptable", checkSecrets},
		{"ollama", "Ollama reachable", checkOllama},
		{"opencode", "opencode binary present", checkOpencode},
		{"telegram", "Telegram token + allowlist", checkTelegram},
		{"http_port", "HTTP port free", checkHTTPPort},
		{"disk", "Disk space (write probe)", checkDisk},
		{"termux", "Termux APIs", checkTermux},
		{"memory", "MEMORY.md present", checkMemory},
		{"skills", "Skills directory non-empty", checkSkills},
	}
	out := make([]Check, 0, len(suite))
	for _, s := range suite {
		out = append(out, runOne(ctx, dataDir, s.id, s.title, s.fn))
	}
	return out
}

func validStatus(s string) bool {
	return s == StatusOK || s == StatusWarn || s == StatusFail
}

// runOne executes a single check with panic recovery and a hard per-check
// timeout. Any panic, timeout or invalid status becomes Status "fail" with a
// concrete Fix; the suite itself is never aborted.
func runOne(ctx context.Context, dataDir, id, title string, fn func(context.Context, string) Check) (out Check) {
	type result struct{ c Check }
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{Check{
					ID:     id,
					Title:  title,
					Status: StatusFail,
					Detail: fmt.Sprintf("check panicked: %v", r),
					Fix:    "re-run `nimbus-one doctor`; if this persists, attach a support bundle (`nimbus-one support bundle`) and report it",
				}}
			}
		}()
		cctx, cancel := context.WithTimeout(ctx, perCheckTimeout)
		defer cancel()
		got := fn(cctx, dataDir)
		if got.ID == "" {
			got.ID = id
		}
		if got.Title == "" {
			got.Title = title
		}
		if !validStatus(got.Status) {
			got.Status = StatusFail
			if got.Detail == "" {
				got.Detail = "check returned an invalid status"
			}
		}
		ch <- result{got}
	}()
	timer := time.NewTimer(perCheckTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.c
	case <-ctx.Done():
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("check aborted: %v", ctx.Err()),
			Fix:    "re-run `nimbus-one doctor`"}
	case <-timer.C:
		return Check{ID: id, Title: title, Status: StatusFail,
			Detail: "check timed out after 5s",
			Fix:    "re-run `nimbus-one doctor`; if this persists, report it with a support bundle (`nimbus-one support bundle`)"}
	}
}

// yamlValue reads a simple `key: value` line out of a YAML-ish file.
// It mirrors the subset parser in internal/config without side effects.
func yamlValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		parts := strings.SplitN(t, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == key {
			return strings.Trim(strings.TrimSpace(parts[1]), `"' `)
		}
	}
	return ""
}

// parsePort extracts leading digits like internal/config does.
func parsePort(s string, def int) int {
	n := 0
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
		} else if n > 0 {
			break
		}
	}
	if n > 0 && n < 65536 {
		return n
	}
	return def
}

// --- individual checks ---

func checkGoVersion(_ context.Context, _ string) Check {
	return Check{ID: "go_version", Title: "Go/binary version", Status: StatusOK,
		Detail: fmt.Sprintf("%s (%s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)}
}

func checkDataDir(ctx context.Context, dataDir string) Check {
	const title = "Data directory writable"
	if err := ctx.Err(); err != nil {
		return Check{ID: "data_dir", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("check aborted: %v", err), Fix: "re-run `nimbus-one doctor`"}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Check{ID: "data_dir", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot create %s: %v", dataDir, err),
			Fix:    fmt.Sprintf("mkdir -p %s && chmod 700 %s", dataDir, dataDir)}
	}
	probe := filepath.Join(dataDir, ".doctor-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		return Check{ID: "data_dir", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("not writable: %s: %v", dataDir, err),
			Fix:    fmt.Sprintf("chmod u+w %s; check ownership with `ls -ld %s`", dataDir, dataDir)}
	}
	_ = os.Remove(probe)
	return Check{ID: "data_dir", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("%s exists and is writable", dataDir)}
}

func checkVaultKey(ctx context.Context, dataDir string) Check {
	const title = "Vault key present with 0600"
	_ = ctx
	p := filepath.Join(dataDir, "vault.key")
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{ID: "vault_key", Title: title, Status: StatusFail,
				Detail: "vault.key is missing (encryption-at-rest has no key yet)",
				Fix:    "restart nimbus-one to auto-generate vault.key, or set NIMBUS_VAULT_KEY and run `nimbus-one secrets set <name>`"}
		}
		return Check{ID: "vault_key", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot stat vault.key: %v", err),
			Fix:    fmt.Sprintf("ls -l %s; fix permissions or restore the file from backup", p)}
	}
	if fi.Size() == 0 {
		return Check{ID: "vault_key", Title: title, Status: StatusFail,
			Detail: "vault.key is empty",
			Fix:    fmt.Sprintf("restore %s from backup, or delete it and restart nimbus-one to generate a new key (old secrets will be unrecoverable)", p)}
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		return Check{ID: "vault_key", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("vault.key mode is %04o, want 0600 (key is world-readable)", perm),
			Fix:    fmt.Sprintf("chmod 600 %s", p)}
	}
	return Check{ID: "vault_key", Title: title, Status: StatusOK,
		Detail: "vault.key present with mode 0600"}
}

func checkConfig(_ context.Context, dataDir string) Check {
	const title = "config.yaml parseable"
	p := filepath.Join(dataDir, "config.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{ID: "config", Title: title, Status: StatusWarn,
				Detail: "config.yaml not found, running on built-in defaults",
				Fix:    fmt.Sprintf("create %s (see README) or run `nimbus-one init`", p)}
		}
		return Check{ID: "config", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot read config.yaml: %v", err),
			Fix:    fmt.Sprintf("ls -l %s; fix permissions or restore the file from backup", p)}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Check{ID: "config", Title: title, Status: StatusWarn,
			Detail: "config.yaml is empty, running on built-in defaults",
			Fix:    fmt.Sprintf("populate %s or delete it to silence this warning", p)}
	}
	for i, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") {
			continue
		}
		if !strings.Contains(t, ":") {
			return Check{ID: "config", Title: title, Status: StatusFail,
				Detail: fmt.Sprintf("config.yaml line %d is not valid `key: value`: %q", i+1, t),
				Fix:    fmt.Sprintf("edit %s and fix line %d (expected `key: value`)", p, i+1)}
		}
	}
	return Check{ID: "config", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("config.yaml parsed (%d bytes)", len(data))}
}

// deriveKey normalizes key material to 32 bytes, mirroring internal/secure.
func deriveKey(material string) []byte {
	sum := sha256.Sum256([]byte(material))
	out := make([]byte, 32)
	copy(out, sum[:])
	return out
}

// verifyKey resolves the vault key read-only (never creates files, unlike
// secure.LoadVault which auto-generates a missing key).
func verifyKey(dataDir string) ([]byte, bool) {
	if env := os.Getenv("NIMBUS_VAULT_KEY"); env != "" {
		if decoded, err := base64.StdEncoding.DecodeString(env); err == nil && len(decoded) == 32 {
			return decoded, true
		}
		return deriveKey(env), true
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "vault.key"))
	if err != nil {
		return nil, false
	}
	k := bytes.TrimSpace(data)
	if len(k) == 0 {
		return nil, false
	}
	if len(k) == 32 {
		out := make([]byte, 32)
		copy(out, k)
		return out, true
	}
	return deriveKey(string(k)), true
}

func decryptBlob(key, blob []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if len(blob) < gcm.NonceSize() {
		return fmt.Errorf("blob too short (%d bytes)", len(blob))
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	_, err = gcm.Open(nil, nonce, ct, nil)
	return err
}

func checkSecrets(_ context.Context, dataDir string) Check {
	const title = "secrets.enc decryptable"
	p := filepath.Join(dataDir, "secrets.enc")
	blob, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{ID: "secrets", Title: title, Status: StatusOK,
				Detail: "no secrets.enc yet (nothing to decrypt)"}
		}
		return Check{ID: "secrets", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot read secrets.enc: %v", err),
			Fix:    fmt.Sprintf("ls -l %s; fix permissions or restore the file from backup", p)}
	}
	if len(blob) == 0 {
		return Check{ID: "secrets", Title: title, Status: StatusWarn,
			Detail: "secrets.enc is empty",
			Fix:    "re-create secrets with `nimbus-one secrets set <name>`, or delete secrets.enc if unused"}
	}
	key, ok := verifyKey(dataDir)
	if !ok {
		// Best effort: without a key we cannot distinguish "corrupt" from
		// "encrypted with another key", so warn instead of failing.
		return Check{ID: "secrets", Title: title, Status: StatusWarn,
			Detail: "secrets.enc exists but no vault key is available to verify it",
			Fix:    "restore vault.key from backup or set NIMBUS_VAULT_KEY, then re-run `nimbus-one doctor`"}
	}
	if err := decryptBlob(key, blob); err != nil {
		return Check{ID: "secrets", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("secrets.enc failed to decrypt (corrupt or wrong key): %v", err),
			Fix:    "restore secrets.enc + vault.key from backup; if the key is lost, delete secrets.enc and re-create entries with `nimbus-one secrets set <name>`"}
	}
	return Check{ID: "secrets", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("secrets.enc decrypts cleanly (%d bytes)", len(blob))}
}

// ollamaHostPort resolves the Ollama TCP endpoint, defaulting to localhost.
func ollamaHostPort() string {
	const def = "127.0.0.1:11434"
	raw := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL"))
	if raw == "" {
		return def
	}
	if i := strings.Index(raw, "://"); i != -1 {
		raw = raw[i+3:]
	}
	if i := strings.Index(raw, "/"); i != -1 {
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return net.JoinHostPort(raw, "11434")
	}
	return raw
}

func checkOllama(ctx context.Context, _ string) Check {
	const title = "Ollama reachable"
	addr := ollamaHostPort()
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Check{ID: "ollama", Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("cannot reach Ollama at %s: %v (local models unavailable)", addr, err),
			Fix:    "start Ollama with `ollama serve`, or point OLLAMA_BASE_URL at a reachable host"}
	}
	_ = conn.Close()
	return Check{ID: "ollama", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("Ollama reachable at %s", addr)}
}

func checkOpencode(_ context.Context, _ string) Check {
	const title = "opencode binary present"
	if path, err := exec.LookPath("opencode"); err == nil {
		return Check{ID: "opencode", Title: title, Status: StatusOK,
			Detail: fmt.Sprintf("found opencode at %s", path)}
	}
	if alt := strings.TrimSpace(os.Getenv("OPENCODE_BIN")); alt != "" {
		if _, err := os.Stat(alt); err == nil {
			return Check{ID: "opencode", Title: title, Status: StatusOK,
				Detail: fmt.Sprintf("found opencode at OPENCODE_BIN=%s", alt)}
		}
	}
	return Check{ID: "opencode", Title: title, Status: StatusWarn,
		Detail: "opencode binary not found in PATH (sidecar runs unavailable)",
		Fix:    "install it (e.g. `npm install -g opencode-ai`, see https://opencode.ai) or set OPENCODE_BIN=/path/to/opencode"}
}

func checkTelegram(_ context.Context, dataDir string) Check {
	const title = "Telegram token + allowlist"
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		token = yamlValue(filepath.Join(dataDir, "config.yaml"), "telegram_token")
	}
	if token == "" {
		return Check{ID: "telegram", Title: title, Status: StatusWarn,
			Detail: "TELEGRAM_BOT_TOKEN is not set (Telegram gateway disabled)",
			Fix:    "export TELEGRAM_BOT_TOKEN=<bot-token> and TELEGRAM_ALLOW_FROM=<chat-id> to enable Telegram"}
	}
	allow := strings.TrimSpace(os.Getenv("TELEGRAM_ALLOW_FROM"))
	if allow == "" {
		return Check{ID: "telegram", Title: title, Status: StatusWarn,
			Detail: "telegram token is set but no allowlist (open to any sender)",
			Fix:    "export TELEGRAM_ALLOW_FROM=<chat-id>[,<chat-id>...] to restrict who may talk to the bot"}
	}
	return Check{ID: "telegram", Title: title, Status: StatusOK,
		Detail: "telegram token and allowlist are set (values redacted)"}
}

// httpBindPort resolves the configured HTTP endpoint (defaults mirror
// internal/config: 127.0.0.1:8787).
func httpBindPort(dataDir string) (string, int) {
	cfg := filepath.Join(dataDir, "config.yaml")
	bind := yamlValue(cfg, "http_bind")
	if bind == "" {
		bind = "127.0.0.1"
	}
	port := parsePort(yamlValue(cfg, "http_port"), 8787)
	return bind, port
}

func checkHTTPPort(_ context.Context, dataDir string) Check {
	const title = "HTTP port free"
	bind, port := httpBindPort(dataDir)
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return Check{ID: "http_port", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot bind %s (already in use?): %v", addr, err),
			Fix:    fmt.Sprintf("stop the other instance (`lsof -i :%d`), or set `http_port:` to a free port in %s", port, filepath.Join(dataDir, "config.yaml"))}
	}
	_ = ln.Close()
	return Check{ID: "http_port", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("port %d on %s is free", port, bind)}
}

func checkDisk(ctx context.Context, dataDir string) Check {
	const title = "Disk space (write probe)"
	if err := ctx.Err(); err != nil {
		return Check{ID: "disk", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("check aborted: %v", err), Fix: "re-run `nimbus-one doctor`"}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Check{ID: "disk", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot create %s: %v", dataDir, err),
			Fix:    fmt.Sprintf("check mounts with `df -h %s` and free space if full", dataDir)}
	}
	// stdlib has no statfs, so prove writability with a probe file instead.
	probe := filepath.Join(dataDir, ".doctor-disk-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Check{ID: "disk", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("write probe failed in %s: %v (disk full or read-only?)", dataDir, err),
			Fix:    fmt.Sprintf("free space with `df -h %s`, clean old logs, then re-run `nimbus-one doctor`", dataDir)}
	}
	if _, err := f.Write(make([]byte, 32<<10)); err != nil {
		_ = f.Close()
		_ = os.Remove(probe)
		return Check{ID: "disk", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("write probe failed in %s: %v (disk full?)", dataDir, err),
			Fix:    fmt.Sprintf("free space with `df -h %s`, then re-run `nimbus-one doctor`", dataDir)}
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return Check{ID: "disk", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("write probe succeeded in %s", dataDir)}
}

func checkTermux(_ context.Context, _ string) Check {
	const title = "Termux APIs"
	if !config.IsTermux() {
		return Check{ID: "termux", Title: title, Status: StatusOK,
			Detail: "not running on Termux, nothing to check"}
	}
	if path, err := exec.LookPath("termux-battery-status"); err == nil {
		return Check{ID: "termux", Title: title, Status: StatusOK,
			Detail: fmt.Sprintf("Termux detected, termux API present (%s)", path)}
	}
	// Warn-only: missing Termux APIs degrade extras, never core function.
	return Check{ID: "termux", Title: title, Status: StatusWarn,
		Detail: "running on Termux but termux-battery-status is missing (device extras unavailable)",
		Fix:    "pkg install termux-api && grant the Termux:API app permissions, then verify with `termux-battery-status`"}
}

func checkMemory(_ context.Context, dataDir string) Check {
	const title = "MEMORY.md present"
	candidates := []string{
		filepath.Join(dataDir, "workspace", "MEMORY.md"),
		filepath.Join(dataDir, "MEMORY.md"),
	}
	for _, p := range candidates {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			continue
		}
		return Check{ID: "memory", Title: title, Status: StatusOK,
			Detail: fmt.Sprintf("found %s (%d bytes)", p, fi.Size())}
	}
	return Check{ID: "memory", Title: title, Status: StatusWarn,
		Detail: "no MEMORY.md found (long-term memory starts empty)",
		Fix:    fmt.Sprintf("create %s, e.g. `printf '# Memory\\n' > %s` or run `nimbus-one init`", candidates[0], candidates[0])}
}

func checkSkills(_ context.Context, dataDir string) Check {
	const title = "Skills directory non-empty"
	dir := filepath.Join(dataDir, "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{ID: "skills", Title: title, Status: StatusWarn,
				Detail: fmt.Sprintf("skills dir %s does not exist", dir),
				Fix:    fmt.Sprintf("mkdir -p %s and add skills as %s/*/SKILL.md", dir, dir)}
		}
		return Check{ID: "skills", Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("cannot read skills dir %s: %v", dir, err),
			Fix:    fmt.Sprintf("ls -ld %s; fix permissions", dir)}
	}
	names := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names++
		}
	}
	if names == 0 {
		return Check{ID: "skills", Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("skills dir %s is empty", dir),
			Fix:    fmt.Sprintf("add skills as %s/*/SKILL.md (see README)", dir)}
	}
	return Check{ID: "skills", Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("%d entries in %s", names, dir)}
}
