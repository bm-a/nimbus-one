// Package engine model routing helpers.
package engine

import (
	"strings"
)

// Planner routes tasks between a cheap and a smart model.
type Planner struct {
	CheapModel string
	SmartModel string
}

// codeKeywords force escalation to the smart model.
var codeKeywords = []string{
	"code", "func", "struct", "python", "golang", "javascript",
	"typescript", "bug", "error", "stack", "traceback", "api", "sql",
	"regex", "class", "import", "package", "interface", "test",
	"debug", "algorithm", "refactor", "deploy", "docker", "kubernetes",
	"http", "json", "database", "compiler", "```", "def ", "fn ",
	"panic", "goroutine", "channel", "struct{", "=>", "ssh", "linux",
	"query", "schema", "migration",
}

// Route returns CheapModel for short, non-technical tasks and SmartModel
// otherwise (len >= 200 or any code keyword present, case-insensitive).
func (p *Planner) Route(task string) string {
	cheap := p.CheapModel
	if cheap == "" {
		cheap = "cheap"
	}
	smart := p.SmartModel
	if smart == "" {
		smart = "smart"
	}
	if len(task) >= 200 {
		return smart
	}
	lower := strings.ToLower(task)
	for _, kw := range codeKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return smart
		}
	}
	return cheap
}

// ReflectPrompt returns a self-reflection suffix appended after tool-heavy
// or long tasks so the model critiques its own draft.
func (p *Planner) ReflectPrompt() string {
	return "\n\nBefore finalizing, reflect: (1) Did I fully address every part of the task? " +
		"(2) Is there any error, missing edge case, or unsupported claim in my answer? " +
		"(3) What would I change if I had one more iteration? " +
		"Briefly note corrections, then give the final answer."
}
