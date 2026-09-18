package gateway

import "sync"

// SessionMsg is a single stored turn. Role is "user", "assistant" or "system".
// Content is the raw text. This intentionally mirrors llm.Message without
// importing internal/llm.
type SessionMsg struct {
	Role    string
	Content string
}

// Sessions stores per-key histories. Key namespaces by channel/user
// so REPL, HTTP, Telegram, Discord etc. never share context.
type Sessions struct {
	mu sync.Mutex
	m  map[string][]SessionMsg
}

// NewSessions creates an empty store.
func NewSessions() *Sessions {
	return &Sessions{m: make(map[string][]SessionMsg)}
}

// Key returns the isolation key for a channel/user pair.
func Key(channel, user string) string {
	return channel + "/" + user
}

// Append adds a turn to key's history.
func (s *Sessions) Append(key, role, content string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string][]SessionMsg)
	}
	s.m[key] = append(s.m[key], SessionMsg{Role: role, Content: content})
}

// History returns up to the last n messages for key (n<=0 means all).
// The returned slice is a copy.
func (s *Sessions) History(key string, n int) []SessionMsg {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.m[key]
	if len(h) == 0 {
		return nil
	}
	if n <= 0 || n >= len(h) {
		out := make([]SessionMsg, len(h))
		copy(out, h)
		return out
	}
	out := make([]SessionMsg, n)
	copy(out, h[len(h)-n:])
	return out
}

// Reset clears history for key.
func (s *Sessions) Reset(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}
