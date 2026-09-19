// Session key helpers mirror OpenClaw's routing contract:
// src/routing/session-key.ts, packages/session-url-contract/src/session-key.ts
// (buildAgentMainSessionKey, DEFAULT_MAIN_KEY=main, parseAgentSessionKeyParts)
// and src/sessions/session-key-utils.ts (isSubagentSessionKey,
// isAcpSessionKey, parseAgentSessionKey).
//
// Canonical shape is agent:<id>:<rest>; the main key is agent:<id>:main.
package agents

import (
	"fmt"
	"strings"
)

// DefaultMainKey is the rest segment of an agent main session key.
// Mirrors DEFAULT_MAIN_KEY in packages/session-url-contract/src/session-key.ts.
const DefaultMainKey = "main"

// SessionKeyError describes a malformed agent session key.
type SessionKeyError struct {
	Key    string
	Reason string
}

// Error implements error.
func (e *SessionKeyError) Error() string {
	if e == nil {
		return "agents: malformed session key"
	}
	if e.Reason != "" {
		return fmt.Sprintf("agents: malformed session key %q: %s", e.Key, e.Reason)
	}
	return fmt.Sprintf("agents: malformed session key %q", e.Key)
}

// NormalizeAgentID canonicalizes an agent id for keys (trim + lowercase,
// empty maps to the legacy implicit id). Mirrors normalizeAgentId in
// src/routing/session-key.ts.
func NormalizeAgentID(id string) string { return normalizeAgentID(id) }

// NormalizeModel canonicalizes a model string for comparison (trim +
// lowercase). Model ids compare case-insensitively like OpenClaw's
// normalizeLowercaseStringOrEmpty.
func NormalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// NormalizeMainKey canonicalizes a main-key segment, defaulting blanks to
// "main". Mirrors normalizeMainKey in
// packages/session-url-contract/src/session-key.ts.
func NormalizeMainKey(mainKey string) string {
	if t := strings.ToLower(strings.TrimSpace(mainKey)); t != "" {
		return t
	}
	return DefaultMainKey
}

// BuildMain returns the main session key agent:<id>:main. Mirrors
// buildAgentMainSessionKey in packages/session-url-contract/src/session-key.ts.
func BuildMain(agentID string) string {
	return "agent:" + NormalizeAgentID(agentID) + ":" + DefaultMainKey
}

// Parse splits agent:<id>:<rest>, returning the normalized agent id and the
// verbatim rest tail (opaque peer bytes keep their case, mirroring
// parseAgentSessionKeyParts which never folds the tail).
func Parse(key string) (agentID, rest string, err error) {
	raw := strings.TrimSpace(key)
	if raw == "" {
		return "", "", &SessionKeyError{Key: key, Reason: "empty key"}
	}
	if len(raw) < len("agent:x:y") || !strings.EqualFold(raw[:6], "agent:") {
		return "", "", &SessionKeyError{Key: key, Reason: "missing agent: prefix"}
	}
	end := strings.Index(raw[6:], ":")
	if end < 0 {
		return "", "", &SessionKeyError{Key: key, Reason: "missing rest segment"}
	}
	id := strings.TrimSpace(raw[6 : 6+end])
	tail := strings.TrimSpace(raw[6+end+1:])
	if id == "" {
		return "", "", &SessionKeyError{Key: key, Reason: "empty agent id"}
	}
	if tail == "" || strings.HasPrefix(tail, ":") {
		return "", "", &SessionKeyError{Key: key, Reason: "empty rest segment"}
	}
	return normalizeAgentID(id), tail, nil
}

// IsMain reports whether key is an agent main key (rest == "main",
// case-insensitive).
func IsMain(key string) bool {
	_, rest, err := Parse(key)
	if err != nil {
		return false
	}
	return strings.EqualFold(rest, DefaultMainKey)
}

// IsSubagent reports whether key routes inside a subagent scope. Mirrors
// isSubagentSessionKey: a bare subagent: head or any ":subagent:" segment.
func IsSubagent(key string) bool {
	l := strings.ToLower(strings.TrimSpace(key))
	if l == "" {
		return false
	}
	if strings.HasPrefix(l, "subagent:") || strings.Contains(l, ":subagent:") {
		return true
	}
	_, rest, err := Parse(key)
	if err != nil {
		return false
	}
	rl := strings.ToLower(rest)
	return strings.HasPrefix(rl, "subagent:") || strings.Contains(rl, ":subagent:")
}

// IsAcp reports whether key routes to ACP dispatch. Mirrors
// isAcpSessionKey: a bare acp: head or any ":acp:" segment.
func IsAcp(key string) bool {
	l := strings.ToLower(strings.TrimSpace(key))
	if l == "" {
		return false
	}
	if strings.HasPrefix(l, "acp:") || strings.Contains(l, ":acp:") {
		return true
	}
	_, rest, err := Parse(key)
	if err != nil {
		return false
	}
	rl := strings.ToLower(rest)
	return strings.HasPrefix(rl, "acp:") || strings.Contains(rl, ":acp:")
}

// IsPrimary reports whether key is a primary (main, non-subagent, non-ACP)
// session: main and not subagent/acp.
func IsPrimary(key string) bool {
	return IsMain(key) && !IsSubagent(key) && !IsAcp(key)
}
