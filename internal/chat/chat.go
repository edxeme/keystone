// Package chat defines the canonical chat-completion wire shapes the
// gateway speaks internally: the OpenAI dialect. Providers with their
// own dialect are translated at the upstream boundary.
package chat

import (
	"encoding/json"
	"strings"
)

// Message is a single chat message. Content stays raw so pass-through
// routing never mangles richer payloads (content parts, images).
// Role and Content are omitempty so stream deltas re-encode exactly
// as the upstream emitted them.
type Message struct {
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

// ChatRequest is the canonical request body.
type ChatRequest struct {
	Model       string          `json:"model"`
	Messages    []Message       `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Extra       json.RawMessage `json:"-"`
}

// ChatResponse is the canonical response body; for streaming it
// carries one delta per Choice.
type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is one completion alternative.
type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

// Usage carries token accounting as reported by the upstream.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Stream yields canonical response chunks and is safe to close twice.
// Next returns io.EOF when the stream is complete.
type Stream interface {
	Next() (*ChatResponse, error)
	Close() error
}

// TextContent decodes a plain-string message content, the only shape
// translation supports today.
func (m Message) TextContent() (string, error) {
	if len(m.Content) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err != nil {
		return "", err
	}
	return s, nil
}

// MarshalText encodes a plain string as message content.
func MarshalText(text string) (json.RawMessage, error) {
	return json.Marshal(text)
}

// EstimateTokens approximates the token count of a text payload.
// Rough on purpose: it only backs usage rows for upstreams that do
// not report token counts.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

// EstimateRequestTokens sums a rough token estimate over all messages.
func EstimateRequestTokens(req *ChatRequest) int {
	var b strings.Builder
	for _, m := range req.Messages {
		b.Write(m.Content)
	}
	return EstimateTokens(b.String())
}
