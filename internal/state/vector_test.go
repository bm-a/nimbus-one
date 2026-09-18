package state

import (
	"math"
	"strings"
	"testing"
)

func vecNorm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

func TestEmbedL2Normalized(t *testing.T) {
	v := Embed("the quick brown fox", 64)
	if len(v) != 64 {
		t.Fatalf("expected dim 64, got %d", len(v))
	}
	n := vecNorm(v)
	if math.Abs(n-1) > 1e-3 {
		t.Fatalf("expected norm≈1, got %v", n)
	}
}

func TestCosineSelfIsOne(t *testing.T) {
	v := Embed("hello world testing", 64)
	c := Cosine(v, v)
	if math.Abs(float64(c)-1) > 1e-3 {
		t.Fatalf("expected Cosine(v,v)≈1, got %v", c)
	}
}

func TestCosineOrthogonalLessThanThreshold(t *testing.T) {
	// Manual orthogonal vectors.
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	if c := Cosine(a, b); c != 0 {
		t.Fatalf("expected 0 for orthogonal, got %v", c)
	}
	// Embedding of unrelated texts should be well below 0.99.
	v1 := Embed("blue sky ocean water sailing clouds", 256)
	v2 := Embed("red apple fruit banana quantum transistor", 256)
	c := Cosine(v1, v2)
	if c >= 0.99 {
		t.Fatalf("expected Cosine < 0.99 for unrelated texts, got %v", c)
	}
}

func TestMemoryRecallReturnsRelevantFact(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := &Memory{Store: s, Dim: 64}
	if err := m.IndexFact("the sky is blue and vast", "test"); err != nil {
		t.Fatalf("IndexFact: %v", err)
	}
	if err := m.IndexFact("apples are red fruits that grow on trees", "test"); err != nil {
		t.Fatalf("IndexFact: %v", err)
	}
	res := m.Recall("blue sky", 5)
	if len(res) == 0 {
		t.Fatalf("expected recall results, got none")
	}
	found := false
	for _, r := range res {
		if strings.Contains(strings.ToLower(r), "sky") && strings.Contains(strings.ToLower(r), "blue") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected relevant fact about blue sky, got %v", res)
	}
}
