// Second-generation fallback chain: deduped candidates, session
// skip-cache, per-candidate observability.
//
// OpenClaw reference (read-only):
//
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/agents/model-fallback-candidates.ts
//	  (candidate origins, dedupe)
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/agents/model-fallback-runner.ts
//	  (cooldown-probe, skip-cache, observability)
//
// NOTE on pool.ExhaustedError: pool imports llm, so reusing that type here
// would be an import cycle. Chain2 therefore defines its own local
// Exhausted error with the same spirit (typed detail + wrapped cause
// chain); callers can distinguish it with errors.As.
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Candidate origins.
const (
	OriginRequested = "requested"
	OriginFallback  = "fallback"
	OriginPrimary   = "primary"
)

// Candidate is one attemptable model route.
type Candidate struct {
	Model    string // model id attempted for this candidate
	Origin   string // requested | fallback | primary
	Provider string // provider name (observability)
	Key      string // key label, if known (observability only)
}

// Exhausted is returned when every candidate failed. Detail is a compact
// "provider/model: error" trace; Errs preserves the full per-candidate
// error chain for errors.Is/As.
type Exhausted struct {
	Detail string
	Errs   []error
}

// Error implements error.
func (e *Exhausted) Error() string {
	if strings.TrimSpace(e.Detail) == "" {
		return "llm chain2: all candidates exhausted"
	}
	return "llm chain2: all candidates exhausted: " + e.Detail
}

// Unwrap exposes the per-candidate error chain.
func (e *Exhausted) Unwrap() []error { return e.Errs }

// errSkipped marks a candidate bypassed by the session skip-cache.
var errSkipped = errors.New("llm chain2: skipped (cached session failure)")

// SkipKey builds the session skip-cache key for a provider/model pair.
func SkipKey(provider, model string) string {
	return provider + "\x00" + model
}

// Chain2 tries providers in order with dedupe, session skip-cache, and a
// per-candidate observation hook. It complements Chain (fallback.go) with
// candidate identity tracking; Chain remains the plain failover path.
type Chain2 struct {
	// Providers are tried in order. Nil entries are dropped at Run time.
	Providers []Provider
	// Models optionally overrides the request model per provider index;
	// empty entries (or a short slice) fall back to req.Model.
	Models []string
	// Keys optionally labels the credential per provider index for
	// Candidate.Key observability. Never logged by this file.
	Keys []string
	// Primary optionally names a model id whose candidates are labelled
	// origin "primary" instead of "fallback".
	Primary string
	// OnStep observes every candidate outcome: nil error on success, the
	// attempt error on failure, errSkipped-class on skip-cache bypass.
	// A panicking hook is not recovered; keep hooks small.
	OnStep func(Candidate, error)
	// Skip is the session skip-cache: Skip[SkipKey(provider, model)]
	// bypasses candidates that already failed this session (mirrors
	// OpenClaw's fallback-skip-cache for non-primary auth failures).
	Skip map[string]bool
}

// slot pairs a provider with its deduped candidate.
type slot struct {
	p Provider
	c Candidate
}

// slots builds the deduped attempt list for req.
func (c *Chain2) slots(req ChatRequest) []slot {
	var out []slot
	seen := map[string]bool{}
	first := true
	for i, p := range c.Providers {
		if p == nil {
			continue
		}
		model := req.Model
		if i < len(c.Models) && strings.TrimSpace(c.Models[i]) != "" {
			model = c.Models[i]
		}
		k := SkipKey(p.Name(), model)
		if seen[k] {
			continue // identical provider/model already queued
		}
		seen[k] = true
		origin := OriginRequested
		if !first {
			origin = OriginFallback
		}
		first = false
		if c.Primary != "" && model == c.Primary {
			origin = OriginPrimary
		}
		var key string
		if i < len(c.Keys) {
			key = c.Keys[i]
		}
		out = append(out, slot{
			p: p,
			c: Candidate{Model: model, Origin: origin, Provider: p.Name(), Key: key},
		})
	}
	return out
}

// candidates reports the deduped attempt list for req (observability).
func (c *Chain2) candidates(req ChatRequest) []Candidate {
	slots := c.slots(req)
	out := make([]Candidate, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.c)
	}
	return out
}

// Run attempts each candidate in order and returns the first success.
// Every candidate failure (and skip) is reported to OnStep. When all
// candidates fail, Run returns *Exhausted carrying the detail trace plus
// the full error chain. Context cancellation aborts immediately and is
// returned as-is (not wrapped in Exhausted).
func (c *Chain2) Run(ctx context.Context, req ChatRequest) (string, []ToolCall, error) {
	slots := c.slots(req)
	if len(slots) == 0 {
		return "", nil, &Exhausted{Detail: "no providers configured"}
	}
	var errs []error
	var detail strings.Builder
	for _, s := range slots {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		if c.Skip[SkipKey(s.c.Provider, s.c.Model)] {
			if c.OnStep != nil {
				c.OnStep(s.c, errSkipped)
			}
			_, _ = fmt.Fprintf(&detail, "%s/%s: skipped ", s.c.Provider, s.c.Model)
			continue
		}
		r := req
		r.Model = s.c.Model
		text, tcs, err := s.p.Complete(ctx, r)
		if c.OnStep != nil {
			c.OnStep(s.c, err)
		}
		if err == nil {
			return text, tcs, nil
		}
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s/%s: %w", s.c.Provider, s.c.Model, err))
		_, _ = fmt.Fprintf(&detail, "%s/%s: %v ", s.c.Provider, s.c.Model, err)
	}
	last := ""
	if d := strings.TrimSpace(detail.String()); d != "" {
		last = d
	} else {
		last = "no candidates attempted"
	}
	return "", nil, &Exhausted{Detail: last, Errs: errs}
}
