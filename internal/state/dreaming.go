// Offline memory-consolidation helpers (pure functions).
//
// OpenClaw reference (read-only):
//
//	src/memory-host-sdk/dreaming.ts — light pass over ~2 days of entries
//	  with dedup, deep pass promoting entries with score >= 0.75, REM pass
//	  extracting repeated patterns over ~7 days of facts.
//
// Windowing is a scheduling concern: callers (the daemon) scope the input
// slices to the relevant day ranges. No cron lives here; the daemon owns
// scheduling.
package state

import (
	"sort"
	"strings"
)

// Candidate is a memory entry paired with its consolidation score.
type Candidate struct {
	Text  string
	Score float64
}

// lightDedupSimilarity is the token-Jaccard similarity at or above which a
// later entry is considered a duplicate of an already-kept entry.
const lightDedupSimilarity = 0.9

// deepPromoteThreshold is the minimum score for a candidate to survive the
// deep consolidation pass.
const deepPromoteThreshold = 0.75

// LightDedup removes near-duplicate entries, keeping the first occurrence.
// Two entries are duplicates when their token-level Jaccard similarity is
// >= 0.9 after case-insensitive tokenization. Blank entries carry no memory
// and are dropped.
func LightDedup(entries []string) []string {
	var kept []string
	var keptSets []map[string]struct{}
	for _, e := range entries {
		toks := dreamTokens(e)
		if len(toks) == 0 {
			continue
		}
		set := make(map[string]struct{}, len(toks))
		for _, t := range toks {
			set[t] = struct{}{}
		}
		dup := false
		for _, ks := range keptSets {
			if jaccard(set, ks) >= lightDedupSimilarity {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, e)
			keptSets = append(keptSets, set)
		}
	}
	return kept
}

// DeepPromote keeps candidates scoring at or above 0.75, preserving input
// order.
func DeepPromote(cands []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cands {
		if c.Score >= deepPromoteThreshold {
			out = append(out, c)
		}
	}
	return out
}

// REMPatterns extracts repeated token 3-grams across facts: any consecutive
// word triple (lowercased) appearing in at least two distinct facts is
// returned as a "w1 w2 w3" pattern string, sorted alphabetically.
func REMPatterns(facts []string) []string {
	counts := make(map[string]int)
	for _, f := range facts {
		toks := dreamTokens(f)
		if len(toks) < 3 {
			continue
		}
		seen := make(map[string]struct{})
		for i := 0; i+3 <= len(toks); i++ {
			g := toks[i] + " " + toks[i+1] + " " + toks[i+2]
			seen[g] = struct{}{}
		}
		for g := range seen {
			counts[g]++
		}
	}
	var out []string
	for g, n := range counts {
		if n >= 2 {
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// dreamTokens lowercases s and splits it into alphanumeric tokens.
func dreamTokens(s string) []string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}
	return strings.Fields(sb.String())
}

// jaccard returns the token-set Jaccard similarity. Two empty sets are
// identical (1.0); an empty vs non-empty pair has no overlap (0.0).
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	var inter int
	for t := range a {
		if _, ok := b[t]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1.0
	}
	return float64(inter) / float64(union)
}
