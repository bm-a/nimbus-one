// Package update self-updates the nimbus-one binary from GitHub releases.
//
// Contract (caller responsibilities):
//   - The caller MUST obtain explicit user consent (prompt) before calling Run.
//     This package never prompts.
//   - The caller MUST call BackupDataDir first and keep the returned backup
//     path so user data + memories can be restored on failure.
//     Run does NOT back up anything itself.
//   - Run only replaces the binary; it never touches DataDir.
//
// Data preservation:
//   - BackupDataDir copies dataDir recursively (vault.key, secrets.enc,
//     workspace/*.md, skills, memories) to dataDir+".bak-<timestamp>".
//     It skips nothing by construction.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// minBinarySize is the sanity threshold for a downloaded replacement binary.
// Anything smaller is treated as an error page / truncated payload, not a binary.
const minBinarySize = 100 * 1024 // 100KB

// githubTimeout bounds the releases API call when the context has no deadline.
const githubTimeout = 15 * time.Second

// Config describes a self-update check + replace operation.
type Config struct {
	// Repo is "owner/name", e.g. "owner/nimbus-one". Required.
	Repo string
	// Current is the running version, e.g. "v0.1.0". Compared with the
	// release tag via NeedsUpdate.
	Current string
	// Asset maps (goos, goarch) to the expected release asset file name.
	// If nil, DefaultAssetName is used.
	Asset func(goos, arch string) string
	// HTTP is the client for API + download calls. If nil, a default
	// client is used (15s timeout for the API; no timeout for downloads
	// beyond context cancellation).
	HTTP *http.Client
	// APIBase overrides the GitHub API base URL for tests
	// (e.g. an httptest server URL). Defaults to "https://api.github.com".
	APIBase string
	// DataDir is informational here (Run never touches it); kept so the
	// caller can thread backup + binary paths through one value.
	DataDir string
	// BinPath is the running binary to replace. If empty, Run falls back
	// to os.Executable.
	BinPath string
}

// DefaultAssetName returns the conventional asset file name for a platform.
func DefaultAssetName(goos, arch string) string {
	name := "nimbus-one-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func (c Config) assetName(goos, arch string) string {
	if c.Asset != nil {
		return c.Asset(goos, arch)
	}
	return DefaultAssetName(goos, arch)
}

func (c Config) apiBase() string {
	if c.APIBase != "" {
		return strings.TrimSuffix(c.APIBase, "/")
	}
	return "https://api.github.com"
}

func (c Config) apiClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: githubTimeout}
}

func (c Config) downloadClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// releasePayload mirrors the subset of the GitHub releases API we need.
type releasePayload struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease fetches the latest GitHub release tag and its assets.
//
// GET <APIBase>/repos/<repo>/releases/latest with a 15s timeout (when the
// context carries no deadline) and User-Agent "NimbusOne".
// Returns the tag (e.g. "v0.2.0") and a map of asset name -> download URL.
// Network errors from the HTTP call are returned verbatim so the caller can
// decide on fallback behavior.
func LatestRelease(ctx context.Context, cfg Config) (string, map[string]string, error) {
	if strings.TrimSpace(cfg.Repo) == "" {
		return "", nil, fmt.Errorf("update: repo is empty (want owner/name)")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, githubTimeout)
		defer cancel()
	}
	url := cfg.apiBase() + "/repos/" + strings.Trim(cfg.Repo, "/") + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "NimbusOne")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := cfg.apiClient().Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return "", nil, fmt.Errorf("update: releases API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var p releasePayload
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return "", nil, fmt.Errorf("update: decode releases API response: %w", err)
	}
	if strings.TrimSpace(p.TagName) == "" {
		return "", nil, fmt.Errorf("update: releases API response missing tag_name")
	}
	out := make(map[string]string, len(p.Assets))
	for _, a := range p.Assets {
		if a.Name == "" || a.BrowserDownloadURL == "" {
			continue
		}
		out[a.Name] = a.BrowserDownloadURL
	}
	return p.TagName, out, nil
}

// parseVersion strips a leading "v"/"V" and parses dot-separated numeric
// components. A trailing non-numeric suffix on a component (e.g. "3-beta")
// is ignored as long as the component has a numeric prefix. Returns ok=false
// on any garbage (empty, no digits, empty component).
func parseVersion(s string) ([]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, false
		}
		digits := p
		for i, r := range p {
			if r < '0' || r > '9' {
				digits = p[:i]
				break
			}
		}
		if digits == "" {
			return nil, false
		}
		// Reject absurdly long components to avoid overflow games.
		if len(digits) > 9 {
			return nil, false
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	// A version must have at least one numeric component; also reject
	// strings with no digits at all (already covered) or stray separators
	// like "1..2" (covered by empty component above).
	return nums, true
}

// NeedsUpdate reports whether latest is numerically newer than current.
// Both are semver-ish: a leading "v" is stripped and dot-separated parts
// are compared numerically (so "0.10.0" > "0.9.0"). Missing trailing parts
// count as zero ("1.2" == "1.2.0"). Equal versions, unparseable input, or
// latest <= current all return false — garbage never claims an update.
func NeedsUpdate(latest, current string) bool {
	lv, ok1 := parseVersion(latest)
	cv, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	n := len(lv)
	if len(cv) > n {
		n = len(cv)
	}
	for i := 0; i < n; i++ {
		var l, c int
		if i < len(lv) {
			l = lv[i]
		}
		if i < len(cv) {
			c = cv[i]
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

// BackupDataDir copies dataDir recursively to dataDir+".bak-<timestamp>",
// preserving file modes. It skips nothing: vault.key, secrets.enc,
// workspace/*.md, skills and memories are all preserved by construction.
// Returns the backup path.
func BackupDataDir(dataDir string) (string, error) {
	fi, err := os.Stat(dataDir)
	if err != nil {
		return "", fmt.Errorf("update: stat data dir: %w", err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("update: not a directory: %s", dataDir)
	}
	stamp := time.Now().Format("20060102-150405")
	dst := dataDir + ".bak-" + stamp
	if _, err := os.Lstat(dst); err == nil {
		for i := 2; ; i++ {
			cand := fmt.Sprintf("%s-%d", dst, i)
			if _, err := os.Lstat(cand); os.IsNotExist(err) {
				dst = cand
				break
			} else if err != nil {
				return "", fmt.Errorf("update: stat backup candidate: %w", err)
			}
		}
	}
	if err := os.MkdirAll(dst, fi.Mode().Perm()); err != nil {
		return "", fmt.Errorf("update: create backup dir: %w", err)
	}
	// Best effort: match the top-level dir mode exactly.
	_ = os.Chmod(dst, fi.Mode().Perm())

	err = filepath.WalkDir(dataDir, func(src string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dataDir, src)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, mode.Perm()); err != nil {
				return err
			}
			_ = os.Chmod(target, mode.Perm())
		case mode.IsRegular():
			if err := copyFile(src, target, mode.Perm()); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(src)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		default:
			// Skip sockets, devices, etc. rather than failing the backup.
			// Regular files and dirs (all user data + memories) are copied.
			return nil
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("update: backup %s: %w", dataDir, err)
	}
	return dst, nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	// Preserve the source permission bits exactly (umask may have narrowed them).
	_ = os.Chmod(dst, perm)
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// ReplaceBinary downloads url to binPath+".new" (fsynced), chmods it 0755,
// sanity-checks that it is larger than 100KB, then renames it over binPath.
//
// On Windows a rename over a running binary fails; the error names both
// paths and tells the user to replace the file manually.
func ReplaceBinary(ctx context.Context, url, binPath string) error {
	return replaceBinaryWithClient(ctx, http.DefaultClient, url, binPath)
}

func replaceBinaryWithClient(ctx context.Context, client *http.Client, url, binPath string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("update: download URL is empty")
	}
	if strings.TrimSpace(binPath) == "" {
		return fmt.Errorf("update: binary path is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NimbusOne")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: download %s: %s", url, resp.Status)
	}
	if dir := filepath.Dir(binPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("update: create binary dir: %w", err)
		}
	}
	tmp := binPath + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("update: create %s: %w", tmp, err)
	}
	_, copyErr := io.Copy(out, resp.Body)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: download %s: %w", url, copyErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: fsync %s: %w", tmp, syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: write %s: %w", tmp, closeErr)
	}
	fi, err := os.Stat(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: stat %s: %w", tmp, err)
	}
	if fi.Size() <= minBinarySize {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: downloaded file too small (%d bytes, want >%d): refusing to replace %s", fi.Size(), minBinarySize, binPath)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, binPath); err != nil {
		if runtime.GOOS == "windows" {
			return fmt.Errorf("update: cannot replace running binary on Windows: %v; close nimbus-one and manually replace %s with %s, then restart", err, binPath, tmp)
		}
		return fmt.Errorf("update: replace %s with %s: %w", binPath, tmp, err)
	}
	return nil
}

// Run checks the latest release and, if newer than cfg.Current, downloads
// the matching asset and replaces the binary.
//
// Returns "already current" when no update is needed, or "updated to <tag>"
// on success.
//
// The caller MUST prompt for explicit user consent before calling Run and
// MUST call BackupDataDir first: Run performs no backup (so user data and
// memories are never touched here) and never prompts.
func Run(ctx context.Context, cfg Config) (string, error) {
	tag, assets, err := LatestRelease(ctx, cfg)
	if err != nil {
		return "", err
	}
	if !NeedsUpdate(tag, cfg.Current) {
		return "already current", nil
	}
	name := cfg.assetName(runtime.GOOS, runtime.GOARCH)
	url, ok := assets[name]
	if !ok {
		return "", fmt.Errorf("update: release %s has no asset %q", tag, name)
	}
	binPath := cfg.BinPath
	if strings.TrimSpace(binPath) == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("update: resolve binary path: %w", err)
		}
		binPath = exe
	}
	if err := replaceBinaryWithClient(ctx, cfg.downloadClient(), url, binPath); err != nil {
		return "", err
	}
	return "updated to " + tag, nil
}
