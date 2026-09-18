// Package llm opencode sidecar backend.
package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// isPipeClosed reports the benign race where cmd.Wait closes the stdout
// pipe while the scanner is mid-read on a fast-exiting process. Output
// already accumulated stands; only real decode/IO errors propagate.
func isPipeClosed(err error) bool {
	return errors.Is(err, os.ErrClosed) ||
		strings.Contains(strings.ToLower(err.Error()), "file already closed")
}

// OpEvent is a single normalized sidecar event.
type OpEvent struct {
	Type string
	Text string
}

// Sidecar shells out to the `opencode` binary for agentic runs.
type Sidecar struct {
	Bin     string
	Dir     string
	Model   string
	Timeout time.Duration
}

func (s *Sidecar) bin() string {
	if strings.TrimSpace(s.Bin) != "" {
		return s.Bin
	}
	return "opencode"
}

// opencodeLine is the best-effort NDJSON schema emitted with --format json.
type opencodeLine struct {
	Type  string          `json:"type"`
	Part  json.RawMessage `json:"part"`
	Text  string          `json:"text"`
	Data  string          `json:"data"`
	Error string          `json:"error"`
}

func extractText(raw json.RawMessage, fallbacks ...string) string {
	if len(raw) == 0 {
		for _, f := range fallbacks {
			if strings.TrimSpace(f) != "" {
				return f
			}
		}
		return ""
	}
	// Plain string part.
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	// Object part: look for common text keys.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, k := range []string{"text", "content", "data", "message", "output"} {
			if v, ok := obj[k]; ok {
				if vs, ok := v.(string); ok && vs != "" {
					return vs
				}
			}
		}
		// Nested delta part: {"delta":{"text":...}}
		if d, ok := obj["delta"]; ok {
			if dm, ok := d.(map[string]any); ok {
				if t, ok := dm["text"].(string); ok {
					return t
				}
			}
		}
	}
	for _, f := range fallbacks {
		if strings.TrimSpace(f) != "" {
			return f
		}
	}
	return ""
}

// Run executes `opencode run --format json [-m model] [--dir dir] <prompt>`,
// streams NDJSON stdout, accumulates text, and forwards events.
func (s *Sidecar) Run(ctx context.Context, prompt string, onEvent func(OpEvent)) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("opencode sidecar: empty prompt")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runCtx := ctx
	cancel := context.CancelFunc(func() {})
	if s.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	} else {
		defer cancel()
	}

	args := []string{"run", "--format", "json"}
	if strings.TrimSpace(s.Model) != "" {
		args = append(args, "-m", s.Model)
	}
	if strings.TrimSpace(s.Dir) != "" {
		args = append(args, "--dir", s.Dir)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(runCtx, s.bin(), args...)
	// Give I/O goroutines time to drain fast-exiting processes before Wait
	// closes the pipes — without this the scanner can lose output or report
	// "file already closed" under scheduler pressure (single-core phones).
	cmd.WaitDelay = 2 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("opencode sidecar: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("opencode sidecar: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("opencode sidecar: start %q: %w", s.bin(), err)
	}

	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		b, _ := io.ReadAll(stderr)
		stderrBuf.Write(b)
	}()

	var combined strings.Builder
	scanErr := make(chan error, 1)
	go func() {
		// No explicit Close: cmd.Wait owns the pipe. Closing here races
		// with Wait on fast-exiting processes ("file already closed").
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var ev opencodeLine
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				// Non-JSON line: treat as raw text output.
				combined.WriteString(line + "\n")
				if onEvent != nil {
					onEvent(OpEvent{Type: "text", Text: line})
				}
				continue
			}
			etype := strings.ToLower(strings.TrimSpace(ev.Type))
			if etype == "" && ev.Error != "" {
				etype = "error"
			}
			if etype == "" {
				etype = "text"
			}
			text := extractText(ev.Part, ev.Text, ev.Data)
			switch etype {
			case "error":
				msg := ev.Error
				if msg == "" {
					msg = text
				}
				if msg == "" {
					msg = line
				}
				if onEvent != nil {
					onEvent(OpEvent{Type: "error", Text: msg})
				}
			case "text", "tool_use", "step_start", "step_finish", "reasoning":
				if etype == "text" || etype == "reasoning" {
					combined.WriteString(text)
				}
				if onEvent != nil {
					onEvent(OpEvent{Type: etype, Text: text})
				}
			default:
				if text != "" && onEvent != nil {
					onEvent(OpEvent{Type: etype, Text: text})
				} else if onEvent != nil {
					onEvent(OpEvent{Type: etype, Text: line})
				}
				// Unknown textual payloads still contribute if they look like text.
				if text != "" && (strings.Contains(etype, "text") || strings.Contains(etype, "message") || strings.Contains(etype, "output")) {
					combined.WriteString(text)
				}
			}
		}
		scanErr <- sc.Err()
	}()

	waitErr := cmd.Wait()
	<-stderrDone
	if serr := <-scanErr; serr != nil && runCtx.Err() == nil && !isPipeClosed(serr) {
		// Scanner failure (not caused by cancellation) is reported unless
		// the process itself already failed with a clearer error.
		if waitErr == nil {
			return strings.TrimSpace(combined.String()), fmt.Errorf("opencode sidecar: scan stdout: %w", serr)
		}
	}
	if runCtx.Err() != nil && runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("opencode sidecar: timeout after %s: %w", s.Timeout, runCtx.Err())
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if waitErr != nil {
		serr := strings.TrimSpace(stderrBuf.String())
		if serr == "" {
			return "", fmt.Errorf("opencode sidecar: %w", waitErr)
		}
		return "", fmt.Errorf("opencode sidecar: %v: %s", waitErr, serr)
	}
	return combined.String(), nil
}
