package gateway

import (
	"errors"
	"strings"
	"testing"
)

func TestPolicyStoreGroupMatrix(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		allow  []string
		chatID int64
		typ    string
		want   bool
	}{
		{"open group", "open", nil, -100, "group", true},
		{"open supergroup", "open", nil, -200, "supergroup", true},
		{"open channel", "open", nil, -300, "channel", true},
		{"empty policy defaults open", "", nil, -100, "group", true},
		{"private always allowed even when disabled", "disabled", nil, 42, "private", true},
		{"empty type treated as private", "disabled", nil, 42, "", true},
		{"dm treated as private", "disabled", nil, 42, "dm", true},
		{"disabled blocks group", "disabled", nil, -100, "group", false},
		{"disabled blocks supergroup", "disabled", nil, -200, "supergroup", false},
		{"allowlist member", "allowlist", []string{"-100"}, -100, "group", true},
		{"allowlist nonmember", "allowlist", []string{"-100"}, -999, "group", false},
		{"allowlist empty blocks group", "allowlist", nil, -100, "group", false},
		{"allowlist still allows private", "allowlist", nil, 42, "private", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &PolicyStore{GroupPolicy: c.policy, GroupAllow: c.allow}
			if got := p.IsGroupAllowed(c.chatID, c.typ); got != c.want {
				t.Fatalf("IsGroupAllowed(%d,%q) mode=%q = %v, want %v",
					c.chatID, c.typ, c.policy, got, c.want)
			}
		})
	}
}

func TestPolicyStoreGroupConfig(t *testing.T) {
	p := &PolicyStore{Groups: map[int64]GroupPolicy{
		-100: {RequireMention: true, SystemPrompt: "sp"},
	}}
	g := p.GroupConfig(-100)
	if !g.RequireMention || g.SystemPrompt != "sp" {
		t.Fatalf("GroupConfig(-100) = %+v, want RequireMention+SystemPrompt", g)
	}
	if got := p.GroupConfig(-999); got.RequireMention || got.SystemPrompt != "" ||
		len(got.AllowedTools) != 0 || len(got.DeniedTools) != 0 {
		t.Fatalf("GroupConfig unknown = %+v, want zero", got)
	}
	empty := &PolicyStore{}
	if got := empty.GroupConfig(-100); got.RequireMention || got.SystemPrompt != "" ||
		len(got.AllowedTools) != 0 || len(got.DeniedTools) != 0 {
		t.Fatalf("GroupConfig nil map = %+v, want zero", got)
	}
}

func TestPolicyStoreChunkLimit(t *testing.T) {
	if got := (&PolicyStore{}).ChunkLimit(); got != 4000 {
		t.Fatalf("default ChunkLimit = %d, want 4000", got)
	}
	if got := (&PolicyStore{TextChunkLimit: 1200}).ChunkLimit(); got != 1200 {
		t.Fatalf("override ChunkLimit = %d, want 1200", got)
	}
	if got := (&PolicyStore{TextChunkLimit: -5}).ChunkLimit(); got != 4000 {
		t.Fatalf("negative ChunkLimit = %d, want 4000", got)
	}
}

func TestPolicyStoreShouldMention(t *testing.T) {
	open := &PolicyStore{}
	if !open.ShouldMention("hello there", "mybot") {
		t.Fatal("no group requires mention: want true without mention")
	}
	strict := &PolicyStore{Groups: map[int64]GroupPolicy{
		-100: {RequireMention: true},
	}}
	if strict.ShouldMention("hello there", "mybot") {
		t.Fatal("requireMention group, no mention: want false")
	}
	if !strict.ShouldMention("hello @mybot help", "mybot") {
		t.Fatal("requireMention group, with mention: want true")
	}
	if !strict.ShouldMention("HELLO @MYBOT", "mybot") {
		t.Fatal("mention match should be case-insensitive")
	}
	if !strict.ShouldMention("anything", "") {
		t.Fatal("empty botName: want true")
	}
}

func TestStreamingSenderNewline(t *testing.T) {
	var sent []string
	s := &StreamingSender{Mode: "newline", Send: func(c string) error {
		sent = append(sent, c)
		return nil
	}}
	if err := s.Write("partial line"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 {
		t.Fatalf("no newline yet: sent=%q", sent)
	}
	if err := s.Write(" rest\nnext line\ntail"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "partial line rest\nnext line\n" {
		t.Fatalf("newline flush: sent=%q", sent)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[1] != "tail" {
		t.Fatalf("flush remainder: sent=%q", sent)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("empty flush must not send: sent=%q", sent)
	}
}

func TestStreamingSenderLength(t *testing.T) {
	var sent []string
	s := &StreamingSender{Mode: "length", MaxChars: 4, Send: func(c string) error {
		sent = append(sent, c)
		return nil
	}}
	if err := s.Write("abc"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 {
		t.Fatalf("under limit: sent=%q", sent)
	}
	if err := s.Write("defg"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "abcd" {
		t.Fatalf("length flush: sent=%q", sent)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[1] != "efg" {
		t.Fatalf("flush remainder: sent=%q", sent)
	}
}

func TestStreamingSenderLengthDefault(t *testing.T) {
	var sizes []int
	s := &StreamingSender{Send: func(c string) error {
		sizes = append(sizes, len(c))
		return nil
	}}
	if err := s.Write(strings.Repeat("x", 4001)); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 1 || sizes[0] != 4000 {
		t.Fatalf("default MaxChars: sizes=%v", sizes)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[1] != 1 {
		t.Fatalf("remainder: sizes=%v", sizes)
	}
}

func TestHealthMonitorTripAndRecovery(t *testing.T) {
	var notes []string
	h := &HealthMonitor{Notify: func(s string) { notes = append(notes, s) }}
	if !h.IsHealthy() {
		t.Fatal("fresh monitor: want healthy")
	}
	h.RecordFailure(errors.New("poll timeout"))
	h.RecordFailure(errors.New("poll timeout"))
	if !h.IsHealthy() {
		t.Fatal("streak 2: want still healthy")
	}
	if len(notes) != 0 {
		t.Fatalf("streak 2: want no notify, got %q", notes)
	}
	h.RecordFailure(errors.New("poll timeout"))
	if h.IsHealthy() {
		t.Fatal("streak 3: want unhealthy")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "telegram unhealthy") {
		t.Fatalf("streak 3: want one 'telegram unhealthy' notify, got %q", notes)
	}
	h.RecordFailure(errors.New("still down"))
	if len(notes) != 1 {
		t.Fatalf("streak 4: notify must fire once, got %q", notes)
	}
	h.RecordSuccess()
	if !h.IsHealthy() {
		t.Fatal("after success: want healthy")
	}
	h.RecordFailure(errors.New("flap"))
	if !h.IsHealthy() || len(notes) != 1 {
		t.Fatalf("after recovery single failure must not re-notify: healthy=%v notes=%q",
			h.IsHealthy(), notes)
	}
	if h.LastOK.IsZero() {
		t.Fatal("RecordSuccess must stamp LastOK")
	}
}
