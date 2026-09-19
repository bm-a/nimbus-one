// Tests for wsserver.go.
//
// Honesty note: httptest.ResponseWriter does not implement http.Hijacker, so
// HandleConn's upgrade path cannot run under httptest. These tests cover the
// seams instead: the 101 handshake-response builder (RFC6455 test vector),
// masked client frame round-trips through the shared discord_ws.go framing
// the serve loop uses, and the dispatch table plus reply envelopes.
package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWSHandshakeResponseVector(t *testing.T) {
	// RFC6455 §1.3 example vector.
	resp := wsHandshakeResponse("dGhlIHNhbXBsZSBub25jZQ==")
	if !strings.HasPrefix(resp, "HTTP/1.1 101 Switching Protocols\r\n") {
		t.Fatalf("missing 101 status line:\n%q", resp)
	}
	if !strings.Contains(resp, "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n") {
		t.Fatalf("bad accept key:\n%q", resp)
	}
	if !strings.HasSuffix(resp, "\r\n\r\n") {
		t.Fatalf("response must end with blank line:\n%q", resp)
	}
}

func TestWSMaskedClientFrameRoundTrip(t *testing.T) {
	// What a browser sends (masked) must decode through the reader the
	// server loop uses, including 16-bit extended lengths.
	for _, size := range []int{5, 125, 126, 1000} {
		payload := bytes.Repeat([]byte("abcdefgh"), size/8+1)[:size]
		var buf bytes.Buffer
		if err := wsWriteRaw(&buf, true, wsOpText, payload, true); err != nil {
			t.Fatalf("size %d: write: %v", size, err)
		}
		op, got, err := wsReadFrame(bufio.NewReader(&buf))
		if err != nil {
			t.Fatalf("size %d: read: %v", size, err)
		}
		if op != wsOpText || !bytes.Equal(got, payload) {
			t.Fatalf("size %d: round-trip mismatch (op=%d len=%d)", size, op, len(got))
		}
	}
}

func TestWSServerWriteIsUnmasked(t *testing.T) {
	var buf bytes.Buffer
	if err := wsWriteRaw(&buf, true, wsOpText, []byte("hi"), false); err != nil {
		t.Fatal(err)
	}
	if buf.Bytes()[1]&0x80 != 0 {
		t.Fatal("server frames must be unmasked")
	}
}

func TestWSDispatchPing(t *testing.T) {
	ws := &WSServer{}
	out, err := ws.dispatch("ping", nil)
	if err != nil || out != "pong" {
		t.Fatalf("ping = %v, %v; want pong, nil", out, err)
	}
}

func TestWSDispatchChat(t *testing.T) {
	eng := &stubEngine{reply: "hello back"}
	ws := &WSServer{Broker: New(eng, "sys")}
	out, err := ws.dispatch("chat", json.RawMessage(`{"message":"hi","user":"u1"}`))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if out != "hello back" {
		t.Fatalf("chat = %q, want engine reply", out)
	}
	if !strings.Contains(eng.last, "hi") {
		t.Fatalf("engine prompt = %q, want message text", eng.last)
	}
	if _, err := ws.dispatch("chat", json.RawMessage(`{"message":"  "}`)); err == nil {
		t.Fatal("empty message must error")
	}
	if _, err := ws.dispatch("chat", json.RawMessage(`{bad`)); err == nil {
		t.Fatal("malformed params must error")
	}
	if _, err := (&WSServer{}).dispatch("chat", json.RawMessage(`{"message":"hi"}`)); err == nil {
		t.Fatal("nil broker must error, not silently drop")
	}
}

func TestWSDispatchStatus(t *testing.T) {
	ws := &WSServer{Broker: New(&stubEngine{reply: "x"}, "")}
	out, err := ws.dispatch("status", nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["ok"] != true || m["mode"] != "build" {
		t.Fatalf("status = %#v, want {ok:true mode:build}", out)
	}
}

func TestWSDispatchSessionsList(t *testing.T) {
	b := New(&stubEngine{reply: "hey"}, "")
	b.Handle("ws", "u1", "first")
	ws := &WSServer{Broker: b}
	out, err := ws.dispatch("sessions.list", nil)
	if err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	keys, ok := out.([]string)
	if !ok || len(keys) != 1 || keys[0] != "ws/u1" {
		t.Fatalf("sessions.list = %#v, want [ws/u1]", out)
	}
	if _, err := (&WSServer{}).dispatch("sessions.list", nil); err == nil {
		t.Fatal("nil broker must error")
	}
}

func TestWSDispatchUnknown(t *testing.T) {
	if _, err := (&WSServer{}).dispatch("cron.create", nil); err == nil {
		t.Fatal("unknown method must error")
	}
}

func TestWSHandleMessageEnvelopes(t *testing.T) {
	eng := &stubEngine{reply: "yo"}
	ws := &WSServer{Broker: New(eng, "sys")}

	// Happy path preserves the caller id.
	var okResp wsResponse
	if err := json.Unmarshal(ws.handleMessage([]byte(`{"id":7,"method":"ping"}`)), &okResp); err != nil {
		t.Fatal(err)
	}
	if string(okResp.ID) != "7" || okResp.Result != "pong" || okResp.Error != "" {
		t.Fatalf("ping envelope = %+v", okResp)
	}

	// Unknown method keeps id and sets error.
	var errResp wsResponse
	if err := json.Unmarshal(ws.handleMessage([]byte(`{"id":"a","method":"nope"}`)), &errResp); err != nil {
		t.Fatal(err)
	}
	if string(errResp.ID) != `"a"` || errResp.Error == "" {
		t.Fatalf("error envelope = %+v", errResp)
	}

	// Unparseable input yields an id:null error envelope, never silence.
	var parseResp wsResponse
	if err := json.Unmarshal(ws.handleMessage([]byte(`{broken`)), &parseResp); err != nil {
		t.Fatal(err)
	}
	if parseResp.Error == "" {
		t.Fatalf("parse envelope = %+v, want error text", parseResp)
	}

	// Missing method is a client bug: say so.
	var missingResp wsResponse
	if err := json.Unmarshal(ws.handleMessage([]byte(`{"id":1}`)), &missingResp); err != nil {
		t.Fatal(err)
	}
	if missingResp.Error == "" {
		t.Fatalf("missing-method envelope = %+v, want error text", missingResp)
	}
}

func TestWSHeaderHelpers(t *testing.T) {
	if !headerHasToken("keep-alive, Upgrade", "upgrade") {
		t.Fatal("Connection: keep-alive, Upgrade must match")
	}
	if headerHasToken("keep-alive", "upgrade") {
		t.Fatal("must not false-positive")
	}
	if bearerToken("Bearer abc") != "abc" {
		t.Fatal("bearer parse failed")
	}
	if bearerToken("Basic abc") != "" {
		t.Fatal("non-bearer scheme must yield empty")
	}
}
