// Package state provides pure-Go BM25 retrieval over facts and turns.
package state

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 parameters.
const (
	bm25k1 = 1.2
	bm25b  = 0.75
)

// Index is a BM25 index over added documents.
type Index struct {
	docs      []string
	docTokens [][]string
	docFreqs  []map[string]int
	docLens   []int
	df        map[string]int
	totalLen  int
}

// Add appends text as a new document.
func (ix *Index) Add(text string) {
	if ix.df == nil {
		ix.df = map[string]int{}
	}
	toks := tokenize(text)
	freq := map[string]int{}
	for _, t := range toks {
		freq[t]++
	}
	seen := map[string]bool{}
	for t := range freq {
		if !seen[t] {
			ix.df[t]++
			seen[t] = true
		}
	}
	ix.docs = append(ix.docs, text)
	ix.docTokens = append(ix.docTokens, toks)
	ix.docFreqs = append(ix.docFreqs, freq)
	ix.docLens = append(ix.docLens, len(toks))
	ix.totalLen += len(toks)
}

// Len returns the number of indexed documents.
func (ix *Index) Len() int { return len(ix.docs) }

// Search returns the top-k doc indices ranked by BM25 (descending).
// Only docs with score > 0 are returned.
func (ix *Index) Search(query string, k int) []int {
	if len(ix.docs) == 0 || k == 0 {
		return nil
	}
	qtoks := tokenize(query)
	if len(qtoks) == 0 {
		return nil
	}
	n := len(ix.docs)
	avgLen := 0.0
	if n > 0 {
		avgLen = float64(ix.totalLen) / float64(n)
	}
	if avgLen == 0 {
		avgLen = 1
	}
	type scored struct {
		idx   int
		score float64
	}
	scores := make([]scored, 0, n)
	// Query term frequencies (to avoid double-counting same term twice,
	// we score unique query terms; repeated query terms get single weight).
	seenQ := map[string]bool{}
	for _, qt := range qtoks {
		if seenQ[qt] {
			continue
		}
		seenQ[qt] = true
		df := ix.df[qt]
		if df == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
		for i := 0; i < n; i++ {
			tf := float64(ix.docFreqs[i][qt])
			if tf == 0 {
				continue
			}
			dl := float64(ix.docLens[i])
			den := tf + bm25k1*(1-bm25b+bm25b*dl/avgLen)
			// accumulate: find existing entry or create
			found := -1
			for j := range scores {
				if scores[j].idx == i {
					found = j
					break
				}
			}
			s := idf * (tf * (bm25k1 + 1) / den)
			if found >= 0 {
				scores[found].score += s
			} else {
				scores = append(scores, scored{idx: i, score: s})
			}
		}
	}
	if len(scores) == 0 {
		return nil
	}
	sort.Slice(scores, func(a, b int) bool {
		if scores[a].score == scores[b].score {
			return scores[a].idx < scores[b].idx
		}
		return scores[a].score > scores[b].score
	})
	if k < 0 || k > len(scores) {
		k = len(scores)
	}
	out := make([]int, k)
	for i := 0; i < k; i++ {
		out[i] = scores[i].idx
	}
	return out
}

// tokenize lowercases and splits on non-alphanumeric runes.
// Shared by BM25 and vector code.
func tokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return toks
}
