// Per-agent heartbeat runner policy: scheduling, quiet hours, run keys,
// visibility defaults, and empty-content detection.
//
// OpenClaw references (read-only):
//
//	src/heartbeat/heartbeat-runner-scheduler.ts — per-agent intervalMs,
//	  cooldown gate, flood ring, deterministic phase hash spreading agents,
//	  and the isolated run key agent:<id>:main:heartbeat.
//	src/heartbeat/heartbeat-visibility.ts — showOk defaults false,
//	  showAlerts defaults true, precedence account > channel > defaults.
//	src/auto-reply/heartbeat.ts — HEARTBEAT_PROMPT, SILENT_REPLY_TOKEN, and
//	  the empty-content detector.
package daemon

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// HeartbeatPrompt is the system instruction for a heartbeat worker run.
const HeartbeatPrompt = "You are heartbeat worker. Execute the checklist item concisely. Reply NO_REPLY if nothing needs attention."

// SilentReplyToken marks a heartbeat turn that needs no user-visible output.
const SilentReplyToken = "NO_REPLY"

// Visibility defaults: quiet successes stay hidden, alerts surface.
const (
	DefaultShowOK     = false
	DefaultShowAlerts = true
)

// IsNoReply reports heartbeat no-op markers: empty output, NO_REPLY
// (any case), or HEARTBEAT_OK.
func IsNoReply(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	u := strings.ToUpper(t)
	return u == SilentReplyToken || u == "HEARTBEAT_OK"
}

// Runner is the per-agent heartbeat schedule + display policy.
type Runner struct {
	// Every is the nominal interval between runs.
	Every time.Duration
	// ActiveHours restricts runs to a daily window "HH-HH" (24h hours,
	// e.g. "9-17"); "" means always active. Overnight windows like
	// "22-6" are supported.
	ActiveHours string
	// Cooldown is the minimum gap between runs (flood protection).
	Cooldown time.Duration
	// ShowOK surfaces successful runs; ShowAlerts surfaces alert runs.
	ShowOK     bool
	ShowAlerts bool
	// PhaseSeed spreads agents deterministically: the phase offset is
	// sha256(seed) mod Every.
	PhaseSeed string
}

// phaseOffset returns the deterministic stagger for this runner, in
// [0, Every). Zero when Every is non-positive or no seed is set.
func (r Runner) phaseOffset() time.Duration {
	if r.Every <= 0 || r.PhaseSeed == "" {
		return 0
	}
	sum := sha256.Sum256([]byte(r.PhaseSeed))
	return time.Duration(binary.BigEndian.Uint64(sum[:8]) % uint64(r.Every))
}

// Due reports whether a run is due at now given the last run time.
// A zero lastRun means "never ran" and is always due. Otherwise the
// elapsed time must clear both the cooldown gate and the phased interval
// (Every + deterministic phase offset). A non-positive Every disables the
// interval gate (cooldown still applies). A lastRun in the future is never
// due.
func (r Runner) Due(lastRun, now time.Time) bool {
	if lastRun.IsZero() {
		return true
	}
	elapsed := now.Sub(lastRun)
	if elapsed < 0 {
		return false
	}
	if r.Cooldown > 0 && elapsed < r.Cooldown {
		return false
	}
	want := r.Every + r.phaseOffset()
	if want <= 0 {
		return true
	}
	return elapsed >= want
}

// InActiveHours reports whether now falls inside the runner's active
// window. "" (and unparseable values, fail-open to preserve liveness) mean
// always active. The start hour is inclusive, the end hour exclusive.
func (r Runner) InActiveHours(now time.Time) bool {
	s := strings.TrimSpace(r.ActiveHours)
	if s == "" {
		return true
	}
	start, end, ok := parseActiveHours(s)
	if !ok {
		return true
	}
	if start == end {
		return true
	}
	h := now.Hour()
	if start < end {
		return h >= start && h < end
	}
	return h >= start || h < end
}

// RunKey returns the isolated run key for an agent's heartbeat runs:
// agent:<id>:main:heartbeat.
func RunKey(agentID string) string {
	return "agent:" + strings.TrimSpace(agentID) + ":main:heartbeat"
}

// ResolveVisibility picks an effective visibility flag with precedence
// account > channel > default, where nil means "not set at this level".
func ResolveVisibility(account, channel *bool, def bool) bool {
	if account != nil {
		return *account
	}
	if channel != nil {
		return *channel
	}
	return def
}

// IsEmptyContent reports whether rendered markdown carries no user-visible
// substance: after removing HTML comments and fenced code blocks, only ATX
// headers, horizontal rules, blank lines, and unchecked ("- [ ]") task
// items may remain. Checked items with text and any other prose count as
// content.
func IsEmptyContent(md string) bool {
	s := stripHTMLComments(md)
	inFence := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// Blockquote markers add no content of their own.
		for strings.HasPrefix(t, ">") {
			t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
		}
		if t == "" {
			continue
		}
		if isATXHeader(t) || isHR(t) {
			continue
		}
		if checked, hasText, ok := parseCheckbox(t); ok {
			if checked && hasText {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// stripHTMLComments removes <!-- ... --> spans (possibly multiline). A
// never-closed opener discards the rest of the input.
func stripHTMLComments(s string) string {
	var sb strings.Builder
	for {
		start := strings.Index(s, "<!--")
		if start < 0 {
			sb.WriteString(s)
			break
		}
		sb.WriteString(s[:start])
		rest := s[start+len("<!--"):]
		end := strings.Index(rest, "-->")
		if end < 0 {
			break
		}
		s = rest[end+len("-->"):]
	}
	return sb.String()
}

// isATXHeader reports "# foo" style headers (1-6 hashes + space/EOL).
func isATXHeader(t string) bool {
	i := 0
	for i < len(t) && t[i] == '#' {
		i++
	}
	if i == 0 || i > 6 {
		return false
	}
	return i == len(t) || t[i] == ' ' || t[i] == '\t'
}

// isHR reports horizontal rules: 3+ of one of -, *, _ (spaces allowed).
func isHR(t string) bool {
	stripped := strings.ReplaceAll(t, " ", "")
	if len(stripped) < 3 {
		return false
	}
	c := stripped[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 1; i < len(stripped); i++ {
		if stripped[i] != c {
			return false
		}
	}
	return true
}

// parseCheckbox splits "- [ ] rest" / "* [x] rest" list items, reporting
// whether the box is checked, whether any text follows it, and whether the
// line is a checkbox at all.
func parseCheckbox(t string) (checked, hasText, ok bool) {
	if len(t) < 2 || (t[0] != '-' && t[0] != '*' && t[0] != '+') {
		return false, false, false
	}
	after := strings.TrimSpace(t[1:])
	if len(after) < 3 || after[0] != '[' || after[2] != ']' {
		return false, false, false
	}
	mark := after[1]
	if mark != ' ' && mark != 'x' && mark != 'X' {
		return false, false, false
	}
	rest := strings.TrimSpace(after[3:])
	return mark == 'x' || mark == 'X', rest != "", true
}

// parseActiveHours parses "HH-HH" with 0-23 hours on each side.
func parseActiveHours(s string) (start, end int, ok bool) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var si, ei int
	if _, err := parseHour(strings.TrimSpace(parts[0]), &si); err != nil {
		return 0, 0, false
	}
	if _, err := parseHour(strings.TrimSpace(parts[1]), &ei); err != nil {
		return 0, 0, false
	}
	return si, ei, true
}

func parseHour(s string, out *int) (int, error) {
	if s == "" {
		return 0, errBadHour
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errBadHour
		}
		n = n*10 + int(s[i]-'0')
	}
	if n < 0 || n > 23 {
		return 0, errBadHour
	}
	*out = n
	return n, nil
}

// errBadHour reports an unparseable hour in an ActiveHours window.
var errBadHour = errors.New("daemon: bad hour")
