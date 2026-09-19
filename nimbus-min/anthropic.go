package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Anthropic wire constants.
const (
	anthropicVersion = "2023-06-01"
	anthropicModel   = "claude-sonnet-4-5-20250929"
	maxTokens        = 4096
)

// anthropicClient speaks POST {base}/v1/messages with the pinned version
// header. Transport MUST be the netguard client (api.anthropic.com only).
type anthropicClient struct {
	http  *http.Client
	key   string
	base  string
	model string
}

func newAnthropicClient(key string) *anthropicClient {
	return &anthropicClient{http: guardedHTTPClient(), key: key, base: AnthropicBaseURL, model: anthropicModel}
}

// message is one conversation turn.
type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock is text | tool_use | tool_result.
type contentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
}

// toolDef is the input_schema half of what the model sees.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// messagesRequest is the POST body.
type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
	Tools     []toolDef `json:"tools,omitempty"`
}

// messagesResponse is the subset we consume.
type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// complete sends one turn and returns the raw content blocks.
// HTTP and API errors are returned as errors with the status preserved;
// malformed bodies are errors, never silent empty turns.
func (c *anthropicClient) complete(system string, msgs []message, tools []toolDef) ([]contentBlock, error) {
	if strings.TrimSpace(c.key) == "" {
		return nil, fmt.Errorf("missing Anthropic API key — run `nimbus-min onboard` first")
	}
	body, err := json.Marshal(messagesRequest{
		Model: c.model, MaxTokens: maxTokens, System: system, Messages: msgs, Tools: tools,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %v", err)
	}
	req, err := http.NewRequest("POST", c.base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", anthropicVersion)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Anthropic request failed: %v (check your connection and key)", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read Anthropic response: %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Anthropic error %d: %s", resp.StatusCode, oneLine(raw))
	}
	var out messagesResponse
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("Anthropic returned malformed JSON: %v", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("Anthropic error (%s): %s", out.Error.Type, out.Error.Message)
	}
	if out.Content == nil {
		return nil, fmt.Errorf("Anthropic returned no content blocks")
	}
	return out.Content, nil
}

func oneLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.ReplaceAll(s, "\n", " ")
}
