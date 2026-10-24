package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/edxeme/keystone/internal/config"
)

// newOpenAIUpstream boots a fake OpenAI-compatible upstream.
func newOpenAIUpstream(t *testing.T, name string, status int, body string) config.Upstream {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return config.Upstream{Name: name, Provider: "openai", BaseURL: srv.URL}
}

func gatewayConfig(t *testing.T, upstreams []config.Upstream, rpm int) *config.Config {
	t.Helper()
	routes := make(map[string][]string)
	names := make([]string, 0, len(upstreams))
	for _, u := range upstreams {
		names = append(names, u.Name)
	}
	routes["gpt-4o"] = names
	return &config.Config{
		Listen:    ":0",
		APIKeys:   []string{"sk-test"},
		Upstreams: upstreams,
		Routes:    routes,
		RateLimit: config.RateLimit{RequestsPerMinute: rpm},
	}
}

func chatBody(model string, stream bool) string {
	return fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"stream":%t}`, model, stream)
}

// getUnauthenticated issues a plain GET without credentials.
func getUnauthenticated(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

// postChat sends one chat request through h and returns the response.
func postChat(t *testing.T, h http.Handler, key, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestGatewayAuthRequired(t *testing.T) {
	g, err := New(gatewayConfig(t, []config.Upstream{newOpenAIUpstream(t, "a", 200, `{}`)}, 0), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	resp := postChat(t, g.Handler(), "wrong-key", chatBody("gpt-4o", false))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGatewayUnknownModel(t *testing.T) {
	g, _ := New(gatewayConfig(t, []config.Upstream{newOpenAIUpstream(t, "a", 200, `{}`)}, 0), slog.Default())
	resp := postChat(t, g.Handler(), "sk-test", chatBody("nope", false))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGatewayProxiesAndRecordsUsage(t *testing.T) {
	u := newOpenAIUpstream(t, "openai", 200, `{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	g, _ := New(gatewayConfig(t, []config.Upstream{u}, 0), slog.Default())

	resp := postChat(t, g.Handler(), "sk-test", chatBody("gpt-4o", false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		ID    string `json:"id"`
		Usage *struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.ID != "c1" || out.Usage == nil || out.Usage.TotalTokens != 5 {
		t.Errorf("body = %+v", out)
	}

	snap := g.Usage().Snapshot()
	if len(snap) != 1 || snap[0].Requests != 1 || snap[0].PromptTokens != 3 || snap[0].CompletionTokens != 2 {
		t.Errorf("usage = %+v", snap)
	}
}

func TestGatewayFailsOverAcrossUpstreams(t *testing.T) {
	first := newOpenAIUpstream(t, "flaky", 500, `{"error":"boom"}`)
	second := newOpenAIUpstream(t, "stable", 200, `{"id":"c2","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`)
	g, _ := New(gatewayConfig(t, []config.Upstream{first, second}, 0), slog.Default())

	resp := postChat(t, g.Handler(), "sk-test", chatBody("gpt-4o", false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after failover", resp.StatusCode)
	}
	snap := g.Usage().Snapshot()
	if len(snap) != 2 {
		t.Fatalf("usage entries = %d, want 2", len(snap))
	}
	if snap[0].Upstream != "flaky" || snap[1].Upstream != "stable" {
		t.Errorf("upstream names = %+v", snap)
	}
	if snap[0].Errors != 1 {
		t.Errorf("failed upstream not recorded as error: %+v", snap[0])
	}
}

func TestGatewayRateLimit(t *testing.T) {
	u := newOpenAIUpstream(t, "openai", 200, `{"id":"c3","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"x"}}]}`)
	g, _ := New(gatewayConfig(t, []config.Upstream{u}, 2), slog.Default()) // burst of 2

	if resp := postChat(t, g.Handler(), "sk-test", chatBody("gpt-4o", false)); resp.StatusCode != 200 {
		t.Fatalf("first request status = %d, want 200", resp.StatusCode)
	}
	if resp := postChat(t, g.Handler(), "sk-test", chatBody("gpt-4o", false)); resp.StatusCode != 200 {
		t.Fatalf("second request status = %d, want 200", resp.StatusCode)
	}
	if resp := postChat(t, g.Handler(), "sk-test", chatBody("gpt-4o", false)); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("third request status = %d, want 429", resp.StatusCode)
	}
}

func TestGatewayStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, data := range []string{
			`{"id":"s1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`{"id":"s1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			`{"id":"s1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		} {
			w.Write([]byte("data: " + data + "\n\n"))
			f.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	t.Cleanup(srv.Close)

	cfg := gatewayConfig(t, []config.Upstream{{Name: "stream", Provider: "openai", BaseURL: srv.URL}}, 0)
	g, _ := New(cfg, slog.Default())

	resp := postChat(t, g.Handler(), "sk-test", chatBody("gpt-4o", true))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	for _, want := range []string{`"delta":{"role":"assistant"`, `"delta":{"content":"hi"}`, `"finish_reason":"stop"`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream body missing %q\n%s", want, body)
		}
	}
}

func TestModelsAndMetricsEndpoints(t *testing.T) {
	g, _ := New(gatewayConfig(t, []config.Upstream{newOpenAIUpstream(t, "openai", 200, `{}`)}, 0), slog.Default())
	h := g.Handler()
	get := func(path string) *http.Response {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Result()
	}

	var list struct {
		Data []struct{ ID string }
	}
	json.NewDecoder(get("/v1/models").Body).Decode(&list)
	if len(list.Data) != 1 || list.Data[0].ID != "gpt-4o" {
		t.Errorf("models = %+v", list.Data)
	}

	if code := get("/healthz").StatusCode; code != 200 {
		t.Errorf("healthz status = %d", code)
	}

	// Liveness probes carry no credentials.
	if code := getUnauthenticated(t, h, "/healthz").StatusCode; code != 200 {
		t.Errorf("unauthenticated healthz status = %d, want 200", code)
	}
	if code := getUnauthenticated(t, h, "/v1/models").StatusCode; code != http.StatusUnauthorized {
		t.Errorf("unauthenticated models status = %d, want 401", code)
	}

	if ct := get("/metrics").Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Error("metrics content-type is not text/plain")
	}
}
