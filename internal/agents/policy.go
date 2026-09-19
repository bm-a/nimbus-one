// Model policy mirrors OpenClaw's shared model selection:
// src/agents/model-selection-shared.ts (ModelAliasIndex: byAlias +
// byProviderAlias, exact configured rows win; allowlist with
// segment-boundary provider/* wildcards via isModelKeyAllowedBySet) and
// src/config/model-policy-*.ts (exact refs plus provider wildcards).
package agents

import (
	"strings"
)

// modelRef is a normalized provider/model tuple.
type modelRef struct {
	provider string
	model    string
}

// AliasIndex maps user-facing aliases to provider/model refs with bare-alias
// and provider/alias forms. Exact provider/model rows win over alias
// interpretation, mirroring resolveModelRefFromString's exact-first order.
type AliasIndex struct {
	bare   map[string]modelRef
	scoped map[string]modelRef
	exact  map[string]modelRef
}

// NewAliasIndex returns an empty AliasIndex.
func NewAliasIndex() *AliasIndex {
	return &AliasIndex{
		bare:   map[string]modelRef{},
		scoped: map[string]modelRef{},
		exact:  map[string]modelRef{},
	}
}

// Add registers alias → provider/model and records the exact provider/model
// row so later lookups prefer it over alias readings of the same string.
func (a *AliasIndex) Add(alias, provider, model string) {
	if a == nil {
		return
	}
	alias = strings.TrimSpace(alias)
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	if alias == "" || provider == "" || model == "" {
		return
	}
	ref := modelRef{provider: provider, model: model}
	if a.bare == nil {
		a.bare = map[string]modelRef{}
	}
	if a.scoped == nil {
		a.scoped = map[string]modelRef{}
	}
	if a.exact == nil {
		a.exact = map[string]modelRef{}
	}
	a.bare[strings.ToLower(alias)] = ref
	a.scoped[provider+"/"+strings.ToLower(alias)] = ref
	a.exact[provider+"/"+strings.ToLower(model)] = modelRef{provider: provider, model: model}
}

// Resolve maps a reference to provider/model:
//   - bare "alias" via the alias table (ok=false when unknown);
//   - "provider/alias" via the scoped table, exact rows first;
//   - any other well-formed "provider/model" passes through verbatim
//     (provider lowercased) so configured catalog ids keep working.
func (a *AliasIndex) Resolve(ref string) (provider, model string, ok bool) {
	t := strings.TrimSpace(ref)
	if t == "" {
		return "", "", false
	}
	slash := strings.Index(t, "/")
	if slash < 0 {
		if a == nil {
			return "", "", false
		}
		if r, hit := a.bare[strings.ToLower(t)]; hit {
			return r.provider, r.model, true
		}
		return "", "", false
	}
	prov := strings.ToLower(strings.TrimSpace(t[:slash]))
	rest := strings.TrimSpace(t[slash+1:])
	if prov == "" || rest == "" {
		return "", "", false
	}
	key := prov + "/" + strings.ToLower(rest)
	if a != nil {
		// Exact configured rows win over alias readings of the same string.
		if r, hit := a.exact[key]; hit {
			return r.provider, r.model, true
		}
		if r, hit := a.scoped[key]; hit {
			return r.provider, r.model, true
		}
	}
	return prov, rest, true
}

// Allowlist is the model visibility policy: exact provider/model entries
// plus provider/* wildcards with segment-boundary matching, mirroring
// isModelKeyAllowedBySet. Empty patterns deny everything; use AllowAll for
// the permissive default.
type Allowlist struct {
	Patterns []string
}

// NewAllowlist builds an Allowlist from pattern strings.
func NewAllowlist(patterns ...string) Allowlist {
	kept := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if t := strings.TrimSpace(p); t != "" {
			kept = append(kept, t)
		}
	}
	return Allowlist{Patterns: kept}
}

// AllowAll returns the permissive allowlist (matches everything).
func AllowAll() Allowlist { return Allowlist{Patterns: []string{"*"}} }

// Allow reports whether provider/model is visible. agent is reserved for
// future per-agent policy scoping and is currently ignored.
func (l Allowlist) Allow(agent, provider, model string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	if p == "" || m == "" {
		return false
	}
	key := p + "/" + m
	for _, raw := range l.Patterns {
		pat := strings.ToLower(strings.TrimSpace(raw))
		if pat == "" {
			continue
		}
		if pat == "*" {
			return true
		}
		if pat == key {
			return true
		}
		// Segment-boundary wildcard: only a trailing "/*" matches, and the
		// prefix keeps its slash so "openai/gpt-4/*" never matches
		// "openai/gpt-40".
		if strings.HasSuffix(pat, "/*") && strings.HasPrefix(key, pat[:len(pat)-1]) {
			return true
		}
	}
	return false
}
