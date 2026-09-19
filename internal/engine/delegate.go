package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nimbus-one/internal/tools"
)

// maxDelegateDepth caps subagent nesting: the top agent may delegate, and a
// subagent may do work, but subagents may not spawn their own subagents.
// Depth travels in the context so background tasks inherit it correctly.
const maxDelegateDepth = 2

type depthKey struct{}

// withDepth returns ctx carrying depth+1.
func withDepth(ctx context.Context) context.Context {
	return context.WithValue(ctx, depthKey{}, depthOf(ctx)+1)
}

// depthOf reads the delegation depth (0 = top-level agent).
func depthOf(ctx context.Context) int {
	if d, ok := ctx.Value(depthKey{}).(int); ok {
		return d
	}
	return 0
}

// DelegateTool spawns a subagent: an isolated Engine run with its own
// scratchpad over the same tools, either synchronously or in the background
// via Tasks. This is the multi-agent primitive — fan out, then synthesize.
// ContextMode selects the child context (see context.go: isolated default,
// fork same-agent, light brief-only); it can also be set per-call with the
// "context_mode" arg, which overrides this field.
type DelegateTool struct {
	Eng         *Engine // usually the parent engine itself; depth cap stops recursion
	Tasks       *Tasks
	ContextMode string
}

// Name implements tools.Tool.
func (d *DelegateTool) Name() string { return "delegate" }

func (d *DelegateTool) Description() string {
	return "Spawn a subagent for one bounded piece of work (research, parallel " +
		"legwork, isolated experiments). Give a self-contained brief; you receive " +
		"a summary back. Set background=true for long work and poll tasks_poll."
}

func (d *DelegateTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"brief":        {Type: "string", Description: "Self-contained task brief for the subagent.", Required: true},
		"background":   {Type: "boolean", Description: "Run in background; returns a task id for tasks_poll."},
		"context_mode": {Type: "string", Description: "Child context: isolated (default) | fork (same-agent) | light (brief-only)."},
	}
}

func (d *DelegateTool) tasks() *Tasks {
	if d.Tasks != nil {
		return d.Tasks
	}
	return NewTasks()
}

// Execute implements tools.Tool.
func (d *DelegateTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	brief, _ := args["brief"].(string)
	if strings.TrimSpace(brief) == "" {
		return "", fmt.Errorf("delegate: missing required argument %q", "brief")
	}
	if d.Eng == nil {
		return "", fmt.Errorf("delegate: no engine wired")
	}
	if depthOf(ctx) >= maxDelegateDepth {
		return "delegation refused: subagents cannot spawn subagents (depth cap) — " +
			"finish this piece of work yourself and report back", nil
	}
	bg, _ := args["background"].(bool)
	mode := d.ContextMode
	if m, _ := args["context_mode"].(string); strings.TrimSpace(m) != "" {
		mode = m
	}
	// A delegated subagent runs in-process as the same agent, so fork is
	// valid; Resolve still degrades bad input to isolated with a note.
	spec, note := ChildSpec{Mode: mode}.Resolve(ChildIsolated, true)
	system := "You are a subagent. Complete the brief below and report back concisely: what you did, what you verified, what's left. Do not delegate further."
	switch spec.Mode {
	case ChildLight:
		system = "You are a subagent with no prior conversation context. Complete the brief below and report back concisely."
	case ChildFork:
		system = "You are a subagent continuing this agent's work with shared context. Complete the brief below and report back concisely: what you did, what you verified, what's left."
	}
	if note != "" {
		system += " (" + note + ")"
	}
	run := func(c context.Context) (string, error) {
		sub, cancel := context.WithTimeout(c, 5*time.Minute)
		defer cancel()
		out, err := d.Eng.Run(sub, system, brief)
		if err != nil {
			return "", err
		}
		if len(out) > 8*1024 {
			out = out[:8*1024] + "\n[…subagent report truncated 8KB…]"
		}
		return out, nil
	}
	if !bg {
		return run(withDepth(ctx))
	}
	id := d.tasks().Start(brief, func(c context.Context) (string, error) {
		return run(withDepth(c))
	})
	return fmt.Sprintf("background subagent started as %s — poll with tasks_poll (action=poll, id=%s)", id, id), nil
}

// TasksTool inspects and controls background work.
type TasksTool struct {
	Tasks *Tasks
}

// Name implements tools.Tool.
func (t *TasksTool) Name() string { return "tasks_poll" }

func (t *TasksTool) Description() string {
	return "Poll/list/cancel background subagent tasks. Actions: poll (needs id), list, cancel (needs id)."
}

func (t *TasksTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"action": {Type: "string", Description: "poll | list | cancel.", Required: true},
		"id":     {Type: "string", Description: "Task id for poll/cancel."},
	}
}

func (t *TasksTool) tasks() *Tasks {
	if t.Tasks != nil {
		return t.Tasks
	}
	return NewTasks()
}

// Execute implements tools.Tool.
func (t *TasksTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	action, _ := args["action"].(string)
	id, _ := args["id"].(string)
	ts := t.tasks()
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "poll":
		task, ok := ts.Get(id)
		if !ok {
			return "", fmt.Errorf("tasks_poll: unknown task %q", id)
		}
		switch task.State {
		case TaskRunning:
			return fmt.Sprintf("%s still running (started %s) — poll again later", task.ID, task.CreatedAt.Format("15:04:05")), nil
		case TaskDone:
			return fmt.Sprintf("%s done:\n%s", task.ID, task.Result), nil
		case TaskCancelled:
			return fmt.Sprintf("%s cancelled", task.ID), nil
		default:
			return fmt.Sprintf("%s failed: %s", task.ID, task.Err), nil
		}
	case "list":
		all := ts.List()
		if len(all) == 0 {
			return "no background tasks", nil
		}
		var b strings.Builder
		for _, task := range all {
			fmt.Fprintf(&b, "%s: %s (%.40s)\n", task.ID, task.State, task.Input)
		}
		return b.String(), nil
	case "cancel":
		if ts.Cancel(id) {
			return fmt.Sprintf("%s cancelled", id), nil
		}
		return "", fmt.Errorf("tasks_poll: cannot cancel %q (unknown or finished)", id)
	default:
		return "", fmt.Errorf("tasks_poll: unknown action %q (poll|list|cancel)", action)
	}
}
