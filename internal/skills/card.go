// Package skills — publisher skill cards.
//
// A skill-card.md file next to SKILL.md carries publisher metadata using
// tolerant "## Header" sections:
//
//	## Publisher
//	Acme
//	## License
//	MIT
//	## Version
//	1.2.0
//	## Risks
//	...
//	## Use-Case
//	...
//
// Unknown headers are ignored; missing sections stay empty.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Card is publisher metadata for a skill directory.
type Card struct {
	Publisher string
	License   string
	Version   string
	Risks     string
	UseCase   string
}

// Empty reports whether no field was populated.
func (c *Card) Empty() bool {
	return c == nil || (c.Publisher == "" && c.License == "" && c.Version == "" && c.Risks == "" && c.UseCase == "")
}

// ParseCard reads skill-card.md inside dir (tolerant of case variants).
// It returns an error when no card file exists or no recognized section
// yields content.
func ParseCard(dir string) (*Card, error) {
	var raw []byte
	var err error
	for _, n := range []string{"skill-card.md", "SKILL-CARD.md", "Skill-Card.md"} {
		raw, err = os.ReadFile(filepath.Join(dir, n))
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("skills: no skill-card.md in %s", dir)
	}
	card := &Card{}
	var cur *string
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "##") {
			header := strings.ToLower(strings.TrimSpace(strings.TrimLeft(t, "# ")))
			header = strings.ReplaceAll(header, "-", " ")
			header = strings.Join(strings.Fields(header), " ")
			cur = nil
			switch header {
			case "publisher", "author", "maintainer":
				cur = &card.Publisher
			case "license":
				cur = &card.License
			case "version":
				cur = &card.Version
			case "risks", "risk", "security":
				cur = &card.Risks
			case "use case", "usecase", "use-case", "purpose":
				cur = &card.UseCase
			}
			continue
		}
		if cur != nil && t != "" && !strings.HasPrefix(t, "#") {
			if *cur != "" {
				*cur += "\n"
			}
			*cur += t
		}
	}
	card.Publisher = strings.TrimSpace(card.Publisher)
	card.License = strings.TrimSpace(card.License)
	card.Version = strings.TrimSpace(card.Version)
	card.Risks = strings.TrimSpace(card.Risks)
	card.UseCase = strings.TrimSpace(card.UseCase)
	if card.Empty() {
		return nil, fmt.Errorf("skills: %s has no parseable card sections", dir)
	}
	return card, nil
}
