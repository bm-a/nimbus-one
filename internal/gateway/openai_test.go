// Tests for openai_compat.go. Reuses stubEngine from gateway_test.go
// (same package) and exercises the routes through httptest.
package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func openAITestServer(s *Server) *httptest.Server {
	mux := http.NewServeMux()
	RegisterOpenAI(mux, s)
	return httptest.NewServer(mux)
}

func TestOpenAIModelsList(t *testing.T) {
	SetOpenAIModels([]string{"test-model-a", "test-model-b"})
	defer SetOpenAIModels(nil)
	s := &Server{Broker: New(&stubEngine{reply: "ok"}, "")}
	ts := openAITestServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) != 2 {
		t.Fatalf("body = %+v, want list of 2", body)
	}
	if body.Data[0].ID != "test-model-a" || body.Data[0].Object != "model" {
		t.Fatalf("data[0] = %+v", body.Data[0])
	}
}

func TestOpenAIModelsDefault(t *testing.T) {
	SetOpenAIModels(nil)
	s := &Server{Broker: New(&stubEngine{reply: "ok"}, "")}
	ts := openAITestServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "nimbus-one") {
		t.Fatalf("default models = %s, want nimbus-one fallback", raw)
	}
}

func TestOpenAIChatCompletion(t *testing.T) {
	SetOpenAIModels([]string{"m"})
	defer SetOpenAIModels(nil)
	eng := &stubEngine{reply: "hi there"}
	s := &Server{Broker: New(eng, "sys")}
	ts := openAITestServer(s)
	defer ts.Close()

	reqBody := `{"model":"m","messages":[{"role":"system","content":"be nice"},{"role":"user","content":"hello"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var body struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "chat.completion" || body.Model != "m" {
		t.Fatalf("envelope = %+v", body)
	}
	if len(body.Choices) != 1 || body.Choices[0].Message.Content != "hi there" {
		t.Fatalf("choices = %+v", body.Choices)
	}
	if body.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q", body.Choices[0].FinishReason)
	}
	if body.Usage.PromptTokens <= 0 || body.Usage.CompletionTokens <= 0 {
		t.Fatalf("usage = %+v, want positive len/4 estimates", body.Usage)
	}
	if body.Usage.TotalTokens != body.Usage.PromptTokens+body.Usage.CompletionTokens {
		t.Fatalf("usage total mismatch: %+v", body.Usage)
	}
	// The transcript (both turns) must reach the engine.
	if !strings.Contains(eng.last, "hello") || !strings.Contains(eng.last, "be nice") {
		t.Fatalf("engine prompt = %q, want full transcript", eng.last)
	}
}

func TestOpenAIChatMultipartContent(t *testing.T) {
	eng := &stubEngine{reply: "r"}
	s := &Server{Broker: New(eng, "sys")}
	ts := openAITestServer(s)
	defer ts.Close()

	reqBody := `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hel"},{"type":"text","text":"lo"}]}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(eng.last, "hello") {
		t.Fatalf("engine prompt = %q, want concatenated parts", eng.last)
	}
}

func TestOpenAIChatStreamRejected(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "r"}, "")}
	ts := openAITestServer(s)
	defer ts.Close()

	reqBody := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for stream=true", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "streaming is not supported") || !strings.Contains(string(raw), "stream") {
		t.Fatalf("body = %s, want honest retry hint", raw)
	}
}

func TestOpenAIChatAuth(t *testing.T) {
	s := &Server{Token: "secret", Broker: New(&stubEngine{reply: "r"}, "")}
	ts := openAITestServer(s)
	defer ts.Close()

	reqBody := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", resp2.StatusCode)
	}

	resp3, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("models no-token status = %d, want 401", resp3.StatusCode)
	}
}

func TestOpenAIChatBadInput(t *testing.T) {
	s := &Server{Broker: New(&stubEngine{reply: "r"}, "")}
	ts := openAITestServer(s)
	defer ts.Close()

	for name, body := range map[string]string{
		"bad json":    `{broken`,
		"no messages": `{"model":"m","messages":[]}`,
		"empty text":  `{"model":"m","messages":[{"role":"user","content":""}]}`,
	} {
		resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, resp.StatusCode)
		}
		if !strings.Contains(string(raw), "invalid_request_error") {
			t.Fatalf("%s: body = %s, want OpenAI error shape", name, raw)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if estimateTokens("") != 0 {
		t.Fatal("empty string must be 0")
	}
	if estimateTokens("abcdefgh") != 2 {
		t.Fatalf("8 chars = %d, want 2", estimateTokens("abcdefgh"))
	}
	if estimateTokens("a") != 1 {
		t.Fatal("short strings floor at 1, never 0")
	}
}
