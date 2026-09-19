package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestProviderTableValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range providers {
		if p.ID == "" || p.Name == "" || p.Base == "" {
			t.Fatalf("incomplete row: %+v", p)
		}
		if seen[strings.ToLower(p.ID)] {
			t.Fatalf("duplicate provider id %q", p.ID)
		}
		seen[strings.ToLower(p.ID)] = true
		if p.Family != familyAnthropic && p.Family != familyOpenAI {
			t.Fatalf("%s: unknown family %q", p.ID, p.Family)
		}
		u, err := url.Parse(p.Base)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			t.Fatalf("%s: bad base %q", p.ID, p.Base)
		}
		if !p.Local && len(p.KeyEnvs) == 0 {
			t.Fatalf("%s: cloud provider needs key envs", p.ID)
		}
		if p.Local && len(p.KeyEnvs) != 0 {
			t.Fatalf("%s: local provider must not need keys", p.ID)
		}
		if p.Local && u.Scheme != "http" {
			t.Fatalf("%s: local base should be plain http (loopback)", p.ID)
		}
	}
	if len(providers) < 40 {
		t.Fatalf("table shrank to %d rows, want 40+ (OpenClaw parity)", len(providers))
	}
}

func TestLookupProvider(t *testing.T) {
	p, err := lookupProvider("DeepSeek")
	if err != nil || p.Base != "https://api.deepseek.com" {
		t.Fatalf("lookup = %+v, %v", p, err)
	}
	if _, err := lookupProvider("nope"); err == nil {
		t.Fatal("unknown provider must error with models guidance")
	}
}
