// Package agents mirrors OpenClaw's agent roster:
// src/agents/agent-roster.ts (entries/list dual config, explicit ownership,
// legacy implicit "main" default) and src/routing/session-key.ts
// (LEGACY_IMPLICIT_AGENT_ID=main).
//
// It owns agent identity, per-agent model/delegation config, and the
// tolerant agents.yaml loader (hand-written, stdlib only).
package agents

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// LegacyImplicitAgentID is the default agent id used when no roster is
// configured. Mirrors LEGACY_IMPLICIT_AGENT_ID in
// src/routing/session-key.ts.
const LegacyImplicitAgentID = "main"

// OwnershipExplicit marks a roster as explicitly owned, mirroring the
// ownership:"explicit" marker in src/agents/agent-roster.ts.
const OwnershipExplicit = "explicit"

// EnvDefaultAgent overrides the roster default id.
const EnvDefaultAgent = "NIMBUS_DEFAULT_AGENT"

// Agent is one routable agent entry: per-agent model{primary,fallbacks},
// subagents.allowAgents and delegationMode prefer, mirroring the OpenClaw
// agent entry shape.
type Agent struct {
	ID             string
	Name           string
	Emoji          string
	Workspace      string
	Model          string
	Fallbacks      []string
	AllowAgents    []string
	DelegationMode string
}

// Roster is the loaded agent table with explicit ownership and a default id.
type Roster struct {
	Agents    map[string]Agent
	Ownership string
	DefaultID string
}

// AgentSelectionRequired is returned by Resolve for an unknown id, mirroring
// OpenClaw's unknown-agent selection-required behavior.
type AgentSelectionRequired struct {
	Requested string
	Available []string
}

// Error implements error.
func (e *AgentSelectionRequired) Error() string {
	if len(e.Available) == 0 {
		return fmt.Sprintf("agents: unknown agent %q (no agents configured)", e.Requested)
	}
	return fmt.Sprintf("agents: unknown agent %q (available: %s)", e.Requested, strings.Join(e.Available, ", "))
}

// IsAgentSelectionRequired reports whether err is an *AgentSelectionRequired.
func IsAgentSelectionRequired(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*AgentSelectionRequired)
	return ok
}

// normalizeAgentID canonicalizes an agent id (trim + lowercase, empty→main).
func normalizeAgentID(id string) string {
	t := strings.ToLower(strings.TrimSpace(id))
	if t == "" {
		return LegacyImplicitAgentID
	}
	return t
}

// IDs returns sorted normalized agent ids.
func (r *Roster) IDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.Agents))
	for id := range r.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Resolve returns the agent for id. Empty id resolves to Default().
// Unknown ids return *AgentSelectionRequired.
func (r *Roster) Resolve(id string) (Agent, error) {
	if strings.TrimSpace(id) == "" {
		return r.Default(), nil
	}
	nid := normalizeAgentID(id)
	if r != nil {
		if a, ok := r.Agents[nid]; ok {
			return a, nil
		}
	}
	return Agent{}, &AgentSelectionRequired{Requested: strings.TrimSpace(id), Available: r.IDs()}
}

// Default returns the roster default: DefaultID when present, else the legacy
// "main" entry, else the sole agent, else the first id sorted. Empty rosters
// yield the synthetic legacy main agent.
func (r *Roster) Default() Agent {
	if r == nil || len(r.Agents) == 0 {
		id := LegacyImplicitAgentID
		if r != nil && normalizeAgentID(r.DefaultID) != "" {
			id = normalizeAgentID(r.DefaultID)
		}
		return Agent{ID: id, DelegationMode: "prefer"}
	}
	if r.DefaultID != "" {
		if a, ok := r.Agents[normalizeAgentID(r.DefaultID)]; ok {
			return a
		}
	}
	if a, ok := r.Agents[LegacyImplicitAgentID]; ok {
		return a
	}
	if len(r.Agents) == 1 {
		for _, a := range r.Agents {
			return a
		}
	}
	ids := r.IDs()
	return r.Agents[ids[0]]
}

// IsAllowed reports whether from may delegate to to. An empty AllowAgents
// list allows all; a non-empty list allows only listed ids; unknown callers
// are denied. An empty from resolves to the default agent's policy (open
// when the default is synthetic).
func (r *Roster) IsAllowed(from, to string) bool {
	if r == nil || len(r.Agents) == 0 {
		return true
	}
	nfrom := normalizeAgentID(from)
	if strings.TrimSpace(from) == "" {
		d := r.Default()
		var ok bool
		var f Agent
		if f, ok = r.Agents[normalizeAgentID(d.ID)]; !ok {
			return true
		}
		return allowContains(f.AllowAgents, to)
	}
	f, ok := r.Agents[nfrom]
	if !ok {
		return false
	}
	if len(f.AllowAgents) == 0 {
		return true
	}
	return allowContains(f.AllowAgents, to)
}

func allowContains(list []string, to string) bool {
	if len(list) == 0 {
		return true
	}
	nt := normalizeAgentID(to)
	for _, a := range list {
		if normalizeAgentID(a) == nt {
			return true
		}
	}
	return false
}

// LoadEnv applies NIMBUS_DEFAULT_AGENT over the roster default when set.
func (r *Roster) LoadEnv() {
	if r == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv(EnvDefaultAgent)); v != "" {
		r.DefaultID = normalizeAgentID(v)
	}
}

// LoadFile reads an agents.yaml file with the tolerant stdlib parser.
func LoadFile(path string) (*Roster, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := ParseRoster(string(data))
	if err != nil {
		return nil, err
	}
	r.LoadEnv()
	return r, nil
}

// ParseRoster parses agents.yaml content. Supported shapes (all keys
// case-insensitive, indentation-significant, `#` comments):
//
//	ownership: explicit
//	default: main
//	agents:
//	  main:
//	    name: Main
//	    model: openai/gpt-4
//	    fallbacks: [openai/gpt-4-mini]
//	    allow_agents: [research]
//	    delegation_mode: prefer
//
// entries:/list: dual forms mirroring agent-roster.ts, plus a top-level
// `- id: main` list under agents:/list:.
func ParseRoster(text string) (*Roster, error) {
	r := &Roster{Agents: map[string]Agent{}, DefaultID: LegacyImplicitAgentID}
	lines := preprocessYAML(text)
	if len(lines) == 0 {
		return r, nil
	}
	pos := 0
	root := parseYAMLMap(lines, &pos, lines[0].indent)
	if root == nil {
		return r, nil
	}
	r.Ownership = firstStr(root, "ownership")
	if a := root.get("agents"); a != nil && a.kind == ymapKind {
		if o := a.get("ownership"); o != nil && r.Ownership == "" {
			r.Ownership = o.str()
		}
	}
	if d := firstStr(root, "default", "default_agent", "defaultagent", "default_id", "defaultid"); d != "" {
		r.DefaultID = normalizeAgentID(d)
	} else if a := root.get("agents"); a != nil && a.kind == ymapKind {
		if d := firstStrNode(a, "default", "default_agent", "defaultagent", "default_id", "defaultid"); d != "" {
			r.DefaultID = normalizeAgentID(d)
		}
	}
	for _, entry := range collectAgentNodes(root) {
		a := nodeToAgent(entry.id, entry.node)
		if a.ID == "" {
			continue
		}
		a.ID = normalizeAgentID(a.ID)
		if a.DelegationMode == "" {
			a.DelegationMode = "prefer"
		}
		normAllow := make([]string, 0, len(a.AllowAgents))
		for _, x := range a.AllowAgents {
			if t := strings.TrimSpace(x); t != "" {
				normAllow = append(normAllow, t)
			}
		}
		a.AllowAgents = normAllow
		normFB := make([]string, 0, len(a.Fallbacks))
		for _, x := range a.Fallbacks {
			if t := strings.TrimSpace(x); t != "" {
				normFB = append(normFB, t)
			}
		}
		a.Fallbacks = normFB
		r.Agents[a.ID] = a
	}
	return r, nil
}

type agentNode struct {
	id   string
	node *ynode
}

// collectAgentNodes unwraps the entries/list dual config into id→node pairs.
func collectAgentNodes(root *ynode) []agentNode {
	var out []agentNode
	if a := root.get("agents"); a != nil {
		switch a.kind {
		case ylistKind:
			for _, item := range a.items {
				out = append(out, agentNode{id: firstStr(item, "id"), node: item})
			}
			return out
		case ymapKind:
			if e := a.get("entries"); e != nil {
				if e.kind == ymapKind {
					for _, k := range e.order {
						out = append(out, agentNode{id: k, node: e.mapping[e.norm[k]]})
					}
				}
				if l := a.get("list"); l != nil && l.kind == ylistKind {
					for _, item := range l.items {
						out = append(out, agentNode{id: firstStr(item, "id"), node: item})
					}
				}
				return out
			}
			if l := a.get("list"); l != nil && l.kind == ylistKind {
				for _, item := range l.items {
					out = append(out, agentNode{id: firstStr(item, "id"), node: item})
				}
				return out
			}
			for _, k := range a.order {
				if isReservedRosterKey(k) {
					continue
				}
				out = append(out, agentNode{id: k, node: a.mapping[a.norm[k]]})
			}
			return out
		}
	}
	if e := root.get("entries"); e != nil && e.kind == ymapKind {
		for _, k := range e.order {
			out = append(out, agentNode{id: k, node: e.mapping[e.norm[k]]})
		}
		return out
	}
	if l := root.get("list"); l != nil && l.kind == ylistKind {
		for _, item := range l.items {
			out = append(out, agentNode{id: firstStr(item, "id"), node: item})
		}
		return out
	}
	for _, k := range root.order {
		if isReservedRosterKey(k) {
			continue
		}
		if n := root.mapping[root.norm[k]]; n != nil && n.kind == ymapKind {
			out = append(out, agentNode{id: k, node: n})
		}
	}
	return out
}

func isReservedRosterKey(k string) bool {
	switch strings.ToLower(k) {
	case "ownership", "default", "default_agent", "defaultagent", "default_id", "defaultid", "entries", "list":
		return true
	}
	return false
}

// nodeToAgent converts one agent node. Scalar nodes are model shorthand
// (`main: openai/gpt-4`).
func nodeToAgent(id string, n *ynode) Agent {
	var a Agent
	if n == nil {
		a.ID = id
		return a
	}
	if n.kind == yscalarKind {
		a.ID = id
		a.Model = strings.TrimSpace(n.scalar)
		return a
	}
	if n.kind != ymapKind {
		a.ID = id
		return a
	}
	a.ID = id
	if v := firstStr(n, "id"); v != "" {
		a.ID = v
	}
	a.Name = firstStr(n, "name")
	a.Emoji = firstStr(n, "emoji")
	a.Workspace = firstStr(n, "workspace", "workdir", "dir")
	a.Model = firstStr(n, "model", "primary_model", "primarymodel")
	a.Fallbacks = firstList(n, "fallbacks", "fallback", "fallback_models", "fallbackmodels")
	a.AllowAgents = firstList(n, "allow_agents", "allowagents", "allow", "agents")
	a.DelegationMode = firstStr(n, "delegation_mode", "delegationmode", "delegation")
	return a
}

func firstStr(n *ynode, keys ...string) string {
	if n == nil || n.kind != ymapKind {
		return ""
	}
	for _, k := range keys {
		if c := n.get(k); c != nil {
			if s := c.str(); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstStrNode(n *ynode, keys ...string) string { return firstStr(n, keys...) }

func firstList(n *ynode, keys ...string) []string {
	if n == nil || n.kind != ymapKind {
		return nil
	}
	for _, k := range keys {
		if c := n.get(k); c != nil {
			if l := c.strList(); len(l) > 0 {
				return l
			}
		}
	}
	return nil
}

// --- Tolerant YAML-subset parser (maps, nested maps, dash lists) ---

type ykind int

const (
	ynullKind ykind = iota
	yscalarKind
	ymapKind
	ylistKind
)

type ynode struct {
	kind    ykind
	scalar  string
	mapping map[string]*ynode
	norm    map[string]string
	order   []string
	items   []*ynode
}

func (n *ynode) get(key string) *ynode {
	if n == nil || n.kind != ymapKind {
		return nil
	}
	if v, ok := n.mapping[key]; ok {
		return v
	}
	lk := strings.ToLower(key)
	if orig, ok := n.norm[lk]; ok {
		return n.mapping[orig]
	}
	return nil
}

func (n *ynode) set(key string, v *ynode) {
	if n.mapping == nil {
		n.mapping = map[string]*ynode{}
		n.norm = map[string]string{}
	}
	if _, ok := n.mapping[key]; !ok {
		n.order = append(n.order, key)
	}
	n.mapping[key] = v
	if _, ok := n.norm[strings.ToLower(key)]; !ok {
		n.norm[strings.ToLower(key)] = key
	}
}

func (n *ynode) str() string {
	if n == nil {
		return ""
	}
	if n.kind == yscalarKind {
		return n.scalar
	}
	return ""
}

// strList renders a list node, a bracket/comma scalar, or a single scalar.
func (n *ynode) strList() []string {
	if n == nil {
		return nil
	}
	if n.kind == ylistKind {
		var out []string
		for _, it := range n.items {
			if s := strings.TrimSpace(it.str()); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if n.kind != yscalarKind {
		return nil
	}
	s := strings.TrimSpace(n.scalar)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	if !strings.Contains(s, ",") {
		return []string{strings.TrimSpace(unquoteScalar(s))}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(unquoteScalar(strings.TrimSpace(p))); t != "" {
			out = append(out, t)
		}
	}
	return out
}

type yline struct {
	indent int
	text   string
}

func preprocessYAML(text string) []yline {
	var out []yline
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.ReplaceAll(raw, "\t", "  ")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if cut := stripInlineComment(t); strings.TrimSpace(cut) != "" {
			t = strings.TrimSpace(cut)
		} else if strings.TrimSpace(cut) == "" {
			continue
		}
		out = append(out, yline{indent: indent, text: t})
	}
	return out
}

func stripInlineComment(t string) string {
	var inSingle, inDouble bool
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if c == '"' && !inSingle {
			if i > 0 && t[i-1] == '\\' {
				continue
			}
			inDouble = !inDouble
			continue
		}
		if c == '#' && !inSingle && !inDouble && i > 0 && (t[i-1] == ' ' || t[i-1] == '\t') {
			return strings.TrimSpace(t[:i])
		}
	}
	return t
}

func unquoteScalar(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isListItem(t string) bool {
	return t == "-" || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "-\t")
}

// splitKeyValue splits "key: value" at the first colon that ends the key
// (colon followed by space/tab/end), falling back to the first colon.
func splitKeyValue(t string) (key, val string, ok bool) {
	for i := 0; i < len(t); i++ {
		if t[i] != ':' {
			continue
		}
		if i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t' {
			return strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:]), true
		}
	}
	if i := strings.IndexByte(t, ':'); i > 0 {
		return strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:]), true
	}
	return "", "", false
}

func parseYAMLBlock(lines []yline, pos *int, indent int) *ynode {
	if *pos >= len(lines) {
		return &ynode{kind: ynullKind}
	}
	if isListItem(lines[*pos].text) {
		return parseYAMLList(lines, pos, indent)
	}
	return parseYAMLMap(lines, pos, indent)
}

func parseYAMLMap(lines []yline, pos *int, indent int) *ynode {
	m := &ynode{kind: ymapKind}
	for *pos < len(lines) && lines[*pos].indent == indent && !isListItem(lines[*pos].text) {
		t := lines[*pos].text
		k, v, ok := splitKeyValue(t)
		if !ok || strings.TrimSpace(k) == "" {
			*pos++
			continue
		}
		k = unquoteScalar(k)
		*pos++
		if v != "" {
			m.set(k, &ynode{kind: yscalarKind, scalar: unquoteScalar(v)})
			continue
		}
		if *pos < len(lines) && lines[*pos].indent > indent {
			m.set(k, parseYAMLBlock(lines, pos, lines[*pos].indent))
		} else {
			m.set(k, &ynode{kind: yscalarKind, scalar: ""})
		}
	}
	return m
}

func parseYAMLList(lines []yline, pos *int, indent int) *ynode {
	l := &ynode{kind: ylistKind}
	for *pos < len(lines) && lines[*pos].indent == indent && isListItem(lines[*pos].text) {
		t := lines[*pos].text
		body := ""
		if len(t) > 1 {
			body = strings.TrimSpace(t[1:])
		}
		*pos++
		if body == "" {
			if *pos < len(lines) && lines[*pos].indent > indent {
				l.items = append(l.items, parseYAMLBlock(lines, pos, lines[*pos].indent))
			} else {
				l.items = append(l.items, &ynode{kind: yscalarKind})
			}
			continue
		}
		if k, v, ok := splitKeyValue(body); ok && strings.TrimSpace(k) != "" {
			m := &ynode{kind: ymapKind}
			k = unquoteScalar(strings.TrimSpace(k))
			if v != "" {
				m.set(k, &ynode{kind: yscalarKind, scalar: unquoteScalar(v)})
			} else if *pos < len(lines) && lines[*pos].indent > indent {
				m.set(k, parseYAMLBlock(lines, pos, lines[*pos].indent))
			} else {
				m.set(k, &ynode{kind: yscalarKind})
			}
			for *pos < len(lines) && lines[*pos].indent > indent && !isListItem(lines[*pos].text) {
				sub := parseYAMLMap(lines, pos, lines[*pos].indent)
				for _, sk := range sub.order {
					m.set(sk, sub.mapping[sk])
				}
			}
			l.items = append(l.items, m)
			continue
		}
		l.items = append(l.items, &ynode{kind: yscalarKind, scalar: unquoteScalar(body)})
	}
	return l
}
