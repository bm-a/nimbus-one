// Package state provides a file-backed JSONL store for turns and facts.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Turn is a single conversational turn.
type Turn struct {
	ID        int64     `json:"id"`
	Session   string    `json:"session"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Fact is a single remembered fact.
type Fact struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// Store abstracts turn/fact persistence.
type Store interface {
	SaveTurn(session, role, content string) (Turn, error)
	RecentTurns(session string, n int) ([]Turn, error)
	SaveFact(text, source string) (Fact, error)
	AllFacts() ([]Fact, error)
}

// JSONLStore is a mutex-guarded file-backed Store.
// Files: turns.jsonl + facts.jsonl inside Dir.
type JSONLStore struct {
	Dir string

	mu sync.Mutex
}

var _ Store = (*JSONLStore)(nil)

// Open creates dir (0755) and returns a JSONLStore rooted there.
func Open(dir string) (*JSONLStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &JSONLStore{Dir: dir}, nil
}

func (s *JSONLStore) turnsPath() string { return filepath.Join(s.Dir, "turns.jsonl") }
func (s *JSONLStore) factsPath() string { return filepath.Join(s.Dir, "facts.jsonl") }

// SaveTurn appends a turn, assigning a monotonic ID and timestamp.
func (s *JSONLStore) SaveTurn(session, role, content string) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.readTurnsLocked()
	if err != nil {
		return Turn{}, err
	}
	var maxID int64
	for _, t := range turns {
		if t.ID > maxID {
			maxID = t.ID
		}
	}
	t := Turn{
		ID:        maxID + 1,
		Session:   session,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.appendJSONLocked(s.turnsPath(), t); err != nil {
		return Turn{}, err
	}
	return t, nil
}

// RecentTurns returns the last n turns for session in chronological order.
// Empty session matches all sessions. n <= 0 returns all matches.
func (s *JSONLStore) RecentTurns(session string, n int) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.readTurnsLocked()
	if err != nil {
		return nil, err
	}
	var filtered []Turn
	for _, t := range turns {
		if session == "" || t.Session == session {
			filtered = append(filtered, t)
		}
	}
	if n > 0 && len(filtered) > n {
		filtered = filtered[len(filtered)-n:]
	}
	return filtered, nil
}

// SaveFact appends a fact, assigning a monotonic ID and timestamp.
func (s *JSONLStore) SaveFact(text, source string) (Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	facts, err := s.readFactsLocked()
	if err != nil {
		return Fact{}, err
	}
	var maxID int64
	for _, f := range facts {
		if f.ID > maxID {
			maxID = f.ID
		}
	}
	f := Fact{
		ID:        maxID + 1,
		Text:      text,
		Source:    source,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.appendJSONLocked(s.factsPath(), f); err != nil {
		return Fact{}, err
	}
	return f, nil
}

// AllFacts returns all facts in insertion order.
func (s *JSONLStore) AllFacts() ([]Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readFactsLocked()
}

// SaveTurnRecord appends a pre-built Turn, filling ID/CreatedAt when unset.
func (s *JSONLStore) SaveTurnRecord(t Turn) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns, err := s.readTurnsLocked()
	if err != nil {
		return Turn{}, err
	}
	var maxID int64
	for _, e := range turns {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	if t.ID == 0 {
		t.ID = maxID + 1
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if err := s.appendJSONLocked(s.turnsPath(), t); err != nil {
		return Turn{}, err
	}
	return t, nil
}

// SaveFactRecord appends a pre-built Fact, filling ID/CreatedAt when unset.
func (s *JSONLStore) SaveFactRecord(f Fact) (Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	facts, err := s.readFactsLocked()
	if err != nil {
		return Fact{}, err
	}
	var maxID int64
	for _, e := range facts {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	if f.ID == 0 {
		f.ID = maxID + 1
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	if err := s.appendJSONLocked(s.factsPath(), f); err != nil {
		return Fact{}, err
	}
	return f, nil
}

// AllTurns returns every turn in insertion order.
func (s *JSONLStore) AllTurns() ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readTurnsLocked()
}

func (s *JSONLStore) appendJSONLocked(path string, v any) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

func (s *JSONLStore) readTurnsLocked() ([]Turn, error) {
	return readJSONL[Turn](s.turnsPath())
}

func (s *JSONLStore) readFactsLocked() ([]Fact, error) {
	return readJSONL[Fact](s.factsPath())
}

func readJSONL[T any](path string) ([]T, error) {
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
	// Allow long lines (1 MiB).
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
