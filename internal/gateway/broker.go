// Package gateway multiplexes inbound chat channels onto an engine.
package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// engineIface mirrors internal/engine Engine.Run without importing it
// (avoids an import cycle: engine may import gateway types in future).
type engineIface interface {
	Run(ctx context.Context, system, user string) (string, error)
}

// Inbound is a single incoming message from any channel.
type Inbound struct {
	Channel string
	User    string
	Text    string
	Time    time.Time
}

// Broker routes inbound messages to the engine with per-user sessions
// and optional per-channel reply handlers.
type Broker struct {
	Eng      engineIface
	System   string
	inbound  chan Inbound
	mu       sync.Mutex
	handlers map[string]func(string)
	sessions *Sessions
}

// New creates a Broker. eng may be nil (Handle then returns an error string).
func New(eng engineIface, system string) *Broker {
	return &Broker{
		Eng:      eng,
		System:   system,
		inbound:  make(chan Inbound, 64),
		handlers: make(map[string]func(string)),
		sessions: NewSessions(),
	}
}

// On registers a reply handler invoked by Start for a given channel.
func (b *Broker) On(channel string, fn func(string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = make(map[string]func(string))
	}
	b.handlers[channel] = fn
}

// SessionsStore exposes the session store (for REPL /reset etc).
func (b *Broker) SessionsStore() *Sessions {
	if b.sessions == nil {
		b.sessions = NewSessions()
	}
	return b.sessions
}

// ResetSession clears history for channel/user.
func (b *Broker) ResetSession(channel, user string) {
	b.SessionsStore().Reset(Key(channel, user))
}

// modeSetter is implemented by engines supporting process-wide plan/build mode.
type modeSetter interface {
	SetMode(string)
	CurrentMode() string
}

// SetMode switches the engine to "plan" or "build" process-wide.
// Unknown values normalize to build. No-op when the engine lacks support.
func (b *Broker) SetMode(m string) {
	if b == nil || b.Eng == nil {
		return
	}
	if ms, ok := b.Eng.(modeSetter); ok {
		ms.SetMode(m)
	}
}

// Mode returns the engine's active process-wide mode ("build" when unknown).
func (b *Broker) Mode() string {
	if b == nil || b.Eng == nil {
		return "build"
	}
	if ms, ok := b.Eng.(modeSetter); ok {
		return ms.CurrentMode()
	}
	return "build"
}

// Handle synchronously runs one message through the engine.
// It records user/assistant turns in the session store keyed by channel/user.
// Panics from the engine are recovered and returned as strings.
func (b *Broker) Handle(channel, user, text string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprintf("engine panic: %v", r)
		}
	}()
	if b.Eng == nil {
		return "engine unavailable"
	}
	key := Key(channel, user)
	b.SessionsStore().Append(key, "user", text)
	prompt := "[" + channel + "/" + user + "] " + text
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	reply, err := b.Eng.Run(ctx, b.System, prompt)
	if err != nil {
		return "error: " + err.Error()
	}
	b.SessionsStore().Append(key, "assistant", reply)
	return reply
}

// Enqueue submits an inbound message to the Start dispatch loop.
func (b *Broker) Enqueue(m Inbound) {
	if b.inbound == nil {
		return
	}
	if m.Time.IsZero() {
		m.Time = time.Now()
	}
	b.inbound <- m
}

// Start dispatches queued inbound messages until ctx is done.
// Each message is run via Handle; if a handler is registered for the
// message channel it receives the reply. Handler panics are recovered.
func (b *Broker) Start(ctx context.Context) {
	if b.inbound == nil {
		<-ctx.Done()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-b.inbound:
			reply := b.Handle(m.Channel, m.User, m.Text)
			b.mu.Lock()
			h := b.handlers[m.Channel]
			b.mu.Unlock()
			if h != nil {
				func() {
					defer func() { _ = recover() }()
					h(reply)
				}()
			}
		}
	}
}
