package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server is a minimal HTTP gateway bound to a Broker.
type Server struct {
	Addr   string
	Token  string // optional bearer token; empty = no auth
	Broker *Broker
	// Transcribe converts an audio file to text (STT). Nil = 501 + guidance.
	Transcribe func(ctx context.Context, oggPath string) (string, error)
	// Synthesize renders text to an audio file path (TTS). Nil = 501 + guidance.
	Synthesize func(ctx context.Context, text string) (string, error)

	mu     sync.Mutex
	tasks  map[string]string // id -> reply (pending = "")
	nextID int64
}

// Handler builds routes:
// POST /api/v1/chat {message,user} -> {reply}
// POST /api/v1/task {task[,user]} -> {id} (async)
// GET  /api/v1/task?id=... -> {id, done, reply}
// GET  /healthz -> ok
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/v1/chat", s.handleChat)
	mux.HandleFunc("/api/v1/task", s.handleTask)
	RegisterWebUI(mux, s) // console + SSE stream, CORS + access logging
	RegisterVoice(mux, s) // transcribe + speak, CORS + access logging
	return mux
}

// checkAuth enforces Bearer token when Token is set.
func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || strings.TrimPrefix(h, "Bearer ") != s.Token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}
	defer r.Body.Close()
	var in struct {
		Message string `json:"message"`
		User    string `json:"user"`
		Mode    string `json:"mode"` // optional: "plan"|"build", switches process-wide mode
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Message) == "" {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}
	user := in.User
	if user == "" {
		user = "http"
	}
	if s.Broker == nil {
		http.Error(w, "no broker", http.StatusServiceUnavailable)
		return
	}
	reply := s.Broker.Handle("http", user, in.Message)
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		defer r.Body.Close()
		var in struct {
			Task string `json:"task"`
			User string `json:"user"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(in.Task) == "" {
			http.Error(w, "empty task", http.StatusBadRequest)
			return
		}
		user := in.User
		if user == "" {
			user = "task"
		}
		if s.Broker == nil {
			http.Error(w, "no broker", http.StatusServiceUnavailable)
			return
		}
		id := strconv.FormatInt(atomic.AddInt64(&s.nextID, 1), 10)
		s.mu.Lock()
		if s.tasks == nil {
			s.tasks = make(map[string]string)
		}
		s.tasks[id] = ""
		s.mu.Unlock()
		go func() {
			reply := s.Broker.Handle("http", user, in.Task)
			s.mu.Lock()
			s.tasks[id] = reply
			s.mu.Unlock()
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		reply, ok := s.tasks[id]
		s.mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "done": reply != "", "reply": reply})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
