// Tests for scopes.go.
package gateway

import "testing"

func TestRolePolicyEmptyAllowsAll(t *testing.T) {
	var p RolePolicy
	if !p.Check("anyone", "anything") {
		t.Fatal("empty policy must allow all")
	}
	if !(RolePolicy{Roles: map[string]Scopes{}}).Check("x", "y") {
		t.Fatal("policy with empty map must allow all")
	}
}

func TestRolePolicyUnknownRoleDenied(t *testing.T) {
	p := DefaultRoles()
	if p.Check("ghost", "status") {
		t.Fatal("unknown role must be denied")
	}
}

func TestRolePolicyDefaults(t *testing.T) {
	p := DefaultRoles()
	cases := []struct {
		role, method string
		want         bool
	}{
		{"admin", "chat", true},
		{"admin", "sessions.list", true},
		{"admin", "anything.at.all", true},
		{"operator", "chat", true},
		{"operator", "task", true},
		{"operator", "status", true},
		{"operator", "cron.create", false},
		{"operator", "sessions.list", false},
		{"viewer", "status", true},
		{"viewer", "chat", false},
		{"viewer", "task", false},
	}
	for _, c := range cases {
		if got := p.Check(c.role, c.method); got != c.want {
			t.Errorf("Check(%q,%q) = %v, want %v", c.role, c.method, got, c.want)
		}
	}
}

func TestRolePolicyWildcards(t *testing.T) {
	p := RolePolicy{Roles: map[string]Scopes{
		"ops": {Role: "ops", Allow: []string{"sessions.*", "chat"}},
		"all": {Role: "all", Allow: []string{"*"}},
	}}
	for _, m := range []string{"sessions.list", "sessions.create", "sessions.x.y"} {
		if !p.Check("ops", m) {
			t.Errorf("sessions.* must match %q", m)
		}
	}
	if p.Check("ops", "session") {
		t.Error("sessions.* must not match bare prefix without the dot")
	}
	if p.Check("ops", "cron.create") {
		t.Error("unlisted method must be denied")
	}
	if !p.Check("all", "whatever") {
		t.Error("* must match everything")
	}
	empty := RolePolicy{Roles: map[string]Scopes{
		"none": {Role: "none"},
	}}
	if empty.Check("none", "status") {
		t.Error("role with empty Allow must deny")
	}
}

func TestMatchScope(t *testing.T) {
	cases := []struct {
		pattern, method string
		want            bool
	}{
		{"chat", "chat", true},
		{"chat", "chat2", false},
		{"*", "anything", true},
		{"sessions.*", "sessions.list", true},
		{"sessions.*", "sessions.", true},
		{"sessions.*", "sessions", false},
		{"*.list", "sessions.list", true},
		{"*.list", "sessions.create", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXc", false},
		{"", "", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := matchScope(c.pattern, c.method); got != c.want {
			t.Errorf("matchScope(%q,%q) = %v, want %v", c.pattern, c.method, got, c.want)
		}
	}
}
