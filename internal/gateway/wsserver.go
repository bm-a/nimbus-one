// WSServer is a JSON-RPC-over-WebSocket front end for the gateway Broker.
//
// OpenClaw reference (read-only mirror /data/data/com.termux/files/home/tmp/openclaw-src):
//
//	src/gateway/websocket-protocol.ts  — frame protocol + keepalive policy
//	src/gateway/websocket-keepalive.ts — ping cadence and dead-peer detection
//	src/gateway/server-http.ts         — HTTP route stages + upgrade claim
//	src/gateway/server-methods/*       — agent/sessions/approvals/cron/config/
//	                                     models/nodes/pairing/hooks/plugin
//	                                     methods served over the socket
//
// Design notes:
//   - net/http offers no server-side WebSocket support beyond Hijacker, so
//     HandleConn performs the RFC6455 opening handshake manually and then
//     serves frames with the shared helpers in discord_ws.go (wsAcceptKey,
//     wsReadFrame, wsWriteRaw, wsCloseReason). One framing implementation is
//     kept on purpose instead of a second copy.
//   - Only text frames carry RPC traffic. Binary frames are refused with
//     close 1003. The server emits a ping every PingInterval (default 30s)
//     and drops peers silent for 3x that interval.
//   - Wire shape: {"id":<any>,"method":"chat","params":{...}} →
//     {"id":<same>,"result":...} or {"id":<same>,"error":"..."}.
//   - Test-seam honesty: httptest.ResponseWriter does not implement
//     http.Hijacker, so HandleConn's upgrade path cannot run under httptest.
//     wsserver_test.go therefore covers the handshake-response builder,
//     masked frame round-trips through the shared framing helpers, and the
//     method dispatch table directly.
package gateway

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	wsDefaultPingInterval = 30 * time.Second
	wsReadTimeoutFactor   = 3 // read deadline = factor * ping interval
)

// WSServer serves Broker methods over a WebSocket. Token, when non-empty,
// requires "Authorization: Bearer <token>" (or ?token= for browser clients
// that cannot set upgrade headers); otherwise the handshake is rejected
// with 401 before any upgrade happens.
type WSServer struct {
	Broker *Broker
	Token  string
	// PingInterval is the keepalive cadence. Non-positive means 30s.
	PingInterval time.Duration
}

// Register mounts HandleConn at path ("/ws" when path is empty).
func (s *WSServer) Register(mux *http.ServeMux, path string) {
	if path == "" {
		path = "/ws"
	}
	mux.HandleFunc(path, s.HandleConn)
}

// wsRequest is one inbound RPC call.
type wsRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// wsResponse is the envelope for every reply.
type wsResponse struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// HandleConn upgrades an HTTP request to a WebSocket and serves RPC calls
// until the peer closes or goes silent.
func (s *WSServer) HandleConn(w http.ResponseWriter, r *http.Request) {
	if s != nil && s.Token != "" {
		got := bearerToken(r.Header.Get("Authorization"))
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if got != s.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "" && v != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version (want 13)", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		http.Error(w, "websocket hijack failed", http.StatusInternalServerError)
		return
	}
	if _, err := io.WriteString(rw, wsHandshakeResponse(key)); err != nil {
		_ = conn.Close()
		return
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	s.serveWS(conn)
}

// serveWS runs the frame loop on an already-upgraded connection.
func (s *WSServer) serveWS(conn net.Conn) {
	ping := wsDefaultPingInterval
	if s != nil && s.PingInterval > 0 {
		ping = s.PingInterval
	}
	var wmu sync.Mutex
	write := func(op byte, payload []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return wsWriteRaw(conn, true, op, payload, false)
	}
	done := make(chan struct{})
	defer close(done)
	defer conn.Close()
	go func() {
		t := time.NewTicker(ping)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := write(wsOpPing, nil); err != nil {
					return
				}
			}
		}
	}()
	rd := bufio.NewReader(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(wsReadTimeoutFactor) * ping))
		op, payload, err := wsReadFrame(rd)
		if err != nil {
			return // timeout, close, or protocol error: just drop
		}
		switch op {
		case wsOpClose:
			_ = write(wsOpClose, payload) // echo close, then drop
			return
		case wsOpPing:
			_ = write(wsOpPong, payload)
		case wsOpPong:
			// Keepalive reply; the read deadline was already refreshed.
		case wsOpBinary:
			_ = write(wsOpClose, wsClosePayload(1003, "binary frames unsupported"))
			return
		case wsOpText:
			resp := s.handleMessage(payload)
			if err := write(wsOpText, resp); err != nil {
				return
			}
		default:
			_ = write(wsOpClose, wsClosePayload(1002, "unsupported opcode"))
			return
		}
	}
}

// handleMessage parses one text frame payload and builds the reply bytes.
func (s *WSServer) handleMessage(raw []byte) []byte {
	var req wsRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return mustJSON(wsResponse{Error: "parse error: " + err.Error()})
	}
	if strings.TrimSpace(req.Method) == "" {
		return mustJSON(wsResponse{ID: req.ID, Error: "missing method"})
	}
	result, err := s.dispatch(req.Method, req.Params)
	if err != nil {
		return mustJSON(wsResponse{ID: req.ID, Error: err.Error()})
	}
	return mustJSON(wsResponse{ID: req.ID, Result: result})
}

// dispatch runs one RPC method against the Broker. It is the unit-testable
// core of the socket: ping, chat{message,user}, status, sessions.list.
func (s *WSServer) dispatch(method string, params json.RawMessage) (any, error) {
	switch method {
	case "ping":
		return "pong", nil
	case "chat":
		var p struct {
			Message string `json:"message"`
			User    string `json:"user"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fmt.Errorf("bad params: %w", err)
			}
		}
		if strings.TrimSpace(p.Message) == "" {
			return nil, fmt.Errorf("empty message")
		}
		user := p.User
		if user == "" {
			user = "ws"
		}
		if s == nil || s.Broker == nil {
			return nil, fmt.Errorf("no broker")
		}
		return s.Broker.Handle("ws", user, p.Message), nil
	case "status":
		mode := "build"
		if s != nil && s.Broker != nil {
			mode = s.Broker.Mode()
		}
		return map[string]any{"ok": true, "mode": mode}, nil
	case "sessions.list":
		if s == nil || s.Broker == nil {
			return nil, fmt.Errorf("no broker")
		}
		return s.Broker.SessionsStore().SessionKeys(), nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

// SessionKeys returns a sorted snapshot of session keys. It lives here (not
// session.go) so sessions.list has enumeration support without touching
// existing files.
func (s *Sessions) SessionKeys() []string {
	if s == nil {
		return []string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// wsHandshakeResponse builds the 101 response bytes for a client key.
func wsHandshakeResponse(key string) string {
	return "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(key) + "\r\n\r\n"
}

// wsClosePayload encodes a close frame body (code + reason).
func wsClosePayload(code uint16, reason string) []byte {
	b := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(b, code)
	return append(b, reason...)
}

// bearerToken extracts the token from an Authorization header value.
func bearerToken(h string) string {
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// headerHasToken reports whether a comma-separated header value contains tok
// (case-insensitive), e.g. Connection: keep-alive, Upgrade.
func headerHasToken(header, tok string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), tok) {
			return true
		}
	}
	return false
}

// mustJSON marshals v, falling back to a static error envelope.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"id":null,"error":"internal encode failure"}`)
	}
	return b
}
