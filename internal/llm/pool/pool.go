// Package pool is a deterministic, thread-safe multi-key load balancer.
//
// It holds N API keys per model tier, tracks per-key latency (EMA) and
// failure state, cools keys down on 429/503 using rate-limit headers, kills
// keys on 401/403, and fails over without spending a single LLM token on
// routing decisions. All logic is algorithmic Go — no model calls.
package pool

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"nimbus-one/internal/llm"
)

// Key states.
const (
	StateHealthy     = "Healthy"
	StateDegraded    = "Degraded"
	StateCoolingDown = "CoolingDown"
	StateDead        = "Dead"
)

// KeyConfig describes one API key slot.
type KeyConfig struct {
	ID           string
	ProviderName string
	Model        string
	APIKey       string
	BaseURL      string
}

// Key is one managed credential with rolling metrics.
type Key struct {
	ID           string
	ProviderName string
	Model        string
	APIKey       string
	BaseURL      string

	mu           sync.Mutex
	inFlight     int
	emaMs        float64
	samples      int
	lastUsed     time.Time
	cooldownUntl time.Time
	consecFails  int
	state        string
}

// KeyStats is a dashboard snapshot of one key.
type KeyStats struct {
	ID              string
	Provider        string
	Model           string
	State           string
	AvgMs           float64
	InFlight        int
	CooldownRemainS int64
	ConsecFails     int
	LastUsed        time.Time
}

// ContextOverflowError signals the request exceeded provider context limits.
// The engine catches it, compacts history, and resends.
type ContextOverflowError struct{ Msg string }

func (e *ContextOverflowError) Error() string { return "context overflow: " + e.Msg }

// ExhaustedError signals every key in the pool failed. The TUI escalation
// modal consumes it as a last resort.
type ExhaustedError struct{ Detail string }

func (e *ExhaustedError) Error() string { return "all keys exhausted: " + e.Detail }

// Pool balances across keys.
type Pool struct {
	mu        sync.Mutex
	keys      []*Key
	newClient func(apiKey, baseURL, model string) llm.Provider
}

// NewPool builds a pool from configs. Empty IDs are filled as key-N.
func NewPool(cfgs []KeyConfig) *Pool {
	p := &Pool{
		newClient: func(apiKey, baseURL, model string) llm.Provider {
			return &llm.Client{APIKey: apiKey, BaseURL: baseURL, Model: model}
		},
	}
	for i, c := range cfgs {
		id := c.ID
		if id == "" {
			id = "key-" + itoa(i+1)
		}
		p.keys = append(p.keys, &Key{
			ID: id, ProviderName: c.ProviderName, Model: c.Model,
			APIKey: c.APIKey, BaseURL: c.BaseURL, state: StateHealthy,
		})
	}
	return p
}

// SetClientFactory overrides provider construction (tests).
func (p *Pool) SetClientFactory(fn func(apiKey, baseURL, model string) llm.Provider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.newClient = fn
}

// Name implements llm.Provider.
func (p *Pool) Name() string { return "pool" }

// snapshot of a key for scoring.
type keySnap struct {
	k        *Key
	ema      float64
	inFlight int
	samples  int
}

// Pick returns the best available key: skips Dead and unexpired CoolingDown,
// lazily revives expired cooldowns to Healthy, and scores by
// ema*(1+inFlight) so the fastest idle key wins. Returns nil when nothing
// is usable.
func (p *Pool) Pick() *Key {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var best *Key
	bestScore := 0.0
	for _, k := range p.keys {
		k.mu.Lock()
		if k.state == StateDead {
			k.mu.Unlock()
			continue
		}
		if k.state == StateCoolingDown && now.Before(k.cooldownUntl) {
			k.mu.Unlock()
			continue
		}
		if k.state == StateCoolingDown {
			k.state = StateHealthy // probe-eligible again
			k.consecFails = 0
		}
		ema := k.emaMs
		if k.samples == 0 {
			ema = 500 // neutral prior so new keys get tried
		}
		score := ema * float64(1+k.inFlight)
		st := k.state
		k.mu.Unlock()
		_ = st
		if best == nil || score < bestScore {
			best, bestScore = k, score
		}
	}
	return best
}

// ReportStart marks one in-flight request.
func (p *Pool) ReportStart(k *Key) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.inFlight++
	k.lastUsed = time.Now()
}

func (p *Pool) endFlight(k *Key) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.inFlight > 0 {
		k.inFlight--
	}
}

// ReportSuccess records a round-trip latency.
func (p *Pool) ReportSuccess(k *Key, latency time.Duration) {
	ms := float64(latency) / float64(time.Millisecond)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.inFlight > 0 {
		k.inFlight--
	}
	if k.samples == 0 {
		k.emaMs = ms
	} else {
		k.emaMs = 0.3*ms + 0.7*k.emaMs
	}
	k.samples++
	k.consecFails = 0
	k.lastUsed = time.Now()
	if k.state == StateCoolingDown || k.state == StateDead {
		// keep terminal states; cooldown expiry is handled in Pick
	} else if k.emaMs > 3000 {
		k.state = StateDegraded
	} else {
		k.state = StateHealthy
	}
}

// ReportFailure records a failed attempt. headers carries rate-limit
// metadata when the failure was an HTTP error.
func (p *Pool) ReportFailure(k *Key, statusCode int, headers http.Header) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.inFlight > 0 {
		k.inFlight--
	}
	k.consecFails++
	k.lastUsed = time.Now()
	switch {
	case statusCode == 401 || statusCode == 403:
		k.state = StateDead // invalid credentials — never retry silently
	case statusCode == 429 || statusCode == 503:
		wait := llm.ParseRetryAfter(headers, time.Now())
		if wait <= 0 {
			wait = 60 * time.Second
		}
		k.cooldownUntl = time.Now().Add(wait)
		k.state = StateCoolingDown
	case statusCode >= 500 && statusCode < 600:
		if k.consecFails >= 3 {
			k.cooldownUntl = time.Now().Add(30 * time.Second)
			k.state = StateCoolingDown
		} else if k.state == StateHealthy {
			k.state = StateDegraded
		}
	default:
		// network errors and unknown failures count but don't exile
		if k.consecFails >= 5 {
			k.cooldownUntl = time.Now().Add(15 * time.Second)
			k.state = StateCoolingDown
		}
	}
}

// Stats snapshots every key for the dashboard.
func (p *Pool) Stats() []KeyStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]KeyStats, 0, len(p.keys))
	for _, k := range p.keys {
		k.mu.Lock()
		rem := int64(0)
		if k.state == StateCoolingDown {
			if r := k.cooldownUntl.Sub(now); r > 0 {
				rem = int64(r / time.Second)
			}
		}
		out = append(out, KeyStats{
			ID: k.ID, Provider: k.ProviderName, Model: k.Model,
			State: k.state, AvgMs: k.emaMs, InFlight: k.inFlight,
			CooldownRemainS: rem, ConsecFails: k.consecFails, LastUsed: k.lastUsed,
		})
		k.mu.Unlock()
	}
	return out
}

// statusOf unwraps *llm.StatusError and llm.StatusError values.
func statusOf(err error) (code int, headers http.Header, ok bool) {
	var ps *llm.StatusError
	if errors.As(err, &ps) && ps != nil {
		return ps.Code, ps.Headers, true
	}
	var vs llm.StatusError
	if errors.As(err, &vs) {
		return vs.Code, vs.Headers, true
	}
	return 0, nil, false
}

func isContextOverflow(err error) bool {
	s := strings.ToLower(err.Error())
	for _, m := range []string{"context_length_exceeded", "context length", "too many tokens", "max_tokens", "maximum context"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// Complete implements llm.Provider with key failover: it tries each usable
// key at most once and returns ContextOverflowError / ExhaustedError typed
// failures for the engine and TUI layers.
func (p *Pool) Complete(ctx context.Context, req llm.ChatRequest) (string, []llm.ToolCall, error) {
	p.mu.Lock()
	n := len(p.keys)
	p.mu.Unlock()
	var lastErr error
	var detail strings.Builder
	tried := 0
	for tried < n {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		k := p.Pick()
		if k == nil {
			break
		}
		p.mu.Lock()
		fn := p.newClient
		p.mu.Unlock()
		client := fn(k.APIKey, k.BaseURL, k.Model)
		r := req
		if k.Model != "" {
			r.Model = k.Model
		}
		p.ReportStart(k)
		start := time.Now()
		text, calls, err := client.Complete(ctx, r)
		if err == nil {
			p.ReportSuccess(k, time.Since(start))
			return text, calls, nil
		}
		if isContextOverflow(err) {
			p.endFlight(k)
			return "", nil, &ContextOverflowError{Msg: err.Error()}
		}
		if code, headers, ok := statusOf(err); ok {
			p.ReportFailure(k, code, headers)
			lastErr = err
			detail.WriteString(k.ID + ":http" + itoa(code) + " ")
		} else if ctx.Err() != nil {
			p.endFlight(k)
			return "", nil, ctx.Err()
		} else {
			p.ReportFailure(k, 0, nil) // transient/network
			lastErr = err
			detail.WriteString(k.ID + ":net ")
		}
		tried++
	}
	if lastErr == nil {
		lastErr = errors.New("no usable keys (all dead or cooling down)")
		detail.WriteString("no-usable-keys")
	}
	return "", nil, &ExhaustedError{Detail: strings.TrimSpace(detail.String()) + " | last: " + lastErr.Error()}
}

// Chat implements llm.Provider by delegating to the best current key.
// Failover applies at pick time only; mid-stream errors surface directly
// (use Complete when failover across keys is required).
func (p *Pool) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	k := p.Pick()
	if k == nil {
		return nil, &ExhaustedError{Detail: "no usable keys for stream"}
	}
	p.mu.Lock()
	fn := p.newClient
	p.mu.Unlock()
	client := fn(k.APIKey, k.BaseURL, k.Model)
	r := req
	if k.Model != "" {
		r.Model = k.Model
	}
	return client.Chat(ctx, r)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
