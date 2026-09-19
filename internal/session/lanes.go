// Lane Queue (Phase 3): per-session serial execution.
//
// Any agent reachable from concurrent inputs (channels + scheduler +
// interactive) must never mutate one session from two goroutines. A Lane
// is a named mutex with context-aware acquisition: Acquire blocks until
// the lane is free or ctx cancels; Release must always run (defer it).
// Lanes are created on demand and never deleted — the key space (session
// IDs) is bounded by the session table.
package session

import (
	"context"
	"fmt"
	"sync"
)

// Lanes guards named critical sections.
type Lanes struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

// NewLanes creates an empty lane set.
func NewLanes() *Lanes { return &Lanes{m: map[string]chan struct{}{}} }

func (l *Lanes) lane(id string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch, ok := l.m[id]
	if !ok {
		ch = make(chan struct{}, 1)
		l.m[id] = ch
	}
	return ch
}

// Acquire takes the lane, blocking until free or ctx done. The returned
// release func must be called exactly once (defer it).
func (l *Lanes) Acquire(ctx context.Context, id string) (release func(), err error) {
	if id == "" {
		return nil, fmt.Errorf("session: empty lane id")
	}
	ch := l.lane(id)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Held is a non-blocking probe for tests and health checks.
func (l *Lanes) Held(id string) bool {
	l.mu.Lock()
	ch, ok := l.m[id]
	l.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- struct{}{}:
		<-ch
		return false
	default:
		return true
	}
}
