// OpenAI-compatible HTTP surface for the gateway.
//
// OpenClaw reference (read-only mirror /data/data/com.termux/files/home/tmp/openclaw-src):
//
//	src/gateway/openai-http.ts            — POST /v1/chat/completions shape
//	src/gateway/openresponses-http.ts     — responses API conventions
//	src/gateway/models-http.ts            — GET /v1/models listing
//	src/gateway/embeddings-http.ts        — sibling compat route (not ported:
//	                                        no embedding engine exists here)
//	src/gateway/server-http.openai-compat.test.ts — route contract tests
//
// The Server struct lives in http.go and cannot be extended here, so the
// advertised model list is a package-level var (OpenAIModels) with the
// SetOpenAIModels setter. Auth reuses Server.checkAuth from http.go.
// Supported: non-streaming chat completions only. stream=true is rejected
// with an honest 400 instead of silently degrading.
package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	openAIMu sync.RWMutex
	// OpenAIModels advertises the model ids served at GET /v1/models.
	// Set at startup via SetOpenAIModels; when empty, ["nimbus-one"] is
	// advertised. Direct writes are startup-only; use the setter (and the
	// snapshot reader) once serving to stay race-clean.
	OpenAIModels []string
)

// SetOpenAIModels replaces the advertised model list (copies the input).
func SetOpenAIModels(models []string) {
	openAIMu.Lock()
	defer openAIMu.Unlock()
	OpenAIModels = append([]string(nil), models...)
}

// openAIModelSnapshot returns the advertised models, defaulting to
// ["nimbus-one"] when nothing was configured.
func openAIModelSnapshot() []string {
	openAIMu.RLock()
	defer openAIMu.RUnlock()
	if len(OpenAIModels) == 0 {
		return []string{"nimbus-one"}
	}
	return append([]string(nil), OpenAIModels...)
}

// openAICompletionSeq numbers chatcmpl-* response ids.
var openAICompletionSeq atomic.Int64

// RegisterOpenAI mounts GET /v1/models and POST /v1/chat/completions on mux.
// main.go wires this onto the gateway mux separately from Server.Handler().
func RegisterOpenAI(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("/v1/models", s.handleOpenAIModels)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChat)
}

func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}
	now := time.Now().Unix()
	data := make([]any, 0)
	for _, id := range openAIModelSnapshot() {
		data = append(data, map[string]any{
			"id": id, "object": "model", "created": now, "owned_by": "nimbus-one",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// openAIMessage is one chat message. Content may be a plain string (the
// common case) or an array of {type,text} parts; other part types are
// ignored and contribute no text.
type openAIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// text extracts usable text from string or multipart content.
func (m *openAIMessage) text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(m.Content, &str); err == nil {
		return str
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || (p.Type == "" && p.Text != "") {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func (s *Server) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}
	defer r.Body.Close()
	var in struct {
		Model     string          `json:"model"`
		Messages  []openAIMessage `json:"messages"`
		Stream    bool            `json:"stream"`
		MaxTokens int             `json:"max_tokens"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.Stream {
		writeOpenAIError(w, http.StatusBadRequest, `streaming is not supported; retry with "stream": false`)
		return
	}
	if len(in.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "messages must not be empty")
		return
	}
	model := in.Model
	if model == "" {
		model = openAIModelSnapshot()[0]
	}
	var lines []string
	hasText := false
	for _, m := range in.Messages {
		role := m.Role
		if role == "" {
			role = "user"
		}
		t := m.text()
		if strings.TrimSpace(t) != "" {
			hasText = true
		}
		lines = append(lines, role+": "+t)
	}
	if !hasText {
		writeOpenAIError(w, http.StatusBadRequest, "messages must contain text")
		return
	}
	// The full transcript is the engine prompt so multi-turn context is
	// preserved; the Broker records it as one user turn under openai/openai.
	transcript := strings.Join(lines, "\n")
	if s.Broker == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "no broker")
		return
	}
	reply := s.Broker.Handle("openai", "openai", transcript)
	promptTokens := estimateTokens(transcript)
	completionTokens := estimateTokens(reply)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", openAICompletionSeq.Add(1)),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": reply,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	})
}

// estimateTokens approximates token count as len(s)/4 (1 minimum for
// non-empty strings). It is a billing-shape estimate only, documented as
// such in the usage block.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	if n := len(s) / 4; n > 0 {
		return n
	}
	return 1
}

// writeOpenAIError renders an OpenAI-style {"error":{message,type}} body.
func writeOpenAIError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]string{"message": msg, "type": "invalid_request_error"},
	})
}
