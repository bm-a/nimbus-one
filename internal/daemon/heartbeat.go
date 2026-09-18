package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"nimbus-one/internal/gateway"
)

// engineIface mirrors internal/engine Engine.Run without importing it
// (avoids an import cycle).
type engineIface interface {
	Run(ctx context.Context, system, user string) (string, error)
}

// Heartbeat runs HEARTBEAT.md checklist items through the engine.
type Heartbeat struct {
	WorkspaceDir string
	Broker       *gateway.Broker
	Eng          engineIface
}

// Checklist parses HEARTBEAT.md "- [ ]" lines in WorkspaceDir.
// Returns nil when the file is missing/unreadable.
func (h *Heartbeat) Checklist() []string {
	if h == nil || h.WorkspaceDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(h.WorkspaceDir, "HEARTBEAT.md"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "- [ ]") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(t, "- [ ]"))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// isNoReply reports heartbeat no-op markers.
func isNoReply(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	u := strings.ToUpper(t)
	return u == "NO_REPLY" || u == "HEARTBEAT_OK"
}

// Tick runs each checklist item via Eng.Run and forwards non-empty,
// non-noop results through Broker.Handle("heartbeat","system",...).
// It returns all forwarded results.
func (h *Heartbeat) Tick(ctx context.Context) []string {
	if h == nil {
		return nil
	}
	items := h.Checklist()
	var results []string
	for _, item := range items {
		select {
		case <-ctx.Done():
			return results
		default:
		}
		if h.Eng == nil {
			slog.Error("heartbeat: nil engine", "item", item)
			continue
		}
		res, err := h.Eng.Run(ctx, "You are heartbeat worker. Execute the checklist item concisely. Reply NO_REPLY if nothing needs attention.", item)
		if err != nil {
			slog.Error("heartbeat: engine run failed", "item", item, "err", err)
			continue
		}
		res = strings.TrimSpace(res)
		if isNoReply(res) {
			continue
		}
		results = append(results, res)
		if h.Broker != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("heartbeat: broker panic", "panic", r)
					}
				}()
				h.Broker.Handle("heartbeat", "system", res)
			}()
		}
	}
	return results
}
