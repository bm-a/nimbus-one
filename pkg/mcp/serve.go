// MCP stdio server: expose local handlers to any MCP client.
//
// OpenClaw reference (read-only):
//
//	plugin-tools-serve.ts (serve registry over MCP). This package cannot
//	import internal/tools (import direction: tools may use llm, mcp imports
//	nothing internal), so it defines its own minimal ToolHandler surface and
//	speaks the same JSON-RPC wire as client.go: initialize → tools/list →
//	tools/call over stdin/stdout (line-delimited JSON with tolerant
//	Content-Length framing).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ServeCallTimeout bounds a single handler invocation.
const ServeCallTimeout = 120 * time.Second

// ToolHandler is a single servable tool. It mirrors the internal Tool
// contract without importing it (pkg/mcp must stay dependency-free).
type ToolHandler interface {
	Name() string
	Description() string
	Schema() map[string]any
	Call(ctx context.Context, args map[string]any) (string, error)
}

// HandlerFunc adapts a plain function to a ToolHandler.
type HandlerFunc struct {
	ToolName string
	ToolDesc string
	Input    map[string]any
	Fn       func(ctx context.Context, args map[string]any) (string, error)
}

// Name returns the tool name.
func (h HandlerFunc) Name() string { return h.ToolName }

// Description returns the tool description.
func (h HandlerFunc) Description() string { return h.ToolDesc }

// Schema returns the JSON input schema.
func (h HandlerFunc) Schema() map[string]any { return h.Input }

// Call invokes the wrapped function.
func (h HandlerFunc) Call(ctx context.Context, args map[string]any) (string, error) {
	return h.Fn(ctx, args)
}

// ServeStdio serves handlers on r/w until EOF or ctx cancellation.
// It understands both plain JSON lines and Content-Length framed bodies.
func ServeStdio(ctx context.Context, handlers []ToolHandler, r io.Reader, w io.Writer) error {
	s := &stdioServer{handlers: handlers, out: w}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return fmt.Errorf("mcp serve: stdin: %w", err)
			}
			return nil // EOF: client went away
		}
		line := sc.Text()
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		var raw string
		if strings.HasPrefix(strings.ToLower(t), "content-length:") {
			// Consume header block, then accumulate body lines until JSON.
			for {
				if !sc.Scan() {
					return nil
				}
				if strings.TrimSpace(sc.Text()) == "" {
					break
				}
			}
			var sb strings.Builder
			for sc.Scan() {
				sb.WriteString(sc.Text())
				sb.WriteString("\n")
				if sb.Len() > maxMessageBytes {
					return fmt.Errorf("mcp serve: oversized framed message")
				}
				cand := sb.String()
				var js json.RawMessage
				if json.Unmarshal([]byte(cand), &js) == nil {
					break
				}
			}
			raw = sb.String()
		} else {
			raw = line
		}
		var req Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			continue // skip log noise, like client.go does
		}
		s.handle(ctx, req)
	}
}

type stdioServer struct {
	handlers []ToolHandler
	out      io.Writer
}

func (s *stdioServer) reply(id any, result any, rpcErr *RPCError) {
	resp := Response{JSONRPC: JSONRPCVersion, ID: id, Error: rpcErr}
	if rpcErr == nil {
		raw, err := json.Marshal(result)
		if err != nil {
			resp.Error = &RPCError{Code: -32603, Message: fmt.Sprintf("encode result: %v", err)}
		} else {
			resp.Result = raw
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = s.out.Write(data)
}

func (s *stdioServer) find(name string) ToolHandler {
	for _, h := range s.handlers {
		if h.Name() == name {
			return h
		}
	}
	return nil
}

func (s *stdioServer) handle(ctx context.Context, req Request) {
	switch req.Method {
	case MethodInitialize:
		s.reply(req.ID, InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      ServerInfo{Name: "nimbus-one", Version: "1.0"},
		}, nil)
	case NotificationInitialized, "notifications/cancelled":
		// Notification: no response.
	case MethodToolsList:
		defs := make([]ToolDef, 0, len(s.handlers))
		for _, h := range s.handlers {
			schema := h.Schema()
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			defs = append(defs, ToolDef{Name: h.Name(), Description: h.Description(), InputSchema: schema})
		}
		s.reply(req.ID, ToolsListResult{Tools: defs}, nil)
	case MethodToolsCall:
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if raw, err := json.Marshal(req.Params); err == nil {
			_ = json.Unmarshal(raw, &params)
		}
		h := s.find(params.Name)
		if h == nil {
			s.reply(req.ID, nil, &RPCError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", params.Name)})
			return
		}
		if params.Arguments == nil {
			params.Arguments = map[string]any{}
		}
		cctx, cancel := context.WithTimeout(ctx, ServeCallTimeout)
		defer cancel()
		out, err := h.Call(cctx, params.Arguments)
		res := CallToolResult{}
		if err != nil {
			res.IsError = true
			res.Content = []ContentBlock{{Type: "text", Text: err.Error()}}
			if out != "" {
				res.Content = append(res.Content, ContentBlock{Type: "text", Text: out})
			}
		} else {
			res.Content = []ContentBlock{{Type: "text", Text: out}}
		}
		s.reply(req.ID, res, nil)
	default:
		if req.ID == nil {
			return // unknown notification: stay silent
		}
		s.reply(req.ID, nil, &RPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)})
	}
}

// ServeStdioDefault serves on os.Stdin/os.Stdout (production entrypoint).
func ServeStdioDefault(handlers []ToolHandler) error {
	return ServeStdio(context.Background(), handlers, os.Stdin, os.Stdout)
}
