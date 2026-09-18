package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBackoffBounds(t *testing.T) {
	if got := BackoffN(0, 0); got != 100*time.Millisecond {
		t.Fatalf("n=0 base should be 100ms, got %v", got)
	}
	if got := BackoffN(3, 0); got != 800*time.Millisecond {
		t.Fatalf("n=3 base should be 800ms, got %v", got)
	}
	if got := BackoffN(20, 99999); got != 5*time.Second {
		t.Fatalf("must cap at 5s, got %v", got)
	}
	if got := BackoffN(-5, 0); got != 100*time.Millisecond {
		t.Fatalf("negative n clamps, got %v", got)
	}
	_ = Backoff // jittered variant compiles
}

func TestIsRetryable(t *testing.T) {
	for _, c := range []int{408, 429, 500, 502, 503, 504} {
		if !IsRetryableStatus(c) {
			t.Fatalf("%d should be retryable", c)
		}
	}
	for _, c := range []int{200, 400, 401, 403, 404} {
		if IsRetryableStatus(c) {
			t.Fatalf("%d should not be retryable", c)
		}
	}
	if !IsTerminalAuthCode(401) || !IsTerminalAuthCode(403) || IsTerminalAuthCode(429) {
		t.Fatal("auth code classification wrong")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()
	h := http.Header{"Retry-After": []string{"45"}}
	if d := ParseRetryAfter(h, now); d != 45*time.Second {
		t.Fatalf("delta-seconds: %v", d)
	}
	h = http.Header{"Retry-After": []string{now.Add(90 * time.Second).UTC().Format(http.TimeFormat)}}
	if d := ParseRetryAfter(h, now); d < 85*time.Second || d > 95*time.Second {
		t.Fatalf("http-date: %v", d)
	}
	h = http.Header{"X-Ratelimit-Reset": []string{"9999999999"}}
	if d := ParseRetryAfter(h, now); d <= 0 {
		t.Fatal("epoch reset should be positive")
	}
	if d := ParseRetryAfter(nil, now); d != 0 {
		t.Fatal("nil headers → 0")
	}
	_ = now
}

func TestRetryTransientSuccessAfterFlakes(t *testing.T) {
	ctx := context.Background()
	n := 0
	err := RetryTransient(ctx, 3, func() error {
		n++
		if n < 3 {
			return errors.New("connection reset by peer")
		}
		return nil
	})
	if err != nil || n != 3 {
		t.Fatalf("should succeed on 3rd try (n=%d err=%v)", n, err)
	}
}

func TestRetryTransientNonTransientNoRetry(t *testing.T) {
	n := 0
	err := RetryTransient(context.Background(), 3, func() error {
		n++
		return errors.New("bad request: model not found")
	})
	if err == nil || n != 1 {
		t.Fatalf("non-transient must not retry (n=%d)", n)
	}
}

func TestRetryTransientCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RetryTransient(ctx, 3, func() error { return errors.New("eof") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancel, got %v", err)
	}
}

func TestEscalationRecommend(t *testing.T) {
	e := Escalation{Provider: "openrouter", KeysTotal: 2, KeysDead: 2, LastErr: "401"}
	if !strings.Contains(strings.ToLower(e.Recommend()), "muse") {
		t.Fatalf("default recommendation must mention the Muse route: %q", e.Recommend())
	}
	e.Recommendation = "custom"
	if e.Recommend() != "custom" {
		t.Fatal("custom recommendation must win")
	}
	if !strings.Contains(e.Summary(), "dead=2") {
		t.Fatalf("summary: %q", e.Summary())
	}
}
