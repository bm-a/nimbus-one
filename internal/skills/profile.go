// Package skills — tool profiles (allowlist filtering).
//
// Mirrors OpenClaw tool-policy-pipeline.ts (profile → allow layers): a named
// Profile keeps only the listed tools from a registry so constrained runs
// (plan mode, light subagents) expose a minimal surface.
package skills

import (
	"fmt"

	"nimbus-one/internal/tools"
)

// Profile is a named tool allowlist.
type Profile struct {
	Name string
}

// Filter returns a new registry containing only the allow-listed tools found
// in r. Unknown allow names are reported as warnings, never errors. The input
// registry is never mutated; a nil input yields an empty registry.
func (p *Profile) Filter(r *tools.Registry, allow []string) (*tools.Registry, []string) {
	out := tools.NewRegistry()
	var warnings []string
	if r == nil {
		for _, name := range allow {
			warnings = append(warnings, fmt.Sprintf("profile %q: unknown tool %q", p.name(), name))
		}
		return out, warnings
	}
	seen := map[string]bool{}
	for _, name := range allow {
		if seen[name] {
			continue
		}
		seen[name] = true
		t, ok := r.Get(name)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("profile %q: unknown tool %q", p.name(), name))
			continue
		}
		out.Register(t)
	}
	return out, warnings
}

func (p *Profile) name() string {
	if p == nil || p.Name == "" {
		return "default"
	}
	return p.Name
}
