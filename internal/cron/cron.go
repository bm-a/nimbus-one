// Package cron provides a minimal 5-field cron schedule parser and matcher.
//
// OpenClaw reference (read-only):
//   - src/infra/heartbeat-schedule.ts (cron-owned monitor anchors/due slots)
//   - src/memory-host-sdk/dreaming.ts (DEFAULT_MEMORY_DREAMING_FREQUENCY = "0 3 * * *")
//   - extensions/memory-core/src/dreaming-cron.ts (managed cron scheduling)
//
// Only the standard library is used. Field layout is the classic
// "minute hour dom month dow".
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed 5-field cron expression.
type Schedule struct {
	raw     string
	minutes map[int]bool
	hours   map[int]bool
	dom     map[int]bool
	months  map[int]bool
	dow     map[int]bool
	domStar bool
	dowStar bool
}

// Spec returns the original spec string.
func (s Schedule) Spec() string { return s.raw }

// String returns the original spec string.
func (s Schedule) String() string { return s.raw }

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var dowNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// Parse parses a 5-field cron spec: "minute hour dom month dow".
// Each field supports "*", "*/N" steps, "A-B" ranges (optionally with
// "/S" steps), comma-separated lists, numeric values, and (for month and
// dow) three-letter names JAN..DEC / MON..SUN (case-insensitive).
// Day-of-week accepts 0-7 where both 0 and 7 mean Sunday.
func Parse(spec string) (Schedule, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("cron: expected 5 fields, got %d in %q", len(fields), spec)
	}
	minutes, _, err := parseField(fields[0], 0, 59, nil, "minute")
	if err != nil {
		return Schedule{}, err
	}
	hours, _, err := parseField(fields[1], 0, 23, nil, "hour")
	if err != nil {
		return Schedule{}, err
	}
	dom, domStar, err := parseField(fields[2], 1, 31, nil, "day-of-month")
	if err != nil {
		return Schedule{}, err
	}
	months, _, err := parseField(fields[3], 1, 12, monthNames, "month")
	if err != nil {
		return Schedule{}, err
	}
	dow, dowStar, err := parseField(fields[4], 0, 7, dowNames, "day-of-week")
	if err != nil {
		return Schedule{}, err
	}
	// Normalize Sunday 7 -> 0.
	if dow[7] {
		dow[0] = true
		delete(dow, 7)
	}
	return Schedule{
		raw:     spec,
		minutes: minutes,
		hours:   hours,
		dom:     dom,
		months:  months,
		dow:     dow,
		domStar: domStar,
		dowStar: dowStar,
	}, nil
}

// parseField parses one cron field over [min, max]. names maps optional
// uppercase month/weekday names to values. It returns the matched value set
// and whether the field was an unrestricted "*".
func parseField(field string, min, max int, names map[string]int, what string) (map[int]bool, bool, error) {
	out := map[int]bool{}
	if field == "" {
		return nil, false, fmt.Errorf("cron: empty %s field", what)
	}
	star := false
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("cron: empty entry in %s field %q", what, field)
		}
		base, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return nil, false, fmt.Errorf("cron: bad step %q in %s field %q", part, what, field)
			}
			step = n
		}
		var lo, hi int
		if base == "*" {
			if !hasStep {
				star = true
			}
			lo, hi = min, max
		} else if strings.Contains(base, "-") {
			bounds := strings.SplitN(base, "-", 2)
			a, err1 := fieldValue(bounds[0], names)
			b, err2 := fieldValue(bounds[1], names)
			if err1 != nil || err2 != nil {
				return nil, false, fmt.Errorf("cron: bad range %q in %s field %q", part, what, field)
			}
			lo, hi = a, b
			if lo > hi {
				return nil, false, fmt.Errorf("cron: reversed range %q in %s field %q", part, what, field)
			}
		} else {
			v, err := fieldValue(base, names)
			if err != nil {
				return nil, false, fmt.Errorf("cron: bad value %q in %s field %q", part, what, field)
			}
			if hasStep {
				lo, hi = v, max
			} else {
				lo, hi = v, v
			}
		}
		if lo < min || hi > max {
			return nil, false, fmt.Errorf("cron: value %q out of range [%d-%d] in %s field", part, min, max, what)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, false, fmt.Errorf("cron: empty %s field %q", what, field)
	}
	return out, star && len(out) == max-min+1, nil
}

// fieldValue resolves one numeric token or a name from names.
func fieldValue(tok string, names map[string]int) (int, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return 0, fmt.Errorf("empty token")
	}
	if names != nil {
		if v, ok := names[strings.ToUpper(tok)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// Matches reports whether t (in its own location) matches the schedule.
// Day-of-month vs day-of-week follow classic cron OR semantics: when both
// fields are restricted, a match on either one fires; when one of them is
// "*", only the other constrains the match.
func (s Schedule) Matches(t time.Time) bool {
	if !s.minutes[t.Minute()] || !s.hours[t.Hour()] || !s.months[int(t.Month())] {
		return false
	}
	domMatch := s.dom[t.Day()]
	dowMatch := s.dow[int(t.Weekday())]
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dowMatch
	case s.dowStar:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

const maxNextYears = 10

// Next returns the first scheduled time strictly after from, truncated to
// the minute. It searches at most maxNextYears ahead; a zero time is
// returned only if nothing matches in that window (unreachable for valid
// specs, whose narrowest pattern, e.g. Feb 29, recurs well within it).
func (s Schedule) Next(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(maxNextYears, 0, 0)
	for !t.After(limit) {
		if s.Matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// Due reports whether the schedule fired in the half-open window
// (last, now]. A zero last means "never ran", which is always due.
func (s Schedule) Due(last, now time.Time) bool {
	if !now.After(last) {
		return false
	}
	next := s.Next(last)
	if next.IsZero() {
		return false
	}
	return !next.After(now)
}
