// Package state provides hashed TF-IDF vectors and hybrid memory recall.
package state

import (
	"errors"
	"hash/fnv"
	"math"
	"sort"
)

// defaultDim is used when Dim <= 0.
const defaultDim = 256

// rrfK is the reciprocal-rank-fusion constant.
const rrfK = 60.0

// Embed hashes word unigrams + bigrams into dim buckets and L2-normalizes.
// Pure Go, stdlib only. Deterministic.
func Embed(text string, dim int) []float32 {
	if dim <= 0 {
		dim = defaultDim
	}
	vec := make([]float32, dim)
	toks := tokenize(text)
	if len(toks) == 0 {
		return vec
	}
	// Unigrams.
	for _, t := range toks {
		vec[hashBucket(t, dim)] += 1
	}
	// Word bigrams.
	for i := 0; i+1 < len(toks); i++ {
		vec[hashBucket(toks[i]+"\x00"+toks[i+1], dim)] += 1
	}
	// L2 normalize.
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return vec
	}
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
	return vec
}

// Cosine returns cosine similarity in [-1, 1]. Returns 0 for empty/zero vectors.
func Cosine(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	for _, v := range a {
		na += float64(v) * float64(v)
	}
	for _, v := range b {
		nb += float64(v) * float64(v)
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

func hashBucket(s string, dim int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32() % uint32(dim))
}

// Memory combines a JSONLStore with hybrid BM25 + vector recall.
type Memory struct {
	Store *JSONLStore
	Dim   int
}

// dimOrDefault resolves the embedding dimension.
func (m *Memory) dimOrDefault() int {
	if m == nil || m.Dim <= 0 {
		return defaultDim
	}
	return m.Dim
}

// IndexFact saves a fact to the backing store.
func (m *Memory) IndexFact(text, source string) error {
	if m == nil || m.Store == nil {
		return errors.New("state: nil memory store")
	}
	_, err := m.Store.SaveFact(text, source)
	return err
}

// Recall returns up to k strings ranked by hybrid BM25 + cosine RRF.
// It fuses fact texts with recent turn contents.
func (m *Memory) Recall(query string, k int) []string {
	if m == nil || m.Store == nil {
		return nil
	}
	if k <= 0 {
		return nil
	}
	facts, err := m.Store.AllFacts()
	if err != nil {
		facts = nil
	}
	// Pull a bounded window of recent turns across all sessions.
	turns, err := m.Store.RecentTurns("", 50)
	if err != nil {
		turns = nil
	}
	// Build combined corpus: facts first, then turns.
	docs := make([]string, 0, len(facts)+len(turns))
	for _, f := range facts {
		docs = append(docs, f.Text)
	}
	for _, t := range turns {
		docs = append(docs, t.Content)
	}
	if len(docs) == 0 {
		return nil
	}
	dim := m.dimOrDefault()

	// BM25 ranking.
	ix := &Index{}
	for _, d := range docs {
		ix.Add(d)
	}
	bmOrder := ix.Search(query, len(docs))
	bmRank := make(map[int]int, len(docs))
	for rank, idx := range bmOrder {
		bmRank[idx] = rank
	}

	// Vector ranking.
	qv := Embed(query, dim)
	type scored struct {
		idx   int
		score float32
	}
	vscores := make([]scored, len(docs))
	for i, d := range docs {
		vscores[i] = scored{idx: i, score: Cosine(qv, Embed(d, dim))}
	}
	sort.Slice(vscores, func(a, b int) bool {
		if vscores[a].score == vscores[b].score {
			return vscores[a].idx < vscores[b].idx
		}
		return vscores[a].score > vscores[b].score
	})
	vecRank := make(map[int]int, len(docs))
	for rank, s := range vscores {
		vecRank[s.idx] = rank
	}

	// Reciprocal rank fusion.
	type fused struct {
		idx   int
		score float64
	}
	fusedScores := make([]fused, 0, len(docs))
	for i := range docs {
		rb, okB := bmRank[i]
		rv, okV := vecRank[i]
		var s float64
		if okB {
			s += 1 / (rrfK + float64(rb))
		}
		if okV {
			// Only count vector rank if cosine > 0 to avoid
			// pushing wholly unrelated docs up.
			if vscores[rv].score > 0 {
				s += 1 / (rrfK + float64(rv))
			}
		}
		// Keep docs that matched at least one signal.
		if s > 0 {
			fusedScores = append(fusedScores, fused{idx: i, score: s})
		}
	}
	if len(fusedScores) == 0 {
		// Fall back to BM25 order (may be empty).
		if len(bmOrder) == 0 {
			return nil
		}
		if k > len(bmOrder) {
			k = len(bmOrder)
		}
		out := make([]string, k)
		for i := 0; i < k; i++ {
			out[i] = docs[bmOrder[i]]
		}
		return out
	}
	sort.Slice(fusedScores, func(a, b int) bool {
		if fusedScores[a].score == fusedScores[b].score {
			return fusedScores[a].idx < fusedScores[b].idx
		}
		return fusedScores[a].score > fusedScores[b].score
	})
	if k > len(fusedScores) {
		k = len(fusedScores)
	}
	out := make([]string, k)
	for i := 0; i < k; i++ {
		out[i] = docs[fusedScores[i].idx]
	}
	return out
}
