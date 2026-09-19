package gateway

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GroupPolicy holds per-group behavior overrides.
type GroupPolicy struct {
	RequireMention bool
	AllowedTools   []string
	DeniedTools    []string
	SystemPrompt   string
}

// PolicyStore holds Telegram group gating, chunking, and health state.
// The Telegram struct itself is owned elsewhere, so policy lives here and
// telegram.go consults it via tiny hooks.
type PolicyStore struct {
	Mu                 sync.RWMutex
	GroupPolicy        string // open|disabled|allowlist ("" = open)
	GroupAllow         []string
	Groups             map[int64]GroupPolicy
	TextChunkLimit     int
	StreamingChunkMode string // length|newline ("" = length)
	HealthOK           bool
	LastOK             time.Time
	FailStreak         int
}

// groupMode normalizes the group policy name; empty means open.
func (p *PolicyStore) groupMode() string {
	if p == nil {
		return "open"
	}
	p.Mu.RLock()
	defer p.Mu.RUnlock()
	m := strings.ToLower(strings.TrimSpace(p.GroupPolicy))
	if m == "" {
		return "open"
	}
	return m
}

// IsGroupAllowed reports whether chatID/chatType may use the bot.
// Private (DM) chats are always allowed; group-like chats follow the
// group policy: open allows all, disabled blocks all, allowlist allows
// only chat IDs listed in GroupAllow.
func (p *PolicyStore) IsGroupAllowed(chatID int64, chatType string) bool {
	ct := strings.ToLower(strings.TrimSpace(chatType))
	if ct == "" || ct == "private" || ct == "dm" {
		return true
	}
	switch p.groupMode() {
	case "disabled":
		return false
	case "allowlist":
		if p == nil {
			return false
		}
		id := strconv.FormatInt(chatID, 10)
		p.Mu.RLock()
		defer p.Mu.RUnlock()
		for _, a := range p.GroupAllow {
			if strings.TrimSpace(a) == id {
				return true
			}
		}
		return false
	default: // open and unknown values stay permissive
		return true
	}
}

// GroupConfig returns the per-group policy for chatID, or the zero
// GroupPolicy when none is configured.
func (p *PolicyStore) GroupConfig(chatID int64) GroupPolicy {
	if p == nil {
		return GroupPolicy{}
	}
	p.Mu.RLock()
	defer p.Mu.RUnlock()
	if p.Groups == nil {
		return GroupPolicy{}
	}
	return p.Groups[chatID]
}

// ChunkLimit returns the configured text chunk size, defaulting to 4000.
func (p *PolicyStore) ChunkLimit() int {
	if p == nil {
		return 4000
	}
	p.Mu.RLock()
	defer p.Mu.RUnlock()
	if p.TextChunkLimit <= 0 {
		return 4000
	}
	return p.TextChunkLimit
}

// ShouldMention reports whether an incoming text passes mention gating:
// true when the text contains @botName (case-insensitive), when botName
// is empty, or when no configured group requires a mention.
func (p *PolicyStore) ShouldMention(text string, botName string) bool {
	botName = strings.TrimSpace(botName)
	if botName == "" {
		return true
	}
	if strings.Contains(strings.ToLower(text), "@"+strings.ToLower(botName)) {
		return true
	}
	if p == nil {
		return true
	}
	p.Mu.RLock()
	defer p.Mu.RUnlock()
	for _, g := range p.Groups {
		if g.RequireMention {
			return false
		}
	}
	return true
}

// StreamingSender incrementally forwards buffered text via Send without
// any network logic of its own. Mode "newline" flushes complete lines;
// any other mode ("length", "") flushes every MaxChars characters
// (default 4000). Flush sends whatever is buffered.
type StreamingSender struct {
	Send     func(chunk string) error
	Mode     string
	MaxChars int
	buf      strings.Builder
}

func (s *StreamingSender) mode() string {
	m := strings.ToLower(strings.TrimSpace(s.Mode))
	if m == "" {
		return "length"
	}
	return m
}

func (s *StreamingSender) maxChars() int {
	if s.MaxChars <= 0 {
		return 4000
	}
	return s.MaxChars
}

func (s *StreamingSender) flushChunk(chunk string) error {
	if chunk == "" {
		return nil
	}
	if s.Send == nil {
		return fmt.Errorf("streaming sender: nil Send func")
	}
	return s.Send(chunk)
}

// Write buffers text and flushes according to the mode.
func (s *StreamingSender) Write(text string) error {
	if text == "" {
		return nil
	}
	s.buf.WriteString(text)
	if s.mode() == "newline" {
		data := s.buf.String()
		idx := strings.LastIndex(data, "\n")
		if idx < 0 {
			return nil
		}
		chunk := data[:idx+1]
		s.buf.Reset()
		s.buf.WriteString(data[idx+1:])
		return s.flushChunk(chunk)
	}
	max := s.maxChars()
	for s.buf.Len() >= max {
		data := s.buf.String()
		chunk := data[:max]
		s.buf.Reset()
		s.buf.WriteString(data[max:])
		if err := s.flushChunk(chunk); err != nil {
			return err
		}
	}
	return nil
}

// Flush sends any buffered remainder.
func (s *StreamingSender) Flush() error {
	if s.buf.Len() == 0 {
		return nil
	}
	chunk := s.buf.String()
	s.buf.Reset()
	return s.flushChunk(chunk)
}

// HealthMonitor tracks consecutive Telegram failures and notifies once
// the connection looks unhealthy (streak >= 3). A success resets the
// streak. All methods are safe for concurrent use.
type HealthMonitor struct {
	Mu         sync.Mutex
	FailStreak int
	LastOK     time.Time
	Notify     func(string)
}

// RecordSuccess resets the failure streak and stamps LastOK.
func (h *HealthMonitor) RecordSuccess() {
	if h == nil {
		return
	}
	h.Mu.Lock()
	defer h.Mu.Unlock()
	h.FailStreak = 0
	h.LastOK = time.Now()
}

// RecordFailure bumps the streak and, on the transition to unhealthy
// (streak reaching exactly 3), calls Notify once.
func (h *HealthMonitor) RecordFailure(err error) {
	if h == nil {
		return
	}
	h.Mu.Lock()
	defer h.Mu.Unlock()
	h.FailStreak++
	if h.FailStreak == 3 && h.Notify != nil {
		msg := "telegram unhealthy: repeated poll/send failures"
		if err != nil {
			msg = "telegram unhealthy: " + shortErr(err)
		}
		h.Notify(msg)
	}
}

// IsHealthy reports whether the streak is below the trip threshold.
func (h *HealthMonitor) IsHealthy() bool {
	if h == nil {
		return true
	}
	h.Mu.Lock()
	defer h.Mu.Unlock()
	return h.FailStreak < 3
}
