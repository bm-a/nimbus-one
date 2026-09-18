package pool

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"nimbus-one/internal/llm"
)

type stub struct {
	name  string
	text  string
	err   error
	calls int
}

func (s *stub) Name() string { return s.name }
func (s *stub) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 1)
	ch <- llm.Chunk{Delta: s.text, Done: true}
	close(ch)
	return ch, s.err
}
func (s *stub) Complete(ctx context.Context, req llm.ChatRequest) (string, []llm.ToolCall, error) {
	s.calls++
	if s.err != nil {
		return "", nil, s.err
	}
	return s.text, nil, nil
}

func testPool(stubs ...*stub) (*Pool, []*stub) {
	cfgs := make([]KeyConfig, len(stubs))
	for i, s := range stubs {
		cfgs[i] = KeyConfig{ID: s.name, ProviderName: "p", Model: "m", APIKey: "k"}
	}
	p := NewPool(cfgs)
	idx := map[string]*stub{}
	for _, s := range stubs {
		idx[s.name] = s
	}
	p.SetClientFactory(func(apiKey, baseURL, model string) llm.Provider {
		// factory can't know which key; round-robin by calls: use first stub with fewest calls
		var best *stub
		for _, s := range stubs {
			if best == nil || s.calls < best.calls {
				best = s
			}
		}
		return best
	})
	_ = idx
	return p, stubs
}

func TestPickPrefersLowEMA(t *testing.T) {
	p, _ := testPool(&stub{name: "a"}, &stub{name: "b"})
	ka, kb := p.keys[0], p.keys[1]
	p.ReportStart(ka)
	p.ReportSuccess(ka, 2000*time.Millisecond)
	p.ReportStart(kb)
	p.ReportSuccess(kb, 100*time.Millisecond)
	got := p.Pick()
	if got != kb {
		t.Fatalf("expected fast key b, got %v", got.ID)
	}
}

func TestFailure429CoolsWithRetryAfter(t *testing.T) {
	p, _ := testPool(&stub{name: "a"})
	k := p.keys[0]
	h := http.Header{"Retry-After": []string{"120"}}
	p.ReportFailure(k, 429, h)
	st := p.Stats()[0]
	if st.State != StateCoolingDown {
		t.Fatalf("expected CoolingDown, got %s", st.State)
	}
	if st.CooldownRemainS < 100 || st.CooldownRemainS > 125 {
		t.Fatalf("cooldown remaining out of range: %d", st.CooldownRemainS)
	}
	if p.Pick() != nil {
		t.Fatal("cooling key must not be picked")
	}
}

func TestFailure401Kills(t *testing.T) {
	p, _ := testPool(&stub{name: "a"})
	k := p.keys[0]
	p.ReportFailure(k, 401, nil)
	if p.Stats()[0].State != StateDead {
		t.Fatal("401 must mark key Dead")
	}
	if p.Pick() != nil {
		t.Fatal("dead key must not be picked")
	}
}

func TestExpiredCooldownRecovers(t *testing.T) {
	p, _ := testPool(&stub{name: "a"})
	k := p.keys[0]
	p.ReportFailure(k, 503, nil) // default 60s cooldown
	k.mu.Lock()
	k.cooldownUntl = time.Now().Add(-time.Second)
	k.mu.Unlock()
	if p.Pick() == nil {
		t.Fatal("expired cooldown should recover to pickable")
	}
	if p.Stats()[0].State != StateHealthy {
		t.Fatal("expected Healthy after recovery")
	}
}

func TestCompleteFailsOver(t *testing.T) {
	s1 := &stub{name: "bad", err: &llm.StatusError{Code: 429, Body: "slow down"}}
	s2 := &stub{name: "good", text: "ok"}
	p, _ := testPool(s1, s2)
	text, _, err := p.Complete(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatalf("expected failover success: %v", err)
	}
	if text != "ok" {
		t.Fatalf("unexpected text %q", text)
	}
	if s1.calls != 1 || s2.calls != 1 {
		t.Fatalf("each stub should be called once (bad=%d good=%d)", s1.calls, s2.calls)
	}
}

func TestCompleteExhausted(t *testing.T) {
	s1 := &stub{name: "x", err: &llm.StatusError{Code: 500, Body: "down"}}
	p, _ := testPool(s1)
	// force single-key pool to exhaust: 1 key, 1 attempt
	_, _, err := p.Complete(context.Background(), llm.ChatRequest{})
	var ex *ExhaustedError
	if !errors.As(err, &ex) {
		t.Fatalf("expected ExhaustedError, got %v", err)
	}
	if !strings.Contains(ex.Error(), "x") {
		t.Fatalf("detail should name key: %v", ex)
	}
}

func TestCompleteNoKeys(t *testing.T) {
	p := NewPool(nil)
	_, _, err := p.Complete(context.Background(), llm.ChatRequest{})
	var ex *ExhaustedError
	if !errors.As(err, &ex) {
		t.Fatalf("expected ExhaustedError, got %v", err)
	}
}
