package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, spec string) Schedule {
	t.Helper()
	s, err := Parse(spec)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", spec, err)
	}
	return s
}

func TestMidnightDaily(t *testing.T) {
	s := mustParse(t, "0 0 * * *")
	// Noon -> next midnight.
	from := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	want := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	if got := s.Next(from); !got.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", from, got, want)
	}
	// Exactly midnight is strictly after: next run is tomorrow.
	at := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	if got := s.Next(at); !got.Equal(time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Next(midnight) = %v, want next midnight", got)
	}
	if !s.Matches(at) {
		t.Fatalf("Matches(midnight) = false, want true")
	}
	if s.Matches(at.Add(time.Minute)) {
		t.Fatalf("Matches(00:01) = true, want false")
	}
}

func TestQuarterHourSteps(t *testing.T) {
	s := mustParse(t, "*/15 * * * *")
	from := time.Date(2026, 3, 10, 12, 7, 0, 0, time.UTC)
	want := time.Date(2026, 3, 10, 12, 15, 0, 0, time.UTC)
	if got := s.Next(from); !got.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", from, got, want)
	}
	for _, m := range []int{0, 15, 30, 45} {
		at := time.Date(2026, 3, 10, 12, m, 0, 0, time.UTC)
		if !s.Matches(at) {
			t.Fatalf("Matches(12:%02d) = false, want true", m)
		}
	}
	if s.Matches(time.Date(2026, 3, 10, 12, 16, 0, 0, time.UTC)) {
		t.Fatalf("Matches(12:16) = true, want false")
	}
}

func TestWeekdayRange(t *testing.T) {
	// 09:00 Monday-Friday.
	s := mustParse(t, "0 9 * * MON-FRI")
	friday := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC) // a Friday
	// Next run is Monday 09:00.
	want := time.Date(2026, 3, 16, 9, 0, 0, 0, time.UTC)
	if got := s.Next(friday); !got.Equal(want) {
		t.Fatalf("Next(Friday 10:00) = %v, want %v", got, want)
	}
	sat := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)
	if s.Matches(sat) {
		t.Fatalf("Matches(Saturday 09:00) = true, want false")
	}
	if !s.Matches(time.Date(2026, 3, 16, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("Matches(Monday 09:00) = false, want true")
	}
}

func TestNamesAndLists(t *testing.T) {
	s := mustParse(t, "0 3 * JAN,MAR mon")
	if !s.Matches(time.Date(2026, 1, 5, 3, 0, 0, 0, time.UTC)) { // Monday in JAN
		t.Fatalf("Matches(Mon Jan 03:00) = false, want true")
	}
	if s.Matches(time.Date(2026, 2, 2, 3, 0, 0, 0, time.UTC)) { // Monday in FEB
		t.Fatalf("Matches(Mon Feb 03:00) = true, want false")
	}
	// Numeric Sunday 7 behaves as Sunday.
	s7 := mustParse(t, "0 0 * * 7")
	if !s7.Matches(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)) { // a Sunday
		t.Fatalf("Matches(Sunday, dow=7) = false, want true")
	}
}

func TestDue(t *testing.T) {
	s := mustParse(t, "0 3 * * *") // daily 03:00, cf. OpenClaw dreaming default
	last := time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 11, 4, 0, 0, 0, time.UTC)
	if !s.Due(last, now) {
		t.Fatalf("Due = false across a 03:00 boundary, want true")
	}
	if s.Due(last, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("Due = true before next firing, want false")
	}
	if s.Due(now, last) {
		t.Fatalf("Due with now before last = true, want false")
	}
}

func TestInvalidSpecs(t *testing.T) {
	bad := []string{
		"",
		"0 0 * *",            // only 4 fields
		"0 0 * * * *",        // 6 fields
		"60 * * * *",         // minute out of range
		"*/0 * * * *",        // zero step
		"5-2 * * * *",        // reversed range
		"FOO * * * *",        // unknown name
		"* * * * MON-",       // dangling range
		"0 0 * * *,",         // trailing comma entry
		"0 24 * * *",         // hour out of range
		"0 0 32 * *",         // dom out of range
		"0 0 * 13 *",         // month out of range
		"0 0 * * 8",          // dow out of range
		"0 0 * * MON-FUNDAY", // bad name in range
	}
	for _, spec := range bad {
		if _, err := Parse(spec); err == nil {
			t.Fatalf("Parse(%q) = nil error, want error", spec)
		}
	}
}
