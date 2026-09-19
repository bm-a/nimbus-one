package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAnthropic serves a scripted list of responses, then repeats the last.
type fakeAnthropic struct {
	t     *testing.T
	resps []messagesResponse
	calls int
}

func (f *fakeAnthropic) handler(w http.ResponseWriter, r *http.Request) {
	f.calls++
	if r.URL.Path != "/v1/messages" {
		http.Error(w, "bad path", 404)
		return
	}
	if r.Header.Get("anthropic-version") != anthropicVersion {
		http.Error(w, "bad version", 400)
		return
	}
	if r.Header.Get("x-api-key") == "" {
		http.Error(w, "missing key", 401)
		return
	}
	i := f.calls - 1
	if i >= len(f.resps) {
		i = len(f.resps) - 1
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(f.resps[i]); err != nil {
		f.t.Fatal(err)
	}
}

func textResp(text string) messagesResponse {
	return messagesResponse{Content: []contentBlock{{Type: "text", Text: text}}}
}

func toolResp(id, name string, input map[string]any, text string) messagesResponse {
	blocks := []contentBlock{}
	if text != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: text})
	}
	blocks = append(blocks, contentBlock{Type: "tool_use", ID: id, Name: name, Input: input})
	return messagesResponse{Content: blocks}
}

func testClient(t *testing.T, srv *httptest.Server) *anthropicClient {
	t.Helper()
	// Bypass the network guard for tests: the fake server is local HTTP.
	// (The guard itself is tested separately in netguard_test.go.)
	return &anthropicClient{http: srv.Client(), key: "test-key", base: srv.URL, model: "test"}
}

func TestLoopReadThenAnswer(t *testing.T) {
	fake := &fakeAnthropic{t: t, resps: []messagesResponse{
		toolResp("u1", "read", map[string]any{"path": "note.txt"}, ""),
		textResp("The note says hello."),
	}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	dir := testJail(t)
	writeFile(t, dir, "note.txt", "hello")
	out, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "what is in note.txt?", 5)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "The note says hello." {
		t.Fatalf("answer = %q", out)
	}
	if fake.calls != 2 {
		t.Fatalf("turns = %d, want 2", fake.calls)
	}
}

func TestLoopMultiToolErrorRecovery(t *testing.T) {
	fake := &fakeAnthropic{t: t, resps: []messagesResponse{
		toolResp("u1", "read", map[string]any{"path": "missing.txt"}, ""),
		toolResp("u2", "write", map[string]any{"path": "created.txt", "content": "made it"}, ""),
		textResp("Created the file."),
	}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	testJail(t)
	out, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "do the thing", 5)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "Created the file." {
		t.Fatalf("answer = %q", out)
	}
	if fake.calls != 3 {
		t.Fatalf("turns = %d, want 3 (error recovery)", fake.calls)
	}
}

func TestLoopUnknownToolAndMalformed(t *testing.T) {
	fake := &fakeAnthropic{t: t, resps: []messagesResponse{
		toolResp("u1", "teleport", map[string]any{}, ""),
		toolResp("u2", "", map[string]any{}, ""),
		textResp("Fine, answering directly."),
	}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	testJail(t)
	out, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "go", 5)
	if err != nil {
		t.Fatalf("loop must survive bad tool blocks: %v", err)
	}
	if out != "Fine, answering directly." {
		t.Fatalf("answer = %q", out)
	}
}

func TestLoopShellNeedsYes(t *testing.T) {
	fake := &fakeAnthropic{t: t, resps: []messagesResponse{
		toolResp("u1", "shell", map[string]any{"command": "echo hi"}, ""),
		textResp("done"),
	}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	testJail(t)
	// Headless: shell must be refused, loop continues to final answer.
	out, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "run echo", 5)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "done" {
		t.Fatalf("answer = %q", out)
	}
	// Interactive yes: shell executes.
	fake2 := &fakeAnthropic{t: t, resps: []messagesResponse{
		toolResp("u1", "shell", map[string]any{"command": "echo hi"}, ""),
		textResp("saw hi"),
	}}
	srv2 := httptest.NewServer(http.HandlerFunc(fake2.handler))
	defer srv2.Close()
	out, err = runAgent(testClient(t, srv2), strings.NewReader("yes\n"), &strings.Builder{}, true, "run echo", 5)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "saw hi" {
		t.Fatalf("answer = %q", out)
	}
}

func TestLoopStopsAtMaxTurns(t *testing.T) {
	// Model that never stops calling tools.
	fake := &fakeAnthropic{t: t, resps: []messagesResponse{
		toolResp("u1", "list", map[string]any{"path": "."}, ""),
	}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	testJail(t)
	if _, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "go", 3); err == nil {
		t.Fatal("infinite tool loop must stop at max turns")
	}
}

func TestLoopHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"type":"invalid_request","message":"bad key"}}`, 401)
	}))
	defer srv.Close()
	testJail(t)
	if _, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "hi", 5); err == nil {
		t.Fatal("401 must surface as error")
	}
}

func TestLoopEmptyAnswer(t *testing.T) {
	fake := &fakeAnthropic{t: t, resps: []messagesResponse{textResp("   ")}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()
	testJail(t)
	if _, err := runAgent(testClient(t, srv), strings.NewReader(""), &strings.Builder{}, false, "hi", 5); err == nil {
		t.Fatal("empty answer must error, not return blank")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := osWriteFile(dir, name, content); err != nil {
		t.Fatal(err)
	}
}
