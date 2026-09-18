package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTranscribeEndpointOK(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "x"}, "")}
	s.Transcribe = func(ctx context.Context, path string) (string, error) {
		b, err := os.ReadFile(path)
		if err != nil || len(b) == 0 {
			t.Errorf("audio not stored: %v", err)
		}
		return "heard-it", nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("audio", "voice.ogg")
	part.Write([]byte("OggSfake"))
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcribe", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Text != "heard-it" {
		t.Fatalf("out=%q err=%v", rec.Body.String(), err)
	}
}

func TestTranscribeUnconfigured501(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "x"}, "")}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("audio", "voice.ogg")
	part.Write([]byte("x"))
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcribe", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("want 501 guidance, got %d", rec.Code)
	}
}

func TestSpeakEndpointOK(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "x"}, "")}
	s.Synthesize = func(ctx context.Context, text string) (string, error) {
		f, _ := os.CreateTemp("", "speak-*.mp3")
		f.Write([]byte("ID3fake"))
		f.Close()
		return f.Name(), nil
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speak", strings.NewReader(`{"text":"hi"}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	defer os.RemoveAll("/tmp/speak-*")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "audio/") {
		t.Fatalf("code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// fakeTg pretends to be api.telegram.org: getFile + file download + records sends.
type fakeTg struct {
	t         *testing.T
	sentText  []string
	sentAudio int
	ogg       []byte
}

func (f *fakeTg) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/getFile"):
		w.Write([]byte(`{"ok":true,"result":{"file_path":"voice/file_1.ogg"}}`))
	case strings.Contains(r.URL.Path, "/file/bot"):
		w.Write(f.ogg)
	case strings.HasSuffix(r.URL.Path, "/sendAudio"):
		f.sentAudio++
		w.Write([]byte(`{"ok":true}`))
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		if tx, _ := m["text"].(string); tx != "" {
			f.sentText = append(f.sentText, tx)
		}
		w.Write([]byte(`{"ok":true}`))
	default:
		w.WriteHeader(404)
	}
}

func TestTelegramVoiceToBroker(t *testing.T) {
	fake := &fakeTg{t: t, ogg: []byte("OggSfake-voice")}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	var gotText string
	eng := &stubEngine{reply: "voice-answer"}
	b := New(eng, "")
	tg := &Telegram{Token: "x", Broker: b, APIBase: srv.URL}
	tg.Transcriber = func(ctx context.Context, path string) (string, error) {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != "OggSfake-voice" {
			t.Errorf("ogg not downloaded intact: %v", err)
		}
		return "transcribed-words", nil
	}
	u := tgUpdate{Message: &tgMessage{
		From: &tgUser{ID: 7}, Chat: tgChat{ID: 7},
		Voice: &tgVoice{FileID: "fid123", Duration: 4},
	}}
	tg.handleUpdate(context.Background(), u)
	_ = gotText
	// Broker session must contain the transcript.
	hist := b.SessionsStore().History(Key("telegram", "7"), 5)
	found := false
	for _, m := range hist {
		if strings.Contains(m.Content, "transcribed-words") {
			found = true
		}
	}
	if !found {
		t.Fatalf("transcript never reached broker: %+v", hist)
	}
	if len(fake.sentText) == 0 {
		t.Fatal("no reply sent to chat")
	}
}

func TestTelegramVoiceNoTranscriberGuides(t *testing.T) {
	fake := &fakeTg{t: t}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	tg := &Telegram{Token: "x", Broker: New(&stubEngine{reply: "x"}, ""), APIBase: srv.URL}
	u := tgUpdate{Message: &tgMessage{
		From: &tgUser{ID: 7}, Chat: tgChat{ID: 7},
		Voice: &tgVoice{FileID: "fid"},
	}}
	tg.handleUpdate(context.Background(), u)
	if len(fake.sentText) == 0 || !strings.Contains(fake.sentText[0], "transcription") {
		t.Fatalf("must guide setup: %v", fake.sentText)
	}
}

func TestTelegramSpeakCommand(t *testing.T) {
	fake := &fakeTg{t: t}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	tg := &Telegram{Token: "x", Broker: New(&stubEngine{reply: "x"}, ""), APIBase: srv.URL}
	f, _ := os.CreateTemp("", "spk-*.mp3")
	f.Write([]byte("ID3fake"))
	f.Close()
	defer os.Remove(f.Name())
	tg.Speaker = func(ctx context.Context, text string) (string, error) {
		if text != "hello there" {
			t.Errorf("speak text = %q", text)
		}
		return f.Name(), nil
	}
	u := tgUpdate{Message: &tgMessage{
		From: &tgUser{ID: 7}, Chat: tgChat{ID: 7}, Text: "/speak hello there",
	}}
	tg.handleUpdate(context.Background(), u)
	if fake.sentAudio != 1 {
		t.Fatalf("sendAudio calls = %d", fake.sentAudio)
	}
}
