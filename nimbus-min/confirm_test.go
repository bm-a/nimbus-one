package main

import (
	"strings"
	"testing"
)

func TestConfirmYes(t *testing.T) {
	stdin := strings.NewReader("yes\n")
	var stdout strings.Builder
	if err := confirmShell("ls", "list files", stdin, &stdout, true); err != nil {
		t.Fatalf("yes must approve: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "$ ls") || !strings.Contains(out, "list files") {
		t.Fatalf("prompt must show command + explanation: %q", out)
	}
}

func TestConfirmRejects(t *testing.T) {
	for _, in := range []string{"y\n", "YES please\n", "\n", "no\n", "yes yes\n"} {
		stdin := strings.NewReader(in)
		var stdout strings.Builder
		if err := confirmShell("rm x", "delete", stdin, &stdout, true); err == nil {
			t.Fatalf("input %q must refuse", in)
		}
	}
	// Case-insensitive YES is accepted.
	if err := confirmShell("ls", "x", strings.NewReader("YES\n"), &strings.Builder{}, true); err != nil {
		t.Fatalf("YES must approve: %v", err)
	}
}

func TestConfirmHeadlessDenies(t *testing.T) {
	var stdout strings.Builder
	err := confirmShell("ls", "list", strings.NewReader("yes\n"), &stdout, false)
	if err == nil {
		t.Fatal("headless must refuse even with yes piped in")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("refusal must guide to a terminal: %v", err)
	}
}

func TestExplainShell(t *testing.T) {
	if !strings.Contains(explainShell("ls -la"), "list files") {
		t.Fatal("ls explanation wrong")
	}
	if !strings.Contains(explainShell("rm -rf x"), "cannot be undone") {
		t.Fatal("rm must warn about irreversibility")
	}
	if explainShell("frobnicate --x") == "" {
		t.Fatal("unknown program needs a generic explanation")
	}
}
