// Package skills — tiered skill loading with first-wins precedence.
//
// Mirrors OpenClaw skill tiers (read-only reference): skills resolve across
// ordered tiers — extra → bundled → workshop → managed → agents → workspace —
// where the earliest tier wins and later duplicates are shadow warnings.
package skills

import (
	"fmt"
	"os"
	"path/filepath"

	"nimbus-one/internal/tools"
)

type tierDir struct {
	name string
	dir  string
}

// TieredRegistry loads skills from an ordered list of tier directories.
// Earlier tiers win: a skill name already loaded is kept and the duplicate
// is reported as a shadow warning.
type TieredRegistry struct {
	*SkillRegistry
	tiers []tierDir
}

// NewTieredRegistry creates a tiered registry bound to toolsReg (may be nil).
func NewTieredRegistry(toolsReg *tools.Registry) *TieredRegistry {
	return &TieredRegistry{SkillRegistry: NewSkillRegistry(toolsReg)}
}

// AddTier appends a tier directory scanned by LoadAll in registration order.
// Earlier tiers take precedence over later ones.
func (t *TieredRegistry) AddTier(name, dir string) {
	t.tiers = append(t.tiers, tierDir{name: name, dir: dir})
}

// Tiers returns the registered tier names in precedence order.
func (t *TieredRegistry) Tiers() []string {
	out := make([]string, 0, len(t.tiers))
	for _, td := range t.tiers {
		out = append(out, td.name)
	}
	return out
}

// LoadAll scans every tier directory in order (first-wins) and returns
// shadow/missing warnings. A missing tier directory is a warning, not an
// error, so optional tiers never break loading.
func (t *TieredRegistry) LoadAll() ([]string, error) {
	if t.Skills == nil {
		t.Skills = map[string]*Skill{}
	}
	var warnings []string
	for _, td := range t.tiers {
		fi, err := os.Stat(td.dir)
		if err != nil {
			if os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("tier %q: directory %s missing, skipped", td.name, td.dir))
				continue
			}
			return warnings, fmt.Errorf("skills: stat tier %q: %w", td.name, err)
		}
		if !fi.IsDir() {
			return warnings, fmt.Errorf("skills: tier %q path %s is not a directory", td.name, td.dir)
		}
		for _, p := range discoverSkillFiles(td.dir) {
			sk, err := ParseFile(p)
			if err != nil {
				continue
			}
			if prev, dup := t.Skills[sk.Name]; dup {
				warnings = append(warnings, fmt.Sprintf("tier %q: skill %q shadowed by earlier tier (kept %s)",
					td.name, sk.Name, prev.Path))
				continue
			}
			if card, cerr := ParseCard(filepath.Dir(p)); cerr == nil {
				sk.Card = card
			}
			t.Skills[sk.Name] = sk
		}
	}
	return warnings, nil
}
