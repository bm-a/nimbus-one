package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
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
