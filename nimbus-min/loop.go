package main

import (
	"fmt"
	"io"
	"strings"
)

// Agent tool definitions (the exact surface the model sees).
func agentTools() []toolDef {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	return []toolDef{
		{
			Name:        "read",
			Description: "Read a file inside the workspace. Use paths like notes/todo.md (\".\" is not a file).",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": str()}, "required": []string{"path"}},
		},
		{
			Name:        "write",
			Description: "Create or overwrite a file inside the workspace. Parent folders are created.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": str(), "content": str()}, "required": []string{"path", "content"}},
		},
		{
			Name:        "edit",
			Description: "Change a file with one search/replace. search must occur EXACTLY once in the file; mismatches return a snippet so you can retry with more context.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": str(), "search": str(), "replace": str()}, "required": []string{"path", "search", "replace"}},
		},
		{
			Name:        "list",
			Description: "List a folder inside the workspace. Use \".\" for the workspace root.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": str()}, "required": []string{"path"}},
		},
		{
			Name:        "shell",
			Description: "Run a shell command inside the workspace. The user is asked to type \"yes\" before anything runs — commands that leave the workspace are rejected outright.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": str()}, "required": []string{"command"}},
		},
	}
}

// modelClient is any LLM backend: native Anthropic or OpenAI-compatible.
// Both map to the same content blocks, so the loop never branches on
// provider. New providers are table rows, not loop changes.
type modelClient interface {
	complete(system string, msgs []message, tools []toolDef) ([]contentBlock, error)
}

// agentSystem is the system prompt: identity, workspace facts, tool
// discipline. Short on purpose — every token competes with the task.
func agentSystem() string {
	return "You are Nimbus-One, a careful coding assistant.\n" +
		"Facts:\n" +
		"- Your files live in one workspace; paths like notes/todo.md resolve inside it.\n" +
		"- Absolute paths and anything outside the workspace are rejected — always use relative paths.\n" +
		"- You have 5 tools: read, write, edit, list, shell. Use them; never claim inability without trying.\n" +
		"- edit needs search text occurring EXACTLY once; on mismatch, read the snippet and retry with more context.\n" +
		"- shell commands pause for the user's typed \"yes\"; keep commands small and reversible.\n" +
		"- Work step by step. When the task is done, answer with a short summary instead of calling tools.\n" +
		"- If a tool errors, read the message and recover (different path, narrower match, simpler command)."
}

// runAgent is the agent loop: user request → LLM → validate → execute →
// return → repeat → stop when no tool_use blocks remain. The model never
// touches disk or shell except through the five validated tools.
// confirmShellFn and stdin/stdout are injected so tests drive the loop
// without a terminal.
func runAgent(client modelClient, stdin io.Reader, stdout io.Writer, interactive bool, userText string, maxTurns int) (string, error) {
	if maxTurns <= 0 {
		maxTurns = 25
	}
	msgs := []message{userMessage(userText)}
	tools := agentTools()
	var lastText string
	for turn := 0; turn < maxTurns; turn++ {
		blocks, err := client.complete(agentSystem(), msgs, tools)
		if err != nil {
			return "", err
		}
		uses, text := splitBlocks(blocks)
		if text != "" {
			lastText = text
		}
		if len(uses) == 0 {
			if strings.TrimSpace(lastText) == "" {
				return "", fmt.Errorf("the model returned an empty answer — please rephrase your request")
			}
			return lastText, nil
		}
		msgs = append(msgs, message{Role: "assistant", Content: blocks})
		results := make([]contentBlock, 0, len(uses))
		for _, u := range uses {
			out, toolErr := executeToolCall(u, stdin, stdout, interactive)
			res := contentBlock{Type: "tool_result", ToolUseID: u.ID, Content: out}
			if toolErr != nil {
				res.Content = "error: " + toolErr.Error() + "\n(partial output above, if any)"
				if out != "" {
					res.Content = out + "\nerror: " + toolErr.Error()
				}
			}
			results = append(results, res)
		}
		msgs = append(msgs, message{Role: "user", Content: results})
	}
	return "", fmt.Errorf("stopped after %d turns without finishing — try a smaller step", maxTurns)
}

func userMessage(text string) message {
	return message{Role: "user", Content: []contentBlock{{Type: "text", Text: text}}}
}

// splitBlocks separates tool_use blocks from text, preserving order for
// the transcript while collecting the visible text.
func splitBlocks(blocks []contentBlock) (uses []contentBlock, text string) {
	var sb strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			uses = append(uses, b)
		case "text":
			sb.WriteString(b.Text)
		default:
			// Unknown block types are ignored, not fatal: the model may
			// send thinking/redacted blocks we don't need.
		}
	}
	return uses, sb.String()
}

// executeToolCall validates and runs one tool_use block. Every path goes
// through the jail; shell additionally passes the mandatory confirm gate.
func executeToolCall(u contentBlock, stdin io.Reader, stdout io.Writer, interactive bool) (string, error) {
	strArg := func(key string) string {
		if u.Input == nil {
			return ""
		}
		s, _ := u.Input[key].(string)
		return s
	}
	switch u.Name {
	case "read":
		return toolRead(strArg("path"))
	case "write":
		return toolWrite(strArg("path"), strArg("content"))
	case "edit":
		return toolEdit(strArg("path"), strArg("search"), strArg("replace"))
	case "list":
		return toolList(strArg("path"))
	case "shell":
		cmd := strArg("command")
		if err := checkShellText(cmd); err != nil {
			return "", err
		}
		if err := confirmShell(cmd, explainShell(cmd), stdin, stdout, interactive); err != nil {
			return "", err
		}
		return toolShell(cmd, 60)
	case "":
		return "", fmt.Errorf("the model sent a tool request with no name — skipping")
	default:
		return "", fmt.Errorf("unknown tool %q (I only have read, write, edit, list, shell)", u.Name)
	}
}
