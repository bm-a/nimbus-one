// Background process sessions with supervisor, log capture and stdin.
//
// OpenClaw reference (read-only):
//
//	bash-tools/exec-runtime.ts + exec-defaults.ts (yieldMs/background,
//	host routing, supervisor, output caps). Nimbus port: exec.CommandContext
//	supervisor, per-session output file in os.TempDir, incremental log tails.
package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProcessLogTail caps bytes returned by Poll's incremental tail.
const ProcessLogTail = 4096

// DefaultProcessTimeout applies when timeout_s is unset.
const DefaultProcessTimeout = 300 * time.Second

type bgProc struct {
	id      string
	command string
	cwd     string
	logPath string
	started time.Time

	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser

	mu      sync.Mutex
	done    bool
	exitErr error
}

// ProcessManager supervises background shell sessions.
type ProcessManager struct {
	mu    sync.Mutex
	procs map[string]*bgProc
	next  int
}

// NewProcessManager creates an empty manager.
func NewProcessManager() *ProcessManager {
	return &ProcessManager{procs: map[string]*bgProc{}}
}

// Start launches command in the background, returning a session id.
func (m *ProcessManager) Start(command, cwd string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("process: empty command")
	}
	if timeout <= 0 {
		timeout = DefaultProcessTimeout
	}
	logFile, err := os.CreateTemp("", "nimbus-proc-*.log")
	if err != nil {
		return "", fmt.Errorf("process: log file: %w", err)
	}
	logPath := logFile.Name()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = minimalEnv(nil)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return "", fmt.Errorf("process: stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return "", fmt.Errorf("process: start: %w", err)
	}
	m.mu.Lock()
	m.next++
	id := fmt.Sprintf("proc-%d", m.next)
	p := &bgProc{id: id, command: command, cwd: cwd, logPath: logPath,
		started: time.Now(), cmd: cmd, cancel: cancel, stdin: stdin}
	m.procs[id] = p
	m.mu.Unlock()

	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		p.mu.Lock()
		p.done = true
		p.exitErr = err
		p.mu.Unlock()
	}()
	return id, nil
}

func (m *ProcessManager) get(id string) (*bgProc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("process: unknown session %q", id)
	}
	return p, nil
}

// Poll reports running/done plus an incremental log tail.
func (m *ProcessManager) Poll(id string) (string, error) {
	p, err := m.get(id)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	done := p.done
	exitErr := p.exitErr
	p.mu.Unlock()
	tail, total, terr := readTail(p.logPath, ProcessLogTail)
	if terr != nil {
		tail = ""
		total = 0
	}
	if done {
		status := "done (exit 0)"
		if exitErr != nil {
			status = fmt.Sprintf("done (%v)", exitErr)
		}
		return fmt.Sprintf("%s %s elapsed=%s logBytes=%d\n%s", p.id, status,
			time.Since(p.started).Round(time.Second), total, tail), nil
	}
	return fmt.Sprintf("%s running elapsed=%s logBytes=%d\n%s", p.id,
		time.Since(p.started).Round(time.Second), total, tail), nil
}

// Log returns log bytes from offset with the next cursor.
func (m *ProcessManager) Log(id string, offset int64) (string, int64, error) {
	p, err := m.get(id)
	if err != nil {
		return "", 0, err
	}
	if offset < 0 {
		offset = 0
	}
	f, err := os.Open(p.logPath)
	if err != nil {
		return "", 0, fmt.Errorf("process: open log: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("process: stat log: %w", err)
	}
	if offset > st.Size() {
		offset = st.Size()
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", 0, fmt.Errorf("process: seek log: %w", err)
	}
	// Cap single reads at 64KB to bound memory; caller pages with cursor.
	data, err := io.ReadAll(io.LimitReader(f, 64*1024))
	if err != nil {
		return "", 0, fmt.Errorf("process: read log: %w", err)
	}
	return string(data), offset + int64(len(data)), nil
}

// Write sends text to the session's stdin.
func (m *ProcessManager) Write(id, text string) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	if done {
		return fmt.Errorf("process: session %q already finished", id)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, err := io.WriteString(p.stdin, text); err != nil {
		return fmt.Errorf("process: stdin write: %w", err)
	}
	return nil
}

// Kill stops the session (supervisor cancel + process kill).
func (m *ProcessManager) Kill(id string) (string, error) {
	p, err := m.get(id)
	if err != nil {
		return "", err
	}
	p.cancel()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	// Wait briefly for the waiter goroutine to mark done.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Sprintf("%s killed", p.id), nil
}

// List returns one status line per session.
func (m *ProcessManager) List() string {
	m.mu.Lock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	if len(ids) == 0 {
		return "(no sessions)"
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		line, err := m.Poll(id)
		if err != nil {
			continue
		}
		first := strings.SplitN(line, "\n", 2)[0]
		b.WriteString(first + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Prune removes finished sessions (and their log files), returning the count.
func (m *ProcessManager) Prune() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, p := range m.procs {
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if !done {
			continue
		}
		_ = os.Remove(p.logPath)
		delete(m.procs, id)
		n++
	}
	return n
}

func readTail(path string, limit int) (string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	if len(data) > limit {
		return "...[tail]\n" + string(data[len(data)-limit:]), int64(len(data)), nil
	}
	return string(data), int64(len(data)), nil
}

// ProcessTool exposes background sessions to the agent loop.
type ProcessTool struct {
	AllowDirs []string
	Manager   *ProcessManager
}

// Name returns "process".
func (t *ProcessTool) Name() string { return "process" }

// Description describes the process tool.
func (t *ProcessTool) Description() string {
	return "Background shell sessions (supervisor + log file). Args: action (start|poll|log|write|kill|list|prune), id, command, cwd, timeout_s, offset (log byte cursor), text (stdin). Start returns a session id; poll shows running/done + log tail."
}

// Parameters describes the process arguments.
func (t *ProcessTool) Parameters() map[string]Param {
	return map[string]Param{
		"action":    {Type: "string", Description: "start, poll, log, write, kill, list, prune", Required: true},
		"id":        {Type: "string", Description: "Session id (poll/log/write/kill)"},
		"command":   {Type: "string", Description: "Shell command (start)"},
		"cwd":       {Type: "string", Description: "Working directory (start)"},
		"timeout_s": {Type: "number", Description: "Kill after N seconds (start, default 300)"},
		"offset":    {Type: "number", Description: "Log byte cursor (log)"},
		"text":      {Type: "string", Description: "Stdin text (write)"},
	}
}

func (t *ProcessTool) manager() *ProcessManager {
	if t.Manager != nil {
		return t.Manager
	}
	return NewProcessManager()
}

// Execute dispatches the session action.
func (t *ProcessTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m := t.manager()
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	switch action {
	case "start":
		cwd := stringArg(args, "cwd")
		if cwd == "" && len(t.AllowDirs) > 0 {
			cwd = t.AllowDirs[0]
		}
		if cwd != "" {
			abs, err := resolveWithinAllow(cwd, t.AllowDirs)
			if err != nil {
				return "", fmt.Errorf("process: bad cwd: %w", err)
			}
			cwd = abs
		}
		timeout := DefaultProcessTimeout
		if v, ok := numberArg(args, "timeout_s"); ok && v > 0 {
			timeout = time.Duration(v * float64(time.Second))
		}
		id, err := m.Start(stringArg(args, "command"), cwd, timeout)
		if err != nil {
			return "", err
		}
		return id, nil
	case "poll":
		return m.Poll(stringArg(args, "id"))
	case "log":
		var off int64
		if v, ok := numberArg(args, "offset"); ok && v > 0 {
			off = int64(v)
		}
		out, next, err := m.Log(stringArg(args, "id"), off)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[nextOffset=%d]\n%s", next, out), nil
	case "write":
		return "stdin sent", m.Write(stringArg(args, "id"), stringArg(args, "text"))
	case "kill":
		return m.Kill(stringArg(args, "id"))
	case "list":
		return m.List(), nil
	case "prune":
		return fmt.Sprintf("pruned %d session(s)", m.Prune()), nil
	default:
		return "", fmt.Errorf("process: unknown action %q (want start|poll|log|write|kill|list|prune)", action)
	}
}
