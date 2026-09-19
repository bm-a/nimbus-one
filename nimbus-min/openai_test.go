package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOpenAI serves scripted chat-completions responses.
type fakeOpenAI struct {
	t     *testing.T
	resps []string // raw JSON bodies
	calls int
	last  oaiChatRequest
}

func (f *fakeOpenAI) handler(w http.ResponseWriter, r *http.Request) {
	f.calls++
	if r.URL.Path != "/chat/completions" {
		http.Error(w, "bad path", 404)
		return
	}
	var req oaiChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	f.last = req
	i := f.calls - 1
	if i >= len(f.resps) {
		i = len(f.resps) - 1
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(f.resps[i]))
}

func oaiTextResp(text string) string {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}}}})
	return string(raw)
}

func oaiToolResp(id, name, args string) string {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"role":    "assistant",
		"content": nil,
		"tool_calls": []any{map[string]any{"id": id, "type": "function",
			"function": map[string]any{"name": name, "arguments": args}}},
	}}}})
	return string(raw)
}

func testOAIClient(t *testing.T, srv *httptest.Server, model string) *openaiClient {
	t.Helper()
	return newOpenAIClient(srv.Client(), "k", srv.URL, model)
}

func TestOAIMessageMapping(t *testing.T) {
	msgs := []message{
		userMessage("hi"),
		{Role: "assistant", Content: []contentBlock{
			{Type: "text", Text: "checking"},
			{Type: "tool_use", ID: "c1", Name: "read", Input: map[string]any{"path": "a.txt"}},
		}},
		{Role: "user", Content: []contentBlock{
			{Type: "tool_result", ToolUseID: "c1", Content: "file!"},
		}},
	}
	got := toOAIMessages("sys", msgs)
	if len(got) != 4 {
		t.Fatalf("mapped %d messages, want 4 (system+user+assistant+tool)", len(got))
	}
	if got[0].Role != "system" || got[1].Content != "hi" {
		t.Fatalf("system/user wrong: %+v", got[:2])
	}
	if len(got[2].ToolCalls) != 1 || got[2].ToolCalls[0].Function.Name != "read" {
		t.Fatalf("assistant tool_calls wrong: %+v", got[2])
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "c1" {
		t.Fatalf("tool result wrong: %+v", got[3])
	}
}

func TestOAIToolDefs(t *testing.T) {
	defs := toOAITools(agentTools())
	if len(defs) != 5 {
		t.Fatalf("defs = %d, want 5", len(defs))
	}
	for _, d := range defs {
		if d.Type != "function" || d.Function.Name == "" || d.Function.Parameters["type"] != "object" {
			t.Fatalf("bad def: %+v", d)
		}
	}
}

func TestOAICompleteRoundTrip(t *testing.T) {
	fake := &fakeOpenAI{t: t, resps: []string{
		oaiToolResp("c1", "read", `{"path":"n.txt"}`),
		oaiTextResp("The note says hi."),
	}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	out, err := runAgent(testOAIClient(t, srv, "m"), strings.NewReader(""), &strings.Builder{}, false, "read n?", 5)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "The note says hi." {
		t.Fatalf("answer = %q", out)
	}
	if fake.last.Model != "m" {
		t.Fatalf("model not sent: %+v", fake.last)
	}
	if len(fake.last.Tools) != 5 {
		t.Fatalf("tools not sent: %d", len(fake.last.Tools))
	}
}

func TestOAIErrorShapes(t *testing.T) {
	// Empty choices.
	fake := &fakeOpenAI{t: t, resps: []string{`{"choices":[]}`}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	if _, err := testOAIClient(t, srv, "m").complete("s", []message{userMessage("x")}, nil); err == nil {
		t.Fatal("empty choices must error")
	}
	// Bad tool arguments JSON.
	fake2 := &fakeOpenAI{t: t, resps: []string{oaiToolResp("c1", "read", `{bad`)}}
	srv2 := httptest.NewServer(http.HandlerFunc(fake2.handler))
	defer srv2.Close()
	if _, err := testOAIClient(t, srv2, "m").complete("s", []message{userMessage("x")}, nil); err == nil {
		t.Fatal("bad tool args must error")
	}
	// Status mapping.
	for status, want := range map[int]string{401: "API key", 429: "rate limit", 500: "having trouble", 418: "provider error 418"} {
		srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{}`, status)
		}))
		_, err := testOAIClient(t, srv3, "m").complete("s", []message{userMessage("x")}, nil)
		srv3.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("status %d must mention %q, got %v", status, want, err)
		}
	}
}

func TestOAIArrayContent(t *testing.T) {
	raw := `{"choices":[{"message":{"role":"assistant","content":[{"type":"text","text":"hel"},{"type":"text","text":"lo"}]}}]}`
	fake := &fakeOpenAI{t: t, resps: []string{raw}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	blocks, err := testOAIClient(t, srv, "m").complete("s", []message{userMessage("x")}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Text != "hello" {
		t.Fatalf("array content = %+v", blocks)
	}
}
