// Package llm provider constructors.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Default base URLs for known providers.
const (
	OpenAIBaseURL     = "https://api.openai.com/v1"
	AnthropicBaseURL  = "https://api.anthropic.com"
	GeminiBaseURL     = "https://generativelanguage.googleapis.com/v1beta/openai"
	GroqBaseURL       = "https://api.groq.com/openai/v1"
	DeepSeekBaseURL   = "https://api.deepseek.com/v1"
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"
	OllamaBaseURL     = "http://localhost:11434/v1"
	// MetaBaseURL serves Meta models (Muse) over an OpenAI-compatible
	// surface. The recommended route is OpenRouter (free tier), which serves
	// meta/muse-spark-1.3-contributor without a separate Meta key; direct
	// Meta endpoints work by selecting provider "meta" explicitly.
	MetaBaseURL      = "https://api.meta.ai/v1"
	anthropicVersion = "2023-06-01"
)

// namedProvider wraps a *Client with an explicit provider name.
type namedProvider struct {
	name   string
	client *Client
}

// Name implements Provider.
func (n *namedProvider) Name() string { return n.name }

// Chat implements Provider.
func (n *namedProvider) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	return n.client.Chat(ctx, req)
}

// Complete implements Provider.
func (n *namedProvider) Complete(ctx context.Context, req ChatRequest) (string, []ToolCall, error) {
	return n.client.Complete(ctx, req)
}

// anthropicProvider translates ChatRequests to Anthropic /v1/messages
// and maps the response back to the OpenAI-ish (text, toolcalls) shape.
type anthropicProvider struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// Name implements Provider.
func (a *anthropicProvider) Name() string { return "anthropic" }

func (a *anthropicProvider) httpClient() *http.Client {
	if a.http != nil {
		return a.http
	}
	return http.DefaultClient
}

func (a *anthropicProvider) base() string {
	b := strings.TrimSpace(a.baseURL)
	if b == "" {
		b = AnthropicBaseURL
	}
	return strings.TrimSuffix(b, "/")
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicReq struct {
	Model       string          `json:"model"`
	System      string          `json:"system,omitempty"`
	Messages    []anthropicMsg  `json:"messages"`
	Tools       []anthropicTool `json:"tools,omitempty"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type  string         `json:"type"`
		Text  string         `json:"text,omitempty"`
		ID    string         `json:"id,omitempty"`
		Name  string         `json:"name,omitempty"`
		Input map[string]any `json:"input,omitempty"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

func anthropicMessages(msgs []Message) (system string, out []anthropicMsg) {
	var systems []string
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			systems = append(systems, m.Content)
		case RoleAssistant:
			if len(m.ToolCalls) == 0 {
				out = append(out, anthropicMsg{Role: "assistant", Content: m.Content})
				continue
			}
			blocks := []any{}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any = map[string]any{}
				if strings.TrimSpace(tc.Arguments) != "" {
					var v any
					if err := json.Unmarshal([]byte(tc.Arguments), &v); err == nil {
						input = v
					} else {
						input = map[string]any{"_raw": tc.Arguments}
					}
				}
				id := tc.ID
				if id == "" {
					id = "toolu_" + tc.Name
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": id, "name": tc.Name, "input": input,
				})
			}
			out = append(out, anthropicMsg{Role: "assistant", Content: blocks})
		case RoleTool:
			// Map tool results to user messages carrying tool_result blocks.
			tid := m.ToolCallID
			if tid == "" {
				tid = m.Name
			}
			blocks := []any{
				map[string]any{"type": "tool_result", "tool_use_id": tid, "content": m.Content},
			}
			out = append(out, anthropicMsg{Role: "user", Content: blocks})
		default: // user and anything else
			out = append(out, anthropicMsg{Role: "user", Content: m.Content})
		}
	}
	system = strings.Join(systems, "\n\n")
	if len(out) == 0 {
		out = append(out, anthropicMsg{Role: "user", Content: ""})
	}
	return system, out
}

// Chat implements Provider via a non-stream Anthropic call fanned out as chunks.
func (a *anthropicProvider) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	system, amsgs := anthropicMessages(req.Messages)
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	areq := anthropicReq{
		Model:       req.Model,
		System:      system,
		Messages:    amsgs,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      false,
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		areq.Tools = append(areq.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: params,
		})
	}
	buf, err := json.Marshal(areq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(a.apiKey) != "" {
		hreq.Header.Set("x-api-key", a.apiKey)
	}
	hreq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.httpClient().Do(hreq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		h := resp.Header.Clone()
		resp.Body.Close()
		return nil, &StatusError{Code: resp.StatusCode, Body: string(b), Headers: h}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("anthropic: read body: %w", err)
	}
	var ar anthropicResp
	if err := json.Unmarshal(b, &ar); err != nil {
		return nil, fmt.Errorf("anthropic: decode body: %w", err)
	}
	var text strings.Builder
	var tcs []ToolCall
	for _, blk := range ar.Content {
		switch blk.Type {
		case "text":
			text.WriteString(blk.Text)
		case "tool_use":
			args := "{}"
			if blk.Input != nil {
				if ab, merr := json.Marshal(blk.Input); merr == nil {
					args = string(ab)
				}
			}
			tcs = append(tcs, ToolCall{ID: blk.ID, Name: blk.Name, Arguments: args})
		}
	}
	ch := make(chan Chunk, 4)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- Chunk{Err: ctx.Err()}
			return
		default:
		}
		if text.Len() > 0 {
			ch <- Chunk{Delta: text.String(), RawTokens: text.Len() / 4}
		}
		ch <- Chunk{ToolCalls: tcs, Done: true}
	}()
	return ch, nil
}

// Complete implements Provider by collecting the Chat channel.
func (a *anthropicProvider) Complete(ctx context.Context, req ChatRequest) (string, []ToolCall, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	ch, err := a.Chat(ctx, req)
	if err != nil {
		return "", nil, err
	}
	var sb strings.Builder
	var tcs []ToolCall
	for ck := range ch {
		if ck.Err != nil {
			return "", nil, ck.Err
		}
		sb.WriteString(ck.Delta)
		if ck.Done {
			tcs = ck.ToolCalls
		}
	}
	return sb.String(), tcs, nil
}

// NewProvider builds a Provider by name. Unknown names fall back to an
// OpenAI-compatible client; an empty baseURL selects the provider default.
func NewProvider(name string, apiKey, baseURL string) Provider {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "anthropic":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = AnthropicBaseURL
		}
		return &anthropicProvider{apiKey: apiKey, baseURL: baseURL}
	case "openai":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = OpenAIBaseURL
		}
		return &namedProvider{name: "openai", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	case "gemini":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = GeminiBaseURL
		}
		return &namedProvider{name: "gemini", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	case "groq":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = GroqBaseURL
		}
		return &namedProvider{name: "groq", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	case "deepseek":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = DeepSeekBaseURL
		}
		return &namedProvider{name: "deepseek", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	case "openrouter":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = OpenRouterBaseURL
		}
		return &namedProvider{name: "openrouter", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	case "ollama":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = OllamaBaseURL
		}
		return &namedProvider{name: "ollama", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	case "meta":
		if strings.TrimSpace(baseURL) == "" {
			baseURL = MetaBaseURL
		}
		return &namedProvider{name: "meta", client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	default:
		if strings.TrimSpace(baseURL) == "" {
			baseURL = OpenAIBaseURL
		}
		if n == "" {
			n = "openai"
		}
		return &namedProvider{name: n, client: &Client{APIKey: apiKey, BaseURL: baseURL}}
	}
}

func inferProvider(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "muse"):
		// Recommended route: Meta Muse via OpenRouter (free tier,
		// OpenAI-compatible). Direct Meta endpoints via provider "meta".
		return "openrouter"
	case strings.HasPrefix(m, "gemini"):
		return "gemini"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "llama"), strings.HasPrefix(m, "mixtral"), strings.HasPrefix(m, "qwen"):
		return "groq"
	case strings.Contains(m, "/"):
		return "openrouter"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"), strings.HasPrefix(m, "chatgpt"):
		return "openai"
	default:
		return "openai"
	}
}

// NewFromConfig infers the provider from the primary model name (unless
// primary itself names a provider) and wires keys/urls maps.
func NewFromConfig(primary string, keys, urls map[string]string) Provider {
	p := strings.TrimSpace(primary)
	lp := strings.ToLower(p)
	known := map[string]bool{
		"openai": true, "anthropic": true, "gemini": true, "groq": true,
		"deepseek": true, "openrouter": true, "ollama": true, "meta": true,
	}
	var prov string
	if known[lp] {
		prov = lp
	} else if strings.HasPrefix(lp, "ollama:") {
		prov = "ollama"
		p = strings.TrimSpace(p[len("ollama:"):])
		_ = p
	} else {
		prov = inferProvider(p)
	}
	var key, url string
	if keys != nil {
		if v, ok := keys[prov]; ok {
			key = v
		} else if v, ok := keys[primary]; ok {
			key = v
		}
	}
	if urls != nil {
		if v, ok := urls[prov]; ok {
			url = v
		} else if v, ok := urls[primary]; ok {
			url = v
		}
	}
	return NewProvider(prov, key, url)
}
