package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newWebUIMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterWebUI(mux, s)
	return mux
}

func TestConsoleHTMLViewportAndNoLeak(t *testing.T) {
	if len(ConsoleHTML) >= 14*1024 {
		t.Fatalf("ConsoleHTML = %d bytes, want <14KB", len(ConsoleHTML))
	}
	for _, want := range []string{
		`name="viewport"`,
		"/api/v1/chat",
		"localStorage",
		"<form",
		"<noscript",
		"Nimbus-One",
	} {
		if !strings.Contains(ConsoleHTML, want) {
			t.Fatalf("ConsoleHTML missing %q", want)
		}
	}
	if strings.Contains(ConsoleHTML, "http://") || strings.Contains(ConsoleHTML, "https://") {
		t.Fatalf("ConsoleHTML must not reference remote CDNs")
	}

	s := &Server{Token: "super-secret-xyz-123", Broker: New(&stubEngine{reply: "x"}, "")}
	mux := newWebUIMux(s)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("console status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(res.Body)
	bs := string(body)
	if !strings.Contains(bs, "viewport") {
		t.Fatalf("console body missing viewport")
	}
	if strings.Contains(bs, "super-secret-xyz-123") {
		t.Fatalf("console body leaks server token")
	}
	if !strings.Contains(bs, "Nimbus-One") {
		t.Fatalf("console body missing default title")
	}
}

func TestConsoleExactPath(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "x"}, "")}
	mux := newWebUIMux(s)
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", rec.Code)
	}
}

func TestStreamSSEDone(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "hello-stream"}, "")}
	mux := newWebUIMux(s)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream?message=hi&user=u", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body, _ := io.ReadAll(res.Body)
	bs := string(body)
	if !strings.Contains(bs, "data:") {
		t.Fatalf("stream body missing data: lines: %q", bs)
	}
	if !strings.Contains(bs, "done") {
		t.Fatalf("stream body missing done event: %q", bs)
	}
	if !strings.Contains(bs, "hello-stream") {
		t.Fatalf("stream body missing reply: %q", bs)
	}
}

func TestStreamAuthEnforced(t *testing.T) {
	s := &Server{Token: "tok123", Broker: New(&stubEngine{reply: "r"}, "")}
	mux := newWebUIMux(s)

	// No credential -> 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream?message=hi", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", rec.Code)
	}

	// Bearer -> 200.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/stream?message=hi", nil)
	req2.Header.Set("Authorization", "Bearer tok123")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("bearer status = %d, want 200", rec2.Code)
	}

	// ?token= -> 200 (EventSource can't set headers).
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/stream?message=hi&token=tok123", nil)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("query-token status = %d, want 200", rec3.Code)
	}

	// Wrong token -> 401.
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/stream?message=hi&token=wrong", nil)
	rec4 := httptest.NewRecorder()
	mux.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-token status = %d, want 401", rec4.Code)
	}
}

func TestStreamEmptyMessage400(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "r"}, "")}
	mux := newWebUIMux(s)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream?message=%20", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty message status = %d, want 400", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	old := getCORSOrigins()
	SetCORSOrigins(nil)
	defer SetCORSOrigins(old)

	s := &Server{Broker: New(&stubEngine{reply: "r"}, "")}
	mux := newWebUIMux(s)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/stream", nil)
	req.Header.Set("Origin", "http://lan:8080")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if h := rec.Header().Get("Access-Control-Allow-Origin"); h == "" {
		t.Fatalf("missing Access-Control-Allow-Origin on preflight")
	}
	if h := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(h, "Authorization") {
		t.Fatalf("Allow-Headers = %q, want Authorization", h)
	}

	// Explicit origins: match echoes, mismatch omits.
	SetCORSOrigins([]string{"http://allowed:8080"})
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "http://allowed:8080")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if h := rec2.Header().Get("Access-Control-Allow-Origin"); h != "http://allowed:8080" {
		t.Fatalf("matched origin header = %q, want echo", h)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("Origin", "http://evil:1")
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)
	if h := rec3.Header().Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("mismatched origin header = %q, want empty", h)
	}
}
