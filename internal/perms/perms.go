// Package perms implements the permission ruleset engine: flat
// {tool, pattern, action} triples with last-match-wins evaluation.
//
// Semantics follow the proven OpenCode model: every tool call is checked as
// (tool-name, target) against the session ruleset plus session approvals.
// Actions are allow, ask, or deny. Denied tools are hidden from the model,
// never merely blocked — fewer visible tools means fewer bad calls.
package perms

import (
	"strings"
)

// Action is a permission verdict.
type Action string

// Verdicts.
const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

// Rule grants an action for a tool on targets matching Pattern.
// Tool "*" matches every tool; Pattern "*" (or "") matches every target.
// Patterns are simple globs where "*" matches any sequence INCLUDING path
// separators (so "*.env" also covers nested .env files and "go test*"
// covers "go test ./..."), and "dir/**" additionally matches "dir" itself.
type Rule struct {
	Tool    string
	Pattern string
	Action  Action
}

// Ruleset is an ordered rule list. Later rules override earlier ones.
type Ruleset []Rule

// Evaluate returns the verdict for (tool, target): the last matching rule
// across all sets wins. With no match the verdict is Ask — fail-closed, so
// new tools are never silently permitted.
func Evaluate(tool, target string, sets ...Ruleset) Action {
	verdict := Ask
	for _, rs := range sets {
		for _, r := range rs {
			if !matchTool(r.Tool, tool) {
				continue
			}
			if !matchPattern(r.Pattern, target) {
				continue
			}
			verdict = r.Action
		}
	}
	return verdict
}

func matchTool(rule, tool string) bool {
	if rule == "" || rule == "*" {
		return true
	}
	return rule == tool
}

func matchPattern(pattern, target string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	// Trailing "/**" is a subtree match that also covers the dir itself.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if target == prefix || strings.HasPrefix(target, prefix+"/") {
			return true
		}
	}
	return globStar(pattern, target)
}

// globStar matches with "*" spanning any characters (separators included)
// and "?" matching exactly one character. No character classes.
func globStar(pattern, target string) bool {
	px, tx := 0, 0
	star, mark := -1, 0
	for tx < len(target) {
		if px < len(pattern) && (pattern[px] == '?' || pattern[px] == target[tx]) {
			px++
			tx++
		} else if px < len(pattern) && pattern[px] == '*' {
			star = px
			mark = tx
			px++
		} else if star != -1 {
			mark++
			tx = mark
		} else {
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// TargetFor derives the permission target from tool arguments: the first
// present of path/file/command/cwd/url, else "*".
func TargetFor(tool string, args map[string]any) string {
	_ = tool
	for _, k := range []string{"path", "file", "command", "cwd", "url"} {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return "*"
}

// Allow-all preset: the build-mode equivalent. Explicit, never implicit.
func Build() Ruleset {
	return Ruleset{{Tool: "*", Pattern: "*", Action: Allow}}
}

// Plan returns a read-only preset over the given read-only tool names:
// deny everything, then allow the listed tools. Later user rules appended
// after this set override it (last-match-wins).
func Plan(readOnlyTools []string) Ruleset {
	rs := Ruleset{{Tool: "*", Pattern: "*", Action: Deny}}
	for _, t := range readOnlyTools {
		rs = append(rs, Rule{Tool: t, Pattern: "*", Action: Allow})
	}
	return rs
}

// Explore is a read-only preset identical in shape to Plan; kept separate so
// future subagent presets can diverge without touching plan semantics.
func Explore(readOnlyTools []string) Ruleset {
	return Plan(readOnlyTools)
}

// Approvals is the session-scoped always-allow list (the "always" reply).
// It is evaluated after the base ruleset so user approvals win.
type Approvals struct {
	rules Ruleset
}

// Approve records a permanent allow for (tool, pattern) in this session.
func (a *Approvals) Approve(tool, pattern string) {
	if pattern == "" {
		pattern = "*"
	}
	a.rules = append(a.rules, Rule{Tool: tool, Pattern: pattern, Action: Allow})
}

// AsRuleset exposes approvals for Evaluate.
func (a *Approvals) AsRuleset() Ruleset { return a.rules }

// AskFunc decides an ask-verdict: true approves (and records an approval),
// false denies. Nil means headless — the caller must fail closed.
type AskFunc func(tool, target string) bool
