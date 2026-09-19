package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// OpenClaw app compatibility (protocol subset, tested).
//
// What the OpenClaw app expects (from packages/gateway-protocol in the
// reference tree): WebSocket text frames {type:req,id,method,params},
// a connect handshake returning hello-ok (protocol 4), token auth, and
// chat/sessions methods. Nimbus-One implements exactly this subset:
//   - connect → hello-ok (protocol 4, methods we actually serve)
//   - chat.send → runs one agent turn headless, returns the reply
//   - sessions.list → in-memory session log of this process
// Deliberately NOT implemented (documented, not faked): pairing flows,
// Tailscale auth, operator admin scopes, node/device management. Those
// need infrastructure we cannot verify here; the server answers
// "unknown method" for them instead of pretending.
//
// Shell commands inside chat.send run headless, so the mandatory
// confirmation gate refuses them with guidance (see confirm.go). Nothing
// executes silently.

// bridgeSession is one chat turn record (in-memory only, no database).
type bridgeSession struct {
	ID      string `json:"id"`
	Preview string `json:"preview"`
	Created string `json:"created"`
}

// bridgeServer holds bridge state. run executes one agent turn; it is a
// field so tests inject a fake instead of calling Anthropic.
type bridgeServer struct {
	token    string
	mu       sync.Mutex
	sessions []bridgeSession
	seq      int
	run      func(text string) (string, error)
}

func newBridge(cfg *Config) *bridgeServer {
	b := &bridgeServer{token: cfg.HTTPToken}
	b.run = func(text string) (string, error) {
		if err := cfg.validate(); err != nil {
			return "", err
		}
		r, err := resolveClient(cfg, "", "", "")
		if err != nil {
			return "", err
		}
		// Headless: shell is refused by the confirm gate, never executed.
		return runAgent(r.client, strings.NewReader(""), io.Discard, false, text, 25)
	}
	return b
}

// frame types.
type reqFrame struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type resFrame struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Payload any    `json:"payload,omitempty"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type eventFrame struct {
	Type    string `json:"type"`
	Event   string `json:"event"`
	Payload any    `json:"payload,omitempty"`
}

func errRes(id, code, msg string) []byte {
	raw, _ := json.Marshal(resFrame{Type: "res", ID: id, OK: false,
		Error: &struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: msg}})
	return raw
}

func okRes(id string, payload any) []byte {
	raw, _ := json.Marshal(resFrame{Type: "res", ID: id, OK: true, Payload: payload})
	return raw
}

// connState tracks one WS connection's auth.
type connState struct {
	authed bool
}

// handleFrame processes one parsed request frame. Pure logic — fully
// unit-testable without sockets.
func (b *bridgeServer) handleFrame(st *connState, req reqFrame) [][]byte {
	if req.Type != "req" || req.ID == "" {
		return [][]byte{errRes(req.ID, "BAD_FRAME", "expected {type:req,id,method,params}")}
	}
	switch req.Method {
	case "connect":
		return [][]byte{b.handleConnect(st, req)}
	case "chat.send":
		if !st.authed {
			return [][]byte{errRes(req.ID, "UNAUTHENTICATED", "send connect first")}
		}
		text, _ := req.Params["text"].(string)
		if strings.TrimSpace(text) == "" {
			if m, _ := req.Params["message"].(string); strings.TrimSpace(m) != "" {
				text = m
			}
		}
		if strings.TrimSpace(text) == "" {
			return [][]byte{errRes(req.ID, "BAD_PARAMS", "chat.send needs {text}")}
		}
		reply, err := b.run(text)
		if err != nil {
			return [][]byte{errRes(req.ID, "AGENT_FAILED", err.Error())}
		}
		b.mu.Lock()
		b.seq++
		sess := bridgeSession{
			ID:      fmt.Sprintf("s-%d", b.seq),
			Preview: preview(text, 80),
			Created: time.Now().UTC().Format(time.RFC3339),
		}
		b.sessions = append(b.sessions, sess)
		b.mu.Unlock()
		ev, _ := json.Marshal(eventFrame{Type: "event", Event: "session.message",
			Payload: map[string]any{"sessionId": sess.ID, "role": "assistant", "text": reply}})
		return [][]byte{okRes(req.ID, map[string]any{"reply": reply, "sessionId": sess.ID}), ev}
	case "sessions.list":
		if !st.authed {
			return [][]byte{errRes(req.ID, "UNAUTHENTICATED", "send connect first")}
		}
		b.mu.Lock()
		list := append([]bridgeSession{}, b.sessions...)
		b.mu.Unlock()
		if list == nil {
			list = []bridgeSession{}
		}
		return [][]byte{okRes(req.ID, map[string]any{"sessions": list})}
	default:
		return [][]byte{errRes(req.ID, "UNKNOWN_METHOD",
			fmt.Sprintf("method %q is not part of the Nimbus-One subset (connect, chat.send, sessions.list)", req.Method))}
	}
}

func (b *bridgeServer) handleConnect(st *connState, req reqFrame) []byte {
	// Protocol floor: we speak 4 and accept clients that overlap it.
	if p, ok := req.Params["maxProtocol"].(float64); ok && int(p) < 4 {
		return errRes(req.ID, "VERSION_MISMATCH", "server speaks protocol 4")
	}
	// Token auth when configured; open ("none") when no token was minted.
	method := "none"
	if b.token != "" {
		method = "token"
		got := ""
		if auth, ok := req.Params["auth"].(map[string]any); ok {
			got, _ = auth["token"].(string)
		}
		if tok, _ := req.Params["token"].(string); got == "" {
			got = tok
		}
		if got != b.token {
			return errRes(req.ID, "UNAUTHENTICATED", "bad token")
		}
	}
	st.authed = true
	return okRes(req.ID, map[string]any{
		"type":     "hello-ok",
		"protocol": 4,
		"server":   map[string]any{"version": "nimbus-min " + version, "connId": newToken()[:16]},
		"features": map[string]any{
			"methods": []string{"connect", "chat.send", "sessions.list"},
			"events":  []string{"session.message"},
		},
		"snapshot": map[string]any{},
		"auth":     map[string]any{"method": method, "role": "operator", "scopes": []string{"operator"}},
		"policy":   map[string]any{"maxPayload": 65536, "maxBufferedBytes": 1 << 20, "tickIntervalMs": 15000},
	})
}

func preview(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// --- HTTP + WebSocket transport (stdlib only) ---

// cmdServe starts the local app bridge: green control page + WS + health.
func cmdServe(addr string, stdout, stderr *os.File) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Workspace) != "" {
		if err := cfg.validate(); err != nil {
			return err
		}
	}
	b := newBridge(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, appHTML)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"product":"nimbus-min"}`)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWS(b, w, r)
	})
	fmt.Fprintln(stdout, "Nimbus-One app bridge at http://"+addr+" (control page + app WS /ws)")
	fmt.Fprintln(stdout, "Tip: leave this running, open the page, paste the token from your config.")
	return http.ListenAndServe(addr, mux)
}

// serveWS upgrades to RFC6455 (text frames only) and pumps frames.
func serveWS(b *bridgeServer, w http.ResponseWriter, r *http.Request) {
	if !isWSUpgrade(r) {
		http.Error(w, "websocket upgrade required", 426)
		return
	}
	key := r.Header.Get("Sec-Websocket-Key")
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", 500)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	accept := wsAccept(key)
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	_ = rw.Flush()
	st := &connState{}
	for {
		raw, opcode, err := wsRead(conn)
		if err != nil {
			return
		}
		if opcode == 0x8 {
			return // close
		}
		if opcode == 0x9 {
			_ = wsWrite(conn, 0xA, raw) // pong
			continue
		}
		if opcode != 0x1 {
			continue // binary/continuation unsupported
		}
		var req reqFrame
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = wsWrite(conn, 0x1, errRes("", "BAD_FRAME", "invalid JSON"))
			continue
		}
		for _, out := range b.handleFrame(st, req) {
			_ = wsWrite(conn, 0x1, out)
		}
	}
}

func isWSUpgrade(r *http.Request) bool {
	return headerHas(r.Header.Get("Connection"), "upgrade") &&
		strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		r.Header.Get("Sec-Websocket-Key") != ""
}

func headerHas(v, want string) bool {
	for _, part := range strings.Split(v, ",") {
		if strings.ToLower(strings.TrimSpace(part)) == want {
			return true
		}
	}
	return false
}

func wsAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// wsRead reads one client frame (masked text/ping/close).
func wsRead(conn net.Conn) (payload []byte, opcode byte, err error) {
	var hdr [2]byte
	if _, err = ioReadFull(conn, hdr[:]); err != nil {
		return nil, 0, err
	}
	opcode = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	length := int(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = ioReadFull(conn, ext[:]); err != nil {
			return nil, 0, err
		}
		length = int(ext[0])<<8 | int(ext[1])
	case 127:
		var ext [8]byte
		if _, err = ioReadFull(conn, ext[:]); err != nil {
			return nil, 0, err
		}
		length = 0
		for _, c := range ext {
			length = length<<8 | int(c)
		}
	}
	var mask [4]byte
	if masked {
		if _, err = ioReadFull(conn, mask[:]); err != nil {
			return nil, 0, err
		}
	}
	if length > 1<<20 {
		return nil, 0, fmt.Errorf("frame too large")
	}
	payload = make([]byte, length)
	if _, err = ioReadFull(conn, payload); err != nil {
		return nil, 0, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, opcode, nil
}

// wsWrite writes one server text frame (unmasked).
func wsWrite(conn net.Conn, opcode byte, payload []byte) error {
	var hdr []byte
	if len(payload) < 126 {
		hdr = []byte{0x80 | opcode, byte(len(payload))}
	} else if len(payload) < 65536 {
		hdr = []byte{0x80 | opcode, 126, byte(len(payload) >> 8), byte(len(payload))}
	} else {
		hdr = []byte{0x80 | opcode, 127, 0, 0, 0, 0,
			byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))}
	}
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func ioReadFull(conn net.Conn, b []byte) (int, error) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	n := 0
	for n < len(b) {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		m, err := conn.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// appHTML is the green control page (single file, no dependencies).
const appHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Nimbus-One</title>
<style>
:root { --green: #1d7a3a; --green-dark: #145c2b; --bg: #f2f7f3; --card: #ffffff; --ink: #1c2b21; }
* { box-sizing: border-box; }
body { margin: 0; font-family: system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--ink); }
header { background: var(--green); color: #fff; padding: 14px 20px; }
header h1 { margin: 0; font-size: 20px; }
header p { margin: 4px 0 0; opacity: .85; font-size: 13px; }
main { max-width: 720px; margin: 0 auto; padding: 16px; display: grid; gap: 12px; }
.card { background: var(--card); border: 1px solid #d7e5db; border-radius: 10px; padding: 14px; }
.row { display: flex; gap: 8px; }
input, button { font-size: 15px; padding: 9px 12px; border-radius: 8px; border: 1px solid #c4d8ca; }
input { flex: 1; }
button { background: var(--green); color: #fff; border: none; cursor: pointer; }
button:hover { background: var(--green-dark); }
button:disabled { opacity: .5; }
#status { font-size: 13px; color: #40604a; }
#log { display: flex; flex-direction: column; gap: 8px; max-height: 50vh; overflow-y: auto; }
.msg { padding: 8px 12px; border-radius: 8px; background: #eef5f0; white-space: pre-wrap; word-break: break-word; }
.msg.me { background: #dff0e3; align-self: flex-end; }
.msg small { display: block; opacity: .6; margin-top: 4px; }
#sessions { font-size: 13px; color: #40604a; }
.warn { background: #fff8e6; border: 1px solid #ecd88a; border-radius: 8px; padding: 8px 12px; font-size: 13px; }
</style>
</head>
<body>
<header><h1>Nimbus-One</h1><p>One workspace &middot; one model &middot; your approval for every shell command</p></header>
<main>
<div class="card">
<div class="row"><input id="token" type="password" placeholder="HTTP token (from setup)"><button id="connect">Connect</button></div>
<p id="status">Not connected.</p>
</div>
<div class="card"><div id="sessions">No sessions yet.</div></div>
<div class="card"><div id="log"></div></div>
<div class="card">
<div class="warn">Shell commands always pause for your typed approval in the terminal where the server runs. Nothing runs silently.</div>
<div class="row" style="margin-top:8px"><input id="text" placeholder="Ask Nimbus-One to do something with your files&hellip;"><button id="send">Send</button></div>
</div>
</main>
<script>
(function () {
  var ws = null, token = "";
  var status = document.getElementById("status");
  var log = document.getElementById("log");
  var sessions = document.getElementById("sessions");
  var seq = 0;
  function add(text, me) {
    var d = document.createElement("div");
    d.className = "msg" + (me ? " me" : "");
    d.textContent = text;
    log.appendChild(d);
    log.scrollTop = log.scrollHeight;
  }
  function send(obj) { ws.send(JSON.stringify(obj)); }
  document.getElementById("connect").onclick = function () {
    token = document.getElementById("token").value;
    ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/ws");
    ws.onopen = function () {
      send({ type: "req", id: "c" + (++seq), method: "connect", params: { minProtocol: 4, maxProtocol: 4, auth: { token: token } } });
    };
    ws.onmessage = function (ev) {
      var f;
      try { f = JSON.parse(ev.data); } catch (e) { return; }
      if (f.type === "res" && f.ok && f.payload && f.payload.type === "hello-ok") {
        status.textContent = "Connected — protocol 4, methods: " + f.payload.features.methods.join(", ");
        send({ type: "req", id: "s" + (++seq), method: "sessions.list", params: {} });
      } else if (f.type === "res" && !f.ok) {
        status.textContent = "Error: " + (f.error && f.error.message || "unknown");
      } else if (f.type === "res" && f.payload && f.payload.reply) {
        add(f.payload.reply, false);
      } else if (f.type === "res" && f.payload && f.payload.sessions) {
        sessions.textContent = f.payload.sessions.length ? f.payload.sessions.map(function (s) { return s.id + ": " + s.preview; }).join(" | ") : "No sessions yet.";
      } else if (f.type === "event" && f.event === "session.message") {
        add(f.payload.text, false);
      }
    };
    ws.onclose = function () { status.textContent = "Disconnected."; };
  };
  document.getElementById("send").onclick = function () {
    var t = document.getElementById("text");
    if (!t.value.trim() || !ws) return;
    add(t.value, true);
    send({ type: "req", id: "m" + (++seq), method: "chat.send", params: { text: t.value } });
    t.value = "";
  };
})();
</script>
</body>
</html>`
