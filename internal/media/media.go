// Package media provides speech-to-text and text-to-speech for Telegram
// voice notes (.ogg/Opus) and the web console. Local-first, API second:
// no ffmpeg, no heavy deps — audio travels as-is and transcription uses
// whisper.cpp when present, else any OpenAI-compatible endpoint.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// STTConfig controls transcription. Empty = auto-detect.
type STTConfig struct {
	// Endpoint overrides everything (OpenAI-compatible /audio/transcriptions).
	Endpoint string
	// APIKey used when Endpoint is default or custom.
	APIKey string
	// Model defaults to whisper-1.
	Model string
	// WhisperBin / WhisperModel force local whisper.cpp; empty = auto-find.
	WhisperBin   string
	WhisperModel string
	Timeout      time.Duration
}

func (c STTConfig) model() string {
	if strings.TrimSpace(c.Model) != "" {
		return c.Model
	}
	return "whisper-1"
}

func (c STTConfig) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 120 * time.Second
}

// FindWhisper locates a whisper.cpp binary + model file. Returns paths or
// an explanatory error — never panics, never downloads.
func FindWhisper(binOverride, modelOverride string) (bin, model string, err error) {
	if binOverride != "" {
		bin = binOverride
	} else {
		for _, cand := range []string{"whisper-cpp", "whisper-cli", "whisper"} {
			if p, lerr := exec.LookPath(cand); lerr == nil {
				bin = p
				break
			}
		}
	}
	if bin == "" {
		return "", "", fmt.Errorf("no whisper.cpp binary (looked for whisper-cpp, whisper-cli, whisper)")
	}
	if modelOverride != "" {
		model = modelOverride
	} else {
		home, _ := os.UserHomeDir()
		for _, cand := range []string{
			"models/ggml-base.en.bin",
			"models/ggml-small.en.bin",
			filepath.Join(home, ".cache", "whisper", "ggml-base.en.bin"),
			filepath.Join(home, ".local", "share", "whisper", "ggml-base.en.bin"),
		} {
			if _, serr := os.Stat(cand); serr == nil {
				model = cand
				break
			}
		}
	}
	if model == "" {
		return "", "", fmt.Errorf("whisper binary found but no model file — set NIMBUS_WHISPER_MODEL to a ggml .bin")
	}
	return bin, model, nil
}

// Transcribe converts an audio file (.ogg/.opus from Telegram voice notes
// sent as-is, no conversion) to text. Order: explicit endpoint → local
// whisper.cpp → OpenAI Whisper → guided error.
func Transcribe(ctx context.Context, audioPath string, cfg STTConfig) (string, error) {
	if strings.TrimSpace(audioPath) == "" {
		return "", fmt.Errorf("stt: empty audio path")
	}
	if _, err := os.Stat(audioPath); err != nil {
		return "", fmt.Errorf("stt: unreadable audio: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("NIMBUS_STT_URL"))
	}
	if endpoint != "" {
		return postTranscription(ctx, endpoint, cfg.APIKey, cfg.model(), audioPath)
	}
	if bin, model, err := FindWhisper(cfg.WhisperBin, cfg.WhisperModel); err == nil {
		return runWhisper(ctx, bin, model, audioPath)
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if key != "" {
		return postTranscription(ctx, "https://api.openai.com/v1/audio/transcriptions", key, cfg.model(), audioPath)
	}
	return "", fmt.Errorf("stt: nothing configured — install whisper.cpp with a ggml model (NIMBUS_WHISPER_MODEL), set NIMBUS_STT_URL, or provide an OpenAI key")
}

func runWhisper(ctx context.Context, bin, model, audio string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "-m", model, "-f", audio, "-nt", "-np")
	var out, serr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &serr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("stt: whisper.cpp failed: %v (%s)", err, strings.TrimSpace(serr.String()))
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", fmt.Errorf("stt: whisper.cpp returned empty transcript")
	}
	return text, nil
}

func postTranscription(ctx context.Context, endpoint, apiKey, model, audioPath string) (string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("model", model); err != nil {
		return "", err
	}
	part, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("User-Agent", "NimbusOne-STT/1.0")
	hc := &http.Client{Timeout: 120 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt: endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "", fmt.Errorf("stt: key rejected (status %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("stt: status %d: %.200s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("stt: bad response: %w", err)
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", fmt.Errorf("stt: empty transcript")
	}
	return strings.TrimSpace(out.Text), nil
}

// TTSConfig controls speech synthesis.
type TTSConfig struct {
	// Endpoint for OpenAI-compatible /audio/speech. Empty = auto.
	Endpoint string
	APIKey   string
	Model    string // default tts-1
	Voice    string // default alloy
	Format   string // mp3 (api) — local engines pick their own container
	Timeout  time.Duration
}

func (c TTSConfig) model() string {
	if strings.TrimSpace(c.Model) != "" {
		return c.Model
	}
	return "tts-1"
}

// Speak plays text NOW on local speakers (no file). Termux → termux-tts-speak,
// macOS → say, Linux → espeak-ng/espeak. Returns a clear error when no
// local engine exists (use Synthesize+API instead).
func Speak(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("tts: empty text")
	}
	var bin string
	var args []string
	switch {
	case commandExists("termux-tts-speak"):
		bin, args = "termux-tts-speak", []string{text}
	case runtime.GOOS == "darwin":
		bin, args = "say", []string{text}
	case commandExists("espeak-ng"):
		bin, args = "espeak-ng", []string{text}
	case commandExists("espeak"):
		bin, args = "espeak", []string{text}
	default:
		return fmt.Errorf("tts: no local speech engine (termux-tts-speak/say/espeak-ng) — use the web console speaker button or configure an API voice")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tts: %s failed: %v (%.200s)", bin, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Synthesize renders text to an audio FILE (temp dir, caller removes).
// Local file engines first (espeak-ng/say), else OpenAI-compatible API.
func Synthesize(ctx context.Context, text string, cfg TTSConfig) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("tts: empty text")
	}
	if len(text) > 4000 {
		text = text[:4000] // API + engine sanity bound
	}
	if path, err := synthLocal(ctx, text); err == nil {
		return path, nil
	} else {
		_ = err // local miss is normal on phones; fall through to API
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if key == "" {
		return "", fmt.Errorf("tts: no local file engine and no API key — install espeak-ng, or set an OpenAI key for API voices")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("NIMBUS_TTS_URL"))
	}
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/audio/speech"
	}
	return postSpeech(ctx, endpoint, key, cfg.model(), firstNonEmpty(cfg.Voice, "alloy"), text)
}

func synthLocal(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	tmp, err := os.CreateTemp("", "nimbus-tts-*.wav")
	if err != nil {
		return "", err
	}
	tmp.Close()
	out := tmp.Name()
	var cmd *exec.Cmd
	switch {
	case commandExists("espeak-ng"):
		cmd = exec.CommandContext(ctx, "espeak-ng", "-w", out, text)
	case runtime.GOOS == "darwin":
		cmd = exec.CommandContext(ctx, "say", "-o", out, text)
	default:
		os.Remove(out)
		return "", fmt.Errorf("no local file engine")
	}
	if err := cmd.Run(); err != nil {
		os.Remove(out)
		return "", err
	}
	return out, nil
}

func postSpeech(ctx context.Context, endpoint, apiKey, model, voice, text string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model": model, "voice": voice, "input": text, "response_format": "mp3",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "NimbusOne-TTS/1.0")
	hc := &http.Client{Timeout: 120 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("tts: unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("tts: status %d: %.200s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	tmp, err := os.CreateTemp("", "nimbus-tts-*.mp3")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 25<<20)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
