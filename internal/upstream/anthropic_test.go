package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/edxeme/keystone/internal/config"
)

func configUpstream(provider, name string) config.Upstream {
	if name == "" {
		name = provider
	}
	return config.Upstream{Name: name, Provider: provider}
}

func TestAnthropicCompleteTranslates(t *testing.T) {
	var gotPath, gotKey, gotVersion, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Write([]byte(`{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hey"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer srv.Close()

	u := &Anthropic{name: "anthropic", baseURL: srv.URL, apiKey: "k", hc: srv.Client()}
	resp, err := u.Complete(context.Background(), userRequest(false))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if gotPath != "/v1/messages" || gotKey != "k" || gotVersion != "2023-06-01" {
		t.Errorf("path=%q key=%q version=%q", gotPath, gotKey, gotVersion)
	}
	if !strings.Contains(gotBody, `"max_tokens":4096`) {
		t.Errorf("request body missing default max_tokens: %s", gotBody)
	}
	text, _ := resp.Choices[0].Message.TextContent()
	if text != "hey" {
		t.Errorf("content = %q, want hey", text)
	}
	if resp.Usage.PromptTokens != 2 || resp.Usage.CompletionTokens != 1 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestAnthropicStreamTranslatesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("streaming request did not set stream:true after translation")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, ev := range []struct{ event, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"m2","model":"claude-sonnet-4-5"}}`},
			{"content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hey"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			w.Write([]byte("event: " + ev.event + "\ndata: " + ev.data + "\n\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	u := &Anthropic{name: "anthropic", baseURL: srv.URL, hc: srv.Client()}
	stream, err := u.Stream(context.Background(), userRequest(true))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	var texts []string
	var finish string
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if chunk.Choices[0].FinishReason != nil {
			finish = *chunk.Choices[0].FinishReason
			continue
		}
		text, _ := chunk.Choices[0].Delta.TextContent()
		if text != "" {
			texts = append(texts, text)
		}
	}
	if strings.Join(texts, "") != "hey" {
		t.Errorf("texts = %v, want hey", texts)
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
}

func TestFactoryBuildsProviders(t *testing.T) {
	u, err := New(configUpstream("openai", ""), "key-1")
	if err != nil {
		t.Fatalf("New(openai) error = %v", err)
	}
	if u.Name() != "openai" {
		t.Errorf("Name = %q", u.Name())
	}
	if _, err := New(configUpstream("ollama", ""), ""); err == nil {
		t.Error("New(unknown provider) succeeded, want error")
	}
}
