// Session lifecycle + transcript store, JSONL-backed.
//
// OpenClaw references (read-only):
//
//	src/config/sessions/session-accessor.sqlite-*.ts — SessionEntry with
//	  lifecycleRevision / spawnDepth / spawnedBy, transcript_events table,
//	  and optimistic fencing (writers pass an expected lifecycleRevision;
//	  a mismatch aborts the write).
//	src/config/sessions/session-manager-branching.ts — fork semantics
//	  (child inherits a transcript copy at spawnDepth+1, spawnedBy=parent).
//
// Session keys are parsed locally (no internal/agents import):
// agent:<id>:<rest>; the main session is agent:<id>:main.
//
// Layout: Dir/sessions/sessions.jsonl holds one line per session and is
// rewritten atomically on mutation; Dir/sessions/transcript.jsonl is a
// pure append-only log of TranscriptEvent records.
package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Session lifecycle errors. Use errors.Is to match.
var (
	ErrSessionNotFound  = errors.New("state: session not found")
	ErrSessionExists    = errors.New("state: session already exists")
	ErrRevisionMismatch = errors.New("state: lifecycle revision mismatch")
	ErrSessionArchived  = errors.New("state: session is archived")
)

// Session is a single agent session lifecycle record.
type Session struct {
	Key          string    `json:"key"`
	AgentID      string    `json:"agent_id"`
	LifecycleRev int64     `json:"lifecycle_rev"`
	SpawnDepth   int       `json:"spawn_depth"`
	SpawnedBy    string    `json:"spawned_by"`
	Archived     bool      `json:"archived"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TranscriptEvent is one role/content entry in a session transcript.
type TranscriptEvent struct {
	Seq        int64     `json:"seq"`
	SessionKey string    `json:"session_key"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	At         time.Time `json:"at"`
}

// ParseSessionKey splits agent:<id>:<rest> into its agent id and rest parts.
// It returns an error for anything that is not exactly that shape.
func ParseSessionKey(key string) (agentID, rest string, err error) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 || parts[0] != "agent" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("state: invalid session key %q (want agent:<id>:<rest>)", key)
	}
	return parts[1], parts[2], nil
}

// IsMainSession reports whether key is a main session (agent:<id>:main).
func IsMainSession(key string) bool {
	_, rest, err := ParseSessionKey(key)
	return err == nil && rest == "main"
}

// SessionStore persists sessions + transcripts as JSONL under Dir/sessions/.
type SessionStore struct {
	Dir string

	mu sync.Mutex
}

// OpenSessions creates Dir/sessions/ and returns a SessionStore rooted at Dir.
func OpenSessions(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		return nil, err
	}
	return &SessionStore{Dir: dir}, nil
}

func (s *SessionStore) sessionsPath() string {
	return filepath.Join(s.Dir, "sessions", "sessions.jsonl")
}
func (s *SessionStore) transcriptPath() string {
	return filepath.Join(s.Dir, "sessions", "transcript.jsonl")
}

// Create registers a new session. LifecycleRev starts at 1. Duplicate keys
// and malformed keys are rejected.
func (s *SessionStore) Create(key, spawnedBy string, depth int) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agentID, _, err := ParseSessionKey(key)
	if err != nil {
		return Session{}, err
	}
	if depth < 0 {
		return Session{}, fmt.Errorf("state: negative spawn depth %d", depth)
	}
	sessions, err := s.readSessionsLocked()
	if err != nil {
		return Session{}, err
	}
	for _, e := range sessions {
		if e.Key == key {
			return Session{}, fmt.Errorf("%w: %q", ErrSessionExists, key)
		}
	}
	now := time.Now().UTC()
	sess := Session{
		Key:          key,
		AgentID:      agentID,
		LifecycleRev: 1,
		SpawnDepth:   depth,
		SpawnedBy:    spawnedBy,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	sessions = append(sessions, sess)
	if err := s.writeSessionsLocked(sessions); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Get returns the session record for key.
func (s *SessionStore) Get(key string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findLocked(key)
}

// Append adds a transcript event at the session's current lifecycle revision.
// It fails on archived sessions.
func (s *SessionStore) Append(key, role, content string) (TranscriptEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.findLocked(key)
	if err != nil {
		return TranscriptEvent{}, err
	}
	return s.appendLocked(sess, role, content)
}

// AppendAtRev adds a transcript event only if the session's current
// lifecycle revision equals expectedRev (optimistic fencing). A mismatch
// returns an error wrapping ErrRevisionMismatch and writes nothing.
func (s *SessionStore) AppendAtRev(key, role, content string, expectedRev int64) (TranscriptEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.findLocked(key)
	if err != nil {
		return TranscriptEvent{}, err
	}
	if sess.LifecycleRev != expectedRev {
		return TranscriptEvent{}, fmt.Errorf("%w: session %q at rev %d, writer expected %d",
			ErrRevisionMismatch, key, sess.LifecycleRev, expectedRev)
	}
	return s.appendLocked(sess, role, content)
}

// Recent returns up to the last n events for key in chronological order.
// n <= 0 returns all events.
func (s *SessionStore) Recent(key string, n int) ([]TranscriptEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.findLocked(key); err != nil {
		return nil, err
	}
	events, err := s.readTranscriptLocked()
	if err != nil {
		return nil, err
	}
	var filtered []TranscriptEvent
	for _, e := range events {
		if e.SessionKey == key {
			filtered = append(filtered, e)
		}
	}
	if n > 0 && len(filtered) > n {
		filtered = filtered[len(filtered)-n:]
	}
	return filtered, nil
}

// Fork creates childKey as a branch of parentKey: the child starts at
// LifecycleRev 1 with SpawnDepth parent+1 and SpawnedBy set to the parent
// key, and receives a copy of the parent transcript under its own sequence
// numbers. Later appends to either session are isolated from the other.
func (s *SessionStore) Fork(parentKey, childKey string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, err := s.findLocked(parentKey)
	if err != nil {
		return Session{}, err
	}
	agentID, _, err := ParseSessionKey(childKey)
	if err != nil {
		return Session{}, err
	}
	sessions, err := s.readSessionsLocked()
	if err != nil {
		return Session{}, err
	}
	for _, e := range sessions {
		if e.Key == childKey {
			return Session{}, fmt.Errorf("%w: %q", ErrSessionExists, childKey)
		}
	}
	events, err := s.readTranscriptLocked()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	child := Session{
		Key:          childKey,
		AgentID:      agentID,
		LifecycleRev: 1,
		SpawnDepth:   parent.SpawnDepth + 1,
		SpawnedBy:    parentKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	sessions = append(sessions, child)
	var copied []TranscriptEvent
	var seq int64
	for _, e := range events {
		if e.SessionKey != parentKey {
			continue
		}
		seq++
		copied = append(copied, TranscriptEvent{
			Seq:        seq,
			SessionKey: childKey,
			Role:       e.Role,
			Content:    e.Content,
			At:         e.At,
		})
	}
	if err := s.appendEventsLocked(copied); err != nil {
		return Session{}, err
	}
	if err := s.writeSessionsLocked(sessions); err != nil {
		return Session{}, err
	}
	return child, nil
}

// Archive freezes a session: it sets Archived and bumps LifecycleRev so
// that in-flight writers holding a stale revision fail fencing.
func (s *SessionStore) Archive(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.readSessionsLocked()
	if err != nil {
		return err
	}
	for i, e := range sessions {
		if e.Key == key {
			if e.Archived {
				return nil
			}
			sessions[i].Archived = true
			sessions[i].LifecycleRev++
			sessions[i].UpdatedAt = time.Now().UTC()
			return s.writeSessionsLocked(sessions)
		}
	}
	return fmt.Errorf("%w: %q", ErrSessionNotFound, key)
}

func (s *SessionStore) findLocked(key string) (Session, error) {
	sessions, err := s.readSessionsLocked()
	if err != nil {
		return Session{}, err
	}
	for _, e := range sessions {
		if e.Key == key {
			return e, nil
		}
	}
	return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, key)
}

func (s *SessionStore) appendLocked(sess Session, role, content string) (TranscriptEvent, error) {
	if sess.Archived {
		return TranscriptEvent{}, fmt.Errorf("%w: %q", ErrSessionArchived, sess.Key)
	}
	events, err := s.readTranscriptLocked()
	if err != nil {
		return TranscriptEvent{}, err
	}
	var maxSeq int64
	for _, e := range events {
		if e.SessionKey == sess.Key && e.Seq > maxSeq {
			maxSeq = e.Seq
		}
	}
	ev := TranscriptEvent{
		Seq:        maxSeq + 1,
		SessionKey: sess.Key,
		Role:       role,
		Content:    content,
		At:         time.Now().UTC(),
	}
	if err := s.appendEventsLocked([]TranscriptEvent{ev}); err != nil {
		return TranscriptEvent{}, err
	}
	return ev, nil
}

func (s *SessionStore) readSessionsLocked() ([]Session, error) {
	return readSessionJSONL[Session](s.sessionsPath())
}

func (s *SessionStore) readTranscriptLocked() ([]TranscriptEvent, error) {
	return readSessionJSONL[TranscriptEvent](s.transcriptPath())
}

// writeSessionsLocked rewrites the whole sessions file atomically so that
// Create/Archive mutations never leave duplicate or half-written lines.
func (s *SessionStore) writeSessionsLocked(sessions []Session) error {
	if err := os.MkdirAll(filepath.Join(s.Dir, "sessions"), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.Dir, "sessions"), "sessions-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	for _, sess := range sessions {
		data, err := json.Marshal(sess)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return err
		}
		data = append(data, '\n')
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.sessionsPath())
}

func (s *SessionStore) appendEventsLocked(events []TranscriptEvent) error {
	if len(events) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "sessions"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.transcriptPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func readSessionJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			continue // skip corrupt lines
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
