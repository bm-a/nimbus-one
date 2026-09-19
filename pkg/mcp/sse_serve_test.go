package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ioPipe returns an *os.File pipe pair (ServeStdio works on raw streams).
func ioPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	return r, w
}

var _ = io.Discard // keep io import if unused in future edits

// fakeSSEServer implements the discovery + message flow: GET /sse emits an
// endpoint event, POST /message echoes JSON-RPC responses inline.
func fakeSSEServer(t *testing.T, viaStream bool) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var streamW http.ResponseWriter
	var streamFlush http.Flusher
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprintf(w, "event: endpoint\ndata: /message?sessionId=abc\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if !viaStream {
			// Keep the stream open until the test ends.
			<-r.Context().Done()
			return
		}
		mu.Lock()
		streamW = w
		if f, ok := w.(http.Flusher); ok {
			streamFlush = f
		}
		mu.Unlock()
		<-r.Context().Done()
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		var result any
		switch req.Method {
		case MethodInitialize:
			result = InitializeResult{ProtocolVersion: ProtocolVersion, ServerInfo: ServerInfo{Name: "fake-sse"}}
		case MethodToolsList:
			result = ToolsListResult{Tools: []ToolDef{{Name: "echo", Description: "echo tool"}}}
		case MethodToolsCall:
			result = CallToolResult{Content: []ContentBlock{{Type: "text", Text: "sse-hello"}}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		resp := Response{JSONRPC: JSONRPCVersion, ID: req.ID, Result: raw}
		if viaStream {
			// 202 empty: deliver the response as a later `message` event.
			w.WriteHeader(http.StatusAccepted)
			data, _ := json.Marshal(resp)
			go func() {
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				defer mu.Unlock()
				if streamW != nil {
					fmt.Fprintf(streamW, "event: message\ndata: %s\n\n", data)
					streamFlush.Flush()
				}
			}()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func TestSSEClientInlineResponses(t *testing.T) {
	srv := fakeSSEServer(t, false)
	defer srv.Close()
	c := &SSEClient{BaseURL: srv.URL + "/sse", Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	defer c.Close()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	out, err := c.CallTool(ctx, "echo", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(out, "sse-hello") {
		t.Fatalf("out = %q", out)
	}
}

func TestSSEClientStreamCorrelatedResponses(t *testing.T) {
	srv := fakeSSEServer(t, true)
	defer srv.Close()
	c := &SSEClient{BaseURL: srv.URL + "/sse", Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	defer c.Close()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	out, err := c.CallTool(ctx, "echo", nil)
	if err != nil {
		t.Fatalf("CallTool via stream: %v", err)
	}
	if !strings.Contains(out, "sse-hello") {
		t.Fatalf("out = %q", out)
	}
}

func TestSSEClientEmptyBaseURL(t *testing.T) {
	c := &SSEClient{}
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestSSEClientNoEndpointEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, ": heartbeat\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()
	c := &SSEClient{BaseURL: srv.URL, Timeout: 2 * time.Second}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err == nil {
		t.Fatal("expected error when no endpoint event arrives")
	}
}

func TestServeStdioRoundtrip(t *testing.T) {
	echo := HandlerFunc{
		ToolName: "echo",
		ToolDesc: "echo back input",
		Input:    map[string]any{"type": "object"},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			if v, _ := args["fail"].(bool); v {
				return "partial", fmt.Errorf("boom")
			}
			return fmt.Sprint(args["text"]), nil
		},
	}
	pr, pw := ioPipe(t)
	defer pr.Close()
	outR, outW := ioPipe(t)
	defer outR.Close()
	done := make(chan error, 1)
	go func() {
		done <- ServeStdio(context.Background(), []ToolHandler{echo}, pr, outW)
	}()

	send := func(v any) {
		data, _ := json.Marshal(v)
		data = append(data, '\n')
		if _, err := pw.Write(data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	responses := map[any]Response{}
	respDone := make(chan struct{})
	go func() {
		defer close(respDone)
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var resp Response
			if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
				continue
			}
			responses[keyOf(resp.ID)] = resp
			if len(responses) >= 4 {
				return
			}
		}
	}()

	send(Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodInitialize, Params: InitializeParams{ProtocolVersion: ProtocolVersion}})
	send(map[string]any{"jsonrpc": "2.0", "method": NotificationInitialized, "params": map[string]any{}})
	send(Request{JSONRPC: JSONRPCVersion, ID: 2, Method: MethodToolsList, Params: map[string]any{}})
	send(Request{JSONRPC: JSONRPCVersion, ID: 3, Method: MethodToolsCall,
		Params: map[string]any{"name": "echo", "arguments": map[string]any{"text": "hi-serve"}}})
	send(Request{JSONRPC: JSONRPCVersion, ID: 4, Method: MethodToolsCall,
		Params: map[string]any{"name": "echo", "arguments": map[string]any{"fail": true}}})

	select {
	case <-respDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for serve responses")
	}
	_ = pw.Close()
	_ = outW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeStdio: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeStdio did not exit on EOF")
	}

	var initRes InitializeResult
	if err := json.Unmarshal(responses[1].Result, &initRes); err != nil || initRes.ProtocolVersion != ProtocolVersion {
		t.Fatalf("init = %+v err=%v", initRes, err)
	}
	var listRes ToolsListResult
	if err := json.Unmarshal(responses[2].Result, &listRes); err != nil || len(listRes.Tools) != 1 || listRes.Tools[0].Name != "echo" {
		t.Fatalf("list = %+v err=%v", listRes, err)
	}
	var callRes CallToolResult
	if err := json.Unmarshal(responses[3].Result, &callRes); err != nil || callRes.IsError {
		t.Fatalf("call = %+v err=%v", callRes, err)
	}
	if len(callRes.Content) == 0 || callRes.Content[0].Text != "hi-serve" {
		t.Fatalf("call content = %+v", callRes.Content)
	}
	var failRes CallToolResult
	if err := json.Unmarshal(responses[4].Result, &failRes); err != nil || !failRes.IsError {
		t.Fatalf("fail call must set isError: %+v err=%v", failRes, err)
	}
}

func TestServeStdioUnknownToolAndMethod(t *testing.T) {
	pr, pw := ioPipe(t)
	defer pr.Close()
	outR, outW := ioPipe(t)
	defer outR.Close()
	done := make(chan error, 1)
	go func() {
		done <- ServeStdio(context.Background(), nil, pr, outW)
	}()
	send := func(v any) {
		data, _ := json.Marshal(v)
		data = append(data, '\n')
		_, _ = pw.Write(data)
	}
	send(Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodToolsCall,
		Params: map[string]any{"name": "nope", "arguments": map[string]any{}}})
	send(Request{JSONRPC: JSONRPCVersion, ID: 2, Method: "bogus/method"})
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	got := map[int]Response{}
	timeout := time.After(10 * time.Second)
	for len(got) < 2 {
		select {
		case <-timeout:
			t.Fatalf("responses = %v", got)
		default:
			if sc.Scan() {
				var resp Response
				if err := json.Unmarshal(sc.Bytes(), &resp); err == nil && resp.ID != nil {
					got[int(keyOf(resp.ID).(int))] = resp
				}
			}
		}
	}
	if got[1].Error == nil {
		t.Fatal("unknown tool must return rpc error")
	}
	if got[2].Error == nil {
		t.Fatal("unknown method must return rpc error")
	}
	_ = pw.Close()
	_ = outW.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ServeStdio did not exit on EOF")
	}
}
