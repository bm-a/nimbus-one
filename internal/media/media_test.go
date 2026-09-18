package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func writeOGG(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "voice-*.ogg")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Fake opus bytes — endpoint fakes don't decode.
	if _, err := f.Write([]byte("OggSfake-opus-payload")); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestTranscribeEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Content-Type"), "multipart/") {
			t.Error("want multipart upload")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello from voice"}`))
	}))
	defer srv.Close()
	got, err := Transcribe(context.Background(), writeOGG(t), STTConfig{Endpoint: srv.URL, APIKey: "k"})
	if err != nil || got != "hello from voice" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestTranscribeRejects401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	if _, err := Transcribe(context.Background(), writeOGG(t), STTConfig{Endpoint: srv.URL, APIKey: "bad"}); err == nil {
		t.Fatal("401 must fail")
	}
}

func TestTranscribeWhisperLocal(t *testing.T) {
	// Fake whisper.cpp binary speaking real whisper CLI protocol.
	dir := t.TempDir()
	bin := dir + "/whisper-cpp"
	script := "#!/bin/sh\necho \" palsgaard dansk test transcript\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	model := dir + "/ggml-tiny.bin"
	if err := os.WriteFile(model, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Transcribe(context.Background(), writeOGG(t), STTConfig{WhisperBin: bin, WhisperModel: model})
	if err != nil || !strings.Contains(got, "test transcript") {
		t.Fatalf("whisper path: got %q err %v", got, err)
	}
}

func TestFindWhisperPair(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/whisper-cpp"
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	model := dir + "/models/ggml-base.en.bin"
	if err := os.MkdirAll(dir+"/models", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if _, _, err := FindWhisper("", ""); err != nil {
		t.Fatalf("should find local pair: %v", err)
	}
}

func TestTranscribeUnguided(t *testing.T) {
	t.Setenv("NIMBUS_STT_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	// PATH without whisper binaries.
	t.Setenv("PATH", t.TempDir())
	if _, err := Transcribe(context.Background(), writeOGG(t), STTConfig{}); err == nil {
		t.Fatal("nothing configured must guide, not silently fail")
	} else if !strings.Contains(err.Error(), "whisper") && !strings.Contains(err.Error(), "NIMBUS_STT_URL") {
		t.Fatalf("must guide setup: %v", err)
	}
}

func TestTranscribeMissingFile(t *testing.T) {
	if _, err := Transcribe(context.Background(), "/nonexistent-voice.ogg", STTConfig{Endpoint: "http://x"}); err == nil {
		t.Fatal("missing file must fail")
	}
}

func TestSynthesizeEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3fake-mp3-bytes-padding-1234567890"))
	}))
	defer srv.Close()
	// Force API path: bogus PATH kills local engines on linux too? espeak
	// may exist — accept either local wav or API mp3, assert content sane.
	t.Setenv("PATH", t.TempDir())
	path, err := Synthesize(context.Background(), "hello", TTSConfig{Endpoint: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	defer os.Remove(path)
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		t.Fatalf("audio file bad: %v", st)
	}
}

func TestSpeakEmptyErrors(t *testing.T) {
	if err := Speak(context.Background(), "   "); err == nil {
		t.Fatal("empty text must fail")
	}
}

func TestFindWhisperMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, _, err := FindWhisper("", ""); err == nil {
		t.Fatal("missing whisper must explain, not silently fail")
	}
}
