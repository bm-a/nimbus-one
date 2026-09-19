// Agent-facing cron tool: schedule/list/cancel recurring briefs.
//
// A scheduled brief is a promise the daemon will run later through the
// same agent loop (execution wiring lands with the daemon; until then
// jobs persist in-memory and Due() reports readiness).
package cron

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nimbus-one/internal/tools"
)

// CronTool manages scheduled briefs.
type CronTool struct {
	Jobs *Registry
}

// Name returns "cron".
func (t *CronTool) Name() string { return "cron" }

// Description describes the cron tool.
func (t *CronTool) Description() string {
	return "Schedule recurring work (5-field cron: min hour dom month dow). Args: action (schedule|list|cancel|due), spec (schedule, e.g. '0 9 * * *'), brief (schedule, what to do), id (cancel). Schedules survive in memory only — daemon restarts clear them."
}

// Parameters describes the cron arguments.
func (t *CronTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"action": {Type: "string", Description: "schedule|list|cancel|due", Required: true},
		"spec":   {Type: "string", Description: "Cron spec for schedule."},
		"brief":  {Type: "string", Description: "Task brief for schedule."},
		"id":     {Type: "string", Description: "Job id for cancel."},
	}
}

func (t *CronTool) jobs() *Registry {
	if t.Jobs != nil {
		return t.Jobs
	}
	return NewRegistry()
}

// Execute runs the cron action.
func (t *CronTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	switch strings.ToLower(strings.TrimSpace(strArg(args, "action"))) {
	case "schedule":
		id, err := t.jobs().Add(
			strings.TrimSpace(strArg(args, "spec")),
			strings.TrimSpace(strArg(args, "brief")))
		if err != nil {
			return "", fmt.Errorf("cron: %w", err)
		}
		return fmt.Sprintf("scheduled %s", id), nil
	case "list":
		all := t.jobs().List()
		if len(all) == 0 {
			return "no scheduled jobs", nil
		}
		var b strings.Builder
		for _, j := range all {
			fmt.Fprintf(&b, "%s: %s — %.80s (last run %s)\n", j.ID, j.Spec, j.Brief, lastRunText(j.LastRun))
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "cancel":
		id := strings.TrimSpace(strArg(args, "id"))
		if t.jobs().Remove(id) {
			return fmt.Sprintf("%s cancelled", id), nil
		}
		return "", fmt.Errorf("cron: unknown job %q", id)
	case "due":
		due := t.jobs().Due(time.Now())
		if len(due) == 0 {
			return "nothing due", nil
		}
		var b strings.Builder
		for _, j := range due {
			fmt.Fprintf(&b, "%s: %s — %.80s\n", j.ID, j.Spec, j.Brief)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return "", fmt.Errorf("cron: unknown action (want schedule|list|cancel|due)")
}

func lastRunText(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04")
}

// strArg extracts an optional string argument.
func strArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	s, _ := args[key].(string)
	return s
}
