// SSE (+streamable) MCP transport over stdlib HTTP.
//
// OpenClaw reference (read-only):
//
//	mcp-stdio-transport.ts + mcp-http-transport.ts (SSE/streamable, OAuth).
//	Nimbus port: no OAuth, no external deps — GET the event stream, learn the
//	message endpoint from the `endpoint` event, POST JSON-RPC requests, and
//	correlate responses by id (direct POST body or later `message` events).
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"
)

// SSEHandshakeTimeout bounds endpoint discovery when ctx has no deadline.
const SSEHandshakeTimeout = 30 * time.Second

// SSECallTimeout bounds a single tools/call when ctx has no deadline.
const SSECallTimeout = 60 * time.Second

// SSEClient speaks MCP JSON-RPC over the HTTP+SSE transport.
type SSEClient struct {
	// BaseURL is the SSE endpoint (GET) used for endpoint discovery.
	BaseURL string
	// Headers are sent on both the event stream and message POSTs.
	Headers map[string]string
	// Timeout applies per HTTP round trip when > 0.
	Timeout time.Duration

	client   *http.Client
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	nextID   int
	endpoint string
	pending  map[any]chan Response
	ready    chan struct{}
	readyErr error
}

// Connect opens the event stream and waits for the endpoint event.
func (c *SSEClient) Connect(ctx context.Context) error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("mcp sse: empty base URL")
	}
	jar, _ := cookiejar.New(nil)
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = SSECallTimeout
	}
	c.client = &http.Client{Timeout: 0, Jar: jar} // streaming: no client timeout
	c.pending = map[any]chan Response{}
	c.ready = make(chan struct{})
	c.nextID = 1

	hctx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		hctx, cancel = context.WithTimeout(ctx, SSEHandshakeTimeout)
		defer cancel()
	}
	_ = timeout
	sctx, scancel := context.WithCancel(context.Background())
	c.cancel = scancel
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, c.BaseURL, nil)
	if err != nil {
		scancel()
		return fmt.Errorf("mcp sse: stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		scancel()
		return fmt.Errorf("mcp sse: dial: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		scancel()
		return fmt.Errorf("mcp sse: HTTP %s", resp.Status)
	}
	c.wg.Add(1)
	go c.pump(resp.Body)

	select {
	case <-c.ready:
		if c.readyErr != nil {
			_ = resp.Body.Close()
			return c.readyErr
		}
		// Run the stdio-style initialize handshake over the new transport
		// so servers see a conforming client before tools/list.
		initParams := InitializeParams{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{},
			ClientInfo:      ClientInfo{Name: "nimbus-one", Version: "1.0"},
		}
		var raw json.RawMessage
		if err := c.Call(ctx, MethodInitialize, initParams, &raw); err != nil {
			c.Close()
			return fmt.Errorf("mcp sse: initialize: %w", err)
		}
		return nil
	case <-hctx.Done():
		scancel()
		return fmt.Errorf("mcp sse: no endpoint event: %w", hctx.Err())
	}
}

// pump reads SSE events until the body closes.
func (c *SSEClient) pump(body io.ReadCloser) {
	defer c.wg.Done()
	defer body.Close()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var event, data string
	flush := func() {
		defer func() { event, data = "", "" }()
		switch strings.TrimSpace(event) {
		case "endpoint":
			c.mu.Lock()
			if c.endpoint == "" {
				c.endpoint = strings.TrimSpace(data)
				c.readyErr = nil
				close(c.ready)
			}
			c.mu.Unlock()
		case "", "message":
			var resp Response
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				return
			}
			if resp.ID == nil {
				return // notification, nothing waits on it
			}
			c.mu.Lock()
			ch := c.pending[keyOf(resp.ID)]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- resp:
				default:
				}
			}
		}
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment / heartbeat
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			chunk := strings.TrimPrefix(line, "data:")
			if len(chunk) > 0 && chunk[0] == ' ' {
				chunk = chunk[1:]
			}
			if data != "" {
				data += "\n"
			}
			data += chunk
		}
	}
	flush()
	c.mu.Lock()
	if c.endpoint == "" {
		c.readyErr = fmt.Errorf("mcp sse: stream closed before endpoint event")
		select {
		case <-c.ready:
		default:
			close(c.ready)
		}
	}
	for _, ch := range c.pending {
		select {
		case ch <- Response{Error: &RPCError{Code: -32000, Message: "mcp sse: stream closed"}}:
		default:
		}
	}
	c.mu.Unlock()
}

// messageURL resolves the discovered endpoint against the base URL.
func (c *SSEClient) messageURL() (string, error) {
	c.mu.Lock()
	ep := c.endpoint
	c.mu.Unlock()
	if ep == "" {
		return "", fmt.Errorf("mcp sse: not connected (no endpoint)")
	}
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return ep, nil
	}
	base := strings.TrimSuffix(c.BaseURL, "/")
	if strings.HasPrefix(ep, "/") {
		// Same-origin absolute path: keep scheme+host of base.
		i := strings.Index(base, "://")
		if i < 0 {
			return "", fmt.Errorf("mcp sse: bad base URL %q", c.BaseURL)
		}
		rest := base[i+3:]
		host := rest
		if j := strings.Index(rest, "/"); j >= 0 {
			host = rest[:j]
		}
		return base[:i+3] + host + ep, nil
	}
	return base + "/" + ep, nil
}

// Call sends one JSON-RPC request and waits for the correlated response.
func (c *SSEClient) Call(ctx context.Context, method string, params any, out any) error {
	msgURL, err := c.messageURL()
	if err != nil {
		return err
	}
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()
	reqObj := Request{JSONRPC: JSONRPCVersion, ID: id, Method: method, Params: params}
	payload, err := json.Marshal(reqObj)
	if err != nil {
		return fmt.Errorf("mcp sse: marshal %s: %w", method, err)
	}
	ch := make(chan Response, 1)
	c.mu.Lock()
	c.pending[keyOf(id)] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, keyOf(id))
		c.mu.Unlock()
	}()

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = SSECallTimeout
	}
	pctx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		pctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(pctx, http.MethodPost, msgURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mcp sse: post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: post %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	if err != nil {
		return fmt.Errorf("mcp sse: read %s: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("mcp sse: post %s: HTTP %s", method, resp.Status)
	}
	// Some servers answer inline (202 empty = response arrives via SSE).
	if len(bytes.TrimSpace(body)) > 0 {
		var direct Response
		if err := json.Unmarshal(body, &direct); err == nil && direct.ID != nil && matchID(direct.ID, id) {
			return finishCall(method, direct, out)
		}
	}
	select {
	case r := <-ch:
		return finishCall(method, r, out)
	case <-pctx.Done():
		return fmt.Errorf("mcp sse: call %s: %w", method, pctx.Err())
	}
}

func finishCall(method string, resp Response, out any) error {
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("mcp sse: decode %s result: %w", method, err)
		}
	}
	return nil
}

// ListTools returns the server's tool definitions.
func (c *SSEClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	var res ToolsListResult
	if err := c.Call(ctx, MethodToolsList, map[string]any{}, &res); err != nil {
		return nil, err
	}
	if res.Tools == nil {
		return []ToolDef{}, nil
	}
	return res.Tools, nil
}

// CallTool invokes a tool and returns concatenated text content.
func (c *SSEClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("mcp sse: empty tool name")
	}
	if args == nil {
		args = map[string]any{}
	}
	var res CallToolResult
	if err := c.Call(ctx, MethodToolsCall, map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return "", err
	}
	var parts []string
	for _, b := range res.Content {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	out := strings.Join(parts, "\n")
	if res.IsError {
		return out, fmt.Errorf("mcp sse: tool %q failed: %s", name, out)
	}
	return out, nil
}

// Close shuts down the event stream.
func (c *SSEClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

// keyOf normalizes response/request ids for map correlation.
func keyOf(id any) any {
	switch v := id.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
