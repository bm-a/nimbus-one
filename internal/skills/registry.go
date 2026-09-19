package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nimbus-one/internal/tools"
)

// SkillRegistry holds discovered skills and an optional link to the shared
// tool registry for prompt rendering.
type SkillRegistry struct {
	Skills map[string]*Skill
	Tools  *tools.Registry
}

// NewSkillRegistry creates a registry bound to the given tool registry
// (which may be nil).
func NewSkillRegistry(toolsReg *tools.Registry) *SkillRegistry {
	return &SkillRegistry{Skills: map[string]*Skill{}, Tools: toolsReg}
}

// LoadDir discovers skills under root, supporting both flat (<root>/*/SKILL.md)
// and category (<root>/*/*/SKILL.md) layouts. Individual skill parse failures
// are skipped so one bad skill never breaks loading.
func (r *SkillRegistry) LoadDir(root string) error {
	if r.Skills == nil {
		r.Skills = map[string]*Skill{}
	}
	fi, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("skills: stat %s: %w", root, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("skills: %s is not a directory", root)
	}
	for _, p := range discoverSkillFiles(root) {
		sk, err := ParseFile(p)
		if err != nil {
			continue
		}
		// Attach the publisher card when present; absence is normal.
		if card, cerr := ParseCard(filepath.Dir(p)); cerr == nil {
			sk.Card = card
		}
		r.Skills[sk.Name] = sk
	}
	return nil
}

// Get returns the skill with the given name.
func (r *SkillRegistry) Get(name string) (*Skill, bool) {
	sk, ok := r.Skills[name]
	return sk, ok
}

// Names returns sorted skill names.
func (r *SkillRegistry) Names() []string {
	names := make([]string, 0, len(r.Skills))
	for n := range r.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// CapabilitiesPrompt renders the skills section of the internal prompt.
func (r *SkillRegistry) CapabilitiesPrompt() string {
	var b strings.Builder
	b.WriteString("## Skills\n")
	names := r.Names()
	if len(names) == 0 {
		b.WriteString("(none loaded)\n")
		return b.String()
	}
	for _, n := range names {
		sk := r.Skills[n]
		b.WriteString("- " + sk.Name + ": " + sk.Description)
		if len(sk.Triggers) > 0 {
			b.WriteString(" (triggers: " + strings.Join(sk.Triggers, ", ") + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// CapabilitiesPromptVerbose lists skills with triggers plus a capped body
// excerpt. Models ingest verbose-here/terse-in-tool-description better
// than the reverse (OpenCode skill-prompt finding). Excerpts are capped so
// a large skill library cannot starve the context budget.
func (r *SkillRegistry) CapabilitiesPromptVerbose() string {
	var b strings.Builder
	b.WriteString("## Skills\n")
	names := r.Names()
	if len(names) == 0 {
		b.WriteString("(none loaded)\n")
		return b.String()
	}
	for _, n := range names {
		sk := r.Skills[n]
		b.WriteString("- " + sk.Name + ": " + sk.Description)
		if len(sk.Triggers) > 0 {
			b.WriteString(" (triggers: " + strings.Join(sk.Triggers, ", ") + ")")
		}
		if excerpt := skillExcerpt(sk.Body); excerpt != "" {
			b.WriteString("\n  use: " + excerpt)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// skillExcerpt returns the first paragraph of a skill body, capped.
func skillExcerpt(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if idx := strings.Index(body, "\n\n"); idx >= 0 {
		body = body[:idx]
	}
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > 240 {
		body = body[:240] + "…"
	}
	return body
}

// discoverSkillFiles lists SKILL.md files one and two levels below root.
func discoverSkillFiles(root string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l1 := filepath.Join(root, e.Name())
		// Level 1: <root>/*/SKILL.md
		for _, cand := range skillCandidates(l1) {
			add(cand)
		}
		// Level 2: <root>/*/*/SKILL.md (category/name layout)
		subs, err := os.ReadDir(l1)
		if err != nil {
			continue
		}
		for _, s := range subs {
			if !s.IsDir() {
				continue
			}
			for _, cand := range skillCandidates(filepath.Join(l1, s.Name())) {
				add(cand)
			}
		}
	}
	sort.Strings(out)
	return out
}

// skillCandidates returns the existing SKILL.md (case-insensitive fallback)
// inside dir, or nil.
func skillCandidates(dir string) []string {
	for _, n := range []string{"SKILL.md", "skill.md"} {
		p := filepath.Join(dir, n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return []string{p}
		}
	}
	return nil
}
