// Package llm defines the shared message/provider contracts.
package llm

import "context"

// Role constants.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is a single chat turn.
type Message struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Extra      map[string]any `json:"-"`
}

// ToolCall is a model-requested function invocation.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef is the JSON-schema-ish tool description sent to providers.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ChatRequest is a provider-agnostic completion request.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	Stream      bool      `json:"stream"`
}

// Chunk is a streamed delta.
type Chunk struct {
	Delta     string
	ToolCalls []ToolCall
	Done      bool
	Err       error
	RawTokens int
	// Usage carries normalized token accounting when the provider
	// reported a usage object (non-stream body or stream usage chunk).
	// RawTokens remains a len/4 heuristic fallback when Usage is zero.
	Usage Usage
}

// Provider is implemented by every LLM backend.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
	Complete(ctx context.Context, req ChatRequest) (string, []ToolCall, error)
}
