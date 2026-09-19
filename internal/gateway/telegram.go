package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Telegram is a Bot API client bound to a Broker.
type Telegram struct {
	Token       string
	Broker      *Broker
	Allow       []string
	PollTimeout int // seconds for getUpdates long-poll; default 30
	// APIBase overrides https://api.telegram.org (tests).
	APIBase string
	// Transcriber converts a downloaded .ogg voice file to text.
	// Nil = voice notes get setup guidance instead of silence.
	Transcriber func(ctx context.Context, oggPath string) (string, error)
	// Speaker renders text to an audio file for /speak replies.
	// Nil = /speak answers with setup guidance.
	Speaker func(ctx context.Context, text string) (string, error)
	// Policy gates groups, chunk sizes, streaming and mentions.
	// Nil = open groups, default 4000 chunks, no mention gate.
	Policy *PolicyStore
	// Health tracks consecutive failures for the monitor.
	// Nil = no health tracking.
	Health *HealthMonitor
	// BotName enables @mention gating in groups (e.g. "nimbusbot").
	BotName string
}

// api builds a Bot API URL, honoring test overrides.
func (t *Telegram) api(method string) string {
	base := strings.TrimSuffix(t.APIBase, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return base + "/bot" + t.Token + "/" + method
}

// tgVoice is an incoming voice note (.ogg/Opus).
type tgVoice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration,omitempty"`
}

// tgUser is a Telegram user.
type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

// tgChat is a Telegram chat.
type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"`
}

// tgMessage is an incoming/outgoing message.
type tgMessage struct {
	MessageID int64    `json:"message_id,omitempty"`
	From      *tgUser  `json:"from,omitempty"`
	Chat      tgChat   `json:"chat"`
	Text      string   `json:"text,omitempty"`
	Voice     *tgVoice `json:"voice,omitempty"`
}

// tgUpdate is a getUpdates/webhook update.
type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message,omitempty"`
}

// Allowed reports whether sender id may use the bot. Empty Allow = all.
func (t *Telegram) Allowed(id string) bool {
	if len(t.Allow) == 0 {
		return true
	}
	id = strings.TrimSpace(id)
	for _, a := range t.Allow {
		if strings.TrimSpace(a) == id {
			return true
		}
	}
	return false
}

// chunkMessage splits s into chunks of at most limit bytes, preferring
// newline boundaries. limit <= 0 defaults to 4000.
func chunkMessage(s string, limit int) []string {
	if limit <= 0 {
		limit = 4000
	}
	if len(s) <= limit {
		return []string{s}
	}
	var out []string
	for len(s) > limit {
		cut := strings.LastIndex(s[:limit], "\n")
		if cut <= 0 {
			cut = limit
		}
		out = append(out, s[:cut])
		s = strings.TrimPrefix(s[cut:], "\n")
		if s == "" {
			break
		}
	}
	if s != "" {
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// pollTimeout returns the long-poll timeout in seconds.
func (t *Telegram) pollTimeout() int {
	if t.PollTimeout <= 0 {
		return 30
	}
	return t.PollTimeout
}

// sendMessage posts text to chatID via sendMessage.
func (t *Telegram) sendMessage(ctx context.Context, chatID int64, text string) error {
	if t.Token == "" {
		return fmt.Errorf("telegram: empty token")
	}
	body, err := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}
	endpoint := t.api("sendMessage")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage: status %s", resp.Status)
	}
	return nil
}

// handleUpdate routes one update's text (or transcribed voice) through the
// broker and replies chunked. /speak <text> answers with a voice message.
func (t *Telegram) handleUpdate(ctx context.Context, u tgUpdate) {
	if u.Message == nil {
		return
	}
	var sender string
	if u.Message.From != nil {
		sender = strconv.FormatInt(u.Message.From.ID, 10)
	} else {
		sender = strconv.FormatInt(u.Message.Chat.ID, 10)
	}
	if !t.Allowed(sender) {
		return
	}
	if t.Policy != nil && !t.Policy.IsGroupAllowed(u.Message.Chat.ID, u.Message.Chat.Type) {
		return // group not allowlisted / groups disabled
	}
	if t.Broker == nil {
		return
	}
	text := u.Message.Text
	if u.Message.Voice != nil {
		text = t.handleVoice(ctx, u.Message.Chat.ID, sender, u.Message.Voice)
		if text == "" {
			return // error already reported to chat
		}
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	if args, ok := strings.CutPrefix(strings.TrimSpace(text), "/speak "); ok {
		t.handleSpeakCmd(ctx, u.Message.Chat.ID, strings.TrimSpace(args))
		return
	}
	if strings.TrimSpace(text) == "/speak" {
		_ = t.sendMessage(ctx, u.Message.Chat.ID, "usage: /speak <text> — I reply with a voice message")
		return
	}
	if t.Policy != nil && !t.Policy.ShouldMention(text, t.BotName) {
		return // group requires @mention — silent skip, like OpenClaw
	}
	reply := t.Broker.Handle("telegram", sender, text)
	limit := 4000
	if t.Policy != nil {
		limit = t.Policy.ChunkLimit()
	}
	for _, chunk := range chunkMessage(reply, limit) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := t.sendMessage(ctx, u.Message.Chat.ID, chunk)
		if t.Health != nil {
			if err != nil {
				t.Health.RecordFailure(err)
			} else {
				t.Health.RecordSuccess()
			}
		}
	}
}

// handleVoice downloads a voice note, transcribes it, and feeds the text to
// the broker. Returns "" after reporting failures to the chat.
func (t *Telegram) handleVoice(ctx context.Context, chatID int64, sender string, v *tgVoice) string {
	if t.Transcriber == nil {
		_ = t.sendMessage(ctx, chatID, "Got your voice note, but transcription isn't set up — whisper.cpp, NIMBUS_STT_URL, or an OpenAI key. `nimbus-one transcribe <file>` documents the options.")
		return ""
	}
	path, err := t.downloadFile(ctx, v.FileID, "voice")
	if err != nil {
		_ = t.sendMessage(ctx, chatID, "Couldn't download that voice note: "+shortErr(err))
		return ""
	}
	defer removeFile(path)
	text, err := t.Transcriber(ctx, path)
	if err != nil {
		_ = t.sendMessage(ctx, chatID, "Couldn't transcribe that: "+shortErr(err))
		return ""
	}
	if strings.TrimSpace(text) == "" {
		_ = t.sendMessage(ctx, chatID, "Heard nothing transcribable — try again closer to the mic.")
		return ""
	}
	dur := ""
	if v.Duration > 0 {
		dur = fmt.Sprintf("0:%02d", v.Duration)
	}
	reply := t.Broker.Handle("telegram", sender, "[voice "+dur+"] "+text)
	for _, chunk := range chunkMessage(reply, 4000) {
		_ = t.sendMessage(ctx, chatID, chunk)
	}
	return "[voice " + dur + "] " + text
}

// handleSpeakCmd renders text to audio and sends it as a voice message.
func (t *Telegram) handleSpeakCmd(ctx context.Context, chatID int64, text string) {
	if t.Speaker == nil {
		_ = t.sendMessage(ctx, chatID, "Voice replies aren't set up — espeak-ng/say, or an OpenAI key for API voices.")
		return
	}
	path, err := t.Speaker(ctx, text)
	if err != nil {
		_ = t.sendMessage(ctx, chatID, "Couldn't synthesize that: "+shortErr(err))
		return
	}
	defer removeFile(path)
	if err := t.sendAudio(ctx, chatID, path); err != nil {
		_ = t.sendMessage(ctx, chatID, "Couldn't send the audio — replying as text instead:\n"+text)
	}
}

// downloadFile resolves a Telegram file_id via getFile and saves it locally.
func (t *Telegram) downloadFile(ctx context.Context, fileID, prefix string) (string, error) {
	if t.Token == "" {
		return "", fmt.Errorf("empty token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.api("getFile")+"?file_id="+url.QueryEscape(fileID), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &payload); err != nil || !payload.OK || payload.Result.FilePath == "" {
		return "", fmt.Errorf("getFile rejected the file_id")
	}
	host := t.api("x")
	if idx := strings.Index(host, "/bot"); idx >= 0 {
		host = host[:idx]
	}
	dl, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/file/bot"+t.Token+"/"+payload.Result.FilePath, nil)
	if err != nil {
		return "", err
	}
	dresp, err := http.DefaultClient.Do(dl)
	if err != nil {
		return "", err
	}
	defer dresp.Body.Close()
	if dresp.StatusCode < 200 || dresp.StatusCode >= 300 {
		return "", fmt.Errorf("file download: status %s", dresp.Status)
	}
	tmp, err := os.CreateTemp("", prefix+"-*.ogg")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, io.LimitReader(dresp.Body, 25<<20)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// sendAudio posts an audio file (voice reply) via multipart sendAudio.
func (t *Telegram) sendAudio(ctx context.Context, chatID int64, audioPath string) error {
	if t.Token == "" {
		return fmt.Errorf("empty token")
	}
	f, err := os.Open(audioPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	part, err := w.CreateFormFile("audio", filepath.Base(audioPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, io.LimitReader(f, 25<<20)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.api("sendAudio"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendAudio: status %s", resp.Status)
	}
	return nil
}

func shortErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func removeFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// RunPolling long-polls getUpdates until ctx is done.
func (t *Telegram) RunPolling(ctx context.Context) error {
	if t.Token == "" {
		return fmt.Errorf("telegram: empty token")
	}
	var offset int64
	timeout := t.pollTimeout()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		q := url.Values{}
		q.Set("offset", strconv.FormatInt(offset, 10))
		q.Set("timeout", strconv.Itoa(timeout))
		endpoint := t.api("getUpdates") + "?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		// Bound each poll beyond the long-poll window.
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+15)*time.Second)
		req = req.WithContext(callCtx)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				continue
			}
		} else {
			func() {
				defer resp.Body.Close()
				var payload struct {
					OK     bool       `json:"ok"`
					Result []tgUpdate `json:"result"`
				}
				data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
				if err != nil {
					return
				}
				if err := json.Unmarshal(data, &payload); err != nil {
					return
				}
				for _, u := range payload.Result {
					if u.UpdateID >= offset {
						offset = u.UpdateID + 1
					}
					t.handleUpdate(ctx, u)
					select {
					case <-ctx.Done():
						return
					default:
					}
				}
			}()
		}
	}
}

// ServeWebhook registers POST /telegram/webhook on mux for Bot API webhooks.
func (t *Telegram) ServeWebhook(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/telegram/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var u tgUpdate
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&u); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		go t.handleUpdate(r.Context(), u)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}
