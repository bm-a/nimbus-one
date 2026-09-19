package perms

import "testing"

func TestEvaluate_LastMatchWins(t *testing.T) {
	rs := Ruleset{
		{Tool: "*", Pattern: "*", Action: Deny},
		{Tool: "read", Pattern: "*", Action: Allow},
		{Tool: "read", Pattern: "*.env", Action: Deny},
	}
	if got := Evaluate("read", "main.go", rs); got != Allow {
		t.Fatalf("read main.go = %q, want allow", got)
	}
	if got := Evaluate("read", ".env", rs); got != Deny {
		t.Fatalf("read .env = %q, want deny", got)
	}
	if got := Evaluate("bash", "ls", rs); got != Deny {
		t.Fatalf("bash ls = %q, want deny", got)
	}
}

func TestEvaluate_FailClosed(t *testing.T) {
	if got := Evaluate("brand-new-tool", "x"); got != Ask {
		t.Fatalf("unmatched = %q, want ask", got)
	}
}

func TestEvaluate_ApprovalsWin(t *testing.T) {
	base := Ruleset{{Tool: "bash", Pattern: "*", Action: Ask}}
	var ap Approvals
	ap.Approve("bash", "go test*")
	if got := Evaluate("bash", "go test ./...", base, ap.AsRuleset()); got != Allow {
		t.Fatalf("approved = %q, want allow", got)
	}
	if got := Evaluate("bash", "rm -rf /", base, ap.AsRuleset()); got != Ask {
		t.Fatalf("unapproved = %q, want ask", got)
	}
}

func TestPlan_Preset(t *testing.T) {
	rs := Plan([]string{"read", "list"})
	if got := Evaluate("read", "a.go", rs); got != Allow {
		t.Fatalf("read = %q, want allow", got)
	}
	if got := Evaluate("bash", "ls", rs); got != Deny {
		t.Fatalf("bash = %q, want deny", got)
	}
	// User override appended later wins.
	if got := Evaluate("bash", "ls", rs, Ruleset{{Tool: "bash", Action: Allow}}); got != Allow {
		t.Fatalf("override = %q, want allow", got)
	}
}

func TestMatchPattern_GlobAndDirPrefix(t *testing.T) {
	if !matchPattern("src/**", "src/a/b.go") {
		t.Fatal("src/** should match nested path")
	}
	if !matchPattern("src/**", "src") {
		t.Fatal("src/** should match the dir itself")
	}
	if matchPattern("src/**", "other/a.go") {
		t.Fatal("src/** should not match other/")
	}
	if !matchPattern("*.env", ".env") {
		t.Fatal("*.env should match .env")
	}
}

func TestTargetFor(t *testing.T) {
	if got := TargetFor("read", map[string]any{"path": "a.go"}); got != "a.go" {
		t.Fatalf("target = %q", got)
	}
	if got := TargetFor("bash", map[string]any{"command": "ls"}); got != "ls" {
		t.Fatalf("target = %q", got)
	}
	if got := TargetFor("x", nil); got != "*" {
		t.Fatalf("empty target = %q", got)
	}
}
