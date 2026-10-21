package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/edxeme/keystone/internal/chat"
	"github.com/edxeme/keystone/internal/sse"
	"github.com/edxeme/keystone/internal/translate"
)

// Anthropic talks to the Anthropic Messages API, translating between
// it and the canonical chat dialect at the boundary.
type Anthropic struct {
	name    string
	baseURL string
	apiKey  string
	hc      *http.Client
}

// Name implements Upstream.
func (a *Anthropic) Name() string { return a.name }

// Complete implements Upstream.
func (a *Anthropic) Complete(ctx context.Context, req *chat.ChatRequest) (*chat.ChatResponse, error) {
	body, err := translate.ToAnthropicRequest(req)
	if err != nil {
		return nil, err
	}
	raw, err := a.post(ctx, body)
	if err != nil {
		return nil, err
	}
	return translate.FromAnthropicResponse(raw)
}

// Stream implements Upstream: Anthropic SSE events are translated
// into canonical chunks as they arrive.
func (a *Anthropic) Stream(ctx context.Context, req *chat.ChatRequest) (chat.Stream, error) {
	body, err := translate.ToAnthropicRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := a.postStream(ctx, body)
	if err != nil {
		return nil, err
	}
	return &anthropicStream{
		resp:       resp,
		r:          sse.NewReader(resp.Body),
		translator: &translate.StreamTranslator{},
		name:       a.name,
	}, nil
}

func (a *Anthropic) post(ctx context.Context, body []byte) ([]byte, error) {
	req, err := a.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream %s: %w", a.name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("upstream %s: read response: %w", a.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(a.name, resp.StatusCode, raw)
	}
	return raw, nil
}

func (a *Anthropic) postStream(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := a.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream %s: %w", a.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		return nil, statusError(a.name, resp.StatusCode, raw)
	}
	return resp, nil
}

func (a *Anthropic) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.apiKey != "" {
		req.Header.Set("x-api-key", a.apiKey)
	}
	return req, nil
}

type anthropicStream struct {
	resp       *http.Response
	r          *sse.Reader
	translator *translate.StreamTranslator
	pending    []*chat.ChatResponse
	name       string
}

// Next implements chat.Stream. Anthropic events fan out to zero or
// more canonical chunks; the queue smooths that out.
func (s *anthropicStream) Next() (*chat.ChatResponse, error) {
	for {
		if len(s.pending) > 0 {
			next := s.pending[0]
			s.pending = s.pending[1:]
			return next, nil
		}
		ev, err := s.r.Next()
		if err != nil {
			return nil, err
		}
		chunks, err := s.translator.Translate(ev)
		if err != nil {
			return nil, fmt.Errorf("upstream %s: %w", s.name, err)
		}
		if len(chunks) > 0 {
			s.pending = append(s.pending, chunks...)
		}
	}
}

// Close implements chat.Stream.
func (s *anthropicStream) Close() error {
	return s.resp.Body.Close()
}
