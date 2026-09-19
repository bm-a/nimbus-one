package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testBridge() *bridgeServer {
	b := &bridgeServer{token: "tok123"}
	b.run = func(text string) (string, error) { return "echo:" + text, nil }
	return b
}

func parseRes(t *testing.T, raw []byte) resFrame {
	t.Helper()
	var f resFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("bad res frame: %v\n%s", err, raw)
	}
	return f
}

func TestConnectOK(t *testing.T) {
	b := testBridge()
	st := &connState{}
	out := b.handleFrame(st, reqFrame{Type: "req", ID: "1", Method: "connect",
		Params: map[string]any{"minProtocol": 4.0, "maxProtocol": 4.0, "auth": map[string]any{"token": "tok123"}}})
	if len(out) != 1 {
		t.Fatalf("connect must return exactly one frame, got %d", len(out))
	}
	f := parseRes(t, out[0])
	if !f.OK {
		t.Fatalf("connect failed: %+v", f)
	}
	if !st.authed {
		t.Fatal("state must be authed after connect")
	}
}

func TestConnectBadToken(t *testing.T) {
	b := testBridge()
	st := &connState{}
	out := b.handleFrame(st, reqFrame{Type: "req", ID: "1", Method: "connect",
		Params: map[string]any{"auth": map[string]any{"token": "wrong"}}})
	if parseRes(t, out[0]).OK {
		t.Fatal("bad token must fail")
	}
	if st.authed {
		t.Fatal("state must stay unauthed")
	}
}

func TestConnectOldClient(t *testing.T) {
	b := testBridge()
	out := b.handleFrame(&connState{}, reqFrame{Type: "req", ID: "1", Method: "connect",
		Params: map[string]any{"maxProtocol": 2.0}})
	if parseRes(t, out[0]).OK {
		t.Fatal("protocol < 4 must be rejected")
	}
}

func TestChatSendFlow(t *testing.T) {
	b := testBridge()
	st := &connState{authed: true}
	// Unauthenticated first.
	out := b.handleFrame(&connState{}, reqFrame{Type: "req", ID: "0", Method: "chat.send",
		Params: map[string]any{"text": "hi"}})
	if parseRes(t, out[0]).OK {
		t.Fatal("chat.send without connect must fail")
	}
	// Empty text.
	out = b.handleFrame(st, reqFrame{Type: "req", ID: "1", Method: "chat.send", Params: map[string]any{}})
	if parseRes(t, out[0]).OK {
		t.Fatal("empty text must fail")
	}
	// Real send: res + event.
	out = b.handleFrame(st, reqFrame{Type: "req", ID: "2", Method: "chat.send",
		Params: map[string]any{"text": "hello"}})
	if len(out) != 2 {
		t.Fatalf("chat.send must return res+event, got %d", len(out))
	}
	f := parseRes(t, out[0])
	if !f.OK {
		t.Fatalf("chat.send failed: %+v", f)
	}
	var ev eventFrame
	if err := json.Unmarshal(out[1], &ev); err != nil || ev.Event != "session.message" {
		t.Fatalf("second frame must be session.message event: %s", out[1])
	}
	// Session recorded.
	out = b.handleFrame(st, reqFrame{Type: "req", ID: "3", Method: "sessions.list", Params: map[string]any{}})
	if !parseRes(t, out[0]).OK {
		t.Fatal("sessions.list must succeed")
	}
	if !strings.Contains(string(out[0]), "s-1") {
		t.Fatalf("session missing from list: %s", out[0])
	}
}

func TestUnknownMethod(t *testing.T) {
	b := testBridge()
	out := b.handleFrame(&connState{authed: true},
		reqFrame{Type: "req", ID: "1", Method: "nodes.pairing", Params: map[string]any{}})
	f := parseRes(t, out[0])
	if f.OK || f.Error.Code != "UNKNOWN_METHOD" {
		t.Fatalf("unimplemented method must be UNKNOWN_METHOD: %s", out[0])
	}
}

func TestBadFrame(t *testing.T) {
	b := testBridge()
	out := b.handleFrame(&connState{}, reqFrame{Type: "event", ID: "", Method: ""})
	if parseRes(t, out[0]).OK {
		t.Fatal("non-req frame must fail")
	}
}

func TestHTTPHandlers(t *testing.T) {
	b := testBridge()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"product":"nimbus-min"}`))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWS(b, w, r)
	})
	// healthz.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "nimbus-min") {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
	// Plain GET on /ws must demand upgrade, not crash.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/ws", nil))
	if rec.Code != 426 {
		t.Fatalf("/ws without upgrade = %d, want 426", rec.Code)
	}
}

func TestWSFrameRoundTrip(t *testing.T) {
	// Client-masked write -> server read -> server write -> client read.
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	_ = server.SetDeadline(time.Now().Add(10 * time.Second))
	go func() {
		_ = wsWriteClient(client, 0x1, []byte(`{"type":"req","id":"1","method":"sessions.list","params":{}}`))
	}()
	raw, op, err := wsRead(server)
	if err != nil || op != 0x1 {
		t.Fatalf("read: %v op=%d", err, op)
	}
	var req reqFrame
	if err := json.Unmarshal(raw, &req); err != nil || req.Method != "sessions.list" {
		t.Fatalf("decoded = %s, %v", raw, err)
	}
	// net.Pipe is synchronous: the client must read concurrently.
	type res struct {
		got []byte
		op  byte
		err error
	}
	done := make(chan res, 1)
	go func() {
		got, op, err := wsRead(client)
		done <- res{got, op, err}
	}()
	if err := wsWrite(server, 0x1, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := <-done
	if r.err != nil || r.op != 0x1 || string(r.got) != `{"ok":true}` {
		t.Fatalf("client readback: %v op=%d %q", r.err, r.op, r.got)
	}
}

// wsWriteClient writes a masked client frame (test helper: servers must
// accept masked frames per RFC6455).
func wsWriteClient(conn net.Conn, opcode byte, payload []byte) error {
	var hdr []byte
	if len(payload) < 126 {
		hdr = []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	} else {
		hdr = []byte{0x80 | opcode, 0x80 | 126, byte(len(payload) >> 8), byte(len(payload))}
	}
	mask := []byte{0x1, 0x2, 0x3, 0x4}
	hdr = append(hdr, mask...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	_, err := conn.Write(masked)
	return err
}
