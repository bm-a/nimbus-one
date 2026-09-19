package state

import (
	"reflect"
	"testing"
)

func TestLightDedup(t *testing.T) {
	in := []string{
		"User prefers dark mode",
		"user prefers dark mode",  // case-insensitive exact dup
		"user prefers dark mode!", // near-dup (>=0.9 Jaccard)
		"",
		"   ",
		"Deploy on Fridays is forbidden",
		"User prefers dark mode and green accents", // similar but distinct
	}
	got := LightDedup(in)
	want := []string{
		"User prefers dark mode",
		"Deploy on Fridays is forbidden",
		"User prefers dark mode and green accents",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := LightDedup(nil); len(got) != 0 {
		t.Fatalf("nil in, got %q", got)
	}
}

func TestDeepPromote(t *testing.T) {
	in := []Candidate{
		{"low", 0.1},
		{"edge-low", 0.749},
		{"edge-high", 0.75},
		{"high", 0.99},
	}
	got := DeepPromote(in)
	want := []Candidate{{"edge-high", 0.75}, {"high", 0.99}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestREMPatterns(t *testing.T) {
	facts := []string{
		"the user likes morning standups",
		"the user likes evening reviews",
		"deploy on friday evening",
		"short",
		"",
	}
	got := REMPatterns(facts)
	want := []string{"the user likes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	// Sorted output with multiple patterns.
	multi := []string{
		"alpha beta gamma one",
		"alpha beta gamma two",
		"delta epsilon zeta one",
		"delta epsilon zeta two",
	}
	got = REMPatterns(multi)
	want = []string{"alpha beta gamma", "delta epsilon zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	// Same 3-gram twice in one fact counts once -> no pattern.
	if got := REMPatterns([]string{"only once here yes"}); len(got) != 0 {
		t.Fatalf("singleton leaked: %q", got)
	}
}
