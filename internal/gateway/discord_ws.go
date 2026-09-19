// Discord websocket inbound gateway (stdlib only: net/http, crypto/tls,
// crypto/sha1, crypto/rand, encoding/* — no new dependencies).
//
// Required privileged intents: MESSAGE_CONTENT (1<<15) must be enabled for
// the bot in the Discord developer portal, otherwise MESSAGE_CREATE events
// arrive with empty content and there is nothing to answer (such events are
// skipped honestly instead of producing empty replies). The default intents
// also subscribe to GUILD_MESSAGES (1<<9) and DIRECT_MESSAGES (1<<12).
// GUILD_MEMBERS / PRESENCE privileged intents are NOT needed.
//
// Protocol summary: GET {base}/gateway/bot (Bot auth) yields the websocket
// URL; the client performs an RFC6455 handshake, waits for HELLO(10), starts
// a heartbeat loop, sends IDENTIFY(2), then handles DISPATCH(0) events.
// MESSAGE_CREATE and INTERACTION_CREATE (button presses) are routed through
// Broker.Handle("discord", authorID, content) and the reply is posted back
// via REST. Any close / missed heartbeat ACK / reconnect request triggers a
// reconnect with backoff+jitter, up to wsMaxAttempts tries, then Connect
// returns an error. ctx cancellation always wins and is reported as ctx.Err().
package gateway

import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultDiscordBase = "https://discord.com/api/v10"

	// wsMagic is the RFC6455 GUID used for Sec-WebSocket-Accept.
	wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// wsMaxAttempts bounds the number of consecutive failed sessions
	// (initial dial + reconnects) before Connect gives up.
	wsMaxAttempts = 5

	wsDialTimeout      = 15 * time.Second
	wsHandshakeTimeout = 20 * time.Second
	wsHelloTimeout     = 30 * time.Second
	wsMaxFrame         = 16 << 20 // 16 MiB sanity cap per frame payload

	// Gateway opcodes (Discord).
	gwDispatch       = 0
	gwHeartbeat      = 1
	gwIdentify       = 2
	gwReconnect      = 7
	gwInvalidSession = 9
	gwHello          = 10
	gwHeartbeatACK   = 11

	// Websocket frame opcodes (RFC6455).
	wsOpCont   = 0x0
	wsOpText   = 0x1
	wsOpBinary = 0x2
	wsOpClose  = 0x8
	wsOpPing   = 0x9
	wsOpPong   = 0xA

	// Discord intent bits. MESSAGE_CONTENT is privileged and must be
	// toggled on in the developer portal for the bot.
	discordIntentGuildMessages  = 1 << 9
	discordIntentDirectMessages = 1 << 12
	discordIntentMessageContent = 1 << 15
)

// DefaultDiscordIntents subscribes to guild + DM messages including content.
const DefaultDiscordIntents = discordIntentGuildMessages | discordIntentDirectMessages | discordIntentMessageContent

// Gateway is a Discord websocket inbound client bound to a Broker.
// Token and Broker are required. Intents defaults to DefaultDiscordIntents
// when zero. baseURL defaults to https://discord.com/api/v10 and wsURL, when
// set, skips the gateway/bot lookup (both are overrides for tests).
type Gateway struct {
	Token   string
	Broker  *Broker
	Intents int

	baseURL string
	wsURL   string

	// testBackoff overrides wsBackoffDelay in tests (nil = default).
	testBackoff func(attempt int) time.Duration
}

func (g *Gateway) restBase() string {
	if g.baseURL != "" {
		return strings.TrimSuffix(g.baseURL, "/")
	}
	return defaultDiscordBase
}

func (g *Gateway) effectiveIntents() int {
	if g.Intents == 0 {
		return DefaultDiscordIntents
	}
	return g.Intents
}

// Connect runs the websocket lifecycle until ctx is cancelled (returns
// ctx.Err()) or wsMaxAttempts consecutive sessions fail (returns an error).
func (g *Gateway) Connect(ctx context.Context) error {
	if g.Token == "" {
		return fmt.Errorf("discord ws: empty token")
	}
	if g.Broker == nil {
		return fmt.Errorf("discord ws: nil broker")
	}
	wsURL := g.wsURL
	if wsURL == "" {
		u, err := g.fetchGatewayURL(ctx)
		if err != nil {
			return err
		}
		wsURL = u
	}
	backoff := g.testBackoff
	if backoff == nil {
		backoff = wsBackoffDelay
	}
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		established := false
		serr := g.runSession(ctx, wsURL, func() { established = true })
		if err := ctx.Err(); err != nil {
			return err
		}
		if established {
			attempt = 0
		}
		attempt++
		if attempt >= wsMaxAttempts {
			return fmt.Errorf("discord ws: reconnect limit reached (%d tries): last error: %v", wsMaxAttempts, serr)
		}
		wait := backoff(attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// fetchGatewayURL performs GET {base}/gateway/bot with Bot auth.
func (g *Gateway) fetchGatewayURL(ctx context.Context) (string, error) {
	endpoint := g.restBase() + "/gateway/bot"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("discord ws: gateway/bot request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+g.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("discord ws: gateway/bot: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord ws: gateway/bot: status %s", resp.Status)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("discord ws: gateway/bot: bad JSON: %w", err)
	}
	u := strings.TrimSpace(out.URL)
	if u == "" {
		return "", fmt.Errorf("discord ws: gateway/bot: empty url in response")
	}
	if !strings.Contains(u, "?") {
		u += "?v=10&encoding=json"
	}
	return u, nil
}

// runSession dials once, performs HELLO/IDENTIFY, and pumps events until the
// connection drops, the server asks for a reconnect, or ctx ends.
func (g *Gateway) runSession(ctx context.Context, wsURL string, onEstablished func()) error {
	wc, err := wsDial(ctx, wsURL)
	if err != nil {
		return err
	}
	defer wc.closeConn()

	sctx, stopSess := context.WithCancel(ctx)
	defer stopSess()
	// Unblock a pending Read when the session ends or ctx is cancelled.
	go func() {
		<-sctx.Done()
		wc.closeConn()
	}()

	_ = wc.conn.SetReadDeadline(time.Now().Add(wsHelloTimeout))
	op, payload, err := wc.read()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("discord ws: waiting for HELLO: %w", err)
	}
	if op == wsOpClose {
		return fmt.Errorf("discord ws: closed before HELLO: %s", wsCloseReason(payload))
	}
	if op != wsOpText && op != wsOpBinary {
		return fmt.Errorf("discord ws: expected HELLO text frame, got opcode %d", op)
	}
	var env gwEnvelope
	if err := json.Unmarshal(payload, &env); err != nil || env.Op != gwHello {
		return fmt.Errorf("discord ws: expected HELLO(10), got %q", truncateForError(payload, 160))
	}
	var hello helloData
	if err := json.Unmarshal(env.Data, &hello); err != nil || hello.HeartbeatInterval <= 0 {
		return fmt.Errorf("discord ws: bad HELLO heartbeat_interval in %q", truncateForError(env.Data, 160))
	}
	interval := time.Duration(hello.HeartbeatInterval) * time.Millisecond

	if err := wc.sendText(identifyPayload(g.Token, g.effectiveIntents())); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("discord ws: identify: %w", err)
	}
	if onEstablished != nil {
		onEstablished()
	}

	var seq atomic.Int64
	seq.Store(-1)
	var awaitingAck atomic.Bool
	hbDone := make(chan struct{})
	go g.heartbeatLoop(sctx, wc, interval, &seq, &awaitingAck, hbDone)
	defer func() {
		stopSess()
		<-hbDone
	}()

	selfID := ""
	for {
		_ = wc.conn.SetReadDeadline(time.Now().Add(interval*2 + 15*time.Second))
		op, payload, err := wc.read()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("discord ws: read: %w", err)
		}
		switch op {
		case wsOpClose:
			return fmt.Errorf("discord ws: server closed connection: %s", wsCloseReason(payload))
		case wsOpPing:
			_ = wc.sendPong(payload) // best effort
			continue
		case wsOpPong:
			continue
		case wsOpCont:
			// wsReadFrame reassembles fragments, so this is unreachable.
			continue
		}
		var env gwEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			continue // ignore malformed payloads, keep the session alive
		}
		switch env.Op {
		case gwDispatch:
			if env.Seq != nil {
				seq.Store(int64(*env.Seq))
			}
			switch env.Type {
			case "READY":
				var ready struct {
					User struct {
						ID string `json:"id"`
					} `json:"user"`
				}
				if json.Unmarshal(env.Data, &ready) == nil {
					selfID = ready.User.ID
				}
			case "MESSAGE_CREATE":
				g.handleMessageCreate(ctx, selfID, env.Data)
			case "INTERACTION_CREATE":
				g.handleInteractionCreate(ctx, env.Data)
			}
		case gwHeartbeat:
			// Server-requested heartbeat: extra beat, does not disturb
			// the interval ACK tracking.
			_ = wc.sendText(heartbeatPayload(seq.Load()))
		case gwHeartbeatACK:
			awaitingAck.Store(false)
		case gwReconnect:
			return fmt.Errorf("discord ws: server requested reconnect (op 7)")
		case gwInvalidSession:
			return fmt.Errorf("discord ws: invalid session (op 9)")
		}
	}
}

// heartbeatLoop sends a heartbeat every interval (after an initial jittered
// delay, per the Discord gateway docs). If an ACK is still outstanding when
// the next beat is due, the connection is presumed stale and closed so the
// read loop errors out and Connect reconnects.
func (g *Gateway) heartbeatLoop(ctx context.Context, wc *wsClient, interval time.Duration, seq *atomic.Int64, awaitingAck *atomic.Bool, done chan<- struct{}) {
	defer close(done)
	jitter := time.Duration(rand.Int63n(int64(interval) + 1))
	t := time.NewTimer(jitter)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if awaitingAck.Load() {
			wc.closeConn()
			return
		}
		if err := wc.sendText(heartbeatPayload(seq.Load())); err != nil {
			return
		}
		awaitingAck.Store(true)
		t.Reset(interval)
	}
}

// handleMessageCreate routes a MESSAGE_CREATE event through the broker and
// posts the reply back to the originating channel. Bot messages (including
// our own), empty content (missing MESSAGE_CONTENT intent), and empty
// replies are skipped. REST delivery is best effort: a send failure never
// kills the session.
func (g *Gateway) handleMessageCreate(ctx context.Context, selfID string, raw json.RawMessage) {
	author, channel, content, isBot, ok := parseMessageCreate(raw)
	if !ok || isBot || (selfID != "" && author == selfID) || strings.TrimSpace(content) == "" {
		return
	}
	if g.Broker == nil {
		return
	}
	reply := g.Broker.Handle("discord", author, content)
	if strings.TrimSpace(reply) == "" {
		return
	}
	_ = g.sendReply(ctx, channel, reply)
}

// handleInteractionCreate routes button presses (INTERACTION_CREATE with a
// message-component custom_id) through the broker as "button:<custom_id>".
func (g *Gateway) handleInteractionCreate(ctx context.Context, raw json.RawMessage) {
	user, channel, customID, ok := parseInteractionCreate(raw)
	if !ok || g.Broker == nil {
		return
	}
	reply := g.Broker.Handle("discord", user, "button:"+customID)
	if strings.TrimSpace(reply) == "" {
		return
	}
	_ = g.sendReply(ctx, channel, reply)
}

// sendReply posts text to channelID, chunked to 2000 chars per message.
func (g *Gateway) sendReply(ctx context.Context, channelID, text string) error {
	if g.Token == "" {
		return fmt.Errorf("discord: empty token")
	}
	if channelID == "" {
		return fmt.Errorf("discord: empty channel id")
	}
	for _, chunk := range chunkDiscord(text, 2000) {
		body, err := json.Marshal(map[string]string{"content": chunk})
		if err != nil {
			return err
		}
		endpoint := g.restBase() + "/channels/" + channelID + "/messages"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bot "+g.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("discord send: status %s", resp.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

// wsBackoffDelay returns the wait before reconnect attempt (1-based):
// 1s, 2s, 4s, … capped at 30s, plus uniform jitter in [0,1s).
func wsBackoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	base := time.Second << shift
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	return base + time.Duration(rand.Int63n(int64(time.Second)))
}

// gwEnvelope is a Discord gateway payload.
type gwEnvelope struct {
	Op   int             `json:"op"`
	Type string          `json:"t"`
	Seq  *int            `json:"s"`
	Data json.RawMessage `json:"d"`
}

// helloData is the HELLO(10) data block.
type helloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

// heartbeatPayload builds the {"op":1,"d":seq} heartbeat body; seq < 0
// encodes a null sequence (no dispatch seen yet).
func heartbeatPayload(seq int64) []byte {
	if seq < 0 {
		return []byte(`{"op":1,"d":null}`)
	}
	return []byte(fmt.Sprintf(`{"op":1,"d":%d}`, seq))
}

// identifyPayload builds the IDENTIFY(2) body.
func identifyPayload(token string, intents int) []byte {
	body, _ := json.Marshal(map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   token,
			"intents": intents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "nimbus-one",
				"device":  "nimbus-one",
			},
		},
	})
	return body
}

// parseMessageCreate extracts author/channel/content from a MESSAGE_CREATE
// data block. isBot reports the author bot flag; ok is false when required
// fields are missing.
func parseMessageCreate(raw json.RawMessage) (authorID, channelID, content string, isBot bool, ok bool) {
	var m struct {
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
		Author    struct {
			ID  string `json:"id"`
			Bot *bool  `json:"bot"`
		} `json:"author"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "", "", false, false
	}
	if m.Author.ID == "" || m.ChannelID == "" {
		return "", "", "", false, false
	}
	if m.Author.Bot != nil && *m.Author.Bot {
		isBot = true
	}
	return m.Author.ID, m.ChannelID, m.Content, isBot, true
}

// parseInteractionCreate extracts user/channel/custom_id from an
// INTERACTION_CREATE data block (button presses carry data.custom_id).
func parseInteractionCreate(raw json.RawMessage) (userID, channelID, customID string, ok bool) {
	var in struct {
		ChannelID string `json:"channel_id"`
		Data      struct {
			CustomID string `json:"custom_id"`
		} `json:"data"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Member struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"member"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", "", "", false
	}
	userID = in.Member.User.ID
	if userID == "" {
		userID = in.User.ID
	}
	if userID == "" || in.ChannelID == "" || in.Data.CustomID == "" {
		return "", "", "", false
	}
	return userID, in.ChannelID, in.Data.CustomID, true
}

// wsClient is a minimal RFC6455 client: Lib-free framing over net.Conn.
// Reads happen on one goroutine; all writes are serialized by mu.
type wsClient struct {
	conn   net.Conn
	rd     *bufio.Reader
	mu     sync.Mutex
	closed atomic.Bool
}

func (c *wsClient) closeConn() {
	if c.closed.CompareAndSwap(false, true) {
		_ = c.conn.Close()
	}
}

func (c *wsClient) sendText(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("discord ws: connection closed")
	}
	return wsWriteRaw(c.conn, true, wsOpText, payload, true)
}

func (c *wsClient) sendPong(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("discord ws: connection closed")
	}
	return wsWriteRaw(c.conn, true, wsOpPong, payload, true)
}

func (c *wsClient) read() (byte, []byte, error) {
	return wsReadFrame(c.rd)
}

// wsAcceptKey derives Sec-WebSocket-Accept from the client key (RFC6455).
func wsAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsMagic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// wsGenerateKey returns a random base64 Sec-WebSocket-Key.
func wsGenerateKey() (string, error) {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		// crypto/rand should never fail; fall back to math/rand rather
		// than aborting the dial.
		for i := range b {
			b[i] = byte(rand.Intn(256))
		}
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

// wsDial opens a TCP (ws) or TLS (wss) connection and performs the RFC6455
// opening handshake. Failures return descriptive errors; there is no
// half-connected state — either a validated *wsClient or an error.
func wsDial(ctx context.Context, rawURL string) (*wsClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("discord ws: bad websocket url %q: %w", rawURL, err)
	}
	var useTLS bool
	switch strings.ToLower(u.Scheme) {
	case "ws":
	case "wss":
		useTLS = true
	default:
		return nil, fmt.Errorf("discord ws: unsupported scheme %q (want ws or wss)", u.Scheme)
	}
	host := u.Host
	if host == "" {
		return nil, fmt.Errorf("discord ws: missing host in url %q", rawURL)
	}
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	dialer := &net.Dialer{Timeout: wsDialTimeout}
	var conn net.Conn
	if !useTLS {
		conn, err = dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			return nil, fmt.Errorf("discord ws: dial %s: %w", host, err)
		}
	} else {
		plain, err := dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			return nil, fmt.Errorf("discord ws: dial %s: %w", host, err)
		}
		tc := tls.Client(plain, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = plain.Close()
			return nil, fmt.Errorf("discord ws: tls handshake with %s: %w", host, err)
		}
		conn = tc
	}
	key, err := wsGenerateKey()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(wsHandshakeTimeout))
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("discord ws: handshake write: %w", err)
	}
	rd := bufio.NewReader(conn)
	accept, statusErr := wsReadHandshake(rd)
	_ = conn.SetDeadline(time.Time{})
	if statusErr != nil {
		_ = conn.Close()
		return nil, statusErr
	}
	if accept != wsAcceptKey(key) {
		_ = conn.Close()
		return nil, fmt.Errorf("discord ws: handshake failed: bad Sec-WebSocket-Accept (possible proxy interference)")
	}
	return &wsClient{conn: conn, rd: rd}, nil
}

// wsReadHandshake parses the server handshake response, requiring
// "101 Switching Protocols" plus a Sec-WebSocket-Accept value.
func wsReadHandshake(rd *bufio.Reader) (string, error) {
	status, err := rd.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("discord ws: handshake failed: cannot read status line: %w", err)
	}
	if !strings.Contains(status, "101") {
		return "", fmt.Errorf("discord ws: handshake failed: unexpected status %q (want 101 Switching Protocols)", strings.TrimSpace(status))
	}
	var accept string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("discord ws: handshake failed: cannot read headers: %w", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(value)
		}
	}
	if accept == "" {
		return "", fmt.Errorf("discord ws: handshake failed: missing Sec-WebSocket-Accept header")
	}
	return accept, nil
}

// wsWriteRaw writes one frame. Client frames are masked (masked=true);
// fin=false emits a fragment (opcode wsOpCont for continuations).
func wsWriteRaw(w io.Writer, fin bool, opcode byte, payload []byte, masked bool) error {
	var b0 byte = opcode & 0x0f
	if fin {
		b0 |= 0x80
	}
	n := len(payload)
	var hdr [10]byte
	hdr[0] = b0
	pos := 2
	second := byte(0)
	if masked {
		second |= 0x80
	}
	switch {
	case n < 126:
		hdr[1] = second | byte(n)
	case n <= 0xffff:
		hdr[1] = second | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		pos = 4
	default:
		hdr[1] = second | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		pos = 10
	}
	if _, err := w.Write(hdr[:pos]); err != nil {
		return fmt.Errorf("discord ws: write frame header: %w", err)
	}
	if !masked {
		if n > 0 {
			if _, err := w.Write(payload); err != nil {
				return fmt.Errorf("discord ws: write frame body: %w", err)
			}
		}
		return nil
	}
	var mask [4]byte
	if _, err := crand.Read(mask[:]); err != nil {
		for i := range mask {
			mask[i] = byte(rand.Intn(256))
		}
	}
	if _, err := w.Write(mask[:]); err != nil {
		return fmt.Errorf("discord ws: write frame mask: %w", err)
	}
	var buf [4096]byte
	for off := 0; off < n; {
		chunk := n - off
		if chunk > len(buf) {
			chunk = len(buf)
		}
		for i := 0; i < chunk; i++ {
			buf[i] = payload[off+i] ^ mask[(off+i)%4]
		}
		if _, err := w.Write(buf[:chunk]); err != nil {
			return fmt.Errorf("discord ws: write frame body: %w", err)
		}
		off += chunk
	}
	return nil
}

// wsServerWriteText writes an unmasked server-style text frame (used by tests).
func wsServerWriteText(w io.Writer, payload []byte) error {
	return wsWriteRaw(w, true, wsOpText, payload, false)
}

// wsReadFrame reads one complete message, reassembling fragments and
// returning control frames (close/ping/pong) immediately.
func wsReadFrame(r *bufio.Reader) (byte, []byte, error) {
	var msg []byte
	var msgOp byte
	started := false
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return 0, nil, fmt.Errorf("discord ws: read frame: %w", err)
		}
		fin := hdr[0]&0x80 != 0
		if hdr[0]&0x70 != 0 {
			return 0, nil, fmt.Errorf("discord ws: protocol error: RSV bits set")
		}
		op := hdr[0] & 0x0f
		masked := hdr[1]&0x80 != 0
		length := int(hdr[1] & 0x7f)
		switch length {
		case 126:
			ext := make([]byte, 2)
			if _, err := io.ReadFull(r, ext); err != nil {
				return 0, nil, fmt.Errorf("discord ws: read frame: %w", err)
			}
			length = int(binary.BigEndian.Uint16(ext))
		case 127:
			ext := make([]byte, 8)
			if _, err := io.ReadFull(r, ext); err != nil {
				return 0, nil, fmt.Errorf("discord ws: read frame: %w", err)
			}
			u64 := binary.BigEndian.Uint64(ext)
			if u64 > wsMaxFrame {
				return 0, nil, fmt.Errorf("discord ws: frame too large (%d bytes)", u64)
			}
			length = int(u64)
		}
		if op >= 0x8 {
			if !fin {
				return 0, nil, fmt.Errorf("discord ws: protocol error: fragmented control frame")
			}
			if length > 125 {
				return 0, nil, fmt.Errorf("discord ws: protocol error: control frame too large")
			}
		} else if length > wsMaxFrame {
			return 0, nil, fmt.Errorf("discord ws: frame too large (%d bytes)", length)
		}
		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(r, maskKey[:]); err != nil {
				return 0, nil, fmt.Errorf("discord ws: read frame: %w", err)
			}
		}
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(r, payload); err != nil {
				return 0, nil, fmt.Errorf("discord ws: read frame: %w", err)
			}
			if masked {
				for i := range payload {
					payload[i] ^= maskKey[i%4]
				}
			}
		}
		if op >= 0x8 {
			return op, payload, nil
		}
		if op != wsOpCont {
			if started {
				return 0, nil, fmt.Errorf("discord ws: protocol error: new data frame before continuation finished")
			}
			if op != wsOpText && op != wsOpBinary {
				return 0, nil, fmt.Errorf("discord ws: protocol error: unsupported opcode %d", op)
			}
			msgOp = op
			started = true
		} else if !started {
			return 0, nil, fmt.Errorf("discord ws: protocol error: unexpected continuation frame")
		}
		msg = append(msg, payload...)
		if len(msg) > wsMaxFrame {
			return 0, nil, fmt.Errorf("discord ws: message too large")
		}
		if fin {
			return msgOp, msg, nil
		}
	}
}

// wsCloseReason formats a close frame payload as "code reason".
func wsCloseReason(payload []byte) string {
	if len(payload) < 2 {
		return strings.TrimSpace(string(payload))
	}
	code := binary.BigEndian.Uint16(payload[:2])
	reason := strings.TrimSpace(string(payload[2:]))
	if reason == "" {
		return fmt.Sprintf("code %d", code)
	}
	return fmt.Sprintf("code %d (%s)", code, reason)
}

// truncateForError keeps error messages bounded for large payloads.
func truncateForError(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	if s == "" {
		return "<empty>"
	}
	return s
}
