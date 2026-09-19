package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// telegramHTTPClient bounds every Bot API call. Requests also carry ctx.
var telegramHTTPClient = &http.Client{Timeout: 15 * time.Second}

// truncateBody caps error bodies so failures stay descriptive but bounded.
func truncateBody(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 {
		n = 500
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// postTelegramMethod POSTs a JSON payload to a Bot API method.
// Returns the raw response body on 2xx, else a descriptive error that
// includes the HTTP status and a truncated body. Empty token is an error.
func (t *Telegram) postTelegramMethod(ctx context.Context, method string, payload any) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("telegram %s: nil client", method)
	}
	if strings.TrimSpace(t.Token) == "" {
		return nil, fmt.Errorf("telegram %s: empty token", method)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: encode payload: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.api(method), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: post: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: read response: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram %s: status %s: %s", method, resp.Status, truncateBody(string(data), 500))
	}
	// The Bot API can answer 200 with {"ok":false,...}; surface that too.
	var envelope struct {
		OK          *bool  `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.OK != nil && !*envelope.OK {
		desc := truncateBody(envelope.Description, 500)
		if desc == "" {
			desc = truncateBody(string(data), 500)
		}
		return nil, fmt.Errorf("telegram %s: ok=false: %s", method, desc)
	}
	return data, nil
}

// React posts an emoji reaction to a message via setMessageReaction.
func (t *Telegram) React(ctx context.Context, chatID, messageID int64, emoji string) error {
	if strings.TrimSpace(emoji) == "" {
		return fmt.Errorf("telegram setMessageReaction: empty emoji")
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction": []map[string]string{
			{"type": "emoji", "emoji": emoji},
		},
	}
	_, err := t.postTelegramMethod(ctx, "setMessageReaction", payload)
	return err
}

// Edit replaces a message's text via editMessageText.
func (t *Telegram) Edit(ctx context.Context, chatID, messageID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("telegram editMessageText: empty text")
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	_, err := t.postTelegramMethod(ctx, "editMessageText", payload)
	return err
}

// Unsend deletes a message via deleteMessage.
func (t *Telegram) Unsend(ctx context.Context, chatID, messageID int64) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	}
	_, err := t.postTelegramMethod(ctx, "deleteMessage", payload)
	return err
}

// SendPoll posts a poll via sendPoll and returns the poll id.
func (t *Telegram) SendPoll(ctx context.Context, chatID int64, question string, options []string) (string, error) {
	if strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("telegram sendPoll: empty question")
	}
	if len(options) < 2 {
		return "", fmt.Errorf("telegram sendPoll: need at least 2 options, got %d", len(options))
	}
	payload := map[string]any{
		"chat_id":  chatID,
		"question": question,
		"options":  options,
	}
	data, err := t.postTelegramMethod(ctx, "sendPoll", payload)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Result struct {
			Poll struct {
				ID string `json:"id"`
			} `json:"poll"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("telegram sendPoll: decode response: %w", err)
	}
	if strings.TrimSpace(parsed.Result.Poll.ID) == "" {
		return "", fmt.Errorf("telegram sendPoll: missing poll id in response: %s", truncateBody(string(data), 500))
	}
	return parsed.Result.Poll.ID, nil
}

// pairingEntry is one pending pairing code.
type pairingEntry struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// pairingExpiry is how long a pairing code stays valid.
const pairingExpiry = 10 * time.Minute

// PairingStore is a file-backed store of pending pairing codes.
// JSON is persisted atomically at Path with 0600 permissions.
type PairingStore struct {
	Path string

	mu      sync.Mutex
	entries map[string]pairingEntry
	loaded  bool
}

// NewPairingStore opens (or creates) a file-backed store at path.
func NewPairingStore(path string) *PairingStore {
	s := &PairingStore{Path: path}
	s.load()
	return s
}

// ensureLocked lazily initializes the map and loads from disk once.
func (s *PairingStore) ensureLocked() {
	if s.entries == nil {
		s.entries = map[string]pairingEntry{}
	}
	if !s.loaded {
		s.loaded = true
		if s.Path == "" {
			return
		}
		data, err := os.ReadFile(s.Path)
		if err != nil {
			return // missing file = empty store
		}
		var onDisk map[string]pairingEntry
		if err := json.Unmarshal(data, &onDisk); err != nil {
			return // corrupt file: start empty rather than crash
		}
		now := time.Now()
		for id, e := range onDisk {
			if e.Code == "" {
				continue
			}
			if !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt) {
				continue // drop expired on load
			}
			s.entries[id] = e
		}
	}
}

// load forces a reload from disk (used at construction).
func (s *PairingStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
}

// saveLocked persists entries atomically with 0600 permissions.
// Caller must hold s.mu.
func (s *PairingStore) saveLocked() error {
	if strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("pairing store: empty path")
	}
	dir := filepath.Dir(s.Path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("pairing store: mkdir: %w", err)
		}
	}
	data, err := json.Marshal(s.entries)
	if err != nil {
		return fmt.Errorf("pairing store: encode: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".pairing-*.tmp")
	if err != nil {
		return fmt.Errorf("pairing store: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("pairing store: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("pairing store: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("pairing store: chmod: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("pairing store: rename: %w", err)
	}
	// Belt-and-braces: rename can lose the mode on some filesystems.
	_ = os.Chmod(s.Path, 0o600)
	return nil
}

// RequestPairing issues a fresh 6-digit code for userID, valid for 10 minutes.
func (s *PairingStore) RequestPairing(userID string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", fmt.Errorf("pairing store: empty user id")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("pairing store: rand: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	s.entries[userID] = pairingEntry{
		Code:      code,
		ExpiresAt: time.Now().Add(pairingExpiry),
	}
	if err := s.saveLocked(); err != nil {
		delete(s.entries, userID)
		return "", err
	}
	return code, nil
}

// Verify checks code for userID with constant-time comparison.
// Codes are single-use and expire after 10 minutes.
func (s *PairingStore) Verify(code, userID string) bool {
	userID = strings.TrimSpace(userID)
	code = strings.TrimSpace(code)
	if userID == "" || code == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	e, ok := s.entries[userID]
	if !ok {
		return false
	}
	if !time.Now().Before(e.ExpiresAt) {
		delete(s.entries, userID)
		_ = s.saveLocked()
		return false
	}
	match := len(e.Code) == len(code) && subtle.ConstantTimeCompare([]byte(e.Code), []byte(code)) == 1
	if !match {
		return false
	}
	delete(s.entries, userID)
	_ = s.saveLocked()
	return true
}

// List returns the pending (unexpired) user ids, sorted.
func (s *PairingStore) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	now := time.Now()
	var out []string
	for id, e := range s.entries {
		if e.Code == "" {
			continue
		}
		if !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt) {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
