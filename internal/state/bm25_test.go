package state

import (
	"testing"
)

func TestBM25RanksBlueSkyFirst(t *testing.T) {
	var ix Index
	ix.Add("blue sky")
	ix.Add("red apple")
	if ix.Len() != 2 {
		t.Fatalf("expected 2 docs, got %d", ix.Len())
	}
	res := ix.Search("blue sky", 2)
	if len(res) == 0 {
		t.Fatalf("expected results, got none")
	}
	if res[0] != 0 {
		t.Fatalf("expected doc0 first, got %v", res)
	}
}

func TestBM25EmptyQueryReturnsEmpty(t *testing.T) {
	var ix Index
	ix.Add("blue sky")
	ix.Add("red apple")
	for _, q := range []string{"", "   ", "!!!", "\t\n "} {
		res := ix.Search(q, 10)
		if len(res) != 0 {
			t.Fatalf("query %q: expected empty/none, got %v", q, res)
		}
	}
}

func TestBM25NoMatchReturnsEmpty(t *testing.T) {
	var ix Index
	ix.Add("blue sky")
	ix.Add("red apple")
	res := ix.Search("zebra quantum xylophone", 10)
	if len(res) != 0 {
		t.Fatalf("expected no results, got %v", res)
	}
}

func TestBM25EmptyIndexReturnsEmpty(t *testing.T) {
	var ix Index
	res := ix.Search("blue sky", 10)
	if len(res) != 0 {
		t.Fatalf("expected empty for empty index, got %v", res)
	}
}
