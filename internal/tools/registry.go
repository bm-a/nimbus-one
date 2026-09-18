// Package tools defines the shared Tool contract used by engine, skills and MCP.
package tools

import "context"

// Param describes a single tool parameter.
type Param struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Tool is the single interface every built-in, skill-backed, and MCP-backed
// capability must implement. Capabilities are discovered via the registry and
// exposed to the model through the internal prompt — never hardcoded in engine.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]Param
	Execute(ctx context.Context, args map[string]any) (string, error)
}

// Registry holds named tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds or replaces a tool.
func (r *Registry) Register(t Tool) { r.tools[t.Name()] = t }

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) { t, ok := r.tools[name]; return t, ok }

// All returns all registered tools.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Names returns sorted tool names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

// Count returns the number of tools.
func (r *Registry) Count() int { return len(r.tools) }
