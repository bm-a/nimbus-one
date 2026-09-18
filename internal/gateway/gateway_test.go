package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubEngine struct {
	reply string
	err   error
	calls int
	last  string
}

func (s *stubEngine) Run(ctx context.Context, system, user string) (string, error) {
	s.calls++
	s.last = user
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

func TestSessionsAppendHistoryReset(t *testing.T) {
	s := NewSessions()
	key := Key("http", "u1")
	s.Append(key, "user", "hi")
	s.Append(key, "assistant", "hello")
	h := s.History(key, 0)
	if len(h) != 2 {
		t.Fatalf("History len = %d, want 2", len(h))
	}
	if h[0].Role != "user" || h[0].Content != "hi" {
		t.Fatalf("h[0] = %+v", h[0])
	}
	last := s.History(key, 1)
	if len(last) != 1 || last[0].Content != "hello" {
		t.Fatalf("last = %+v", last)
	}
	s.Reset(key)
	if got := s.History(key, 0); len(got) != 0 {
		t.Fatalf("after Reset History = %v, want empty", got)
	}
}

func TestBrokerHandleRecordsSession(t *testing.T) {
	eng := &stubEngine{reply: "world"}
	b := New(eng, "sys")
	reply := b.Handle("http", "alice", "hello")
	if reply != "world" {
		t.Fatalf("Handle = %q, want world", reply)
	}
	if eng.calls != 1 {
		t.Fatalf("engine calls = %d, want 1", eng.calls)
	}
	h := b.SessionsStore().History(Key("http", "alice"), 0)
	if len(h) != 2 {
		t.Fatalf("session len = %d, want 2 (user+assistant)", len(h))
	}
	if h[0].Role != "user" || h[1].Role != "assistant" {
		t.Fatalf("roles = %+v", h)
	}
}

func TestBrokerHandleEngineError(t *testing.T) {
	eng := &stubEngine{err: fmt.Errorf("boom")}
	b := New(eng, "sys")
	reply := b.Handle("http", "bob", "hi")
	if !strings.Contains(reply, "boom") {
		t.Fatalf("Handle = %q, want error text", reply)
	}
}

func TestHTTPHealthz(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "ok"}, "")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestHTTPChatWithToken(t *testing.T) {
	s := &Server{Token: "secret", Broker: New(&stubEngine{reply: "hi there"}, "")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Without token -> 401.
	resp, err := http.Post(ts.URL+"/api/v1/chat", "application/json", strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", resp.StatusCode)
	}

	// With token -> 200 with reply.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/chat", strings.NewReader(`{"message":"hello","user":"u"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body), "hi there") {
		t.Fatalf("body = %q, want reply", body)
	}
}

func TestHTTPChatBadJSON400(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "x"}, "")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/v1/chat", "application/json", strings.NewReader(`{bad json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestChunkMessageSplit(t *testing.T) {
	s := strings.Repeat("a", 50) + "\n" + strings.Repeat("b", 50)
	chunks := chunkMessage(s, 60)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2: %q", len(chunks), chunks)
	}
	for _, c := range chunks {
		if len(c) > 60 {
			t.Fatalf("chunk too long (%d): %q", len(c), c)
		}
	}
	joined := strings.Join(chunks, "\n")
	if joined != s {
		t.Fatalf("rejoined mismatch:\n%q\nvs\n%q", joined, s)
	}
	// No newlines: hard split.
	big := strings.Repeat("z", 5000)
	chunks = chunkMessage(big, 4000)
	if len(chunks) != 2 || len(chunks[0]) != 4000 {
		t.Fatalf("hard split chunks = %v lens", len(chunks))
	}
	// Small message: single chunk.
	if got := chunkMessage("hi", 4000); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("small = %v", got)
	}
}

func TestTelegramAllowed(t *testing.T) {
	open := &Telegram{}
	if !open.Allowed("anything") {
		t.Fatal("empty Allow should permit all")
	}
	locked := &Telegram{Allow: []string{"123", "456"}}
	if !locked.Allowed("123") {
		t.Fatal("123 should be allowed")
	}
	if locked.Allowed("999") {
		t.Fatal("999 should be denied")
	}
}

func TestChunkDiscord(t *testing.T) {
	s := strings.Repeat("x", 2100)
	chunks := chunkDiscord(s, 2000)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if got := chunkDiscord("hi", 0); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("default limit small = %v", got)
	}
}

func TestDiscordSendValidation(t *testing.T) {
	d := &Discord{}
	if err := d.Send(context.Background(), "chan", "hi"); err == nil {
		t.Fatal("expected error with empty token")
	}
	d2 := &Discord{Token: "tok"}
	if err := d2.Send(context.Background(), "", "hi"); err == nil {
		t.Fatal("expected error with empty channel id")
	}
}

func TestTelegramSendEmptyToken(t *testing.T) {
	tg := &Telegram{}
	if err := tg.sendMessage(context.Background(), 1, "hi"); err == nil {
		t.Fatal("expected error with empty token")
	}
}
