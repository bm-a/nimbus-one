// Package engine — inter-session send, yield, wait, and structured-output tools.
//
// Mirrors OpenClaw (read-only reference in /data/.../tmp/openclaw-src):
//   - src/agents/tools/sessions-send-tool.ts (notify/steer/followup, timeout watch)
//   - src/agents/tools/sessions-yield-tool.ts (claimYield/pauseReason)
//   - src/agents/tools/swarm-collector.ts + agents-wait-tool.ts (collect/poll ids)
//   - src/agents/tools/structured-output-tool.ts (outputSchema validation)
//
// The host injects the actual transport (Send/Await funcs); these tools only
// implement the mode/timeout/validation semantics so they stay testable.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nimbus-one/internal/tools"
)

// SendTool delivers a message to another session.
//
// Modes (arg "mode", default "notify"):
//   - notify  — enqueue and return "accepted" immediately.
//   - steer / followup — run-and-wait: block until the injected Send returns
//     or "timeout_s" elapses (default 30s). A timeout is reported as a plain
//     status string (not an error) so the agent loop can retry or move on.
type SendTool struct {
	// Send performs the actual delivery. It returns the session's reply and
	// whether the reply is still pending (more output expected later).
	Send func(targetSession, message string) (reply string, pending bool, err error)
}

// Name implements tools.Tool.
func (s *SendTool) Name() string { return "sessions_send" }

// Description implements tools.Tool.
func (s *SendTool) Description() string {
	return "Send a message to another session. mode=notify enqueues and returns " +
		"accepted; mode=steer|followup waits up to timeout_s seconds (default 30) for a reply."
}

// Parameters implements tools.Tool.
func (s *SendTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"target_session": {Type: "string", Description: "Target session id or label.", Required: true},
		"message":        {Type: "string", Description: "Message text to deliver.", Required: true},
		"mode":           {Type: "string", Description: "notify | steer | followup (default notify)."},
		"timeout_s":      {Type: "number", Description: "Wait budget for steer/followup in seconds (default 30)."},
	}
}

// Execute implements tools.Tool.
func (s *SendTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	target := firstStringArg(args, "target_session", "session", "target", "sessionKey")
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("sessions_send: missing required argument %q", "target_session")
	}
	msg := firstStringArg(args, "message", "text", "content")
	if strings.TrimSpace(msg) == "" {
		return "", fmt.Errorf("sessions_send: missing required argument %q", "message")
	}
	mode := strings.ToLower(strings.TrimSpace(stringArg(args, "mode")))
	if mode == "" {
		mode = "notify"
	}
	if s.Send == nil {
		return "", fmt.Errorf("sessions_send: no send transport wired")
	}
	switch mode {
	case "notify":
		reply, _, err := s.Send(target, msg)
		if err != nil {
			return "", err
		}
		out := fmt.Sprintf("message queued to %s: accepted", target)
		if strings.TrimSpace(reply) != "" {
			out += " (immediate reply: " + truncate(reply, 200) + ")"
		}
		return out, nil
	case "steer", "followup":
		timeout := timeoutArg(args, 30*time.Second)
		type result struct {
			reply   string
			pending bool
			err     error
		}
		ch := make(chan result, 1)
		go func() {
			reply, pending, err := s.Send(target, msg)
			ch <- result{reply: reply, pending: pending, err: err}
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return fmt.Sprintf("sessions_send: %s to %s timed out after %s — reply still pending; retry or poll later",
				mode, target, timeout), nil
		case r := <-ch:
			if r.err != nil {
				return "", r.err
			}
			if r.pending {
				out := fmt.Sprintf("%s to %s pending", mode, target)
				if strings.TrimSpace(r.reply) != "" {
					out += ": " + truncate(r.reply, 500)
				}
				return out, nil
			}
			return r.reply, nil
		}
	default:
		return "", fmt.Errorf("sessions_send: unknown mode %q (notify|steer|followup)", mode)
	}
}

// YieldTool pauses the current run until a child run completes.
//
// Mirrors sessions-yield-tool.ts claimYield/pauseReason: the first Execute
// claims the pause (via Claim) and blocks in Await; a second claim in the
// same run reports "already claimed" instead of deadlocking.
type YieldTool struct {
	Claim func() bool
	Await func(ctx context.Context) (string, error)
}

// Name implements tools.Tool.
func (y *YieldTool) Name() string { return "sessions_yield" }

// Description implements tools.Tool.
func (y *YieldTool) Description() string {
	return "Pause this run until a spawned child run completes; returns the child's result."
}

// Parameters implements tools.Tool.
func (y *YieldTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"reason": {Type: "string", Description: "Why this run is yielding (pause reason)."},
	}
}

// Execute implements tools.Tool.
func (y *YieldTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if y.Claim == nil || y.Await == nil {
		return "", fmt.Errorf("sessions_yield: no yield bridge wired")
	}
	if !y.Claim() {
		return "sessions_yield: yield already claimed for this run — continuing without pause", nil
	}
	res, err := y.Await(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(res) == "" {
		return "sessions_yield: resumed with empty child output", nil
	}
	return res, nil
}

// WaitTool blocks until a set of background task ids all reach a terminal
// state (done/failed/cancelled) or the timeout elapses.
//
// Mirrors agents-wait-tool.ts: polls with a 500ms ticker (no busy-spin) and
// reports per-task outcomes; a timeout is a status string, not an error.
type WaitTool struct {
	Tasks *Tasks
}

// Name implements tools.Tool.
func (w *WaitTool) Name() string { return "agents_wait" }

// Description implements tools.Tool.
func (w *WaitTool) Description() string {
	return "Wait until background task ids all finish (done/failed/cancelled) or timeout_s elapses. Polls every 500ms."
}

// Parameters implements tools.Tool.
func (w *WaitTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"ids":       {Type: "array", Description: "Task ids to wait for (JSON array or comma-separated string).", Required: true},
		"timeout_s": {Type: "number", Description: "Wait budget in seconds (default 30)."},
	}
}

func (w *WaitTool) tasks() *Tasks {
	if w.Tasks != nil {
		return w.Tasks
	}
	return NewTasks()
}

// Execute implements tools.Tool.
func (w *WaitTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	ids, err := idListArg(args)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("agents_wait: no task ids given")
	}
	ts := w.tasks()
	for _, id := range ids {
		if _, ok := ts.Get(id); !ok {
			return "", fmt.Errorf("agents_wait: unknown task %q", id)
		}
	}
	timeout := timeoutArg(args, 30*time.Second)
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var finished []string
		var running []string
		for _, id := range ids {
			task, ok := ts.Get(id)
			if !ok {
				return "", fmt.Errorf("agents_wait: unknown task %q", id)
			}
			if task.State == TaskRunning {
				running = append(running, id)
				continue
			}
			line := id + ": " + task.State
			switch task.State {
			case TaskDone:
				if strings.TrimSpace(task.Result) != "" {
					line += ": " + truncate(task.Result, 200)
				}
			case TaskFailed:
				if strings.TrimSpace(task.Err) != "" {
					line += ": " + truncate(task.Err, 200)
				}
			}
			finished = append(finished, line)
		}
		if len(running) == 0 {
			return "agents_wait: all " + itoaStr(len(ids)) + " finished\n" + strings.Join(finished, "\n"), nil
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("agents_wait: timeout after %s — still running: %s; finished:\n%s",
				timeout, strings.Join(running, ", "), strings.Join(finished, "\n")), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// StructuredTool records the agent's final structured result.
//
// Mirrors structured-output-tool.ts: the result must be non-empty, and when
// Schema is set it must parse as JSON (lightweight shape check — a full
// JSON-schema validator would need a non-stdlib dependency).
type StructuredTool struct {
	Schema string
	Result *string
}

// Name implements tools.Tool.
func (s *StructuredTool) Name() string { return "structured_output" }

// Description implements tools.Tool.
func (s *StructuredTool) Description() string {
	return "Record the final structured result. Fails on empty output; when an output schema is set the result must be valid JSON."
}

// Parameters implements tools.Tool.
func (s *StructuredTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"result": {Type: "string", Description: "Final structured result payload.", Required: true},
	}
}

// Execute implements tools.Tool.
func (s *StructuredTool) Execute(_ context.Context, args map[string]any) (string, error) {
	res := firstStringArg(args, "result", "output", "text")
	if strings.TrimSpace(res) == "" {
		return "", fmt.Errorf("structured_output: empty result — produce the required output before calling")
	}
	if strings.TrimSpace(s.Schema) != "" {
		var v any
		if err := json.Unmarshal([]byte(res), &v); err != nil {
			return "", fmt.Errorf("structured_output: result is not valid JSON (schema set): %w", err)
		}
	}
	if s.Result != nil {
		*s.Result = res
	}
	return fmt.Sprintf("structured output recorded (%d bytes)", len(res)), nil
}

// --- arg helpers ---

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	s, _ := args[key].(string)
	return s
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := stringArg(args, k); strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// timeoutArg reads "timeout_s"/"timeout" as seconds (float64, int, or numeric
// string). Non-positive or missing values fall back to def.
func timeoutArg(args map[string]any, def time.Duration) time.Duration {
	v, ok := args["timeout_s"]
	if !ok {
		v, ok = args["timeout"]
	}
	if !ok {
		return def
	}
	var secs float64
	switch t := v.(type) {
	case float64:
		secs = t
	case float32:
		secs = float64(t)
	case int:
		secs = float64(t)
	case int64:
		secs = float64(t)
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%f", &f); err == nil {
			secs = f
		}
	}
	if secs <= 0 {
		return def
	}
	return time.Duration(secs * float64(time.Second))
}

// idListArg accepts ids as []string, []any-of-string, a JSON array string,
// a comma/space-separated string, or a single "id" string.
func idListArg(args map[string]any) ([]string, error) {
	v, ok := args["ids"]
	if !ok {
		if single, _ := args["id"].(string); strings.TrimSpace(single) != "" {
			return []string{strings.TrimSpace(single)}, nil
		}
		return nil, fmt.Errorf("agents_wait: missing required argument %q", "ids")
	}
	switch t := v.(type) {
	case []string:
		return cleanIDs(t), nil
	case []any:
		out := make([]string, 0, len(t))
		for i, e := range t {
			s, ok := e.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("agents_wait: ids[%d] is not a string", i)
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		if strings.HasPrefix(s, "[") {
			var arr []string
			if err := json.Unmarshal([]byte(s), &arr); err == nil {
				return cleanIDs(arr), nil
			}
		}
		return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }), nil
	default:
		return nil, fmt.Errorf("agents_wait: ids must be an array of strings, got %T", v)
	}
}

func cleanIDs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func itoaStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
