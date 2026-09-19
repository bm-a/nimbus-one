package llm

import (
	"context"
	"errors"
	"testing"
)

// Tests reuse the stubProvider fake from fallback_test.go.

func TestChain2_OriginOrder(t *testing.T) {
	a := &stubProvider{name: "pa", err: errors.New("boom-a")}
	b := &stubProvider{name: "pb", text: "ok-b"}
	var origins []string
	ch := &Chain2{
		Providers: []Provider{a, b},
		OnStep: func(c Candidate, err error) {
			origins = append(origins, c.Origin)
		},
	}
	text, _, err := ch.Run(context.Background(), ChatRequest{Model: "m"})
	if err != nil || text != "ok-b" {
		t.Fatalf("Run = %q, %v; want ok-b, nil", text, err)
	}
	if len(origins) != 2 || origins[0] != OriginRequested || origins[1] != OriginFallback {
		t.Fatalf("origins = %v, want [requested fallback]", origins)
	}
}

func TestChain2_Dedupe(t *testing.T) {
	a := &stubProvider{name: "pa", text: "first"}
	dup := &stubProvider{name: "pa", text: "second"}
	ch := &Chain2{Providers: []Provider{a, dup}}
	text, _, err := ch.Run(context.Background(), ChatRequest{Model: "m"})
	if err != nil || text != "first" {
		t.Fatalf("Run = %q, %v; want first, nil", text, err)
	}
	if dup.calls != 0 {
		t.Fatalf("dupe provider called %d times, want 0", dup.calls)
	}
}

func TestChain2_SkipCache(t *testing.T) {
	a := &stubProvider{name: "pa", err: errors.New("cached-auth-failure")}
	b := &stubProvider{name: "pb", text: "recovered"}
	var skipped bool
	ch := &Chain2{
		Providers: []Provider{a, b},
		Skip:      map[string]bool{SkipKey("pa", "m"): true},
		OnStep: func(c Candidate, err error) {
			if c.Provider == "pa" && errors.Is(err, errSkipped) {
				skipped = true
			}
		},
	}
	text, _, err := ch.Run(context.Background(), ChatRequest{Model: "m"})
	if err != nil || text != "recovered" {
		t.Fatalf("Run = %q, %v; want recovered, nil", text, err)
	}
	if a.calls != 0 {
		t.Fatalf("skipped provider called %d times, want 0", a.calls)
	}
	if !skipped {
		t.Fatalf("OnStep never observed the skip")
	}
}

func TestChain2_Exhausted(t *testing.T) {
	sentinel := errors.New("nope")
	a := &stubProvider{name: "pa", err: sentinel}
	b := &stubProvider{name: "pb", err: errors.New("also-nope")}
	ch := &Chain2{Providers: []Provider{a, b}}
	_, _, err := ch.Run(context.Background(), ChatRequest{Model: "m"})
	var ex *Exhausted
	if !errors.As(err, &ex) {
		t.Fatalf("err type = %T, want *Exhausted", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("last-error chain does not unwrap to sentinel: %v", err)
	}
	if ex.Detail == "" {
		t.Fatalf("empty Exhausted detail")
	}
}
