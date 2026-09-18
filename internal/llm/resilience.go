// Deterministic self-healing: exponential backoff with full jitter,
// transient-error retries, rate-limit header parsing, and the escalation
// payload used as a last resort. No model calls — pure algorithmic Go.
package llm

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBackoff caps exponential growth so phones never sleep excessively.
const maxBackoff = 5 * time.Second

// Backoff returns 2^n * 100ms of full jitter, capped at 5s.
// n starts at 0 (first retry waits ~0-100ms).
func Backoff(n int) time.Duration {
	return BackoffN(n, rand.Int63n(100))
}

// BackoffN is the testable core: base 2^n*100ms plus an explicit jitter.
func BackoffN(n int, jitterMs int64) time.Duration {
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	base := time.Duration(int64(1) << uint(n) * int64(100*time.Millisecond))
	d := base + time.Duration(jitterMs)*time.Millisecond
	if d > maxBackoff {
		d = maxBackoff
	}
	if d < 0 {
		return 0
	}
	return d
}

// IsRetryableStatus reports whether an HTTP code merits another key/attempt.
func IsRetryableStatus(code int) bool {
	switch code {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// IsTerminalAuthCode reports invalid-credential codes: the key is Dead,
// never retried silently.
func IsTerminalAuthCode(code int) bool { return code == 401 || code == 403 }

// ParseRetryAfter converts rate-limit headers to a wait duration:
// Retry-After (delta-seconds or HTTP date), x-ratelimit-reset (unix epoch),
// x-ratelimit-reset-requests. Returns <=0 when nothing parseable.
func ParseRetryAfter(h http.Header, now time.Time) time.Duration {
	if h == nil {
		return 0
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := t.Sub(now); d > 0 {
				return d
			}
			return 0
		}
	}
	for _, k := range []string{"x-ratelimit-reset", "x-ratelimit-reset-requests"} {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
				if d := time.Unix(epoch, 0).Sub(now); d > 0 {
					return d
				}
				return 0
			}
			// some providers send milliseconds
			if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 1e12 {
				if d := time.Unix(ms/1000, 0).Sub(now); d > 0 {
					return d
				}
				return 0
			}
		}
	}
	return 0
}

// isTransientErr matches TCP timeouts, EOFs, resets, DNS blips.
func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && (ne.Timeout() || ne.Temporary()) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, m := range []string{
		"eof", "connection reset", "connection refused", "connection aborted",
		"timeout", "temporary failure", "no such host", "network is unreachable",
		"broken pipe", "reset by peer",
	} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// RetryTransient runs fn up to attempts times, retrying only transient
// network errors with Backoff delays. Context cancellation aborts at once.
func RetryTransient(ctx context.Context, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = fn(); err == nil {
			return nil
		}
		if !isTransientErr(err) || i == attempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(Backoff(i)):
		}
	}
	return err
}

// Escalation is the last-resort payload shown when every key across every
// tier failed. The TUI renders it as an interactive card.
type Escalation struct {
	Provider    string
	KeysTotal   int
	KeysCooling int
	KeysDead    int
	LastErr     string
	// Recommendation is the built-in suggested path. Defaults to Meta Muse.
	Recommendation string
}

// DefaultRecommendation points at Nimbus-One — the agent identity — with
// its recommended model route (Meta Muse Spark 1.3, OpenAI-compatible via
// OpenRouter free tier or a direct Meta endpoint). Users choose their own
// models; this is only the suggestion shown when everything else failed.
const DefaultRecommendation = "Nimbus-One (recommended route: meta/muse-spark-1.3-contributor via OpenRouter free tier or your Meta endpoint): run `nimbus-one config`, choose OpenRouter, paste a key."

// Recommend returns the configured recommendation or the default.
func (e Escalation) Recommend() string {
	if strings.TrimSpace(e.Recommendation) != "" {
		return e.Recommendation
	}
	return DefaultRecommendation
}

// Summary renders one-line diagnostics for logs and the escalation card.
func (e Escalation) Summary() string {
	var b strings.Builder
	b.WriteString("provider=" + e.Provider + " ")
	b.WriteString("keys=" + itoa2(e.KeysTotal) + " ")
	b.WriteString("cooling=" + itoa2(e.KeysCooling) + " ")
	b.WriteString("dead=" + itoa2(e.KeysDead))
	if e.LastErr != "" {
		msg := e.LastErr
		if len(msg) > 200 {
			msg = msg[:200]
		}
		b.WriteString(" last=" + msg)
	}
	return b.String()
}

func itoa2(n int) string { return strconv.Itoa(n) }
