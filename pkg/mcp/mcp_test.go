package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProtocolRequestRoundtrip(t *testing.T) {
	req := Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodInitialize, Params: InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "nimbus-one"},
	}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back Request
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Method != MethodInitialize || back.JSONRPC != JSONRPCVersion {
		t.Fatalf("roundtrip = %+v", back)
	}
}

func TestProtocolResponseToolDefRoundtrip(t *testing.T) {
	res := ToolsListResult{Tools: []ToolDef{{Name: "echo", Description: "echo it", InputSchema: map[string]any{"type": "object"}}}}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	resp := Response{JSONRPC: JSONRPCVersion, ID: 2, Result: raw}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var back Response
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	var list ToolsListResult
	if err := json.Unmarshal(back.Result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", list.Tools)
	}
}

func TestProtocolCallToolResultRoundtrip(t *testing.T) {
	r := CallToolResult{Content: []ContentBlock{{Type: "text", Text: "hi"}}}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back CallToolResult
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Content) != 1 || back.Content[0].Text != "hi" {
		t.Fatalf("back = %+v", back)
	}
}

func TestNewStdioEmptyBinary(t *testing.T) {
	if _, err := NewStdio(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty binary")
	}
	if _, err := NewStdio(context.Background(), "   "); err == nil {
		t.Fatal("expected error for blank binary")
	}
}

func TestNewStdioBadBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := NewStdio(ctx, "/nonexistent-binary-xyz-nimbus-one"); err == nil {
		t.Fatal("expected error for bad binary path")
	}
}

// TestClientAgainstFakeShellServer runs a full hermetic fake MCP server as a
// shell script that pre-emits canned responses with matching IDs.
func TestClientAgainstFakeShellServer(t *testing.T) {
	initResp := `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"1.0"}}}`
	listResp := `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"echo tool","inputSchema":{"type":"object"}}]}}`
	callResp := `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hello fake"}],"isError":false}}`
	script := "printf '%s\\n' '" + initResp + "' '" + listResp + "' '" + callResp + "'; cat >/dev/null"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := NewStdio(ctx, "sh", "-c", script)
	if err != nil {
		t.Fatalf("NewStdio against fake server: %v", err)
	}
	defer c.Close()

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
	if !strings.Contains(out, "hello fake") {
		t.Fatalf("CallTool out = %q", out)
	}
}
