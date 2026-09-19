package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// dRichRecorder fakes the Discord REST API and records calls.
type dRichRecorder struct {
	mu      sync.Mutex
	methods []string
	paths   []string
	bodies  []string
}

func (f *dRichRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		f.mu.Lock()
		f.methods = append(f.methods, r.Method)
		f.paths = append(f.paths, r.URL.Path)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/threads") {
			_, _ = w.Write([]byte(`{"id":"T9","name":"review"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"M1"}`))
	}
}

func (f *dRichRecorder) snapshot() (methods, paths, bodies []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...),
		append([]string(nil), f.paths...),
		append([]string(nil), f.bodies...)
}

func TestDiscordRouteUser(t *testing.T) {
	d := &Discord{GuildRoutes: map[string]string{"G1": "g1/"}}
	if got := d.RouteUser("G1", "U5"); got != "g1/U5" {
		t.Fatalf("routed = %q, want g1/U5", got)
	}
	if got := d.RouteUser("G9", "U5"); got != "U5" {
		t.Fatalf("unknown guild = %q, want bare U5", got)
	}
	if got := d.RouteUser("", "U5"); got != "U5" {
		t.Fatalf("DM = %q, want bare U5", got)
	}
	var nilD *Discord
	if got := nilD.RouteUser("G1", "U5"); got != "U5" {
		t.Fatalf("nil client = %q, want U5", got)
	}
}

func TestDiscordSendThread(t *testing.T) {
	rec := &dRichRecorder{}
	ts := httptest.NewServer(rec.handler())
	defer ts.Close()
	d := &Discord{Token: "tok", BaseURL: ts.URL}
	threadID, err := d.SendThread(context.Background(), "C1", "review", "thread body")
	if err != nil {
		t.Fatalf("SendThread: %v", err)
	}
	if threadID != "T9" {
		t.Fatalf("thread id = %q, want T9", threadID)
	}
	methods, paths, bodies := rec.snapshot()
	if len(methods) != 2 {
		t.Fatalf("calls = %d, want 2 (create thread + post message)", len(methods))
	}
	if methods[0] != http.MethodPost || paths[0] != "/channels/C1/threads" {
		t.Fatalf("thread create = %s %s", methods[0], paths[0])
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &created); err != nil {
		t.Fatal(err)
	}
	if created["name"] != "review" {
		t.Fatalf("thread name = %v, want review", created["name"])
	}
	if methods[1] != http.MethodPost || paths[1] != "/channels/T9/messages" {
		t.Fatalf("thread post = %s %s", methods[1], paths[1])
	}
	var posted map[string]string
	if err := json.Unmarshal([]byte(bodies[1]), &posted); err != nil {
		t.Fatal(err)
	}
	if posted["content"] != "thread body" {
		t.Fatalf("thread content = %q", posted["content"])
	}
}

func TestDiscordEditMessage(t *testing.T) {
	rec := &dRichRecorder{}
	ts := httptest.NewServer(rec.handler())
	defer ts.Close()
	d := &Discord{Token: "tok", BaseURL: ts.URL}
	if err := d.EditMessage(context.Background(), "C1", "M2", "approved by alice"); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	methods, paths, bodies := rec.snapshot()
	if len(methods) != 1 || methods[0] != http.MethodPatch {
		t.Fatalf("method = %v, want one PATCH", methods)
	}
	if paths[0] != "/channels/C1/messages/M2" {
		t.Fatalf("path = %q", paths[0])
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["content"] != "approved by alice" {
		t.Fatalf("content = %q", payload["content"])
	}
	if err := d.EditMessage(context.Background(), "", "M2", "x"); err == nil {
		t.Fatal("expected error with empty channel id")
	}
	if err := d.EditMessage(context.Background(), "C1", "", "x"); err == nil {
		t.Fatal("expected error with empty message id")
	}
	if err := d.EditMessage(context.Background(), "C1", "M2", "  "); err == nil {
		t.Fatal("expected error with empty text")
	}
}

func TestDiscordSendUsesBaseURL(t *testing.T) {
	rec := &dRichRecorder{}
	ts := httptest.NewServer(rec.handler())
	defer ts.Close()
	d := &Discord{Token: "tok", BaseURL: ts.URL}
	if err := d.Send(context.Background(), "C7", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, paths, bodies := rec.snapshot()
	if len(paths) != 1 || paths[0] != "/channels/C7/messages" {
		t.Fatalf("paths = %v, want channel C7 post", paths)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["content"] != "hello" {
		t.Fatalf("content = %q", payload["content"])
	}
}

func TestGatewayBaseURLOverride(t *testing.T) {
	g := &Gateway{}
	if got := g.restBase(); got != defaultDiscordBase {
		t.Fatalf("default = %q, want %q", got, defaultDiscordBase)
	}
	g.BaseURL = "http://example.test/api/"
	if got := g.restBase(); got != "http://example.test/api" {
		t.Fatalf("override = %q, want trimmed URL", got)
	}
}
