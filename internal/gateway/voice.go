package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Voice wires speech endpoints onto a Server. Transcribe converts uploaded
// audio to text; Synthesize renders text to an audio file path. Nil means
// unconfigured — endpoints answer 501 with setup guidance, never silence.

// RegisterVoice attaches POST /api/v1/transcribe (multipart audio → text)
// and POST /api/v1/speak ({text} → audio bytes) with CORS + logging.
func RegisterVoice(mux *http.ServeMux, s *Server) {
	if mux == nil || s == nil {
		return
	}
	mux.Handle("/api/v1/transcribe", s.logRequest(s.withCORS(http.HandlerFunc(s.handleTranscribe))))
	mux.Handle("/api/v1/speak", s.logRequest(s.withCORS(http.HandlerFunc(s.handleSpeak))))
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}
	if s.Transcribe == nil {
		http.Error(w, "transcription unconfigured — whisper.cpp, NIMBUS_STT_URL, or OpenAI key required", http.StatusNotImplemented)
		return
	}
	if err := r.ParseMultipartForm(15 << 20); err != nil {
		http.Error(w, "multipart audio required (max 15MB)", http.StatusBadRequest)
		return
	}
	f, hdr, err := r.FormFile("audio")
	if err != nil {
		f, hdr, err = r.FormFile("file")
	}
	if err != nil {
		http.Error(w, "field 'audio' (ogg/opus/m4a) required", http.StatusBadRequest)
		return
	}
	defer f.Close()
	tmp, err := os.CreateTemp("", "nimbus-up-*.ogg")
	if err != nil {
		http.Error(w, "temp store failed", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, io.LimitReader(f, 15<<20)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		http.Error(w, "upload failed", http.StatusBadRequest)
		return
	}
	tmp.Close()
	defer os.Remove(tmpName)
	_ = hdr
	text, err := s.Transcribe(r.Context(), tmpName)
	if err != nil {
		http.Error(w, "transcribe: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

func (s *Server) handleSpeak(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}
	if s.Synthesize == nil {
		http.Error(w, "speech unconfigured — espeak-ng/say, or OpenAI key for API voices", http.StatusNotImplemented)
		return
	}
	defer r.Body.Close()
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	path, err := s.Synthesize(r.Context(), in.Text)
	if err != nil {
		http.Error(w, "speak: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer os.Remove(path)
	ct := "audio/mpeg"
	if strings.HasSuffix(strings.ToLower(path), ".wav") {
		ct = "audio/wav"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(path))
	http.ServeFile(w, r, path)
}
