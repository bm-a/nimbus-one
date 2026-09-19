package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func botAPIHandler(t *testing.T, token, method string, check func(t *testing.T, payload map[string]any), reply any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		want := "/bot" + token + "/" + method
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if check != nil {
			check(t, payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}
}

func TestTelegramReactSuccess(t *testing.T) {
	const token = "test-token"
	var gotChat, gotMsg float64
	var gotEmoji string
	ts := httptest.NewServer(botAPIHandler(t, token, "setMessageReaction", func(t *testing.T, p map[string]any) {
		var ok bool
		gotChat, ok = p["chat_id"].(float64)
		if !ok {
			t.Errorf("chat_id missing: %v", p)
		}
		gotMsg, ok = p["message_id"].(float64)
		if !ok {
			t.Errorf("message_id missing: %v", p)
		}
		reactions, ok := p["reaction"].([]any)
		if !ok || len(reactions) != 1 {
			t.Fatalf("reaction = %v, want single entry", p["reaction"])
		}
		m, _ := reactions[0].(map[string]any)
		if m["type"] != "emoji" {
			t.Errorf("reaction type = %v, want emoji", m["type"])
		}
		gotEmoji, _ = m["emoji"].(string)
	}, map[string]any{"ok": true, "result": true}))
	defer ts.Close()

	tg := &Telegram{Token: token, APIBase: ts.URL}
	if err := tg.React(context.Background(), 123, 456, "👍"); err != nil {
		t.Fatalf("React: %v", err)
	}
	if gotChat != 123 || gotMsg != 456 || gotEmoji != "👍" {
		t.Fatalf("payload = chat %v msg %v emoji %q", gotChat, gotMsg, gotEmoji)
	}
}

func TestTelegramReact401(t *testing.T) {
	const token = "bad-token"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer ts.Close()

	tg := &Telegram{Token: token, APIBase: ts.URL}
	err := tg.React(context.Background(), 1, 2, "🔥")
	if err == nil {
		t.Fatal("expected 401 error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should mention 401: %v", err)
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("error should include truncated body: %v", err)
	}
}

func TestTelegramEditSuccess(t *testing.T) {
	const token = "test-token"
	var gotText string
	ts := httptest.NewServer(botAPIHandler(t, token, "editMessageText", func(t *testing.T, p map[string]any) {
		gotText, _ = p["text"].(string)
		if p["chat_id"] == nil || p["message_id"] == nil {
			t.Errorf("missing ids: %v", p)
		}
	}, map[string]any{"ok": true, "result": map[string]any{"message_id": 7}}))
	defer ts.Close()

	tg := &Telegram{Token: token, APIBase: ts.URL}
	if err := tg.Edit(context.Background(), 11, 22, "new text"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if gotText != "new text" {
		t.Fatalf("text = %q", gotText)
	}
}

func TestTelegramEdit401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized: bot was blocked"}`))
	}))
	defer ts.Close()

	tg := &Telegram{Token: "tok", APIBase: ts.URL}
	if err := tg.Edit(context.Background(), 1, 2, "hi"); err == nil {
		t.Fatal("expected 401 error")
	} else if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("error = %v, want 401 + body", err)
	}
}

func TestTelegramUnsendSuccess(t *testing.T) {
	const token = "test-token"
	ts := httptest.NewServer(botAPIHandler(t, token, "deleteMessage", func(t *testing.T, p map[string]any) {
		if p["chat_id"] == nil || p["message_id"] == nil {
			t.Errorf("missing ids: %v", p)
		}
	}, map[string]any{"ok": true, "result": true}))
	defer ts.Close()

	tg := &Telegram{Token: token, APIBase: ts.URL}
	if err := tg.Unsend(context.Background(), 9, 10); err != nil {
		t.Fatalf("Unsend: %v", err)
	}
}

func TestTelegramUnsend401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer ts.Close()

	tg := &Telegram{Token: "tok", APIBase: ts.URL}
	if err := tg.Unsend(context.Background(), 1, 2); err == nil {
		t.Fatal("expected 401 error")
	} else if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want 401", err)
	}
}

func TestTelegramSendPollSuccess(t *testing.T) {
	const token = "test-token"
	var gotQ string
	var gotOpts []any
	ts := httptest.NewServer(botAPIHandler(t, token, "sendPoll", func(t *testing.T, p map[string]any) {
		gotQ, _ = p["question"].(string)
		gotOpts, _ = p["options"].([]any)
	}, map[string]any{"ok": true, "result": map[string]any{"poll": map[string]any{"id": "poll-123"}}}))
	defer ts.Close()

	tg := &Telegram{Token: token, APIBase: ts.URL}
	id, err := tg.SendPoll(context.Background(), 5, "Best?", []string{"a", "b"})
	if err != nil {
		t.Fatalf("SendPoll: %v", err)
	}
	if id != "poll-123" {
		t.Fatalf("poll id = %q, want poll-123", id)
	}
	if gotQ != "Best?" || len(gotOpts) != 2 {
		t.Fatalf("payload q=%q opts=%v", gotQ, gotOpts)
	}
}

func TestTelegramSendPoll401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer ts.Close()

	tg := &Telegram{Token: "tok", APIBase: ts.URL}
	if _, err := tg.SendPoll(context.Background(), 1, "q?", []string{"a", "b"}); err == nil {
		t.Fatal("expected 401 error")
	} else if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("error = %v, want 401 + body", err)
	}
}

func TestTelegramActionsEmptyToken(t *testing.T) {
	tg := &Telegram{}
	if err := tg.React(context.Background(), 1, 1, "👍"); err == nil {
		t.Fatal("React should fail with empty token")
	}
	if err := tg.Edit(context.Background(), 1, 1, "x"); err == nil {
		t.Fatal("Edit should fail with empty token")
	}
	if err := tg.Unsend(context.Background(), 1, 1); err == nil {
		t.Fatal("Unsend should fail with empty token")
	}
	if _, err := tg.SendPoll(context.Background(), 1, "q", []string{"a", "b"}); err == nil {
		t.Fatal("SendPoll should fail with empty token")
	}
}

func TestPairingRequestVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pairing.json")
	s := NewPairingStore(path)

	code, err := s.RequestPairing("alice")
	if err != nil {
		t.Fatalf("RequestPairing: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 digits", code)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code = %q, want digits only", code)
		}
	}
	ids := s.List()
	if len(ids) != 1 || ids[0] != "alice" {
		t.Fatalf("List = %v, want [alice]", ids)
	}
	if !s.Verify(code, "alice") {
		t.Fatal("Verify correct code should succeed")
	}
	// Single-use: second attempt must fail.
	if s.Verify(code, "alice") {
		t.Fatal("Verify should be single-use")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List after verify = %v, want empty", got)
	}
}

func TestPairingWrongCode(t *testing.T) {
	dir := t.TempDir()
	s := NewPairingStore(filepath.Join(dir, "p.json"))
	code, err := s.RequestPairing("bob")
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "999999"
	}
	if s.Verify(wrong, "bob") {
		t.Fatal("wrong code should not verify")
	}
	// Correct code must still work after a wrong attempt.
	if !s.Verify(code, "bob") {
		t.Fatal("correct code should still verify after wrong attempt")
	}
}

func TestPairingExpiry(t *testing.T) {
	dir := t.TempDir()
	s := NewPairingStore(filepath.Join(dir, "p.json"))
	code, err := s.RequestPairing("carol")
	if err != nil {
		t.Fatal(err)
	}
	// Force expiry.
	s.mu.Lock()
	e := s.entries["carol"]
	e.ExpiresAt = time.Now().Add(-time.Minute)
	s.entries["carol"] = e
	s.mu.Unlock()

	if s.Verify(code, "carol") {
		t.Fatal("expired code should not verify")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("expired id should not be listed: %v", got)
	}
}

func TestPairingPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pairing.json")
	s := NewPairingStore(path)
	code, err := s.RequestPairing("dave")
	if err != nil {
		t.Fatal(err)
	}
	// File must exist with 0600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
	}
	// Reopen: pending id survives, code verifies.
	s2 := NewPairingStore(path)
	ids := s2.List()
	if len(ids) != 1 || ids[0] != "dave" {
		t.Fatalf("reopened List = %v, want [dave]", ids)
	}
	if !s2.Verify(code, "dave") {
		t.Fatal("reopened store should verify code")
	}
}
