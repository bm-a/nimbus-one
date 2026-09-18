package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"garbage", "v0.1.0", false},
		{"v0.2.0", "garbage", false},
		{"garbage", "also-garbage", false},
		{"", "", false},
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
		{"0.10.0", "0.9.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.2", "v1.2.0", false}, // missing trailing part == zero
		{"v1.2.1", "v1.2", true},
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v10.0.0", false},
	}
	for _, tc := range cases {
		if got := NeedsUpdate(tc.latest, tc.current); got != tc.want {
			t.Errorf("NeedsUpdate(%q,%q)=%v want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestDefaultAssetName(t *testing.T) {
	if got := DefaultAssetName("linux", "arm64"); got != "nimbus-one-linux-arm64" {
		t.Errorf("linux asset = %q", got)
	}
	if got := DefaultAssetName("windows", "amd64"); got != "nimbus-one-windows-amd64.exe" {
		t.Errorf("windows asset = %q", got)
	}
}

func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	// WriteFile is umask-affected; chmod to pin the exact mode under test.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func TestBackupDataDirRoundtrip(t *testing.T) {
	src := t.TempDir()
	dataDir := filepath.Join(src, "data")
	// Representative user data + memories.
	writeFile(t, filepath.Join(dataDir, "vault.key"), "vault-bytes", 0o600)
	writeFile(t, filepath.Join(dataDir, "secrets.enc"), "secrets-bytes", 0o600)
	writeFile(t, filepath.Join(dataDir, "workspace", "MEMORY.md"), "# memories\n- likes tea\n", 0o644)
	writeFile(t, filepath.Join(dataDir, "workspace", "SOUL.md"), "# soul\n", 0o644)
	writeFile(t, filepath.Join(dataDir, "skills", "demo", "SKILL.md"), "# skill\n", 0o644)
	writeFile(t, filepath.Join(dataDir, "nested", "deep", "note.md"), "deep memory", 0o640)

	backup, err := BackupDataDir(dataDir)
	if err != nil {
		t.Fatalf("BackupDataDir: %v", err)
	}
	if !strings.HasPrefix(backup, dataDir+".bak-") {
		t.Fatalf("backup path %q missing expected prefix", backup)
	}

	expect := map[string]string{
		"vault.key":            "vault-bytes",
		"secrets.enc":          "secrets-bytes",
		"workspace/MEMORY.md":  "# memories\n- likes tea\n",
		"workspace/SOUL.md":    "# soul\n",
		"skills/demo/SKILL.md": "# skill\n",
		"nested/deep/note.md":  "deep memory",
	}
	expectPerm := map[string]os.FileMode{
		"vault.key":           0o600,
		"secrets.enc":         0o600,
		"workspace/MEMORY.md": 0o644,
		"nested/deep/note.md": 0o640,
	}
	for rel, want := range expect {
		got, err := os.ReadFile(filepath.Join(backup, rel))
		if err != nil {
			t.Errorf("backup missing %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("backup %s = %q want %q", rel, got, want)
		}
	}
	for rel, wantPerm := range expectPerm {
		fi, err := os.Stat(filepath.Join(backup, rel))
		if err != nil {
			t.Errorf("stat backup %s: %v", rel, err)
			continue
		}
		if fi.Mode().Perm() != wantPerm {
			t.Errorf("backup %s perm = %o want %o", rel, fi.Mode().Perm(), wantPerm)
		}
	}
	// Memories intact: original untouched.
	orig, _ := os.ReadFile(filepath.Join(dataDir, "workspace", "MEMORY.md"))
	if string(orig) != "# memories\n- likes tea\n" {
		t.Errorf("original MEMORY.md mutated: %q", orig)
	}
}

func TestBackupDataDirMissing(t *testing.T) {
	if _, err := BackupDataDir(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("want error for missing data dir")
	}
}

func TestReplaceBinarySmallPayloadFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tiny"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nimbus-one")
	if err := ReplaceBinary(context.Background(), srv.URL, bin); err == nil {
		t.Fatal("want size-sanity error for small payload")
	} else if !strings.Contains(err.Error(), "too small") {
		t.Fatalf("error should mention size sanity, got: %v", err)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Errorf("binPath should not exist after failed replace")
	}
}

func TestReplaceBinaryLargePayloadSucceeds(t *testing.T) {
	big := make([]byte, 150*1024)
	for i := range big {
		big[i] = byte(i % 251)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(big)
	}))
	defer srv.Close()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nimbus-one")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ReplaceBinary(ctx, srv.URL, bin); err != nil {
		t.Fatalf("ReplaceBinary: %v", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read replaced binary: %v", err)
	}
	if len(got) != len(big) {
		t.Fatalf("replaced size = %d want %d", len(got), len(big))
	}
	fi, _ := os.Stat(bin)
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("replaced binary not executable: perm %o", fi.Mode().Perm())
	}
	if _, err := os.Stat(bin + ".new"); !os.IsNotExist(err) {
		t.Errorf(".new file should have been renamed away")
	}
}

func TestLatestReleaseAgainstHttptest(t *testing.T) {
	const tag = "v0.2.0"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/nimbus-one/releases/latest" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("User-Agent"); got != "NimbusOne" {
			t.Errorf("User-Agent = %q want NimbusOne", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"nimbus-one-linux-arm64","browser_download_url":%q},{"name":"nimbus-one-windows-amd64.exe","browser_download_url":%q}]}`,
			tag, srv.URL+"/dl/linux", srv.URL+"/dl/win")
	}))
	defer srv.Close()

	cfg := Config{Repo: "owner/nimbus-one", APIBase: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gotTag, assets, err := LatestRelease(ctx, cfg)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if gotTag != tag {
		t.Errorf("tag = %q want %q", gotTag, tag)
	}
	if assets["nimbus-one-linux-arm64"] != srv.URL+"/dl/linux" {
		t.Errorf("linux asset URL = %q", assets["nimbus-one-linux-arm64"])
	}
	if assets["nimbus-one-windows-amd64.exe"] != srv.URL+"/dl/win" {
		t.Errorf("windows asset URL = %q", assets["nimbus-one-windows-amd64.exe"])
	}
}

func TestLatestReleaseNetworkErrorVerbatim(t *testing.T) {
	// Unroutable port: connection refused must propagate as an error.
	cfg := Config{Repo: "owner/nimbus-one", APIBase: "http://127.0.0.1:1"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := LatestRelease(ctx, cfg); err == nil {
		t.Fatal("want network error")
	}
}

func TestRunAlreadyCurrent(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v0.1.0","assets":[]}`)
	}))
	defer srv.Close()
	cfg := Config{Repo: "owner/nimbus-one", Current: "v0.1.0", APIBase: srv.URL}
	msg, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "already current" {
		t.Errorf("msg = %q want %q", msg, "already current")
	}
}

func TestRunUpdatesBinary(t *testing.T) {
	big := make([]byte, 120*1024)
	for i := range big {
		big[i] = 0xAB
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			name := DefaultAssetName(runtime.GOOS, runtime.GOARCH)
			fmt.Fprintf(w, `{"tag_name":"v0.2.0","assets":[{"name":%q,"browser_download_url":%q}]}`,
				name, srv.URL+"/dl/bin")
		case strings.HasSuffix(r.URL.Path, "/dl/bin"):
			_, _ = w.Write(big)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := Config{
		Repo:    "owner/nimbus-one",
		Current: "v0.1.0",
		APIBase: srv.URL,
		BinPath: filepath.Join(dir, "nimbus-one"),
	}
	msg, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg != "updated to v0.2.0" {
		t.Errorf("msg = %q want %q", msg, "updated to v0.2.0")
	}
	fi, err := os.Stat(cfg.BinPath)
	if err != nil {
		t.Fatalf("replaced binary missing: %v", err)
	}
	if fi.Size() != int64(len(big)) {
		t.Errorf("replaced size = %d want %d", fi.Size(), len(big))
	}
}
