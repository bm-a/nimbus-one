package llm

import "testing"

func TestMatchModelProfile(t *testing.T) {
	// Mirrors OpenCode's registry rule: gpt-* minus oss/gpt-4 strains.
	// (gpt-4o* contains "gpt-4" and stays on edit/write upstream too.)
	gpt := MatchModelProfile("gpt-5-mini")
	if gpt.ToolShape != ShapePatch || gpt.PromptVariant != VariantGPT {
		t.Fatalf("gpt profile = %+v", gpt)
	}
	for _, id := range []string{"gpt-oss-120b", "gpt-4", "claude-sonnet-4", "llama-3.3-70b", "", "gpt-4-turbo"} {
		p := MatchModelProfile(id)
		if p.ToolShape != ShapeEdit || p.PromptVariant != VariantDefault {
			t.Fatalf("MatchModelProfile(%q) = %+v, want default", id, p)
		}
	}
}
