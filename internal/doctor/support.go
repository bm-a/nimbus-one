// Support bundle + troubleshooter persona for package doctor.
//
// Privacy-first rules:
//   - Bundle redacts by default (includeSecrets=false).
//   - When includeSecrets=true the bundle ships a WARNING.txt telling the
//     user the archive may contain sensitive material.
//   - The support contact is configurable via NIMBUS_SUPPORT_URL; the default
//     is a placeholder GitHub issues URL, never a personal contact.
package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"nimbus-one/internal/config"
)

// DefaultSupportURL is the placeholder public contact for support requests.
// Override it per-installation with the NIMBUS_SUPPORT_URL environment
// variable (see SupportEndpoint). It intentionally names no person.
const DefaultSupportURL = "https://github.com/<org>/nimbus-one/issues"

// SupportEndpoint returns the configurable support contact endpoint.
func SupportEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("NIMBUS_SUPPORT_URL")); v != "" {
		return v
	}
	return DefaultSupportURL
}

// redactedMarker replaces secret values in redacted output.
const redactedMarker = "[REDACTED]"

// sensitiveKeyFrag matches config keys whose values must be masked.
var sensitiveKeyFrag = regexp.MustCompile(`(?i)(token|api[_-]?key|secret|password|private[_-]?key)`)

// redactConfigLine masks one `key: value` / `key = value` line when the key
// looks sensitive. It returns the (possibly rewritten) line and the removed
// raw value ("" when nothing was masked).
func redactConfigLine(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return line, ""
	}
	sep := strings.Index(line, ":")
	if eq := strings.Index(line, "="); eq != -1 && (sep == -1 || eq < sep) {
		sep = eq
	}
	if sep <= 0 {
		return line, ""
	}
	key := strings.ToLower(strings.TrimSpace(line[:sep]))
	key = strings.TrimSpace(strings.TrimPrefix(key, "-"))
	if !sensitiveKeyFrag.MatchString(key) {
		return line, ""
	}
	val := strings.Trim(strings.TrimSpace(line[sep+1:]), `"' `)
	return line[:sep+1] + " " + redactedMarker, val
}

// redactConfigYAML masks sensitive values in a YAML-ish config and reports
// the removed raw values so the bundle self-check can prove they are gone.
func redactConfigYAML(raw []byte) (string, []string) {
	lines := strings.Split(string(raw), "\n")
	var removed []string
	for i, ln := range lines {
		red, val := redactConfigLine(ln)
		lines[i] = red
		if val != "" {
			removed = append(removed, val)
		}
	}
	return redactSecrets(strings.Join(lines, "\n")), removed
}

// secretValuePatterns mask well-known secret shapes inside free text
// (logs, details). Prefix-preserving patterns keep the `key=` part.
var secretValuePatterns = []struct {
	re       *regexp.Regexp
	preserve bool
}{
	{regexp.MustCompile(`(?i)sk-[A-Za-z0-9_-]{8,}`), false},
	{regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|gho_[A-Za-z0-9_]{8,}|AKIA[0-9A-Z]{16})\b`), false},
	{regexp.MustCompile(`\bxox[bap]-[A-Za-z0-9-]{8,}`), false},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), false},
	{regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*['"]?)[A-Za-z0-9_\-.~+/=]{8,}`), true},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9_\-.~+/=]{8,}`), true},
	{regexp.MustCompile(`(?i)(token\s*[:=]\s*['"]?)[A-Za-z0-9_\-.~+/=]{6,}`), true},
	{regexp.MustCompile(`(?i)(client[_-]?secret\s*[:=]\s*['"]?)[A-Za-z0-9_\-.~+/=]{6,}`), true},
	{regexp.MustCompile(`(?i)(password\s*[:=]\s*['"]?)[^\s'";,]{4,}`), true},
}

// redactSecrets masks secret-looking values inside free text.
func redactSecrets(s string) string {
	out := s
	for _, p := range secretValuePatterns {
		if p.preserve {
			out = p.re.ReplaceAllString(out, "${1}"+redactedMarker)
		} else {
			out = p.re.ReplaceAllString(out, redactedMarker)
		}
	}
	return out
}

// leakPatterns detect leftover secrets during the pre-return self-check.
var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`\bxox[bap]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b[a-z0-9_]*(token|api[_-]?key|password|client[_-]?secret)\b\s*[:=]\s*['"]?[^'"\s]*[A-Za-z0-9_\-/+=]{4,}`),
}

// selfCheckClean asserts that no raw secret value or secret pattern survives
// in the about-to-be-shipped bundle files. Matches containing [REDACTED] are
// accepted (they are the redaction itself, not a leak).
func selfCheckClean(files map[string]string, rawValues []string) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		for _, v := range rawValues {
			if len(v) < 8 {
				continue
			}
			if strings.Contains(body, v) {
				return fmt.Errorf("doctor: refusing bundle: redacted value leaked in %s", name)
			}
		}
		for _, re := range leakPatterns {
			for _, m := range re.FindAllString(body, -1) {
				// Accept the redaction itself (regex may clip the
				// trailing "]" since it is outside the value class).
				if strings.Contains(m, "[REDACTED") {
					continue
				}
				return fmt.Errorf("doctor: refusing bundle: possible secret pattern in %s: %q", name, m)
			}
		}
	}
	return nil
}

// newBundleID mints a timestamped random bundle id for escalation threads.
func newBundleID() string {
	var b [8]byte
	now := time.Now().UTC().Format("20060102-150405")
	if _, err := rand.Read(b[:]); err == nil {
		return now + "-" + hex.EncodeToString(b[:])
	}
	return now
}

// tailFile returns the last maxBytes / maxLines of a log file (best effort).
func tailFile(path string, maxBytes, maxLines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(unreadable: %v)", err)
	}
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
		if i := bytes.IndexByte(data, '\n'); i != -1 {
			data = data[i+1:]
		}
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// collectLogs gathers recent log tails (best effort, capped).
func collectLogs(dataDir string) map[string]string {
	out := map[string]string{}
	var paths []string
	for _, pat := range []string{
		filepath.Join(dataDir, "logs", "*.log"),
		filepath.Join(dataDir, "*.log"),
	} {
		m, _ := filepath.Glob(pat)
		paths = append(paths, m...)
	}
	sort.Slice(paths, func(i, j int) bool {
		fi, err1 := os.Stat(paths[i])
		fj, err2 := os.Stat(paths[j])
		if err1 != nil || err2 != nil {
			return paths[i] < paths[j]
		}
		return fi.ModTime().After(fj.ModTime())
	})
	if len(paths) > 5 {
		paths = paths[:5]
	}
	for _, p := range paths {
		out["logs/"+filepath.Base(p)] = tailFile(p, 32*1024, 200)
	}
	return out
}

// Bundle builds nimbus-one-support.zip diagnostics in memory.
//
// Files: checks.json, config.yaml (redacted unless includeSecrets),
// versions.txt, recent log tails when logs exist, plus bundle.json.
//
// Privacy: with includeSecrets=false (default) every text file is passed
// through redaction and the bundle is self-checked with leakPatterns plus
// the exact masked values before it is returned; any hit is an error, not
// a shipped bundle. With includeSecrets=true a WARNING.txt is added telling
// the user the archive may contain sensitive material — only share it over
// a trusted channel.
func Bundle(ctx context.Context, dataDir string, includeSecrets bool) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = config.DefaultDataDir()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	id := newBundleID()
	now := time.Now().UTC().Format(time.RFC3339)

	checks := Run(ctx, dataDir)

	rawCfg, err := os.ReadFile(filepath.Join(dataDir, "config.yaml"))
	if err != nil {
		rawCfg = []byte("# config.yaml not found\n")
	}
	var cfgText string
	var removed []string
	if includeSecrets {
		cfgText = string(rawCfg)
	} else {
		cfgText, removed = redactConfigYAML(rawCfg)
	}

	versions := fmt.Sprintf("nimbus-one support bundle %s\ncreated: %s\ngo: %s\nos/arch: %s/%s\ntermux: %v\ndata_dir: %s\nsupport: %s\ninclude_secrets: %v\n",
		id, now, runtime.Version(), runtime.GOOS, runtime.GOARCH,
		config.IsTermux(), dataDir, SupportEndpoint(), includeSecrets)

	payload := struct {
		BundleID  string  `json:"bundle_id"`
		CreatedAt string  `json:"created_at"`
		Checks    []Check `json:"checks"`
	}{BundleID: id, CreatedAt: now, Checks: checks}
	checksJSON, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("doctor: marshal checks: %w", err)
	}

	meta, err := json.MarshalIndent(map[string]any{
		"bundle_id":       id,
		"created_at":      now,
		"include_secrets": includeSecrets,
		"support":         SupportEndpoint(),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("doctor: marshal bundle meta: %w", err)
	}

	files := map[string]string{
		"bundle.json":  string(meta) + "\n",
		"checks.json":  string(checksJSON) + "\n",
		"config.yaml":  cfgText,
		"versions.txt": versions,
	}
	for name, body := range collectLogs(dataDir) {
		files[name] = body
	}
	if len(files) == 4 {
		files["logs.txt"] = "no log files found under " + dataDir + "\n"
	}

	if includeSecrets {
		files["WARNING.txt"] = "WARNING: this bundle was built with includeSecrets=true and MAY CONTAIN SECRETS " +
			"(tokens, keys, passwords).\nOnly share it over a trusted channel, and prefer the default redacted bundle.\n"
	} else {
		for name, body := range files {
			files[name] = redactSecrets(body)
		}
		if err := selfCheckClean(files, removed); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return nil, fmt.Errorf("doctor: zip %s: %w", n, err)
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			return nil, fmt.Errorf("doctor: zip %s: %w", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("doctor: close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// SystemPrompt returns the persona for the local support-troubleshooter: it
// reads a Bundle summary plus Checks, proposes minimal fixes, asks for
// missing info, keeps secrets-safety, and escalates with a bundle id.
func SystemPrompt() string {
	return `You are Nimbus One Support, a privacy-first troubleshooting assistant running locally inside Nimbus One.

INPUTS YOU RECEIVE
- A support bundle summary: bundle id, versions.txt, checks.json (the doctor suite), the REDACTED config.yaml, and recent log tails.
- The user's description of what is broken.

HOW TO WORK
1. Read checks.json first. Failing checks outrank warnings; warnings outrank guesses. Quote the failing check IDs.
2. Propose the MINIMAL fix that addresses the root cause: prefer one concrete command or one config edit (use the Fix field verbatim when it fits). Do not pile on unrelated hardening.
3. If information is missing (model, platform, exact error, what changed), ask at most 3 targeted questions before proposing alternatives. Never stall when a safe default exists.
4. Explain what each suggested command does in one line, especially on Termux/Android.

SECRETS-SAFETY (NON-NEGOTIABLE)
- Never ask the user to paste secrets, tokens, API keys, or passwords into chat. Chat history and bundles must stay secret-free.
- If a credential is actually needed, direct the user to set it locally: 'nimbus-one secrets set <name>' (or the matching NIMBUS_* / *_TOKEN env var), then re-run 'nimbus-one doctor'.
- Treat any secret-looking value the user pastes as already compromised: tell them to revoke/rotate it, then continue with placeholders like <token>.
- Default bundles are redacted ([REDACTED]); if you need unredacted data, warn the user explicitly before they rebuild with secrets included, and remind them to share it only over a trusted channel.

ESCALATION
- If the issue is unresolved after two fix attempts, or looks like a bug (doctor panics, repeatable crash, corrupt secrets.enc with a known-good key), escalate with this template:

  ---
  Nimbus One support escalation
  - bundle id: <paste bundle id here, e.g. from bundle.json>
  - failing checks: <IDs + one-line Details>
  - expected vs actual: <one line each>
  - tried: <commands run + outcome>
  - contact: https://github.com/<org>/nimbus-one/issues (override via NIMBUS_SUPPORT_URL)
  - attach: the redacted nimbus-one-support.zip bundle id above
  ---

- Tell the user: "paste this block plus the bundle id at the support endpoint; do not paste secrets."
- The support contact endpoint is configurable (NIMBUS_SUPPORT_URL); the default is the placeholder above and you must never invent or hardcode a personal contact.

STYLE
- Be terse: diagnosis, fix, verify (the exact command to confirm, e.g. 'nimbus-one doctor'). No lectures, no telemetry, everything stays local.`
}
