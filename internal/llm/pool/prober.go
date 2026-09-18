package pool

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ProbeTarget is the read-only key material handed to probe functions.
type ProbeTarget struct {
	ID       string
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

// ProbeFunc checks whether a cooled-down key is usable again.
// Nil probe means revive-without-probe (next real request validates,
// failover still protects it).
type ProbeFunc func(ctx context.Context, t ProbeTarget) error

// snapshot extracts probe material for one key.
func snapshot(k *Key) ProbeTarget {
	k.mu.Lock()
	defer k.mu.Unlock()
	return ProbeTarget{ID: k.ID, Provider: k.ProviderName, Model: k.Model, APIKey: k.APIKey, BaseURL: k.BaseURL}
}

// revive marks a key Healthy with decayed latency so it can win traffic
// back on merit instead of jumping the queue or starving forever.
func (p *Pool) revive(k *Key) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.state = StateHealthy
	k.consecFails = 0
	k.cooldownUntl = time.Time{}
	k.emaMs = (k.emaMs + 500) / 2
}

// extend pushes a still-sick key back into cooldown with growing backoff.
func (p *Pool) extend(k *Key) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.consecFails++
	backoff := time.Duration(60*(1<<min(k.consecFails, 4))) * time.Second
	k.cooldownUntl = time.Now().Add(backoff)
	k.state = StateCoolingDown
}

// exile kills a key the probe proved invalid (bad credentials).
func (p *Pool) exile(k *Key) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.state = StateDead
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// StartProber blocks until ctx ends, periodically scanning for keys whose
// cooldown expired: it probes them and switches healthy ones back into
// rotation. Sick keys get extended backoff instead of hammering providers.
// Interval <=0 defaults to 60s. Never fails — probe errors only extend.
func (p *Pool) StartProber(ctx context.Context, interval time.Duration, probe ProbeFunc) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.probeOnce(ctx, probe)
		}
	}
}

func (p *Pool) probeOnce(ctx context.Context, probe ProbeFunc) {
	p.mu.Lock()
	keys := append([]*Key{}, p.keys...)
	p.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		k.mu.Lock()
		cooling := k.state == StateCoolingDown
		expired := !now.Before(k.cooldownUntl)
		k.mu.Unlock()
		if !cooling || !expired {
			continue
		}
		target := snapshot(k)
		if probe == nil {
			p.revive(k) // next real request is the probe; failover guards it
			continue
		}
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := probe(c, target)
		cancel()
		if err == nil {
			p.revive(k)
		} else if IsAuthFailure(err) {
			p.exile(k) // credentials dead — never silently retried
		} else {
			p.extend(k)
		}
	}
}

// ModelsProbe is a token-free health check for OpenAI-compatible endpoints:
// GET {base}/models must answer 200. Providers without that surface
// (Anthropic native, empty base) are skipped as healthy-unknown.
func ModelsProbe(ctx context.Context, baseURL, apiKey string) error {
	base := strings.TrimSpace(strings.TrimSuffix(baseURL, "/"))
	if base == "" || strings.Contains(base, "anthropic.com") {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("User-Agent", "NimbusOne-Prober/1.0")
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		// Key itself is bad — do NOT revive; report so the pool can exile.
		return &probeAuthError{provider: base}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 500 {
		return &probeError{msg: "models endpoint status"}
	}
	return nil // 2xx/429/404 all mean "endpoint alive, timer was the issue"
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return "prober: " + e.msg }

type probeAuthError struct{ provider string }

func (e *probeAuthError) Error() string { return "prober: credentials rejected" }

// IsAuthFailure reports a probe that proved the KEY bad (pool should exile
// via ReportFailure 401 semantics, not merely extend cooldown).
func IsAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*probeAuthError)
	return ok
}
