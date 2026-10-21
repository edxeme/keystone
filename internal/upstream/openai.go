package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/edxeme/keystone/internal/chat"
	"github.com/edxeme/keystone/internal/sse"
)

// OpenAI talks to the OpenAI-compatible chat completions API. It also
// serves Ollama, vLLM and every other compatible endpoint by base URL.
// Requests and stream chunks pass through untouched: the canonical
// dialect IS the OpenAI dialect.
type OpenAI struct {
	name    string
	baseURL string
	apiKey  string
	hc      *http.Client
}

// Name implements Upstream.
func (o *OpenAI) Name() string { return o.name }

// Complete implements Upstream.
func (o *OpenAI) Complete(ctx context.Context, req *chat.ChatRequest) (*chat.ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	raw, err := o.post(ctx, body)
	if err != nil {
		return nil, err
	}
	var resp chat.ChatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("upstream %s: decode response: %w", o.name, err)
	}
	return &resp, nil
}

// Stream implements Upstream: SSE data lines decode directly into
// canonical chunks until the [DONE] sentinel.
func (o *OpenAI) Stream(ctx context.Context, req *chat.ChatRequest) (chat.Stream, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := o.postStream(ctx, body)
	if err != nil {
		return nil, err
	}
	return &openaiStream{resp: resp, r: sse.NewReader(resp.Body), name: o.name}, nil
}

func (o *OpenAI) post(ctx context.Context, body []byte) ([]byte, error) {
	req, err := o.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream %s: %w", o.name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("upstream %s: read response: %w", o.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(o.name, resp.StatusCode, raw)
	}
	return raw, nil
}

func (o *OpenAI) postStream(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := o.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream %s: %w", o.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		return nil, statusError(o.name, resp.StatusCode, raw)
	}
	return resp, nil
}

func (o *OpenAI) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	return req, nil
}

type openaiStream struct {
	resp *http.Response
	r    *sse.Reader
	name string
}

// Next implements chat.Stream.
func (s *openaiStream) Next() (*chat.ChatResponse, error) {
	for {
		ev, err := s.r.Next()
		if err != nil {
			return nil, err
		}
		if ev.Event != "" || ev.Data == "" {
			continue
		}
		if ev.Data == "[DONE]" {
			return nil, io.EOF
		}
		var chunk chat.ChatResponse
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return nil, fmt.Errorf("upstream %s: decode chunk: %w", s.name, err)
		}
		return &chunk, nil
	}
}

// Close implements chat.Stream.
func (s *openaiStream) Close() error {
	return s.resp.Body.Close()
}
