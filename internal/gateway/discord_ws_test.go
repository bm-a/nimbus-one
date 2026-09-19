package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscordWSHandshakeAcceptVector(t *testing.T) {
	// RFC6455 §1.3 example vector.
	if got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept = %q, want s3pPLMBiTxaQ9kYGzzhZRbK+xOo=", got)
	}
}

func TestDiscordWSFrameRoundtrip(t *testing.T) {
	sizes := []int{0, 1, 5, 125, 126, 127, 1000, 65535, 65536, 100000}
	for _, n := range sizes {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i*31 + 7)
		}
		client, server := net.Pipe()
		writeErr := make(chan error, 1)
		go func() {
			writeErr <- wsWriteRaw(client, true, wsOpText, payload, true)
			_ = client.Close()
		}()
		op, got, err := wsReadFrame(bufio.NewReader(server))
		_ = server.Close()
		if werr := <-writeErr; werr != nil {
			t.Fatalf("size %d: write: %v", n, werr)
		}
		if err != nil {
			t.Fatalf("size %d: read: %v", n, err)
		}
		if op != wsOpText {
			t.Fatalf("size %d: opcode = %d, want %d", n, op, wsOpText)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("size %d: payload mismatch (got %d bytes)", n, len(got))
		}
	}
}

func TestDiscordWSFrameUnmaskedAndFragmented(t *testing.T) {
	// Unmasked server-style frame decodes as-is.
	var buf bytes.Buffer
	if err := wsWriteRaw(&buf, true, wsOpText, []byte("hello"), false); err != nil {
		t.Fatal(err)
	}
	op, got, err := wsReadFrame(bufio.NewReader(&buf))
	if err != nil || op != wsOpText || string(got) != "hello" {
		t.Fatalf("unmasked: op=%d got=%q err=%v", op, got, err)
	}

	// Fragments reassemble.
	buf.Reset()
	if err := wsWriteRaw(&buf, false, wsOpText, []byte("hel"), false); err != nil {
		t.Fatal(err)
	}
	if err := wsWriteRaw(&buf, true, wsOpCont, []byte("lo"), false); err != nil {
		t.Fatal(err)
	}
	op, got, err = wsReadFrame(bufio.NewReader(&buf))
	if err != nil || op != wsOpText || string(got) != "hello" {
		t.Fatalf("fragmented: op=%d got=%q err=%v", op, got, err)
	}

	// Control frames pass through with their opcode.
	buf.Reset()
	if err := wsWriteRaw(&buf, true, wsOpPing, []byte("ping"), false); err != nil {
		t.Fatal(err)
	}
	op, got, err = wsReadFrame(bufio.NewReader(&buf))
	if err != nil || op != wsOpPing || string(got) != "ping" {
		t.Fatalf("ping: op=%d got=%q err=%v", op, got, err)
	}
}

func TestDiscordWSHeartbeatPayloadShape(t *testing.T) {
	var m struct {
		Op int    `json:"op"`
		D  *int64 `json:"d"`
	}
	if err := json.Unmarshal(heartbeatPayload(42), &m); err != nil {
		t.Fatal(err)
	}
	if m.Op != 1 || m.D == nil || *m.D != 42 {
		t.Fatalf("heartbeat(42) = %+v", m)
	}
	m.D = nil
	if err := json.Unmarshal(heartbeatPayload(-1), &m); err != nil {
		t.Fatal(err)
	}
	if m.Op != 1 || m.D != nil {
		t.Fatalf("heartbeat(-1) = %+v, want null sequence", m)
	}
}

func TestDiscordWSIdentifyPayloadShape(t *testing.T) {
	var m struct {
		Op int `json:"op"`
		D  struct {
			Token   string `json:"token"`
			Intents int    `json:"intents"`
		} `json:"d"`
	}
	if err := json.Unmarshal(identifyPayload("tok", 37376), &m); err != nil {
		t.Fatal(err)
	}
	if m.Op != 2 || m.D.Token != "tok" || m.D.Intents != 37376 {
		t.Fatalf("identify = %+v", m)
	}
}

func TestDiscordWSParseMessageCreate(t *testing.T) {
	raw := json.RawMessage(`{"id":"m1","channel_id":"C1","content":"hi bot",` +
		`"author":{"id":"U9","username":"alice","bot":false}}`)
	author, channel, content, isBot, ok := parseMessageCreate(raw)
	if !ok || isBot {
		t.Fatalf("ok=%v isBot=%v", ok, isBot)
	}
	if author != "U9" || channel != "C1" || content != "hi bot" {
		t.Fatalf("parsed = %q %q %q", author, channel, content)
	}

	bot := json.RawMessage(`{"id":"m2","channel_id":"C1","content":"x",` +
		`"author":{"id":"B1","bot":true}}`)
	if _, _, _, isBot, ok := parseMessageCreate(bot); !ok || !isBot {
		t.Fatalf("bot message: ok=%v isBot=%v", ok, isBot)
	}

	bad := json.RawMessage(`{"content":"no author or channel"}`)
	if _, _, _, _, ok := parseMessageCreate(bad); ok {
		t.Fatal("expected ok=false for missing author/channel")
	}
	if _, _, _, _, ok := parseMessageCreate(json.RawMessage(`{bad`)); ok {
		t.Fatal("expected ok=false for invalid JSON")
	}
}

func TestDiscordWSParseInteractionCreate(t *testing.T) {
	raw := json.RawMessage(`{"id":"i1","channel_id":"C7",` +
		`"data":{"custom_id":"btn_ok","component_type":2},` +
		`"member":{"user":{"id":"U5"}}}`)
	user, channel, custom, ok := parseInteractionCreate(raw)
	if !ok || user != "U5" || channel != "C7" || custom != "btn_ok" {
		t.Fatalf("parsed = %q %q %q ok=%v", user, channel, custom, ok)
	}
	// DM interaction carries a top-level user instead of member.
	dm := json.RawMessage(`{"channel_id":"C8",` +
		`"data":{"custom_id":"btn_dm"},"user":{"id":"U6"}}`)
	if user, _, custom, ok := parseInteractionCreate(dm); !ok || user != "U6" || custom != "btn_dm" {
		t.Fatalf("dm interaction: %q %q ok=%v", user, custom, ok)
	}
	if _, _, _, ok := parseInteractionCreate(json.RawMessage(`{"channel_id":"C8"}`)); ok {
		t.Fatal("expected ok=false without custom_id")
	}
}

func TestDiscordWSBackoffBounds(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		shift := attempt - 1
		if shift > 5 {
			shift = 5
		}
		floor := time.Second << shift
		if floor > 30*time.Second {
			floor = 30 * time.Second
		}
		d := wsBackoffDelay(attempt)
		if d < floor || d >= floor+time.Second {
			t.Fatalf("attempt %d: backoff %v out of [%v, %v)", attempt, d, floor, floor+time.Second)
		}
	}
}

func TestDiscordWSHandshakeRejectsNon101(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()
	wsURL := "ws://" + strings.TrimPrefix(plain.URL, "http://") + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := wsDial(ctx, wsURL)
	if err == nil || !strings.Contains(err.Error(), "handshake") {
		t.Fatalf("expected descriptive handshake error, got %v", err)
	}
}

func TestDiscordWSConnectValidation(t *testing.T) {
	if err := (&Gateway{}).Connect(context.Background()); err == nil {
		t.Fatal("expected error with empty token")
	}
	g := &Gateway{Token: "tok"}
	if err := g.Connect(context.Background()); err == nil {
		t.Fatal("expected error with nil broker")
	}
}

// TestDiscordWSReconnectLimit dials a server that always fails the handshake
// and asserts Connect gives up after exactly wsMaxAttempts tries.
func TestDiscordWSReconnectLimit(t *testing.T) {
	var accepts atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			br := bufio.NewReader(c)
			for {
				line, err := br.ReadString('\n')
				if err != nil || line == "\r\n" {
					break
				}
			}
			_, _ = io.WriteString(c, "HTTP/1.1 500 nope\r\nContent-Length: 0\r\n\r\n")
			_ = c.Close()
		}
	}()
	g := &Gateway{Token: "tok", Broker: New(&stubEngine{reply: "x"}, "")}
	g.wsURL = "ws://" + ln.Addr().String() + "/"
	g.testBackoff = func(int) time.Duration { return 5 * time.Millisecond }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = g.Connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "reconnect limit") {
		t.Fatalf("expected reconnect-limit error, got %v", err)
	}
	if got := accepts.Load(); got != wsMaxAttempts {
		t.Fatalf("server accepts = %d, want %d", got, wsMaxAttempts)
	}
}

// TestDiscordWSConnectIntegration runs Connect against a local plain-ws
// server (manual handshake over net.Listen) plus an httptest REST endpoint,
// asserting HELLO→IDENTIFY→DISPATCH→Broker.Handle→REST reply, heartbeat
// flow, and that bot-authored messages are ignored.
func TestDiscordWSConnectIntegration(t *testing.T) {
	type post struct {
		path string
		body string
	}
	var mu sync.Mutex
	var posts []post
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			posts = append(posts, post{path: r.URL.Path, body: string(body)})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"m1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer rest.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	identifyCh := make(chan []byte, 1)
	hbCh := make(chan []byte, 64)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		var key string
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if name, value, found := strings.Cut(line, ":"); found &&
				strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Key") {
				key = strings.TrimSpace(value)
			}
			if line == "\r\n" {
				break
			}
		}
		if key == "" {
			return
		}
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + wsAcceptKey(key) + "\r\n\r\n"
		if _, err := io.WriteString(conn, resp); err != nil {
			return
		}
		if err := wsServerWriteText(conn, []byte(`{"op":10,"d":{"heartbeat_interval":50}}`)); err != nil {
			return
		}
		op, p, err := wsReadFrame(br)
		if err != nil || op != wsOpText {
			return
		}
		identifyCh <- p
		for _, msg := range []string{
			`{"op":0,"t":"READY","s":1,"d":{"user":{"id":"BOTID"}}}`,
			`{"op":0,"t":"MESSAGE_CREATE","s":2,"d":{"id":"m1","channel_id":"C123","content":"hello bot","author":{"id":"U1","username":"alice"}}}`,
			`{"op":0,"t":"MESSAGE_CREATE","s":3,"d":{"id":"m2","channel_id":"C123","content":"bot loop","author":{"id":"BOTID","bot":true}}}`,
		} {
			if err := wsServerWriteText(conn, []byte(msg)); err != nil {
				return
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			op, p, err := wsReadFrame(br)
			if err != nil {
				return
			}
			if op == wsOpClose {
				return
			}
			if op == wsOpText {
				select {
				case hbCh <- p:
				default:
				}
			}
		}
	}()

	eng := &stubEngine{reply: "test-reply"}
	g := &Gateway{Token: "test-token", Broker: New(eng, "sys"), Intents: DefaultDiscordIntents}
	g.baseURL = rest.URL
	g.wsURL = "ws://" + ln.Addr().String() + "/?v=10&encoding=json"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- g.Connect(ctx) }()

	// IDENTIFY must carry our token and intents.
	select {
	case raw := <-identifyCh:
		var m struct {
			Op int `json:"op"`
			D  struct {
				Token   string `json:"token"`
				Intents int    `json:"intents"`
			} `json:"d"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("identify not JSON: %v", err)
		}
		if m.Op != 2 || m.D.Token != "test-token" || m.D.Intents != DefaultDiscordIntents {
			t.Fatalf("identify = %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for IDENTIFY")
	}

	// Wait for the REST reply triggered by MESSAGE_CREATE.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(posts)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for discord reply POST")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Heartbeats must flow (50ms interval — allow generous time).
	sawHeartbeat := false
	hbDeadline := time.Now().Add(3 * time.Second)
	for !sawHeartbeat && time.Now().Before(hbDeadline) {
		select {
		case raw := <-hbCh:
			var m struct {
				Op int `json:"op"`
			}
			if json.Unmarshal(raw, &m) == nil && m.Op == 1 {
				sawHeartbeat = true
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !sawHeartbeat {
		t.Fatal("no heartbeat frame observed from client")
	}

	// The bot-authored message must not produce a second reply.
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	n := len(posts)
	first := posts[0]
	mu.Unlock()
	if n != 1 {
		t.Fatalf("posts = %d, want exactly 1 (bot message must be ignored)", n)
	}
	if !strings.Contains(first.path, "/channels/C123/messages") {
		t.Fatalf("post path = %q, want channel C123", first.path)
	}
	var sent struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(first.body), &sent); err != nil || sent.Content != "test-reply" {
		t.Fatalf("post body = %q", first.body)
	}
	if eng.calls != 1 {
		t.Fatalf("engine calls = %d, want 1", eng.calls)
	}
	if !strings.Contains(eng.last, "hello bot") {
		t.Fatalf("engine input = %q, want message content", eng.last)
	}

	cancel()
	if cerr := <-errCh; !errors.Is(cerr, context.Canceled) && !errors.Is(cerr, context.DeadlineExceeded) {
		t.Fatalf("Connect returned %v, want context cancellation", cerr)
	}
}
