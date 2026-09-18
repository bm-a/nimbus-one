// Package daemon provides background schedulers and workers.
package daemon

import (
	"context"
	"sync"
	"time"
)

// Scheduler calls Fn on a fixed interval until ctx is done or Stop is called.
type Scheduler struct {
	Every time.Duration
	Fn    func(ctx context.Context)

	mu     sync.Mutex
	stopCh chan struct{}
	once   sync.Once
}

// ParseEvery parses strings like "15m", "30m", "1h" into a duration.
// It accepts any Go duration string; empty/invalid/<=0 yields 15m default.
func ParseEvery(s string) time.Duration {
	const def = 15 * time.Minute
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// Start runs the ticker loop, calling Fn each interval until ctx is done
// or Stop is called. A non-positive Every defaults to 15m.
func (s *Scheduler) Start(ctx context.Context) {
	every := s.Every
	if every <= 0 {
		every = 15 * time.Minute
	}
	s.mu.Lock()
	if s.stopCh == nil {
		s.stopCh = make(chan struct{})
	}
	stopCh := s.stopCh
	s.mu.Unlock()

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-t.C:
			if s.Fn != nil {
				func() {
					defer func() { _ = recover() }()
					s.Fn(ctx)
				}()
			}
		}
	}
}

// Stop signals a running Start loop to exit. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopCh == nil {
		s.stopCh = make(chan struct{})
	}
	ch := s.stopCh
	s.mu.Unlock()
	s.once.Do(func() { close(ch) })
}
