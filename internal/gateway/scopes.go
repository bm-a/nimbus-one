// Role-based method scopes for the gateway.
//
// OpenClaw reference (read-only mirror /data/data/com.termux/files/home/tmp/openclaw-src):
//
//	src/gateway/operator-scopes.ts       — operator scope vocabulary
//	src/gateway/role-policy.ts           — role → grant evaluation
//	src/gateway/method-scopes.ts         — per-method scope requirements
//	src/gateway/operator-role-policy.ts  — built-in operator tiers
//	src/gateway/server-methods.authorization.test.ts — policy contract tests
//
// Semantics: an empty RolePolicy (no roles) allows everything — useful before
// auth is configured. A non-empty policy denies unknown roles and denies
// methods with no matching grant. Allow entries are literals ("chat") or
// *-wildcard patterns ("*" or "sessions.*").
package gateway

import "strings"

// Scopes grants a role the methods it may call.
type Scopes struct {
	Role  string
	Allow []string
}

// RolePolicy maps role names to their grants.
type RolePolicy struct {
	Roles map[string]Scopes
}

// Check reports whether role may call method.
func (p RolePolicy) Check(role, method string) bool {
	if len(p.Roles) == 0 {
		return true
	}
	sc, ok := p.Roles[role]
	if !ok {
		return false
	}
	for _, pattern := range sc.Allow {
		if matchScope(pattern, method) {
			return true
		}
	}
	return false
}

// DefaultRoles mirrors OpenClaw's operator tiers: admin may call anything,
// operator may chat, run tasks and check status, viewer may only check
// status.
func DefaultRoles() RolePolicy {
	return RolePolicy{Roles: map[string]Scopes{
		"admin":    {Role: "admin", Allow: []string{"*"}},
		"operator": {Role: "operator", Allow: []string{"chat", "task", "status"}},
		"viewer":   {Role: "viewer", Allow: []string{"status"}},
	}}
}

// matchScope matches method against pattern where every "*" spans any
// (possibly empty) run of characters. Patterns without "*" need exact
// equality.
func matchScope(pattern, method string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == method
	}
	parts := strings.Split(pattern, "*")
	if parts[0] != "" {
		if !strings.HasPrefix(method, parts[0]) {
			return false
		}
		method = method[len(parts[0]):]
	}
	last := parts[len(parts)-1]
	if last != "" {
		if !strings.HasSuffix(method, last) {
			return false
		}
		method = method[:len(method)-len(last)]
	}
	for _, mid := range parts[1 : len(parts)-1] {
		if mid == "" {
			continue
		}
		i := strings.Index(method, mid)
		if i < 0 {
			return false
		}
		method = method[i+len(mid):]
	}
	return true
}
