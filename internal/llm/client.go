// Package llm implements an OpenAI-compatible HTTP client.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StatusError is a typed HTTP failure so callers (e.g. fallback Chain)
// can decide whether to try the next provider.
type StatusError struct {
	Code    int
	Body    string
	Headers http.Header
}

// Error implements error.
func (e StatusError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 500 {
		body = body[:500]
	}
	if body == "" {
		return fmt.Sprintf("llm request failed with status %d", e.Code)
	}
	return fmt.Sprintf("llm request failed with status %d: %s", e.Code, body)
}

// Client is an OpenAI-compatible chat-completions client.
type Client struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// Name implements Provider.
func (c *Client) Name() string { return "openai-compatible" }

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) baseURL() string {
	b := strings.TrimSpace(c.BaseURL)
	if b == "" {
		b = "https://api.openai.com/v1"
	}
	return strings.TrimSuffix(b, "/")
}

func (c *Client) modelFor(req ChatRequest) string {
	if strings.TrimSpace(req.Model) != "" {
		return req.Model
	}
	if strings.TrimSpace(c.Model) != "" {
		return c.Model
	}
	return "gpt-4o-mini"
}

// openAI wire types.

type oaFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type oaToolCall struct {
	ID       string     `json:"id,omitempty"`
	Type     string     `json:"type,omitempty"`
	Function oaFunction `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	Name       string       `json:"name,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	} `json:"function"`
}

type oaRequest struct {
	Model       string      `json:"model"`
	Messages    []oaMessage `json:"messages"`
	Tools       []oaTool    `json:"tools,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Stream      bool        `json:"stream"`
}

type oaStreamChoiceDelta struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int        `json:"index"`
		ID       string     `json:"id,omitempty"`
		Type     string     `json:"type,omitempty"`
		Function oaFunction `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type oaStreamChunk struct {
	Choices []struct {
		Delta        oaStreamChoiceDelta `json:"delta"`
		FinishReason string              `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

type oaNonStreamResp struct {
	Choices []struct {
		Message struct {
			Role      string       `json:"role"`
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func toOAMessages(msgs []Message) []oaMessage {
	out := make([]oaMessage, 0, len(msgs))
	for _, m := range msgs {
		om := oaMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if om.Role == "" {
			om.Role = RoleUser
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: oaFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		out = append(out, om)
	}
	return out
}

func toOATools(defs []ToolDef) []oaTool {
	out := make([]oaTool, 0, len(defs))
	for _, d := range defs {
		t := oaTool{Type: "function"}
		t.Function.Name = d.Name
		t.Function.Description = d.Description
		t.Function.Parameters = d.Parameters
		if t.Function.Parameters == nil {
			t.Function.Parameters = map[string]any{"type": "object"}
		}
		out = append(out, t)
	}
	return out
}

func doPost(ctx context.Context, hc *http.Client, url, apiKey string, body any) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func readStatusError(resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return &StatusError{Code: resp.StatusCode, Body: string(b), Headers: resp.Header.Clone()}
}

// Chat implements Provider by issuing a stream=true request and parsing SSE.
// If the server answers with a plain JSON (non-stream) body, it falls back
// to decoding that shape.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oreq := oaRequest{
		Model:       c.modelFor(req),
		Messages:    toOAMessages(req.Messages),
		Tools:       toOATools(req.Tools),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
	}
	resp, err := doPost(ctx, c.httpClient(), c.baseURL()+"/chat/completions", c.APIKey, oreq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readStatusError(resp)
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		// Non-stream fallback: server ignored stream=true.
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
		if err != nil {
			return nil, fmt.Errorf("llm: read non-stream body: %w", err)
		}
		var ns oaNonStreamResp
		if err := json.Unmarshal(b, &ns); err != nil {
			return nil, fmt.Errorf("llm: decode non-stream body: %w", err)
		}
		ch := make(chan Chunk, 4)
		go func() {
			defer close(ch)
			for _, choice := range ns.Choices {
				var tcs []ToolCall
				for _, tc := range choice.Message.ToolCalls {
					tcs = append(tcs, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
				}
				if choice.Message.Content != "" {
					ch <- Chunk{Delta: choice.Message.Content, RawTokens: len(choice.Message.Content) / 4}
				}
				_ = tcs
			}
			var all []ToolCall
			for _, choice := range ns.Choices {
				for _, tc := range choice.Message.ToolCalls {
					all = append(all, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
				}
			}
			ch <- Chunk{ToolCalls: all, Done: true}
		}()
		return ch, nil
	}

	ch := make(chan Chunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		type acc struct {
			id   string
			name string
			args strings.Builder
		}
		accs := map[int]*acc{}
		emit := func(ck Chunk) bool {
			select {
			case <-ctx.Done():
				ch <- Chunk{Err: ctx.Err()}
				return false
			case ch <- ck:
				return true
			}
		}
		for sc.Scan() {
			if ctx.Err() != nil {
				emit(Chunk{Err: ctx.Err()})
				return
			}
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue // SSE comment / keepalive
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			var evt oaStreamChunk
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue // skip malformed heartbeat lines
			}
			for _, choice := range evt.Choices {
				if choice.Delta.Content != "" {
					if !emit(Chunk{Delta: choice.Delta.Content, RawTokens: len(choice.Delta.Content) / 4}) {
						return
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					a, ok := accs[tc.Index]
					if !ok {
						a = &acc{}
						accs[tc.Index] = a
					}
					if tc.ID != "" {
						a.id = tc.ID
					}
					if tc.Function.Name != "" {
						a.name = tc.Function.Name
					}
					a.args.WriteString(tc.Function.Arguments)
				}
			}
		}
		if err := sc.Err(); err != nil {
			if ctx.Err() != nil {
				emit(Chunk{Err: ctx.Err()})
				return
			}
			emit(Chunk{Err: fmt.Errorf("llm: scan stream: %w", err)})
			return
		}
		// Assemble tool calls in index order.
		maxIdx := -1
		for i := range accs {
			if i > maxIdx {
				maxIdx = i
			}
		}
		var tcs []ToolCall
		for i := 0; i <= maxIdx; i++ {
			if a, ok := accs[i]; ok && (a.id != "" || a.name != "" || a.args.Len() > 0) {
				tcs = append(tcs, ToolCall{ID: a.id, Name: a.name, Arguments: a.args.String()})
			}
		}
		emit(Chunk{ToolCalls: tcs, Done: true})
	}()
	return ch, nil
}

// Complete implements Provider by collecting the Chat stream.
func (c *Client) Complete(ctx context.Context, req ChatRequest) (string, []ToolCall, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	ch, err := c.Chat(ctx, req)
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
		if ck.Done && len(ck.ToolCalls) > 0 {
			tcs = ck.ToolCalls
		} else if len(ck.ToolCalls) > 0 && ck.Delta == "" && !ck.Done {
			tcs = append(tcs, ck.ToolCalls...)
		}
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
	}
	return sb.String(), tcs, nil
}
