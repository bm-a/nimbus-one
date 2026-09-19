package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tgRichRecorder fakes the Telegram Bot API: every method answers
// {"ok":true} and records calls. failMethod forces a 400 for one method
// (used to exercise the edit-fallback path).
type tgRichRecorder struct {
	mu         sync.Mutex
	calls      []string
	payloads   map[string][]map[string]any
	failMethod string
}

func (f *tgRichRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		// Path looks like /bot<token>/<method>.
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		f.mu.Lock()
		f.calls = append(f.calls, method)
		if f.payloads == nil {
			f.payloads = map[string][]map[string]any{}
		}
		f.payloads[method] = append(f.payloads[method], payload)
		f.mu.Unlock()
		if method == f.failMethod {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"forced failure"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}
}

func (f *tgRichRecorder) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.payloads[method])
}

func (f *tgRichRecorder) first(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.payloads[method]) == 0 {
		return nil
	}
	return f.payloads[method][0]
}

func newRichTelegram(t *testing.T, eng *stubEngine, rec *tgRichRecorder) (*Telegram, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(rec.handler())
	t.Cleanup(ts.Close)
	return &Telegram{Token: "test-token", APIBase: ts.URL, Broker: New(eng, "sys")}, ts
}

func TestTelegramOffsetSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "offset")
	tg := &Telegram{OffsetFile: path}
	if got := tg.loadOffset(); got != 0 {
		t.Fatalf("missing file load = %d, want 0", got)
	}
	if err := tg.saveOffset(12345); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := tg.loadOffset(); got != 12345 {
		t.Fatalf("load = %d, want 12345", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "12345" {
		t.Fatalf("file content = %q, want 12345", data)
	}
	// Corrupt content degrades to 0, never an error spiral.
	if err := os.WriteFile(path, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tg.loadOffset(); got != 0 {
		t.Fatalf("corrupt load = %d, want 0", got)
	}
	// Empty OffsetFile: persistence disabled, always no-op/zero.
	plain := &Telegram{}
	if err := plain.saveOffset(99); err != nil {
		t.Fatalf("empty-path save = %v, want nil", err)
	}
	if got := plain.loadOffset(); got != 0 {
		t.Fatalf("empty-path load = %d, want 0", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	resp := func(v string) *http.Response {
		r := &http.Response{Header: http.Header{}}
		if v != "" {
			r.Header.Set("Retry-After", v)
		}
		return r
	}
	if got := parseRetryAfter(nil); got != 0 {
		t.Fatalf("nil = %v, want 0", got)
	}
	if got := parseRetryAfter(resp("")); got != 0 {
		t.Fatalf("missing = %v, want 0", got)
	}
	if got := parseRetryAfter(resp("3")); got != 3*time.Second {
		t.Fatalf("3 = %v, want 3s", got)
	}
	if got := parseRetryAfter(resp("9999")); got != 300*time.Second {
		t.Fatalf("9999 = %v, want capped 5m", got)
	}
	if got := parseRetryAfter(resp("-5")); got != 0 {
		t.Fatalf("negative = %v, want 0", got)
	}
	if got := parseRetryAfter(resp("garbage")); got != 0 {
		t.Fatalf("garbage = %v, want 0", got)
	}
	future := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(resp(future)); got <= 0 || got > 5*time.Minute {
		t.Fatalf("http-date = %v, want (0,5m]", got)
	}
}

func TestTelegramCallbackRouting(t *testing.T) {
	eng := &stubEngine{reply: "approved!"}
	rec := &tgRichRecorder{}
	tg, _ := newRichTelegram(t, eng, rec)
	ctx := context.Background()
	tg.handleUpdate(ctx, tgUpdate{UpdateID: 1, CallbackQuery: &tgCallbackQuery{
		ID:      "q1",
		From:    &tgUser{ID: 42},
		Message: &tgMessage{Chat: tgChat{ID: 7}},
		Data:    "approve_1",
	}})
	if !strings.Contains(eng.last, "button:approve_1") {
		t.Fatalf("engine input = %q, want button:approve_1", eng.last)
	}
	if rec.count("answerCallbackQuery") != 1 {
		t.Fatalf("answerCallbackQuery calls = %d, want 1", rec.count("answerCallbackQuery"))
	}
	if got := rec.first("answerCallbackQuery")["callback_query_id"]; got != "q1" {
		t.Fatalf("callback_query_id = %v, want q1", got)
	}
	sent := rec.first("sendMessage")
	if sent == nil {
		t.Fatal("no sendMessage call recorded")
	}
	if sent["text"] != "approved!" {
		t.Fatalf("reply text = %v, want approved!", sent["text"])
	}
	if chat, _ := sent["chat_id"].(float64); chat != 7 {
		t.Fatalf("reply chat = %v, want 7", sent["chat_id"])
	}
}

func TestTelegramPollRouting(t *testing.T) {
	eng := &stubEngine{reply: "vote counted"}
	rec := &tgRichRecorder{}
	tg, _ := newRichTelegram(t, eng, rec)
	tg.handleUpdate(context.Background(), tgUpdate{UpdateID: 2, PollAnswer: &tgPollAnswer{
		PollID:    "p9",
		User:      &tgUser{ID: 55},
		OptionIDs: []int{0, 2},
	}})
	if !strings.Contains(eng.last, "poll:p9:0,2") {
		t.Fatalf("engine input = %q, want poll:p9:0,2", eng.last)
	}
	sent := rec.first("sendMessage")
	if sent == nil || sent["text"] != "vote counted" {
		t.Fatalf("reply = %v, want vote counted", sent)
	}
	if chat, _ := sent["chat_id"].(float64); chat != 55 {
		t.Fatalf("reply chat = %v, want voter's private chat 55", sent["chat_id"])
	}
}

func TestTelegramEditedMessageEditsInPlace(t *testing.T) {
	eng := &stubEngine{reply: "updated answer"}
	rec := &tgRichRecorder{}
	tg, _ := newRichTelegram(t, eng, rec)
	tg.handleUpdate(context.Background(), tgUpdate{UpdateID: 3, EditedMessage: &tgMessage{
		MessageID: 10,
		From:      &tgUser{ID: 42},
		Chat:      tgChat{ID: 7},
		Text:      "revised question",
	}})
	if !strings.Contains(eng.last, "revised question") {
		t.Fatalf("engine input = %q, want revised question", eng.last)
	}
	edited := rec.first("editMessageText")
	if edited == nil {
		t.Fatal("no editMessageText call recorded")
	}
	if edited["text"] != "updated answer" {
		t.Fatalf("edited text = %v, want updated answer", edited["text"])
	}
	if rec.count("sendMessage") != 0 {
		t.Fatalf("sendMessage calls = %d, want 0 (edit succeeded)", rec.count("sendMessage"))
	}
}

func TestTelegramEditedMessageFallbackOnEditFailure(t *testing.T) {
	eng := &stubEngine{reply: "fallback answer"}
	rec := &tgRichRecorder{failMethod: "editMessageText"}
	tg, _ := newRichTelegram(t, eng, rec)
	tg.handleUpdate(context.Background(), tgUpdate{UpdateID: 4, EditedMessage: &tgMessage{
		MessageID: 11,
		From:      &tgUser{ID: 42},
		Chat:      tgChat{ID: 7},
		Text:      "another revision",
	}})
	if rec.count("editMessageText") != 1 {
		t.Fatalf("edit attempts = %d, want 1", rec.count("editMessageText"))
	}
	sent := rec.first("sendMessage")
	if sent == nil || sent["text"] != "fallback answer" {
		t.Fatalf("fallback reply = %v, want fallback answer", sent)
	}
}

func TestTelegramCommandDispatch(t *testing.T) {
	eng := &stubEngine{reply: "engine-reply"}
	rec := &tgRichRecorder{}
	tg, _ := newRichTelegram(t, eng, rec)
	var gotArgs, gotSender string
	tg.RegisterCommand("echo", func(ctx context.Context, chatID int64, sender, args string) {
		gotArgs, gotSender = args, sender
		_ = tg.sendMessage(ctx, chatID, "echo:"+args)
	})
	msg := func(text string) tgUpdate {
		return tgUpdate{UpdateID: 5, Message: &tgMessage{
			From: &tgUser{ID: 42},
			Chat: tgChat{ID: 7},
			Text: text,
		}}
	}
	ctx := context.Background()
	tg.handleUpdate(ctx, msg("/echo hello world"))
	if gotArgs != "hello world" || gotSender != "42" {
		t.Fatalf("echo got args=%q sender=%q", gotArgs, gotSender)
	}
	if eng.calls != 0 {
		t.Fatalf("engine calls = %d, want 0 (command handled)", eng.calls)
	}
	// Unknown commands fall through to the broker.
	tg.handleUpdate(ctx, msg("/frobnicate xyz"))
	if eng.calls != 1 || !strings.Contains(eng.last, "/frobnicate xyz") {
		t.Fatalf("fallthrough calls=%d input=%q", eng.calls, eng.last)
	}
	// /help lists registered commands.
	tg.handleUpdate(ctx, msg("/help"))
	help := rec.first("sendMessage")
	_ = help
	var helpText string
	rec.mu.Lock()
	for _, p := range rec.payloads["sendMessage"] {
		if s, _ := p["text"].(string); strings.Contains(s, "/echo") {
			helpText = s
		}
	}
	rec.mu.Unlock()
	if !strings.Contains(helpText, "/echo") || !strings.Contains(helpText, "/help") || !strings.Contains(helpText, "/reset") {
		t.Fatalf("/help text = %q, want /echo+/help+/reset", helpText)
	}
	// /reset clears the telegram/42 session.
	tg.handleUpdate(ctx, msg("remember this"))
	if n := len(tg.Broker.SessionsStore().History(Key("telegram", "42"), 0)); n == 0 {
		t.Fatal("expected session history before /reset")
	}
	tg.handleUpdate(ctx, msg("/reset"))
	if n := len(tg.Broker.SessionsStore().History(Key("telegram", "42"), 0)); n != 0 {
		t.Fatalf("history after /reset = %d entries, want 0", n)
	}
}

func TestTelegramGroupContext(t *testing.T) {
	tg := &Telegram{}
	if _, err := tg.GroupContext(1); err == nil {
		t.Fatal("expected error with nil policy store")
	}
	tg.Policy = &PolicyStore{Groups: map[int64]GroupPolicy{
		-100: {AllowedTools: []string{"bash"}, DeniedTools: []string{"web"}, SystemPrompt: "terse"},
	}}
	gp, err := tg.GroupContext(-100)
	if err != nil {
		t.Fatalf("GroupContext: %v", err)
	}
	if len(gp.AllowedTools) != 1 || gp.AllowedTools[0] != "bash" || gp.SystemPrompt != "terse" {
		t.Fatalf("group policy = %+v", gp)
	}
	if gp, err := tg.GroupContext(999); err != nil || len(gp.AllowedTools) != 0 {
		t.Fatalf("unknown group = %+v err=%v, want zero policy", gp, err)
	}
}

func TestBrokerTrimSession(t *testing.T) {
	var nilBroker *Broker
	nilBroker.TrimSession("telegram", "u", 2) // must not panic
	b := New(&stubEngine{reply: "x"}, "")
	b.TrimSession("telegram", "u", 2) // empty history: no-op
	key := Key("telegram", "u")
	for i := 0; i < 6; i++ {
		b.SessionsStore().Append(key, "user", strings.Repeat("m", i+1))
	}
	b.TrimSession("telegram", "u", 0) // non-positive: no-op, never a wipe
	if n := len(b.SessionsStore().History(key, 0)); n != 6 {
		t.Fatalf("after n=0 trim len = %d, want 6", n)
	}
	b.TrimSession("telegram", "u", 99) // >= len: no-op
	if n := len(b.SessionsStore().History(key, 0)); n != 6 {
		t.Fatalf("after oversize trim len = %d, want 6", n)
	}
	b.TrimSession("telegram", "u", 2)
	h := b.SessionsStore().History(key, 0)
	if len(h) != 2 {
		t.Fatalf("after trim len = %d, want 2", len(h))
	}
	if h[0].Content != "mmmmm" || h[1].Content != "mmmmmm" {
		t.Fatalf("tail = %+v, want last two", h)
	}
}
