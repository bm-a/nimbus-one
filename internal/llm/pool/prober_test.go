package pool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func cooledPool() (*Pool, *Key) {
	p := NewPool([]KeyConfig{{ID: "k0", APIKey: "k", BaseURL: "http://x"}})
	k := p.keys[0]
	p.ReportStart(k)
	p.ReportSuccess(k, 1000*1000*1000) // 1000ms EMA baseline
	p.ReportFailure(k, 429, nil)       // 60s cooldown
	if p.Pick() != nil {
		panic("fixture broken: key should cool")
	}
	// Expire the timer to simulate "timer ran out".
	k.mu.Lock()
	k.cooldownUntl = time.Now().Add(-time.Second)
	k.mu.Unlock()
	return p, k
}

func TestProberRevivesHealthy(t *testing.T) {
	p, k := cooledPool()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probed := make(chan string, 1)
	go p.StartProber(ctx, 20*time.Millisecond, func(ctx context.Context, tgt ProbeTarget) error {
		if tgt.ID != "k0" || tgt.APIKey != "k" {
			t.Errorf("probe target wrong: %+v", tgt)
		}
		select {
		case probed <- tgt.ID:
		default:
		}
		return nil
	})
	// No Pick() calls until the prober had several ticks, so only the
	// prober (not Pick's lazy path) can revive.
	select {
	case <-probed:
	case <-time.After(2 * time.Second):
		t.Fatal("prober never ran")
	}
	time.Sleep(100 * time.Millisecond) // let the revive land
	if got := p.Pick(); got == nil {
		t.Fatal("revived key never rejoined rotation")
	}
	k.mu.Lock()
	ema := k.emaMs
	k.mu.Unlock()
	if ema != 750 { // (1000 + 500) / 2 decay
		t.Fatalf("revived EMA should decay toward prior, got %v", ema)
	}
}

func TestProberExtendsSick(t *testing.T) {
	p, _ := cooledPool()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.StartProber(ctx, 20*time.Millisecond, func(ctx context.Context, tgt ProbeTarget) error {
		return errors.New("still down")
	})
	time.Sleep(150 * time.Millisecond)
	if got := p.Pick(); got != nil {
		t.Fatal("sick key must stay out of rotation")
	}
	st := p.Stats()[0]
	if st.State != StateCoolingDown || st.CooldownRemainS <= 0 {
		t.Fatalf("cooldown should extend: %+v", st)
	}
}

func TestProberExilesBadCreds(t *testing.T) {
	p, _ := cooledPool()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.StartProber(ctx, 20*time.Millisecond, func(ctx context.Context, tgt ProbeTarget) error {
		return &probeAuthError{provider: "x"}
	})
	time.Sleep(150 * time.Millisecond)
	if st := p.Stats()[0]; st.State != StateDead {
		t.Fatalf("bad creds must exile, got %s", st.State)
	}
}

func TestProberNilProbeRevives(t *testing.T) {
	p, _ := cooledPool()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.StartProber(ctx, 20*time.Millisecond, nil)
	deadline := time.Now().Add(2 * time.Second)
	for p.Pick() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.Pick() == nil {
		t.Fatal("nil probe should revive on timer expiry")
	}
}

func TestModelsProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(200)
	}))
	defer ok.Close()
	if err := ModelsProbe(context.Background(), ok.URL, "k"); err != nil {
		t.Fatalf("200 should pass: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer bad.Close()
	if err := ModelsProbe(context.Background(), bad.URL, "k"); !IsAuthFailure(err) {
		t.Fatalf("401 must be auth failure: %v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer down.Close()
	if err := ModelsProbe(context.Background(), down.URL, "k"); err == nil || IsAuthFailure(err) {
		t.Fatalf("503 must be plain failure: %v", err)
	}
	if err := ModelsProbe(context.Background(), "https://api.anthropic.com", "k"); err != nil {
		t.Fatalf("anthropic skipped: %v", err)
	}
	if err := ModelsProbe(context.Background(), "", "k"); err != nil {
		t.Fatalf("empty base skipped: %v", err)
	}
}
