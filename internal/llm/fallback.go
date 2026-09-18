// Package llm fallback chain.
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// retryCodes are the HTTP statuses worth failing over on.
var retryCodes = map[int]bool{
	429: true,
	500: true,
	502: true,
	503: true,
}

// statusCodeOf unwraps a *StatusError or StatusError, reporting its code.
func statusCodeOf(err error) (int, bool) {
	var se *StatusError
	if errors.As(err, &se) && se != nil {
		return se.Code, true
	}
	var vse StatusError
	if errors.As(err, &vse) {
		return vse.Code, true
	}
	return 0, false
}

func chunkErrCode(ck Chunk) (int, bool) {
	if ck.Err == nil {
		return 0, false
	}
	return statusCodeOf(ck.Err)
}

// isRetryable reports whether err should trigger failover to the next
// provider. Context cancellation/deadline is never retryable.
func isRetryable(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if code, ok := statusCodeOf(err); ok {
		return retryCodes[code]
	}
	// Unknown (network, connection reset, EOF, ...) errors fail over.
	// Explicit non-retryable client errors encoded as StatusError already
	// returned false above.
	return true
}

// Chain tries providers in order, advancing on retryable failures.
type Chain struct {
	mu        sync.Mutex
	providers []Provider
	lastErr   error
}

// NewChain builds a Chain. At least one provider is expected; an empty
// chain returns an error on every call.
func NewChain(ps ...Provider) *Chain {
	cp := make([]Provider, 0, len(ps))
	for _, p := range ps {
		if p != nil {
			cp = append(cp, p)
		}
	}
	return &Chain{providers: cp}
}

// Name implements Provider.
func (c *Chain) Name() string {
	names := make([]string, 0, len(c.providers))
	for _, p := range c.providers {
		names = append(names, p.Name())
	}
	if len(names) == 0 {
		return "chain(empty)"
	}
	return "chain(" + strings.Join(names, ",") + ")"
}

// LastErr returns the most recent failure.
func (c *Chain) LastErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Chain) record(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr = err
}

// Complete implements Provider with failover.
func (c *Chain) Complete(ctx context.Context, req ChatRequest) (string, []ToolCall, error) {
	if len(c.providers) == 0 {
		err := fmt.Errorf("llm chain: no providers configured")
		c.record(err)
		return "", nil, err
	}
	var last error
	for _, p := range c.providers {
		if err := ctx.Err(); err != nil {
			c.record(err)
			return "", nil, err
		}
		text, tcs, err := p.Complete(ctx, req)
		if err == nil {
			return text, tcs, nil
		}
		last = err
		c.record(err)
		if !isRetryable(ctx, err) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("llm chain: all %d providers failed: %w", len(c.providers), last)
}

// Chat implements Provider with failover, including mid-stream retryable
// chunk errors: the output channel forwards the first provider that yields
// usable chunks, switching to the next provider on retryable failures
// observed before any Done chunk.
func (c *Chain) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if len(c.providers) == 0 {
		return nil, fmt.Errorf("llm chain: no providers configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(chan Chunk, 16)
	go func() {
		defer close(out)
		var last error
		for i, p := range c.providers {
			if ctx.Err() != nil {
				c.record(ctx.Err())
				out <- Chunk{Err: ctx.Err()}
				return
			}
			ch, err := p.Chat(ctx, req)
			if err != nil {
				last = err
				c.record(err)
				if !isRetryable(ctx, err) || i == len(c.providers)-1 {
					out <- Chunk{Err: err}
					return
				}
				continue
			}
			switched := false
			gotDone := false
			for ck := range ch {
				if ck.Err != nil {
					last = ck.Err
					c.record(ck.Err)
					if code, ok := chunkErrCode(ck); ok && !retryCodes[code] {
						out <- ck
						return
					}
					if isRetryable(ctx, ck.Err) && i < len(c.providers)-1 {
						switched = true
						break // try next provider
					}
					out <- ck
					return
				}
				select {
				case <-ctx.Done():
					c.record(ctx.Err())
					out <- Chunk{Err: ctx.Err()}
					return
				case out <- ck:
				}
				if ck.Done {
					gotDone = true
				}
			}
			if gotDone {
				return
			}
			if switched {
				continue
			}
			// Provider closed channel without Done and without error:
			// treat as failure and try next if any remain.
			if i == len(c.providers)-1 {
				if last == nil {
					last = fmt.Errorf("llm chain: provider %s ended stream without Done", p.Name())
					c.record(last)
				}
				return
			}
		}
	}()
	return out, nil
}
