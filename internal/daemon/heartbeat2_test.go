package daemon

import (
	"testing"
	"time"
)

func TestRunnerDue(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	r := Runner{Every: time.Hour, PhaseSeed: "worker-1"}
	if !r.Due(time.Time{}, now) {
		t.Fatal("never-ran must be due")
	}
	// Just ran: neither cooldown nor interval cleared.
	if r.Due(now.Add(-time.Minute), now) {
		t.Fatal("fresh run must not be due")
	}
	// Interval cleared (phase offset < Every, so 2x Every is always due).
	if !r.Due(now.Add(-2*time.Hour), now) {
		t.Fatal("2x interval must be due")
	}
	// Future lastRun never due.
	if r.Due(now.Add(time.Hour), now) {
		t.Fatal("future lastRun must not be due")
	}
	// Cooldown gates even when the interval cleared.
	c := Runner{Every: time.Minute, Cooldown: time.Hour}
	if c.Due(now.Add(-30*time.Minute), now) {
		t.Fatal("cooldown must gate")
	}
	if !c.Due(now.Add(-2*time.Hour), now) {
		t.Fatal("post-cooldown must be due")
	}
	// Phase is deterministic per seed.
	a, b := Runner{Every: time.Hour, PhaseSeed: "s"}, Runner{Every: time.Hour, PhaseSeed: "s"}
	if a.phaseOffset() != b.phaseOffset() {
		t.Fatal("phase not deterministic")
	}
	if off := a.phaseOffset(); off < 0 || off >= time.Hour {
		t.Fatalf("phase out of range: %v", off)
	}
	if off := (Runner{Every: time.Hour}).phaseOffset(); off != 0 {
		t.Fatalf("empty seed must give zero phase, got %v", off)
	}
}

func TestRunnerActiveHours(t *testing.T) {
	at := func(h int) time.Time {
		return time.Date(2026, 9, 19, h, 30, 0, 0, time.UTC)
	}
	if !(Runner{}).InActiveHours(at(3)) {
		t.Fatal("empty window must be always active")
	}
	day := Runner{ActiveHours: "9-17"}
	if !day.InActiveHours(at(9)) || !day.InActiveHours(at(16)) {
		t.Fatal("inside window must be active")
	}
	if day.InActiveHours(at(8)) || day.InActiveHours(at(17)) || day.InActiveHours(at(23)) {
		t.Fatal("outside window must be inactive")
	}
	overnight := Runner{ActiveHours: "22-6"}
	if !overnight.InActiveHours(at(22)) || !overnight.InActiveHours(at(2)) {
		t.Fatal("overnight inside must be active")
	}
	if overnight.InActiveHours(at(12)) || overnight.InActiveHours(at(6)) {
		t.Fatal("overnight outside must be inactive")
	}
	// Invalid windows fail open (preserve liveness).
	for _, bad := range []string{"nope", "9", "9-9-9", "25-3", "9-24", "-"} {
		if !(Runner{ActiveHours: bad}).InActiveHours(at(3)) {
			t.Fatalf("invalid window %q must fail open", bad)
		}
	}
}

func TestRunKey(t *testing.T) {
	if got := RunKey("worker-1"); got != "agent:worker-1:main:heartbeat" {
		t.Fatalf("got %q", got)
	}
}

func TestIsEmptyContent(t *testing.T) {
	empty := []string{
		"",
		"   \n\t\n",
		"# Just a header\n## Sub",
		"```go\nfmt.Println(\"hi\")\n```",
		"<!-- a comment -->",
		"<!--\nmulti\nline\n-->",
		"<!-- unclosed comment tail",
		"- [ ] todo one\n- [ ] todo two",
		"* [ ] x",
		"# Title\n- [ ] item\n---\n<!-- c -->\n```\ncode\n```",
		"> # quoted header only",
		"---",
	}
	for _, s := range empty {
		if !IsEmptyContent(s) {
			t.Errorf("want empty for %q", s)
		}
	}
	full := []string{
		"hello world",
		"# Title\nSome body text",
		"- [x] done item",
		"- a plain bullet",
		"```\ncode\n```\nreal content",
		"NO_REPLY",
		"> quoted prose",
		"#tag not a header",
	}
	for _, s := range full {
		if IsEmptyContent(s) {
			t.Errorf("want content for %q", s)
		}
	}
}

func TestResolveVisibility(t *testing.T) {
	tr, fl := true, false
	if ResolveVisibility(nil, nil, DefaultShowOK) != false {
		t.Fatal("default showOk must be false")
	}
	if ResolveVisibility(nil, nil, DefaultShowAlerts) != true {
		t.Fatal("default showAlerts must be true")
	}
	if !ResolveVisibility(&tr, &fl, false) {
		t.Fatal("account must beat channel")
	}
	if ResolveVisibility(&fl, &tr, true) {
		t.Fatal("account false must beat channel true")
	}
	if !ResolveVisibility(nil, &tr, false) {
		t.Fatal("channel must beat default")
	}
}

func TestIsNoReply(t *testing.T) {
	for _, s := range []string{"", "NO_REPLY", "no_reply", "  HEARTBEAT_OK "} {
		if !IsNoReply(s) {
			t.Errorf("want no-reply for %q", s)
		}
	}
	if IsNoReply("alert: disk full") {
		t.Error("real alert misclassified as no-reply")
	}
}
