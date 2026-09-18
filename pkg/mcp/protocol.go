// Package mcp implements a minimal MCP (Model Context Protocol) client
// speaking JSON-RPC 2.0 over stdio. Standard library only.
package mcp

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC version spoken on the wire.
const JSONRPCVersion = "2.0"

// Protocol version announced during initialize.
const ProtocolVersion = "2024-11-05"

// Method names.
const (
	MethodInitialize        = "initialize"
	MethodToolsList         = "tools/list"
	MethodToolsCall         = "tools/call"
	NotificationInitialized = "notifications/initialized"
)

// MethodInitialized is an alias kept for callers using the Method* prefix.
const MethodInitialized = NotificationInitialized

// Request is a JSON-RPC 2.0 request or notification (no ID).
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements error.
func (e *RPCError) Error() string {
	if e == nil {
		return "mcp: unknown error"
	}
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// ToolDef describes a single MCP tool.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolsListResult is the result of tools/list.
type ToolsListResult struct {
	Tools      []ToolDef `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// ContentBlock is a single content element of a tools/call result.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallToolResult is the result of tools/call.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ClientInfo identifies the connecting client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerInfo identifies the serving side.
type ServerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeParams is the payload of the initialize request.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

// InitializeResult is the payload of the initialize response.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ServerInfo      ServerInfo     `json:"serverInfo,omitempty"`
}
