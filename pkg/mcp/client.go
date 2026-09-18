package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handshakeTimeout bounds the initialize handshake when ctx has no deadline.
const handshakeTimeout = 30 * time.Second

// maxMessageBytes guards Content-Length body accumulation.
const maxMessageBytes = 10 * 1024 * 1024

// Client is a JSON-RPC 2.0 client over a child process's stdio.
// The wire format is line-delimited JSON with tolerant Content-Length
// (LSP-style header framing) fallback.
type Client struct {
	Cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
	nextID int
}

// NewStdio spawns bin with args and performs the initialize handshake
// followed by the notifications/initialized notification.
func NewStdio(ctx context.Context, bin string, args ...string) (*Client, error) {
	if strings.TrimSpace(bin) == "" {
		return nil, fmt.Errorf("mcp: empty binary path")
	}
	cmd := exec.Command(bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp: start %s: %w", bin, err)
	}
	// Drain stderr so a chatty server can never block on a full pipe.
	go func() {
		_, _ = io.Copy(io.Discard, stderrPipe)
	}()

	sc := bufio.NewScanner(stdoutPipe)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	c := &Client{Cmd: cmd, stdin: stdin, stdout: sc, nextID: 1}

	hctx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		hctx, cancel = context.WithTimeout(ctx, handshakeTimeout)
		defer cancel()
	}
	initParams := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "nimbus-one", Version: "1.0"},
	}
	var raw json.RawMessage
	if err := c.call(hctx, MethodInitialize, initParams, &raw); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}
	if err := c.notify(NotificationInitialized, map[string]any{}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialized notification: %w", err)
	}
	return c, nil
}

// ListTools returns the server's tool definitions (following pagination).
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	var all []ToolDef
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res ToolsListResult
		if err := c.call(ctx, MethodToolsList, params, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if all == nil {
		return []ToolDef{}, nil
	}
	return all, nil
}

// CallTool invokes a tool and returns the concatenated text content.
// A server-side tool failure (isError) is returned as a Go error carrying
// the text output.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("mcp: empty tool name")
	}
	if args == nil {
		args = map[string]any{}
	}
	params := map[string]any{"name": name, "arguments": args}
	var res CallToolResult
	if err := c.call(ctx, MethodToolsCall, params, &res); err != nil {
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
		return out, fmt.Errorf("mcp: tool %q failed: %s", name, out)
	}
	return out, nil
}

// Close kills the child process and releases pipes.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var firstErr error
	if c.stdin != nil {
		if err := c.stdin.Close(); err != nil {
			firstErr = err
		}
	}
	if c.Cmd != nil && c.Cmd.Process != nil {
		_ = c.Cmd.Process.Kill()
		_ = c.Cmd.Wait()
	}
	return firstErr
}

// call sends one request and blocks for the matching response ID.
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.nextID
	c.nextID++
	req := Request{JSONRPC: JSONRPCVersion, ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("mcp: marshal %s: %w", method, err)
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("mcp: write %s: %w", method, err)
	}
	for {
		line, err := c.readMessage(ctx)
		if err != nil {
			return err
		}
		var resp Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue // skip server log lines / non-JSON noise
		}
		if resp.JSONRPC != "" && resp.JSONRPC != JSONRPCVersion {
			continue
		}
		if !matchID(resp.ID, id) {
			continue // notification or unrelated response
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("mcp: decode %s result: %w", method, err)
			}
		}
		return nil
	}
}

// notify sends a fire-and-forget notification (no ID, no response).
func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	req := Request{JSONRPC: JSONRPCVersion, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("mcp: marshal %s: %w", method, err)
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("mcp: write %s: %w", method, err)
	}
	return nil
}

// readMessage returns the next wire message: a plain JSON line, or — when a
// Content-Length header block is seen — the framed body (accumulated until it
// parses as JSON so multi-line bodies work).
func (c *Client) readMessage(ctx context.Context) (string, error) {
	for {
		line, err := c.nextLine(ctx)
		if err != nil {
			return "", err
		}
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(t), "content-length:") {
			// Consume remaining headers until the blank line.
			for {
				h, err := c.nextLine(ctx)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(h) == "" {
					break
				}
			}
			var sb strings.Builder
			for {
				b, err := c.nextLine(ctx)
				if err != nil {
					return "", err
				}
				sb.WriteString(b)
				sb.WriteString("\n")
				if sb.Len() > maxMessageBytes {
					return "", fmt.Errorf("mcp: oversized framed message")
				}
				s := sb.String()
				var js json.RawMessage
				if json.Unmarshal([]byte(s), &js) == nil &&
					(strings.Contains(s, `"jsonrpc"`) || strings.Contains(s, `"method"`)) {
					return s, nil
				}
			}
		}
		return line, nil
	}
}

func (c *Client) nextLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !c.stdout.Scan() {
		if err := c.stdout.Err(); err != nil {
			return "", fmt.Errorf("mcp: stdout read: %w", err)
		}
		return "", fmt.Errorf("mcp: server closed stdout (EOF)")
	}
	return c.stdout.Text(), nil
}

// matchID reports whether a decoded response ID equals the request ID.
// JSON numbers decode to float64; servers may also echo strings.
func matchID(got any, want int) bool {
	switch v := got.(type) {
	case nil:
		return false
	case float64:
		return int(v) == want
	case float32:
		return int(v) == want
	case int:
		return v == want
	case int64:
		return int(v) == want
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n == want
		}
		return false
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n) == want
		}
		return false
	default:
		return false
	}
}
