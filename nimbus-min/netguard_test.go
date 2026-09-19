package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func osWriteFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func TestNetguardBlocksNonAnthropic(t *testing.T) {
	c := guardedHTTPClient()
	for _, url := range []string{
		"https://example.com/",
		"http://api.anthropic.com/v1/messages", // wrong port/scheme
		"https://evil-api.anthropic.com/",
	} {
		req, _ := http.NewRequest("GET", url, nil)
		_, err := c.Do(req.WithContext(context.Background()))
		if err == nil {
			t.Fatalf("GET %s must be blocked", url)
		}
	}
}

func TestNetguardProviderScoped(t *testing.T) {
	c := guardedHTTPClientFor("https://api.deepseek.com")
	// Own host allowed (dial will fail closed on connection, not on policy —
	// so assert via the guard directly for determinism).
	g, err := guardForBase("https://api.deepseek.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.dial(context.Background(), "tcp", "api.deepseek.com:443"); err == nil {
		// Local sandbox has no route; a *policy* pass returns a dial error
		// mentioning connection, not "network blocked".
		t.Log("dial attempted (no route here) — policy passed")
	} else if strings.Contains(err.Error(), "network blocked") {
		t.Fatalf("own provider host must pass policy: %v", err)
	}
	if _, err := g.dial(context.Background(), "tcp", "api.anthropic.com:443"); err == nil {
		t.Fatal("other provider host must be blocked")
	} else if !strings.Contains(err.Error(), "network blocked") {
		t.Fatalf("wrong error: %v", err)
	}
	_ = c
}

func TestNetguardLocalBase(t *testing.T) {
	g, err := guardForBase("http://127.0.0.1:11434/v1")
	if err != nil {
		t.Fatal(err)
	}
	// Loopback is the provider itself here — allowed by the exact entry.
	if !g.allowed["127.0.0.1:11434"] {
		t.Fatal("local provider entry missing")
	}
	if g.allowed["localhost:11434"] {
		t.Fatal("no blanket loopback aliases — exact host only")
	}
}

func TestNetguardBadBase(t *testing.T) {
	if _, err := guardForBase("://bad"); err == nil {
		t.Fatal("malformed base must error")
	}
}
