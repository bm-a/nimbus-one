package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// openaiClient speaks POST {base}/chat/completions — the shape ~40
// providers share (OpenClaw `openai-completions` family). Transport MUST
// be the netguard client restricted to the provider's host.
type openaiClient struct {
	http  *http.Client
	key   string
	base  string
	model string
}

func newOpenAIClient(httpClient *http.Client, key, base, model string) *openaiClient {
	return &openaiClient{http: httpClient, key: key, base: strings.TrimRight(base, "/"), model: model}
}

// oaiMessage is a chat-completions message.
type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type oaiChatRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []oaiMessage `json:"messages"`
	Tools     []oaiTool    `json:"tools,omitempty"`
}

type oaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content   any           `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *apiError `json:"error"`
}

// toOAIMessages converts internal turns. Assistant tool_use blocks become
// tool_calls on the assistant message; tool_result blocks become
// role=tool messages. Unknown block types are dropped (same policy as
// the Anthropic path).
func toOAIMessages(system string, msgs []message) []oaiMessage {
	out := []oaiMessage{}
	if strings.TrimSpace(system) != "" {
		out = append(out, oaiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			var text strings.Builder
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					text.WriteString(b.Text)
				case "tool_result":
					s, _ := b.Content.(string)
					out = append(out, oaiMessage{Role: "tool", Content: s, ToolCallID: b.ToolUseID})
				}
			}
			if text.Len() > 0 {
				out = append(out, oaiMessage{Role: "user", Content: text.String()})
			}
		case "assistant":
			var text strings.Builder
			am := oaiMessage{Role: "assistant"}
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					text.WriteString(b.Text)
				case "tool_use":
					args, _ := json.Marshal(b.Input)
					tc := oaiToolCall{ID: b.ID, Type: "function"}
					tc.Function.Name = b.Name
					tc.Function.Arguments = string(args)
					am.ToolCalls = append(am.ToolCalls, tc)
				}
			}
			am.Content = text.String()
			out = append(out, am)
		}
	}
	return out
}

func toOAITools(defs []toolDef) []oaiTool {
	out := make([]oaiTool, 0, len(defs))
	for _, d := range defs {
		var t oaiTool
		t.Type = "function"
		t.Function.Name = d.Name
		t.Function.Description = d.Description
		t.Function.Parameters = d.InputSchema
		out = append(out, t)
	}
	return out
}

// complete sends one turn and maps the reply back to content blocks.
func (c *openaiClient) complete(system string, msgs []message, tools []toolDef) ([]contentBlock, error) {
	body, err := json.Marshal(oaiChatRequest{
		Model: c.model, MaxTokens: maxTokens,
		Messages: toOAIMessages(system, msgs), Tools: toOAITools(tools),
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %v", err)
	}
	req, err := http.NewRequest("POST", c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider request failed: %v (check connection, key, and base URL)", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read provider response: %v", err)
	}
	if resp.StatusCode != 200 {
		return nil, friendlyStatus(resp.StatusCode, raw)
	}
	var out oaiChatResponse
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("provider returned malformed JSON: %v", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("provider error (%s): %s", out.Error.Type, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("provider returned no choices")
	}
	msg := out.Choices[0].Message
	blocks := []contentBlock{}
	switch content := msg.Content.(type) {
	case string:
		if content != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: content})
		}
	case []any:
		// Content parts: concatenate text, ignore the rest.
		var sb strings.Builder
		for _, part := range content {
			if m, ok := part.(map[string]any); ok {
				if t, _ := m["text"].(string); t != "" {
					sb.WriteString(t)
				}
			}
		}
		if sb.Len() > 0 {
			blocks = append(blocks, contentBlock{Type: "text", Text: sb.String()})
		}
	case nil:
		// Text may legitimately be null when only tool calls are present.
	default:
		return nil, fmt.Errorf("provider returned unexpected content shape")
	}
	for _, tc := range msg.ToolCalls {
		input := map[string]any{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				return nil, fmt.Errorf("provider sent bad tool arguments for %q: %v", tc.Function.Name, err)
			}
		}
		blocks = append(blocks, contentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	if blocks == nil {
		return nil, fmt.Errorf("provider returned no content blocks")
	}
	return blocks, nil
}

// friendlyStatus maps HTTP failures to actionable errors (shared wording
// with the Anthropic path, minus Anthropic-specific guidance).
func friendlyStatus(status int, raw []byte) error {
	switch {
	case status == 401 || status == 403:
		return fmt.Errorf("provider rejected the API key (%d) — it is wrong or revoked; check the key for your provider", status)
	case status == 429:
		return fmt.Errorf("provider rate limit (%d) — wait a minute and retry", status)
	case status >= 500:
		return fmt.Errorf("provider is having trouble (%d) — wait and retry", status)
	default:
		return fmt.Errorf("provider error %d: %s", status, oneLine(raw))
	}
}
