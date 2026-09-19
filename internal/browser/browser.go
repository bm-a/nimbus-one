// Package browser drives an EXTERNAL Chromium over the Chrome DevTools
// Protocol (CDP). Nothing is bundled: start Chrome yourself, e.g.
//
//	chrome --headless=new --remote-debugging-port=9222 --no-sandbox
//
// then point CDPClient.URL at http://127.0.0.1:9222. Without a running
// Chrome every action returns an explicit error (never a silent no-op).
//
// OpenClaw reference (read-only):
//
//	extensions/browser (22 CDP actions), plugin-sdk/browser-bridge,
//	computer-tool-node.ts (screenshots/coords). Nimbus port: stdlib only —
//	GET /json/list for target discovery plus a compact RFC6455 websocket
//	dial (net + crypto/tls + crypto/sha1, masked text frames) for
//	Page.navigate / Runtime.evaluate / Page.captureScreenshot.
package browser

import (
	"bufio"
	"context"
	crand "crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// wsMagic is the RFC6455 GUID used for Sec-WebSocket-Accept.
const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsMaxFrame caps a single frame payload.
const wsMaxFrame = 16 << 20 // 16 MiB

// Websocket frame opcodes (RFC6455).
const (
	wsOpCont   = 0x0
	wsOpText   = 0x1
	wsOpBinary = 0x2
	wsOpClose  = 0x8
	wsOpPing   = 0x9
	wsOpPong   = 0xA
)

// DefaultCDPTimeout bounds one CDP action when CDPClient.Timeout is unset.
const DefaultCDPTimeout = 30 * time.Second

// CDPClient drives one Chrome debugger endpoint.
type CDPClient struct {
	// URL is the debugger base, e.g. http://127.0.0.1:9222.
	URL string
	// Timeout bounds HTTP discovery and each CDP round trip.
	Timeout time.Duration

	mu     sync.Mutex
	nextID int
}

// Target is one entry of GET /json/list.
type Target struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func (c *CDPClient) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultCDPTimeout
}

// Targets lists debuggable targets from /json/list.
func (c *CDPClient) Targets(ctx context.Context) ([]Target, error) {
	base := strings.TrimSuffix(strings.TrimSpace(c.URL), "/")
	if base == "" {
		return nil, fmt.Errorf("browser: empty debugger URL (start chrome with --remote-debugging-port=9222)")
	}
	httpClient := &http.Client{Timeout: c.timeout()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/list", nil)
	if err != nil {
		return nil, fmt.Errorf("browser: targets request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("browser: GET /json/list: %w (is Chrome running with --remote-debugging-port?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("browser: GET /json/list: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("browser: read targets: %w", err)
	}
	var targets []Target
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("browser: decode targets: %w", err)
	}
	return targets, nil
}

func pickPage(targets []Target) (Target, error) {
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t, nil
		}
	}
	for _, t := range targets {
		if t.WebSocketDebuggerURL != "" {
			return t, nil
		}
	}
	return Target{}, fmt.Errorf("browser: no debuggable page (open a tab in the remote-debugged Chrome first)")
}

// Navigate loads url in the first page target.
func (c *CDPClient) Navigate(ctx context.Context, pageURL string) (string, error) {
	if strings.TrimSpace(pageURL) == "" {
		return "", fmt.Errorf("browser: empty navigate url")
	}
	if _, err := c.call(ctx, "Page.navigate", map[string]any{"url": pageURL}); err != nil {
		return "", err
	}
	return "navigated to " + pageURL, nil
}

// Eval runs js in the page and returns the value (strings verbatim,
// other JSON values re-encoded).
func (c *CDPClient) Eval(ctx context.Context, js string) (string, error) {
	if strings.TrimSpace(js) == "" {
		return "", fmt.Errorf("browser: empty eval expression")
	}
	raw, err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": js, "returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("browser: decode evaluate result: %w", err)
	}
	if res.ExceptionDetails != nil {
		return "", fmt.Errorf("browser: page threw: %v", res.ExceptionDetails)
	}
	if res.Result.Type == "string" {
		var s string
		if err := json.Unmarshal(res.Result.Value, &s); err != nil {
			return "", fmt.Errorf("browser: decode string value: %w", err)
		}
		return s, nil
	}
	if len(res.Result.Value) == 0 {
		return "undefined", nil
	}
	return string(res.Result.Value), nil
}

// Snapshot returns a compact DOM snapshot (title, url, body text ≤8000 chars).
func (c *CDPClient) Snapshot(ctx context.Context) (string, error) {
	const expr = `JSON.stringify({title:document.title,url:location.href,text:(document.body&&document.body.innerText||'').slice(0,8000)})`
	raw, err := c.Eval(ctx, expr)
	if err != nil {
		return "", err
	}
	var snap struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return raw, nil // non-JSON pages: return verbatim
	}
	var b strings.Builder
	b.WriteString(snap.Title + "\n" + snap.URL + "\n" + snap.Text)
	return b.String(), nil
}

// Screenshot captures the page as PNG bytes.
func (c *CDPClient) Screenshot(ctx context.Context) ([]byte, error) {
	raw, err := c.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		return nil, err
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("browser: decode screenshot: %w", err)
	}
	if res.Data == "" {
		return nil, fmt.Errorf("browser: empty screenshot data")
	}
	data, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		return nil, fmt.Errorf("browser: decode screenshot base64: %w", err)
	}
	return data, nil
}

// call connects to the page socket, sends one command and waits for the
// response with the matching id (protocol events are skipped).
func (c *CDPClient) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	timeout := c.timeout()
	cctx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	targets, err := c.Targets(cctx)
	if err != nil {
		return nil, err
	}
	page, err := pickPage(targets)
	if err != nil {
		return nil, err
	}
	conn, err := dialWS(cctx, page.WebSocketDebuggerURL)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if dl, ok := deadlineOf(cctx, timeout); ok {
		_ = conn.conn.SetDeadline(dl)
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, fmt.Errorf("browser: marshal %s: %w", method, err)
	}
	if err := conn.writeText(payload); err != nil {
		return nil, fmt.Errorf("browser: send %s: %w", method, err)
	}
	for {
		if err := cctx.Err(); err != nil {
			return nil, err
		}
		_, frame, err := conn.readMessage()
		if err != nil {
			return nil, fmt.Errorf("browser: read %s: %w", method, err)
		}
		var msg struct {
			ID     any             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(frame, &msg); err != nil {
			continue // non-JSON frame: ignore
		}
		if !matchIDAny(msg.ID, id) {
			continue // protocol event or another session's reply
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("browser: %s: %s (code %d)", method, msg.Error.Message, msg.Error.Code)
		}
		return msg.Result, nil
	}
}

func matchIDAny(got any, want int) bool {
	switch v := got.(type) {
	case nil:
		return false
	case float64:
		return int(v) == want
	case int:
		return v == want
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n) == want
		}
		return false
	default:
		return false
	}
}

func deadlineOf(ctx context.Context, timeout time.Duration) (time.Time, bool) {
	if dl, ok := ctx.Deadline(); ok {
		return dl, true
	}
	return time.Now().Add(timeout), true
}

// --- minimal RFC6455 client ---

type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
	wmu  sync.Mutex
}

func (w *wsConn) close() {
	// Best-effort close frame, then hard close.
	_ = w.writeFrame(wsOpClose, []byte{})
	_ = w.conn.Close()
}

func (w *wsConn) writeText(payload []byte) error {
	return w.writeFrame(wsOpText, payload)
}

// writeFrame sends one masked client frame.
func (w *wsConn) writeFrame(opcode int, payload []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	var hdr []byte
	hdr = append(hdr, 0x80|byte(opcode))
	switch {
	case len(payload) < 126:
		hdr = append(hdr, 0x80|byte(len(payload)))
	case len(payload) < 65536:
		hdr = append(hdr, 0x80|126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		hdr = append(hdr, ext[:]...)
	default:
		hdr = append(hdr, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		hdr = append(hdr, ext[:]...)
	}
	var mask [4]byte
	if _, err := crand.Read(mask[:]); err != nil {
		return err
	}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := w.conn.Write(hdr); err != nil {
		return err
	}
	if len(masked) > 0 {
		if _, err := w.conn.Write(masked); err != nil {
			return err
		}
	}
	return nil
}

// readMessage returns the next complete data message, answering pings and
// reassembling continuations. Close frames surface as errors.
func (w *wsConn) readMessage() (int, []byte, error) {
	var opcode int
	var buf []byte
	first := true
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(w.r, hdr); err != nil {
			return 0, nil, err
		}
		fin := hdr[0]&0x80 != 0
		op := int(hdr[0] & 0x0F)
		masked := hdr[1]&0x80 != 0
		length := int(hdr[1] & 0x7F)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(w.r, ext[:]); err != nil {
				return 0, nil, err
			}
			length = int(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(w.r, ext[:]); err != nil {
				return 0, nil, err
			}
			n := binary.BigEndian.Uint64(ext[:])
			if n > wsMaxFrame {
				return 0, nil, fmt.Errorf("browser: frame too large (%d bytes)", n)
			}
			length = int(n)
		}
		if length > wsMaxFrame {
			return 0, nil, fmt.Errorf("browser: frame too large (%d bytes)", length)
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return 0, nil, err
			}
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case wsOpPing:
			// Answer with an unmasked... clients MUST mask: use writeFrame.
			if err := w.writeFrame(wsOpPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case wsOpPong:
			continue
		case wsOpClose:
			return 0, nil, fmt.Errorf("browser: websocket closed by peer")
		case wsOpCont:
			if first {
				return 0, nil, fmt.Errorf("browser: stray continuation frame")
			}
			buf = append(buf, payload...)
			if fin {
				return opcode, buf, nil
			}
		case wsOpText, wsOpBinary:
			if !first {
				return 0, nil, fmt.Errorf("browser: interleaved data frame")
			}
			first, opcode, buf = false, op, append(buf, payload...)
			if fin {
				return opcode, buf, nil
			}
		default:
			return 0, nil, fmt.Errorf("browser: unknown opcode %d", op)
		}
	}
}

// dialWS performs the RFC6455 opening handshake.
func dialWS(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("browser: bad debugger ws url: %w", err)
	}
	secure := false
	switch strings.ToLower(u.Scheme) {
	case "ws":
	case "wss":
		secure = true
	default:
		return nil, fmt.Errorf("browser: unsupported debugger scheme %q", u.Scheme)
	}
	host := u.Host
	if host == "" {
		return nil, fmt.Errorf("browser: debugger ws url missing host")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		if secure {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("browser: dial debugger: %w", err)
	}
	if secure {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: u.Hostname()})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("browser: tls handshake: %w", err)
		}
		conn = tlsConn
	}
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	var keyBytes [16]byte
	if _, err := crand.Read(keyBytes[:]); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("browser: rand: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes[:])
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("browser: handshake write: %w", err)
	}
	r := bufio.NewReader(conn)
	status, err := r.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("browser: handshake read: %w", err)
	}
	if !strings.Contains(status, " 101 ") {
		_ = conn.Close()
		return nil, fmt.Errorf("browser: handshake rejected: %s", strings.TrimSpace(status))
	}
	accept := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("browser: handshake headers: %w", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if i := strings.Index(line, ":"); i >= 0 {
			if strings.EqualFold(strings.TrimSpace(line[:i]), "Sec-WebSocket-Accept") {
				accept = strings.TrimSpace(line[i+1:])
			}
		}
	}
	sum := sha1.Sum([]byte(key + wsMagic))
	if want := base64.StdEncoding.EncodeToString(sum[:]); accept != want {
		_ = conn.Close()
		return nil, fmt.Errorf("browser: bad Sec-WebSocket-Accept")
	}
	return &wsConn{conn: conn, r: r}, nil
}
