package prompt

import (
	"strings"
	"testing"
)

func TestEnvBlock(t *testing.T) {
	b := Env{Workdir: "/w", SkillsDir: "/s", ToolNames: []string{"read", "bash"}, IsGitRepo: true}.Block()
	for _, want := range []string{"workspace: /w", "git repo: yes", "platform: ", "tool discipline:"} {
		if !strings.Contains(b, want) {
			t.Fatalf("env block missing %q:\n%s", want, b)
		}
	}
}

func TestVariantFor(t *testing.T) {
	if VariantFor("gpt") != GPT || VariantFor("default") != Default || VariantFor("bogus") != Default {
		t.Fatal("VariantFor mapping wrong")
	}
	if Addendum(Default) != "" {
		t.Fatal("default variant must add nothing")
	}
	if !strings.Contains(Addendum(GPT), "apply_patch") {
		t.Fatal("gpt addendum must steer toward apply_patch")
	}
}

func TestAssemble(t *testing.T) {
	if got := Assemble("a", "", "b"); got != "a\n\nb" {
		t.Fatalf("Assemble = %q", got)
	}
}
