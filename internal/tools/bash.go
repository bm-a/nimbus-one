package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// BashOutputLimit caps captured bash output.
const BashOutputLimit = 20 * 1024

// DefaultBashTimeout applies when BashTool.Timeout is unset.
const DefaultBashTimeout = 60 * time.Second

// BashTool runs shell commands via sh -c (powershell on Windows).
type BashTool struct {
	Timeout time.Duration
	// AllowDirs scopes the working directory; empty means no restriction.
	AllowDirs []string
}

// Name returns "bash".
func (t *BashTool) Name() string { return "bash" }

// Description describes the bash tool.
func (t *BashTool) Description() string {
	return "Run a shell command (sh -c). Args: command (required), cwd (working directory), timeout_s (seconds), env (extra KEY=value map)."
}

// Parameters describes the bash arguments.
func (t *BashTool) Parameters() map[string]Param {
	return map[string]Param{
		"command":   {Type: "string", Description: "Shell command to run", Required: true},
		"cwd":       {Type: "string", Description: "Working directory (must be within allowed dirs when restricted)"},
		"timeout_s": {Type: "number", Description: "Timeout in seconds (default 60, max 300)"},
		"env":       {Type: "object", Description: "Extra environment variables as KEY=value map"},
	}
}

// Execute runs the command with a timeout and isolated minimal environment.
func (t *BashTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	cmdStr := stringArg(args, "command")
	if strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("bash: empty command")
	}
	cwd := stringArg(args, "cwd")
	if cwd == "" && len(t.AllowDirs) > 0 {
		cwd = t.AllowDirs[0]
	}
	if cwd != "" {
		abs, err := resolveWithinAllow(cwd, t.AllowDirs)
		if err != nil {
			return "", fmt.Errorf("bash: bad cwd: %w", err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("bash: cwd %s: %w", abs, err)
		}
		if !st.IsDir() {
			return "", fmt.Errorf("bash: cwd %s is not a directory", abs)
		}
		cwd = abs
	}

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = DefaultBashTimeout
	}
	if v, ok := numberArg(args, "timeout_s"); ok {
		if v < 1 {
			v = 1
		}
		if v > 300 {
			v = 300
		}
		timeout = time.Duration(v * float64(time.Second))
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = minimalEnv(stringMapArg(args, "env"))

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := truncateBytes(buf.String(), BashOutputLimit)
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("bash: timeout after %s", timeout)
	}
	if err != nil {
		return out, fmt.Errorf("bash: %w", err)
	}
	return out, nil
}

// minimalEnv builds an isolated environment: a small allowlisted base plus
// caller-supplied extras (which override the base).
func minimalEnv(extra map[string]string) []string {
	baseKeys := []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "TERM", "LOGNAME", "USER"}
	if runtime.GOOS == "windows" {
		baseKeys = append(baseKeys, "SystemRoot", "WINDIR", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "HOMEDRIVE", "HOMEPATH")
	} else {
		baseKeys = append(baseKeys, "SHELL")
	}
	env := make([]string, 0, len(baseKeys)+len(extra))
	seen := map[string]bool{}
	for _, k := range baseKeys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			env = append(env, k+"="+v)
			seen[k] = true
		}
	}
	for k, v := range extra {
		if !validEnvKey(k) {
			continue
		}
		if seen[k] {
			for i, e := range env {
				if strings.HasPrefix(e, k+"=") {
					env[i] = k + "=" + v
					break
				}
			}
			continue
		}
		env = append(env, k+"="+v)
		seen[k] = true
	}
	return env
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n...[truncated %d bytes]", len(s)-limit)
}

// stringArg extracts an optional string argument.
func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	switch v := args[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

// numberArg extracts an optional numeric argument.
func numberArg(args map[string]any, key string) (float64, bool) {
	if args == nil {
		return 0, false
	}
	switch v := args[key].(type) {
	case nil:
		return 0, false
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case string:
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// stringMapArg extracts an optional string map argument.
func stringMapArg(args map[string]any, key string) map[string]string {
	out := map[string]string{}
	if args == nil {
		return out
	}
	switch v := args[key].(type) {
	case map[string]string:
		for k, val := range v {
			out[k] = val
		}
	case map[string]any:
		for k, val := range v {
			out[k] = fmt.Sprint(val)
		}
	}
	return out
}
