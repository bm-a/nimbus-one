package doctor

import (
	"strings"
	"testing"
)

func TestKnowledgeFind401(t *testing.T) {
	got := Find("401 unauthorized key", 3)
	if len(got) == 0 || got[0].ID != "llm-401" {
		t.Fatalf("expected llm-401 first, got %+v", got)
	}
	if !strings.Contains(got[0].Fix, "wizard") && !strings.Contains(strings.Join(got[0].Commands, " "), "config") {
		t.Fatal("fix must point at config flow")
	}
}

func TestKnowledgeFindByID(t *testing.T) {
	got := Find("tg-open", 3)
	if len(got) == 0 || got[0].ID != "tg-open" {
		t.Fatalf("ID lookup failed: %+v", got)
	}
}

func TestKnowledgeEmptyQuery(t *testing.T) {
	if Find("", 3) != nil {
		t.Fatal("empty query must return nil")
	}
	if len(Find("xyzzy-no-such-thing", 3)) != 0 {
		t.Fatal("nonsense must return nothing")
	}
}

func TestKnowledgeEntriesComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Knowledge {
		if e.ID == "" || e.Title == "" || e.Cause == "" || e.Fix == "" {
			t.Fatalf("incomplete entry: %+v", e)
		}
		if len(e.Keywords) == 0 {
			t.Fatalf("entry %s needs keywords", e.ID)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate id %s", e.ID)
		}
		seen[e.ID] = true
	}
	if len(Knowledge) < 10 {
		t.Fatal("knowledge base too thin")
	}
}
