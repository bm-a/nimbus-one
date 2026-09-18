// Package gateway — web console + SSE streaming (RegisterWebUI).
//
// Wiring (http.go is intentionally untouched):
//
//	Handler() builds its own mux for /healthz, /api/v1/chat, /api/v1/task.
//	RegisterWebUI only adds "/" (console) and "/api/v1/stream" (SSE) to a
//	mux YOU own, complementing Handler() rather than duplicating it.
//	main.go must call it and mount the JSON API alongside, e.g.:
//
//		api := s.Handler() // /healthz, /api/v1/chat, /api/v1/task
//		mux := http.NewServeMux()
//		mux.HandleFunc("/healthz", api.ServeHTTP)
//		mux.HandleFunc("/api/v1/chat", api.ServeHTTP)
//		mux.HandleFunc("/api/v1/task", api.ServeHTTP)
//		RegisterWebUI(mux, s) // adds "/" + "/api/v1/stream" with CORS+logging
//		http.ListenAndServe(s.Addr, mux)
//
// Auth model: the console page (GET /) is always served with 200 and is
// byte-identical whether or not Server.Token is set — it never leaks the
// token or whether one is configured. The page contains a token field; API
// calls (POST /api/v1/chat, GET /api/v1/stream) enforce auth. Because
// EventSource cannot set headers, /api/v1/stream accepts EITHER
// "Authorization: Bearer <token>" OR "?token=<token>".
//
// Streaming honesty: /api/v1/stream runs Broker.Handle (one full engine
// Run) in a goroutine and emits a single "delta" event plus a "done"
// event. It is NOT token-by-token streaming; true token streaming needs
// provider/engine support that Broker does not expose.
package gateway

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ConsoleHTML is a single self-contained mobile-first page (<14KB).
// Vanilla JS, no CDNs (offline/LAN friendly). Title defaults to
// "Nimbus One" and is overridden client-side from ?title=.
// Token is persisted in localStorage and never logged (no console.log of
// the token anywhere). Without JS a <form> fallback note is shown.
const ConsoleHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>Nimbus One</title>
<style>
:root{color-scheme:dark light}
*{box-sizing:border-box}
body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:#0f1419;color:#e6e6e6;display:flex;flex-direction:column;min-height:100dvh}
header{padding:.6rem .8rem;border-bottom:1px solid #2a343d;display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}
h1{font-size:1rem;margin:0;flex:1 1 auto}
#msgs{flex:1;overflow-y:auto;padding:.8rem;display:flex;flex-direction:column;gap:.5rem;max-height:60vh;min-height:40vh}
.msg{max-width:85%;padding:.5rem .7rem;border-radius:.6rem;white-space:pre-wrap;word-break:break-word;font-size:.95rem}
.me{align-self:flex-end;background:#1d6fdc}
.them{align-self:flex-start;background:#222b33}
#bar{display:flex;gap:.5rem;padding:.6rem;border-top:1px solid #2a343d}
input,button{font:inherit;padding:.55rem .6rem;border-radius:.5rem;border:1px solid #3a4650;background:#161c22;color:inherit}
#tok{width:100%}
#inp{flex:1}
button{background:#1d6fdc;border-color:#1d6fdc;cursor:pointer;white-space:nowrap}
.hint{font-size:.75rem;color:#9aa7b2;padding:0 .8rem .6rem}
</style>
</head>
<body>
<header><h1 id="h">Nimbus One</h1></header>
<div style="padding:.6rem .8rem 0"><input id="tok" type="password" placeholder="Bearer token (optional)" autocomplete="off"></div>
<main id="msgs" aria-live="polite"></main>
<noscript><p style="padding:1rem">This console needs JavaScript for live chat. Fallback: POST JSON to <code>/api/v1/chat</code> with an <code>Authorization: Bearer &lt;token&gt;</code> header. A plain <form method="POST" action="/api/v1/chat">form post</form> will not return JSON without JS — use curl instead.</p></noscript>
<form id="bar"><input id="inp" placeholder="Type a message" autocomplete="off"><button type="button" id="mic" title="Voice input">🎤</button><button type="submit">Send</button></form>
<p class="hint">LAN/offline friendly: no CDNs, vanilla JS. Token stays in your browser (localStorage).</p>
<script>
(function(){
var h=document.getElementById("h"),box=document.getElementById("msgs"),
inp=document.getElementById("inp"),tok=document.getElementById("tok"),
bar=document.getElementById("bar");
try{var q=new URLSearchParams(location.search),t=q.get("title");if(t){document.title=t;h.textContent=t;}}catch(e){}
try{tok.value=localStorage.getItem("nimbus_token")||"";}catch(e){}
tok.addEventListener("input",function(){try{localStorage.setItem("nimbus_token",tok.value);}catch(e){}});
function add(txt,me){var d=document.createElement("div");d.className="msg "+(me?"me":"them");if(!me){var sp=document.createElement("button");sp.textContent="🔊";sp.title="Read aloud";sp.style.marginLeft=".4rem";sp.onclick=function(){try{speechSynthesis.cancel();speechSynthesis.speak(new SpeechSynthesisUtterance(txt));}catch(e){}};d.appendChild(sp);}var t=document.createElement("span");t.textContent=txt;d.appendChild(t);box.appendChild(d);box.scrollTop=box.scrollHeight;return d;}
function authHd(extra){var hd=extra||{};if(tok.value)hd["Authorization"]="Bearer "+tok.value;return hd;}
var mic=document.getElementById("mic"),rec=null;
mic.onclick=function(){try{
if(rec&&rec.state==="recording"){rec.stop();mic.textContent="🎤";return;}
var chunks=[];var stream;var startRec=function(s){rec=new MediaRecorder(s);rec.ondataavailable=function(e){if(e.data.size)chunks.push(e.data);};rec.onstop=function(){s.getTracks().forEach(function(tr){tr.stop();});mic.textContent="🎤";var blob=new Blob(chunks,{type:rec.mimeType||"audio/ogg"});var fd=new FormData();fd.append("audio",blob,"voice.ogg");add("(transcribing…)",true);fetch("/api/v1/transcribe",{method:"POST",headers:authHd(),body:fd}).then(function(r){if(!r.ok)throw new Error("HTTP "+r.status+" — transcription unconfigured?");return r.json();}).then(function(j){inp.value=j.text||"";box.lastChild.textContent="(heard) "+inp.value;}).catch(function(err){box.lastChild.textContent="mic error: "+err.message;});};rec.start();mic.textContent="⏹";};
if(navigator.mediaDevices&&navigator.mediaDevices.getUserMedia){navigator.mediaDevices.getUserMedia({audio:true}).then(startRec).catch(function(e){mic.textContent="🎤";add("mic blocked: "+e.message,false);});}else{add("mic unsupported in this browser",false);}
}catch(e){add("mic error: "+e.message,false);}};
function setBody(d,txt){var s=d.querySelector("span");if(s){s.textContent=txt;}else{d.textContent=txt;}}
bar.addEventListener("submit",function(e){e.preventDefault();var m=inp.value;if(!m.trim())return;add(m,true);inp.value="";var b=add("…",false);var hd={"Content-Type":"application/json"};if(tok.value)hd["Authorization"]="Bearer "+tok.value;fetch("/api/v1/chat",{method:"POST",headers:hd,body:JSON.stringify({message:m})}).then(function(r){if(!r.ok)throw new Error("HTTP "+r.status);return r.json();}).then(function(j){setBody(b,j.reply||"(empty)");}).catch(function(err){setBody(b,"error: "+err.message);});box.scrollTop=box.scrollHeight;});
})();
</script>
</body>
</html>`

// ---- CORS origins (package-level; Server struct in http.go is untouched) ----

var (
	corsMu      sync.RWMutex
	corsOrigins []string
)

// SetCORSOrigins replaces the allowed CORS origins consulted by withCORS.
// Empty (default) means "reflect *": same-origin friendly, preflight still
// answers with Access-Control-Allow-Origin: *. Pass []string{"*"} for fully
// open, or explicit origins (e.g. http://lan-host:8080) to lock down.
// Never includes tokens; origins only.
func SetCORSOrigins(origins []string) {
	corsMu.Lock()
	defer corsMu.Unlock()
	corsOrigins = append([]string(nil), origins...)
}

func getCORSOrigins() []string {
	corsMu.RLock()
	defer corsMu.RUnlock()
	return append([]string(nil), corsOrigins...)
}

// withCORS is Server middleware allowing configured origins. It always sets
// Allow-Methods/Allow-Headers and answers OPTIONS preflights with 204 so
// browsers on the LAN can call the API. No bodies or tokens are logged here.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := getCORSOrigins()
		allowOrigin := ""
		if len(allowed) == 0 {
			allowOrigin = "*"
		} else {
			for _, a := range allowed {
				if a == "*" {
					allowOrigin = "*"
					break
				}
				if origin != "" && a == origin {
					allowOrigin = origin
					break
				}
			}
		}
		if allowOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			if allowOrigin != "*" {
				w.Header().Set("Vary", "Origin")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (sr *statusRecorder) WriteHeader(c int) {
	sr.code = c
	sr.ResponseWriter.WriteHeader(c)
}

// Flush forwards to the underlying writer so SSE handlers wrapped in
// logRequest still satisfy http.Flusher (httptest.ResponseRecorder does).
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequest is Server middleware logging "METHOD path status ms".
// It logs r.URL.Path only (never RawQuery, headers, or bodies) so a
// ?token= credential can never appear in logs.
func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sr, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sr.code, time.Since(start).Truncate(time.Millisecond))
	})
}

// handleConsole serves GET / as HTML. The response is identical whether or
// not Server.Token is set (no auth gate, no token material in the body), so
// it never leaks whether a token is configured. Viewing without a token
// simply shows the page with its token prompt; API calls still require auth.
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write([]byte(ConsoleHTML))
}

// checkStreamAuth enforces Bearer auth like checkAuth, but also accepts
// ?token= because EventSource cannot set request headers. 401 otherwise.
func (s *Server) checkStreamAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") && strings.TrimPrefix(h, "Bearer ") == s.Token {
		return true
	}
	if tok := r.URL.Query().Get("token"); tok != "" && tok == s.Token {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// handleStream serves GET /api/v1/stream?message=...&user=... as SSE.
// Emits "data: {delta|done,reply}" lines: one delta event carrying the full
// reply plus a done event. Honest non-token streaming — true token
// streaming needs provider/engine support that Broker.Handle lacks.
// A ": heartbeat" comment is sent every 15s while the engine runs, and
// client disconnect (request context cancel) aborts the wait.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkStreamAuth(w, r) {
		return
	}
	msg := strings.TrimSpace(r.URL.Query().Get("message"))
	if msg == "" {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}
	user := r.URL.Query().Get("user")
	if user == "" {
		user = "http"
	}
	if s.Broker == nil {
		http.Error(w, "no broker", http.StatusServiceUnavailable)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()

	type result struct{ reply string }
	ch := make(chan result, 1)
	go func() {
		reply := s.Broker.Handle("http", user, msg)
		select {
		case ch <- result{reply: reply}:
		case <-r.Context().Done():
		}
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case res := <-ch:
			delta, _ := json.Marshal(map[string]string{"delta": res.reply})
			_, _ = w.Write([]byte("data: " + string(delta) + "\n\n"))
			fl.Flush()
			done, _ := json.Marshal(map[string]any{"done": true, "reply": res.reply})
			_, _ = w.Write([]byte("data: " + string(done) + "\n\n"))
			fl.Flush()
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
		}
	}
}

// RegisterWebUI attaches the console + SSE routes to mux with CORS and
// access logging. It complements Server.Handler (which owns /healthz,
// /api/v1/chat, /api/v1/task) and must be called by main.go (see package
// comment for wiring). Logging records method+path+status+latency only —
// never query strings, headers, or bodies, so tokens are never logged.
func RegisterWebUI(mux *http.ServeMux, s *Server) {
	mux.Handle("/", s.logRequest(s.withCORS(http.HandlerFunc(s.handleConsole))))
	mux.Handle("/api/v1/stream", s.logRequest(s.withCORS(http.HandlerFunc(s.handleStream))))
}
