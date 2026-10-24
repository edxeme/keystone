// Package gateway implements the keystone HTTP surface: an
// OpenAI-compatible chat endpoint with provider routing, failover
// chains, streaming, auth and usage accounting.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/edxeme/keystone/internal/chat"
	"github.com/edxeme/keystone/internal/config"
	"github.com/edxeme/keystone/internal/ratelimit"
	"github.com/edxeme/keystone/internal/sse"
	"github.com/edxeme/keystone/internal/upstream"
	"github.com/edxeme/keystone/internal/usage"
)

// maxBodyBytes caps request bodies; chat payloads are small.
const maxBodyBytes = 8 << 20

// Gateway is the keystone request handler.
type Gateway struct {
	routes  map[string][]upstream.Upstream
	keys    map[string]bool
	limiter *ratelimit.Limiter
	usage   *usage.Tracker
	logger  *slog.Logger
}

// New builds the gateway from configuration, resolving upstream API
// keys from the environment.
func New(cfg *config.Config, logger *slog.Logger) (*Gateway, error) {
	byName := make(map[string]config.Upstream, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		byName[u.Name] = u
	}
	g := &Gateway{
		routes:  make(map[string][]upstream.Upstream, len(cfg.Routes)),
		keys:    make(map[string]bool, len(cfg.APIKeys)),
		limiter: ratelimit.New(cfg.RateLimit.RequestsPerMinute),
		usage:   usage.NewTracker(),
		logger:  logger,
	}
	for _, k := range cfg.APIKeys {
		g.keys[k] = true
	}
	for model, chain := range cfg.Routes {
		for _, name := range chain {
			u, err := upstream.New(byName[name], envKey(byName[name]))
			if err != nil {
				return nil, err
			}
			g.routes[model] = append(g.routes[model], u)
		}
	}
	return g, nil
}

// Usage exposes the accounting table for /admin/usage.
func (g *Gateway) Usage() *usage.Tracker { return g.usage }

func envKey(u config.Upstream) string {
	if u.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(u.APIKeyEnv)
}

// Handler returns the fully wired http.Handler.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /v1/models", g.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", g.handleChat)
	mux.HandleFunc("GET /metrics", g.handleMetrics)
	mux.HandleFunc("GET /admin/usage", g.handleUsage)
	return g.withMiddleware(mux)
}

// handleModels lists the routed models in OpenAI list format.
func (g *Gateway) handleModels(w http.ResponseWriter, _ *http.Request) {
	models := make([]string, 0, len(g.routes))
	for m := range g.routes {
		models = append(models, m)
	}
	sort.Strings(models)
	type model struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	data := make([]model, 0, len(models))
	for _, m := range models {
		data = append(data, model{ID: m, Object: "model"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// handleChat serves POST /v1/chat/completions with failover across
// the model's upstream chain.
func (g *Gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	key := clientKey(r)

	req, err := decodeChatRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	chain, ok := g.routes[req.Model]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown model %q", req.Model))
		return
	}

	var lastErr error
	for _, u := range chain {
		if req.Stream {
			if ok := g.tryStream(w, r, key, req, u); ok {
				return
			}
			continue
		}
		resp, err := u.Complete(r.Context(), req)
		if err != nil {
			g.logger.Error("upstream failed", "upstream", u.Name(), "model", req.Model, "error", err)
			g.usage.RecordError(key, req.Model, u.Name())
			lastErr = err
			continue
		}
		prompt, completion := reportedTokens(req, resp)
		g.usage.Record(key, req.Model, u.Name(), prompt, completion)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	writeError(w, http.StatusBadGateway, fmt.Errorf("all upstreams failed: %v", lastErr))
}

// tryStream opens a streaming completion from u and pipes it to the
// client as OpenAI-style SSE. It reports whether the attempt was
// made: errors before the first byte let the caller fail over.
func (g *Gateway) tryStream(w http.ResponseWriter, r *http.Request, key string, req *chat.ChatRequest, u upstream.Upstream) bool {
	stream, err := u.Stream(r.Context(), req)
	if err != nil {
		g.logger.Error("upstream failed", "upstream", u.Name(), "model", req.Model, "error", err)
		g.usage.RecordError(key, req.Model, u.Name())
		return false
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	var completion strings.Builder
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Mid-stream failures cannot fail over cleanly; terminate
			// the SSE stream with an error event.
			g.usage.RecordError(key, req.Model, u.Name())
			sse.WriteEvent(w, "error", `{"message":"upstream stream failed"}`)
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		if text, err := chunk.Choices[0].Delta.TextContent(); err == nil {
			completion.WriteString(text)
		}
		data, err := json.Marshal(chunk)
		if err != nil {
			return true
		}
		if err := sse.WriteEvent(w, "", string(data)); err != nil {
			return true // client went away
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := sse.WriteEvent(w, "", "[DONE]"); err == nil && flusher != nil {
		flusher.Flush()
	}
	g.usage.Record(key, req.Model, u.Name(), chat.EstimateRequestTokens(req), chat.EstimateTokens(completion.String()))
	return true
}

func (g *Gateway) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(g.usage.Metrics()))
}

func (g *Gateway) handleUsage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, g.usage.Snapshot())
}

// decodeChatRequest reads and validates the canonical request body.
func decodeChatRequest(r *http.Request) (*chat.ChatRequest, error) {
	body := io.LimitReader(r.Body, maxBodyBytes)
	var req chat.ChatRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		return nil, fmt.Errorf("decode request body: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("at least one message is required")
	}
	return &req, nil
}

// reportedTokens prefers upstream-reported usage and falls back to a
// rough estimate when the provider does not report token counts.
func reportedTokens(req *chat.ChatRequest, resp *chat.ChatResponse) (int, int) {
	if resp.Usage != nil {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	prompt := chat.EstimateRequestTokens(req)
	completion := 0
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		if text, err := resp.Choices[0].Message.TextContent(); err == nil {
			completion = chat.EstimateTokens(text)
		}
	}
	return prompt, completion
}

// clientKey identifies the caller for accounting: the bearer token
// when auth is configured, the remote address otherwise.
func clientKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return token
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeError emits an OpenAI-style error envelope.
func writeError(w http.ResponseWriter, code int, err error) {
	type e struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	writeJSON(w, code, map[string]e{"error": {Message: err.Error(), Type: "keystone_error"}})
}
