// Named auth-profile store: ordering, cooldown, pinning, env discovery.
//
// OpenClaw reference (read-only):
//
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/agents/auth-profiles/*
//	  (named profiles, order, eligibility, cooldown, OAuth fences,
//	   session skip-cache)
//	live-auth-keys.ts (multi-key env enumeration, referenced by behaviour;
//	  exact file not present under this snapshot — enumeration follows the
//	  PREFIX_* + base-key convention)
package llm

import (
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// cooldownPeriod is how long a non-terminal failure sidelines a profile.
const cooldownPeriod = 60 * time.Second

// Profile is one named credential slot for a provider.
type Profile struct {
	ID            string
	Provider      string
	Key           string
	CooldownUntil time.Time
	Fails         int
	// Exiled marks terminal (401/403-class) failures: the credential is
	// invalid and never retried silently, mirroring pool StateDead.
	Exiled bool
}

// Eligible reports whether the profile may serve traffic now.
func (p *Profile) Eligible(now time.Time) bool {
	if p == nil || p.Exiled {
		return false
	}
	return !now.Before(p.CooldownUntil)
}

// Store holds a provider's profiles in stable registration order.
type Store struct {
	mu       sync.Mutex
	profiles []*Profile
	pinned   string // user-locked profile id, always ordered first
}

// NewStore builds a Store from the given profiles (nil entries dropped).
func NewStore(ps ...*Profile) *Store {
	s := &Store{}
	for _, p := range ps {
		if p != nil {
			s.profiles = append(s.profiles, p)
		}
	}
	return s
}

// Order returns the provider's profiles with eligible ones first, preserving
// stable registration order within each band. A pinned (user-locked)
// profile always leads. Pass "" to list every provider.
func (s *Store) Order(provider string) []*Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// Lazily revive expired cooldowns so eligibility reads are exact.
	for _, p := range s.profiles {
		if !p.Exiled && !p.CooldownUntil.IsZero() && !now.Before(p.CooldownUntil) {
			p.CooldownUntil = time.Time{}
		}
	}
	var pinned, eligible, rest []*Profile
	for _, p := range s.profiles {
		if provider != "" && !strings.EqualFold(p.Provider, provider) {
			continue
		}
		if s.pinned != "" && p.ID == s.pinned {
			pinned = append(pinned, p)
			continue
		}
		if p.Eligible(now) {
			eligible = append(eligible, p)
		} else {
			rest = append(rest, p)
		}
	}
	return append(append(pinned, eligible...), rest...)
}

// find locates a profile by id. Callers must hold s.mu.
func (s *Store) find(id string) *Profile {
	for _, p := range s.profiles {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// ReportFailure records a failed attempt. Terminal failures (invalid
// credentials) exile the profile; others add a 60s cooldown and bump Fails.
// Unknown ids are ignored.
func (s *Store) ReportFailure(id string, terminal bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(id)
	if p == nil {
		return
	}
	if terminal {
		p.Exiled = true
		return
	}
	p.Fails++
	p.CooldownUntil = time.Now().Add(cooldownPeriod)
}

// ReportSuccess clears a profile's failure counters and cooldown.
// Exile from terminal failures is sticky: re-auth (a new profile) is the
// way back. Unknown ids are ignored.
func (s *Store) ReportSuccess(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(id)
	if p == nil {
		return
	}
	p.Fails = 0
	p.CooldownUntil = time.Time{}
}

// Pin user-locks a profile so Order always returns it first.
func (s *Store) Pin(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = id
}

// Unpin releases the user lock.
func (s *Store) Unpin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = ""
}

// baseEnvKeys lists the well-known base key variables per provider.
func baseEnvKeys(provider string) []string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "anthropic":
		return []string{"ANTHROPIC_API_KEY"}
	case "gemini":
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	case "groq":
		return []string{"GROQ_API_KEY"}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY"}
	case "openrouter":
		return []string{"OPENROUTER_API_KEY"}
	case "meta":
		return []string{"META_API_KEY"}
	case "ollama":
		return []string{"OLLAMA_API_KEY"}
	case "kimi", "moonshot":
		return []string{"MOONSHOT_API_KEY", "KIMI_API_KEY"}
	case "":
		return nil
	default:
		return []string{strings.ToUpper(strings.TrimSpace(provider)) + "_API_KEY"}
	}
}

// FromEnv builds a Store for provider by enumerating multi-key environment
// variables: every PREFIX_* hit for each prefix (in prefix order, names
// sorted for stability), then the provider's base key variables. Empty
// values are skipped; identical key material is deduped; profile ids are
// "provider/<ENV_NAME>" lowercased on the provider half.
func FromEnv(provider string, prefixes []string) *Store {
	prov := strings.ToLower(strings.TrimSpace(provider))
	s := NewStore()
	seenName := map[string]bool{}
	seenKey := map[string]bool{}
	add := func(envName, value string) {
		if seenName[envName] || value == "" {
			return
		}
		if seenKey[value] {
			return
		}
		seenName[envName] = true
		seenKey[value] = true
		s.profiles = append(s.profiles, &Profile{
			ID:       prov + "/" + strings.ToLower(envName),
			Provider: prov,
			Key:      value,
		})
	}
	env := os.Environ()
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		var names []string
		for _, kv := range env {
			name := kv
			if i := strings.Index(kv, "="); i >= 0 {
				name = kv[:i]
			}
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			add(name, os.Getenv(name))
		}
	}
	for _, name := range baseEnvKeys(prov) {
		if seenName[name] {
			continue
		}
		add(name, os.Getenv(name))
	}
	return s
}
