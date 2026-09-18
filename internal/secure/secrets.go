// Package secure provides encryption-at-rest for secrets management.
package secure

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Well-known secret keys.
var knownKeys = []string{
	"openrouter",
	"openai",
	"anthropic",
	"groq",
	"gemini",
	"deepseek",
	"telegram",
	"discord",
	"http_token",
}

// KnownKeys returns the list of well-known secret keys.
func KnownKeys() []string {
	out := make([]string, len(knownKeys))
	copy(out, knownKeys)
	return out
}

// Store is an encrypted on-disk key/value secret store.
// Contents are persisted as vault-encrypted JSON at dataDir/secrets.enc.
type Store struct {
	vault *Vault
	path  string
	mu    sync.Mutex
	items map[string]string
}

// OpenSecrets loads the secret store from dataDir/secrets.enc,
// creating an empty store when no file exists yet.
func OpenSecrets(dataDir string, v *Vault) (*Store, error) {
	if v == nil {
		return nil, fmt.Errorf("secrets: nil vault")
	}
	if dataDir == "" {
		return nil, fmt.Errorf("secrets: empty dataDir")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{
		vault: v,
		path:  filepath.Join(dataDir, "secrets.enc"),
		items: map[string]string{},
	}
	blob, err := v.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(blob) == 0 {
		return s, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal(blob, &m); err != nil {
		return nil, fmt.Errorf("secrets: decode: %w", err)
	}
	if m != nil {
		s.items = m
	}
	return s, nil
}

// persistLocked marshals items and writes them via the vault.
// Caller must hold s.mu.
func (s *Store) persistLocked() error {
	data, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	// Vault.WriteFile encrypts and writes atomically with 0600.
	if err := s.vault.WriteFile(s.path, data); err != nil {
		return err
	}
	return nil
}

// Set stores value under key and persists atomically.
func (s *Store) Set(key, value string) error {
	if key == "" {
		return fmt.Errorf("secrets: empty key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]string{}
	}
	s.items[key] = value
	return s.persistLocked()
}

// Get returns the value for key, or an error when missing.
// It never returns an empty value silently.
func (s *Store) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		return "", fmt.Errorf("secrets: %q not found", key)
	}
	v, ok := s.items[key]
	if !ok {
		return "", fmt.Errorf("secrets: %q not found", key)
	}
	if v == "" {
		return "", fmt.Errorf("secrets: %q not found", key)
	}
	return v, nil
}

// Delete removes key and persists atomically. Missing keys are a no-op.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]string{}
	}
	delete(s.items, key)
	return s.persistLocked()
}

// List returns sorted keys only. Values are never listed.
func (s *Store) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for k := range s.items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
