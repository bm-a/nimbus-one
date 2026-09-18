// Package skills discovers, parses and exposes SKILL.md-based capabilities.
//
// A skill is a directory containing a SKILL.md file with optional YAML
// frontmatter delimited by --- lines. The parser is intentionally tolerant:
// it accepts the OpenClaw-minimal shape (name + description only) as well as
// the Hermes-strict shape (version/author/platforms) without failing, and
// falls back to directory-name/first-heading when no frontmatter is present.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nimbus-one/internal/tools"
)

// Skill is a parsed SKILL.md capability.
type Skill struct {
	Name        string
	Description string
	Version     string
	Author      string
	Triggers    []string
	Params      map[string]tools.Param
	Body        string
	Path        string
}

// ParseDir reads the SKILL.md inside dir (accepting SKILL.md or skill.md).
func ParseDir(dir string) (*Skill, error) {
	for _, n := range []string{"SKILL.md", "skill.md"} {
		p := filepath.Join(dir, n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return ParseFile(p)
		}
	}
	return nil, fmt.Errorf("skills: no SKILL.md in %s", dir)
}

// ParseFile parses a single SKILL.md file.
func ParseFile(path string) (*Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skills: read %s: %w", path, err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	fm, body, hasFM := splitFrontmatter(content)

	sk := &Skill{Path: path, Body: strings.Trim(body, "\n")}
	if hasFM {
		name, desc, version, author, triggers, params := parseFrontmatter(fm)
		sk.Name = name
		sk.Description = desc
		sk.Version = version
		sk.Author = author
		sk.Triggers = triggers
		sk.Params = params
	}
	if sk.Name == "" {
		sk.Name = deriveName(path)
	}
	if sk.Description == "" {
		sk.Description = firstHeading(body)
	}
	sk.Name = normalizeName(sk.Name)
	if sk.Name == "" {
		return nil, fmt.Errorf("skills: %s has no usable skill name", path)
	}
	sk.Description = strings.TrimSpace(sk.Description)
	return sk, nil
}

// splitFrontmatter splits YAML frontmatter (between --- lines) from the body.
// It requires the first non-blank line to be "---" and scans for the closing
// "---" (or "..."). Reports has=false when no frontmatter is present.
func splitFrontmatter(content string) (fm, body string, has bool) {
	lines := strings.Split(content, "\n")
	start := -1
	for i, ln := range lines {
		t := strings.TrimSpace(strings.TrimPrefix(ln, "\xef\xbb\xbf"))
		if t == "" {
			continue
		}
		if t == "---" {
			start = i
		}
		break
	}
	if start < 0 {
		return "", content, false
	}
	for j := start + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "---" || t == "..." {
			return strings.Join(lines[start+1:j], "\n"), strings.Join(lines[j+1:], "\n"), true
		}
	}
	return "", content, false
}

// parseFrontmatter parses the tolerant frontmatter subset. Unknown keys
// (e.g. platforms) are ignored so strict shapes never fail.
func parseFrontmatter(fm string) (name, desc, version, author string, triggers []string, params map[string]tools.Param) {
	scalars := map[string]string{}
	parsed := map[string]*tools.Param{}
	var curParam string
	inParams := false
	expectList := "" // top-level key awaiting "- item" lines, e.g. "triggers"
	lastScalar := ""

	flushParam := func() {}

	lines := strings.Split(fm, "\n")
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := indentWidth(line)
		trimmed := strings.TrimSpace(line)

		// Inside a params: block (indented lines following a top-level params: key).
		if inParams {
			if indent == 0 {
				inParams = false
				curParam = ""
				// Fall through to top-level handling below.
			} else {
				// New param name: an indented single token ending in ':'.
				if isBareKey(trimmed) {
					curParam = unquote(strings.TrimSuffix(trimmed, ":"))
					if curParam != "" {
						if _, ok := parsed[curParam]; !ok {
							parsed[curParam] = &tools.Param{Type: "string"}
						}
					}
					continue
				}
				if curParam != "" {
					if k, v, ok := splitKV(trimmed); ok {
						p := parsed[curParam]
						switch strings.ToLower(k) {
						case "type":
							p.Type = unquote(v)
							if p.Type == "" {
								p.Type = "string"
							}
						case "description", "desc":
							p.Description = unquote(v)
						case "required":
							p.Required = parseBool(v)
						}
						continue
					}
					// Continuation line for a multi-line description.
					if p := parsed[curParam]; p != nil {
						if p.Description != "" {
							p.Description += " "
						}
						p.Description += trimmed
					}
					continue
				}
				continue
			}
		}

		// Awaiting "- item" list lines for a top-level key (triggers).
		if expectList != "" {
			if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
				item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
				item = unquote(item)
				if item != "" {
					if expectList == "triggers" {
						triggers = append(triggers, item)
					}
				}
				continue
			}
			// A new top-level key ends the pending list; fall through.
			if indent == 0 && strings.Contains(trimmed, ":") {
				expectList = ""
			} else if indent > 0 {
				continue
			}
		}

		k, v, ok := splitKV(trimmed)
		if !ok {
			// Possible continuation of a folded description.
			if lastScalar == "description" && indent > 0 {
				if scalars["description"] != "" {
					scalars["description"] += " "
				}
				scalars["description"] += trimmed
			}
			continue
		}
		lk := strings.ToLower(k)
		switch lk {
		case "name":
			scalars["name"] = appendScalar(scalars["name"], unquote(stripInlineComment(v)))
			lastScalar = "name"
		case "description":
			if isFoldIndicator(strings.TrimSpace(v)) {
				lastScalar = "description"
				continue
			}
			scalars["description"] = appendScalar(scalars["description"], unquote(stripInlineComment(v)))
			lastScalar = "description"
		case "version":
			scalars["version"] = unquote(stripInlineComment(v))
			lastScalar = "version"
		case "author":
			scalars["author"] = unquote(stripInlineComment(v))
			lastScalar = "author"
		case "trigger", "triggers":
			lastScalar = "triggers"
			v = strings.TrimSpace(stripInlineComment(v))
			if v == "" || v == "|" || v == ">" {
				expectList = "triggers"
			} else {
				triggers = append(triggers, parseStringList(v)...)
			}
		case "params", "parameters":
			lastScalar = "params"
			inParams = true
			curParam = ""
			_ = flushParam
		default:
			// Unknown keys (platforms, license, ...) are ignored.
			lastScalar = ""
		}
	}

	if len(parsed) > 0 {
		params = make(map[string]tools.Param, len(parsed))
		for k, v := range parsed {
			params[k] = *v
		}
	}
	return scalars["name"], scalars["description"], scalars["version"], scalars["author"], triggers, params
}

// deriveName falls back to the containing directory name.
func deriveName(path string) string {
	base := filepath.Base(filepath.Dir(path))
	if base == "" || base == "." || base == "/" {
		base = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return base
}

// normalizeName lowercases and slugifies a skill name.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '_' || r == '\t'
	})
	return strings.Join(fields, "-")
}

// firstHeading returns the first markdown heading, else the first non-empty
// line, else "".
func firstHeading(body string) string {
	var first string
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if first == "" {
			first = t
		}
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "# "))
		}
	}
	if first == "" {
		return ""
	}
	if len(first) > 200 {
		return first[:200]
	}
	return first
}

func indentWidth(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
		} else if r == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}

// splitKV splits "key: value" on the first colon. ok=false when the line is
// not a key/value pair.
func splitKV(s string) (k, v string, ok bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", "", false
	}
	k = strings.TrimSpace(s[:i])
	if !isKeyToken(k) {
		return "", "", false
	}
	return k, strings.TrimSpace(s[i+1:]), true
}

func isKeyToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// isBareKey reports "name:" style lines (no inline value).
func isBareKey(s string) bool {
	if !strings.HasSuffix(s, ":") {
		return false
	}
	return isKeyToken(strings.TrimSpace(strings.TrimSuffix(s, ":")))
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// stripInlineComment removes a trailing " # comment" for unquoted values.
func stripInlineComment(s string) string {
	t := strings.TrimSpace(s)
	if t == "" || t[0] == '"' || t[0] == '\'' {
		return s
	}
	if i := strings.Index(t, " #"); i >= 0 {
		return strings.TrimSpace(t[:i])
	}
	return s
}

func isFoldIndicator(s string) bool {
	switch s {
	case ">", ">-", ">+", "|", "|-", "|+":
		return true
	}
	return false
}

func appendScalar(cur, add string) string {
	if cur == "" {
		return add
	}
	if add == "" {
		return cur
	}
	return cur + " " + add
}

// parseStringList parses "a, b", "[a, b]" or a single value.
func parseStringList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	var out []string
	if strings.Contains(s, ",") {
		for _, p := range strings.Split(s, ",") {
			p = unquote(strings.TrimSpace(p))
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	if v := unquote(strings.TrimSpace(s)); v != "" {
		out = append(out, v)
	}
	return out
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(unquote(s))) {
	case "true", "yes", "1", "required", "y":
		return true
	}
	return false
}
