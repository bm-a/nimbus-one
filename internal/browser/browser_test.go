package browser

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- mock CDP server (manual WS accept, like the discord_ws tests) ---

type mockCDP struct {
	t       *testing.T
	ln      net.Listener
	mu      sync.Mutex
	methods []string
	params  map[string]json.RawMessage
	respond func(method string, params json.RawMessage) json.RawMessage
	sentEvt bool
}

func startMockCDP(t *testing.T, respond func(method string, params json.RawMessage) json.RawMessage) *mockCDP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	m := &mockCDP{t: t, ln: ln, params: map[string]json.RawMessage{}, respond: respond}
	go m.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *mockCDP) addr() string { return m.ln.Addr().String() }

func (m *mockCDP) serve() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		go m.handle(conn)
	}
}

func (m *mockCDP) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	key := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
			key = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	sum := sha1.Sum([]byte(key + wsMagic))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		return
	}
	for {
		op, payload, err := m.readFrame(r)
		if err != nil {
			return
		}
		if op == wsOpClose {
			m.writeFrame(conn, wsOpClose, nil)
			return
		}
		if op != wsOpText {
			continue
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		m.mu.Lock()
		m.methods = append(m.methods, msg.Method)
		m.params[msg.Method] = msg.Params
		first := !m.sentEvt
		m.sentEvt = true
		m.mu.Unlock()
		if first {
			// Protocol noise the client must skip while correlating by id.
			m.writeFrame(conn, wsOpText, []byte(`{"method":"Page.loadEventFired","params":{}}`))
		}
		result := m.respond(msg.Method, msg.Params)
		out, _ := json.Marshal(map[string]any{"id": json.RawMessage(msg.ID), "result": result})
		// Re-marshal carefully: id must stay numeric.
		var idAny any
		_ = json.Unmarshal(msg.ID, &idAny)
		out, _ = json.Marshal(map[string]any{"id": idAny, "result": result})
		m.writeFrame(conn, wsOpText, out)
	}
}

func (m *mockCDP) readFrame(r *bufio.Reader) (int, []byte, error) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	op := int(hdr[0] & 0x0F)
	length := int(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if hdr[1]&0x80 != 0 {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return op, payload, nil
}

func (m *mockCDP) writeFrame(conn net.Conn, op int, payload []byte) {
	hdr := []byte{0x80 | byte(op)}
	if len(payload) < 126 {
		hdr = append(hdr, byte(len(payload)))
	} else {
		hdr = append(hdr, 126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		hdr = append(hdr, ext[:]...)
	}
	_, _ = conn.Write(hdr)
	if len(payload) > 0 {
		_, _ = conn.Write(payload)
	}
}

func withListServer(t *testing.T, wsURL string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `[{"id":"P1","type":"page","url":"about:blank","webSocketDebuggerUrl":"ws://%s/devtools/page/P1"}]`, wsURL)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWSFrameRoundtripOverTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		// Read the masked client frame and verify masking.
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(r, hdr); err != nil {
			t.Errorf("server read hdr: %v", err)
			return
		}
		if hdr[0] != 0x81 {
			t.Errorf("want FIN+text 0x81, got 0x%02x", hdr[0])
		}
		if hdr[1]&0x80 == 0 {
			t.Error("client frames must be masked")
		}
		n := int(hdr[1] & 0x7F)
		var mask [4]byte
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			t.Errorf("server read mask: %v", err)
			return
		}
		raw := make([]byte, n)
		if _, err := io.ReadFull(r, raw); err != nil {
			t.Errorf("server read payload: %v", err)
			return
		}
		for i := range raw {
			raw[i] ^= mask[i%4]
		}
		if string(raw) != "hello-frame" {
			t.Errorf("payload = %q", raw)
		}
		writeSrv := func(op int, p []byte) {
			h := []byte{0x80 | byte(op), byte(len(p))}
			_, _ = conn.Write(h)
			if len(p) > 0 {
				_, _ = conn.Write(p)
			}
		}
		writeSrv(wsOpText, []byte("back"))
		writeSrv(wsOpPing, []byte("p"))
		writeSrv(wsOpText, []byte("after-ping"))
		// Expect a masked pong echoing "p".
		if _, err := io.ReadFull(r, hdr); err != nil {
			t.Errorf("server read pong hdr: %v", err)
			return
		}
		if hdr[0] != 0x8A {
			t.Errorf("want pong 0x8A, got 0x%02x", hdr[0])
		}
		pn := int(hdr[1] & 0x7F)
		if hdr[1]&0x80 == 0 {
			t.Error("pong must be masked")
		}
		var pm [4]byte
		_, _ = io.ReadFull(r, pm[:])
		pb := make([]byte, pn)
		_, _ = io.ReadFull(r, pb)
		for i := range pb {
			pb[i] ^= pm[i%4]
		}
		if string(pb) != "p" {
			t.Errorf("pong payload = %q", pb)
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	wc := &wsConn{conn: conn, r: bufio.NewReader(conn)}
	if err := wc.writeText([]byte("hello-frame")); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	op, payload, err := wc.readMessage()
	if err != nil || op != wsOpText || string(payload) != "back" {
		t.Fatalf("read1: op=%d payload=%q err=%v", op, payload, err)
	}
	op, payload, err = wc.readMessage()
	if err != nil || string(payload) != "after-ping" {
		t.Fatalf("read2 (ping must be auto-answered): op=%d payload=%q err=%v", op, payload, err)
	}
	<-serverDone
}

func TestCDPActionsAgainstMock(t *testing.T) {
	var mu sync.Mutex
	var evalParams []byte
	mock := startMockCDP(t, func(method string, params json.RawMessage) json.RawMessage {
		switch method {
		case "Page.navigate":
			return json.RawMessage(`{"frameId":"F1"}`)
		case "Runtime.evaluate":
			mu.Lock()
			evalParams = append([]byte{}, params...)
			mu.Unlock()
			var p struct {
				Expression    string `json:"expression"`
				ReturnByValue bool   `json:"returnByValue"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "JSON.stringify") {
				return json.RawMessage(`{"result":{"type":"string","value":"{\"title\":\"T\",\"url\":\"https://x.test/\",\"text\":\"hello body\"}"}}`)
			}
			if p.Expression == "1+1" {
				return json.RawMessage(`{"result":{"type":"number","value":2}}`)
			}
			return json.RawMessage(`{"result":{"type":"string","value":"echo"}}`)
		case "Page.captureScreenshot":
			return json.RawMessage(`{"data":"` + base64.StdEncoding.EncodeToString([]byte("fakepng")) + `"}`)
		default:
			return json.RawMessage(`{}`)
		}
	})
	srv := withListServer(t, mock.addr())
	c := &CDPClient{URL: srv.URL, Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	msg, err := c.Navigate(ctx, "https://example.com/")
	if err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	if msg != "navigated to https://example.com/" {
		t.Fatalf("msg = %q", msg)
	}
	out, err := c.Eval(ctx, "1+1")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out != "2" {
		t.Fatalf("Eval = %q, want 2", out)
	}
	// Evaluate payload shape: expression intact + returnByValue.
	mu.Lock()
	var shape struct {
		Expression    string `json:"expression"`
		ReturnByValue bool   `json:"returnByValue"`
	}
	_ = json.Unmarshal(evalParams, &shape)
	mu.Unlock()
	if shape.Expression != "1+1" || !shape.ReturnByValue {
		t.Fatalf("evaluate payload = %s", evalParams)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !strings.Contains(snap, "hello body") || !strings.Contains(snap, "https://x.test/") {
		t.Fatalf("snapshot = %q", snap)
	}
	shot, err := c.Screenshot(ctx)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if string(shot) != "fakepng" {
		t.Fatalf("screenshot = %q", shot)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, want := range []string{"Page.navigate", "Runtime.evaluate", "Page.captureScreenshot"} {
		found := false
		for _, got := range mock.methods {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("methods %v missing %s", mock.methods, want)
		}
	}
}

func TestCDPNoPageHelpfulError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := &CDPClient{URL: srv.URL, Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.Navigate(ctx, "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "no debuggable page") {
		t.Fatalf("err = %v, want no-debuggable-page guidance", err)
	}
}

func TestCDPUnreachableDebugger(t *testing.T) {
	c := &CDPClient{URL: "http://127.0.0.1:1", Timeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.Navigate(ctx, "https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "Chrome") {
		t.Fatalf("err = %v, want Chrome guidance", err)
	}
	if _, err := c.Eval(ctx, ""); err == nil {
		t.Fatal("empty eval must error")
	}
}
