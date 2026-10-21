// Package upstream implements the provider clients keystone proxies
// to. Every client speaks the canonical chat dialect outwards and
// translates its provider dialect inwards.
package upstream

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/edxeme/keystone/internal/chat"
	"github.com/edxeme/keystone/internal/config"
)

// DefaultTimeout bounds a single upstream request, streaming included.
const DefaultTimeout = 10 * time.Minute

// Upstream is one provider endpoint keystone can route to.
type Upstream interface {
	Name() string
	// Complete runs a non-streaming completion.
	Complete(ctx context.Context, req *chat.ChatRequest) (*chat.ChatResponse, error)
	// Stream opens a streaming completion. Failing before the first
	// byte is reported as error so the gateway can fail over.
	Stream(ctx context.Context, req *chat.ChatRequest) (chat.Stream, error)
}

// New builds the client for a configured upstream. apiKey is resolved
// from the upstream's environment variable when it declares one.
func New(cfg config.Upstream, apiKey string) (Upstream, error) {
	if cfg.BaseURL == "" {
		switch cfg.Provider {
		case config.ProviderOpenAI:
			cfg.BaseURL = "https://api.openai.com/v1"
		case config.ProviderAnthropic:
			cfg.BaseURL = "https://api.anthropic.com"
		}
	}
	hc := &http.Client{Timeout: DefaultTimeout}
	switch cfg.Provider {
	case config.ProviderOpenAI:
		return &OpenAI{name: cfg.Name, baseURL: cfg.BaseURL, apiKey: apiKey, hc: hc}, nil
	case config.ProviderAnthropic:
		return &Anthropic{name: cfg.Name, baseURL: cfg.BaseURL, apiKey: apiKey, hc: hc}, nil
	default:
		return nil, fmt.Errorf("upstream %s: unknown provider %q", cfg.Name, cfg.Provider)
	}
}
