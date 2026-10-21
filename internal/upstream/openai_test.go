package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/edxeme/keystone/internal/chat"
)

func userRequest(stream bool) *chat.ChatRequest {
	msg, _ := chat.MarshalText("hi")
	return &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.Message{{Role: "user", Content: msg}},
		Stream:   stream,
	}
}

func TestOpenAIComplete(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		var req chat.ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("non-streaming request carried stream:true")
		}
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hey"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer srv.Close()

	u := &OpenAI{name: "openai", baseURL: srv.URL, apiKey: "k", hc: srv.Client()}
	resp, err := u.Complete(context.Background(), userRequest(false))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer k" {
		t.Errorf("path=%q auth=%q", gotPath, gotAuth)
	}
	text, _ := resp.Choices[0].Message.TextContent()
	if text != "hey" || resp.Usage.TotalTokens != 3 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestOpenAIStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, line := range []string{
			`data: {"id":"c2","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			``,
			`data: {"id":"c2","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hey"}}]}`,
			``,
			`: keep-alive`,
			`data: {"id":"c2","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: [DONE]`,
			``,
		} {
			w.Write([]byte(line + "\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	u := &OpenAI{name: "openai", baseURL: srv.URL, hc: srv.Client()}
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

func TestOpenAINon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv.Close()

	u := &OpenAI{name: "openai", baseURL: srv.URL, hc: srv.Client()}
	_, err := u.Complete(context.Background(), userRequest(false))
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Errorf("error = %v, want status 429", err)
	}
}
