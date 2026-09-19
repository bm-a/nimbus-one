// Package llm — task-kind model routing (Phase 3).
//
// RouteModel resolves which model serves a task kind. Precedence:
// explicit per-call model > user-configured kind route > primary.
// Empty kinds and unknown routes inherit the primary. Nothing here
// invents a model: every outcome is the user's own configuration.
package llm

import "strings"

// RouteModel picks the model for a task kind.
func RouteModel(kind, explicit string, routes map[string]string, primary string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	if m, ok := routes[strings.ToLower(strings.TrimSpace(kind))]; ok && strings.TrimSpace(m) != "" {
		return strings.TrimSpace(m)
	}
	return primary
}
