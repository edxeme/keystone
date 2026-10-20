// Package translate converts between the canonical OpenAI dialect the
// gateway speaks and the Anthropic Messages API dialect.
package translate

import (
	"encoding/json"
	"fmt"

	"github.com/edxeme/keystone/internal/chat"
	"github.com/edxeme/keystone/internal/sse"
)

// DefaultMaxTokens is used when a client omits max_tokens; the
// Anthropic API requires it.
const DefaultMaxTokens = 4096

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	Stop        []string           `json:"stop_sequences,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ToAnthropicRequest builds the Anthropic Messages API body for a
// canonical request. A leading system message moves to the dedicated
// system field, as the API requires.
func ToAnthropicRequest(req *chat.ChatRequest) ([]byte, error) {
	maxTokens := DefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	out := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		Stop:        req.Stop,
	}
	rest := req.Messages
	if len(rest) > 0 && rest[0].Role == "system" {
		sys, err := rest[0].TextContent()
		if err != nil {
			return nil, fmt.Errorf("translate request: system content: %w", err)
		}
		out.System = sys
		rest = rest[1:]
	}
	for _, m := range rest {
		text, err := m.TextContent()
		if err != nil {
			return nil, fmt.Errorf("translate request: %s content: %w", m.Role, err)
		}
		out.Messages = append(out.Messages, anthropicMessage{Role: m.Role, Content: text})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// FromAnthropicResponse decodes a non-streaming Anthropic response
// into the canonical shape.
func FromAnthropicResponse(raw []byte) (*chat.ChatResponse, error) {
	var r anthropicResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("translate response: %w", err)
	}
	var text string
	for _, block := range r.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	content, err := json.Marshal(text)
	if err != nil {
		return nil, err
	}
	finish := stopReason(r.StopReason)
	return &chat.ChatResponse{
		ID:    r.ID,
		Model: r.Model,
		Choices: []chat.Choice{{
			Index:        0,
			Message:      &chat.Message{Role: "assistant", Content: content},
			FinishReason: &finish,
		}},
		Usage: &chat.Usage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		},
	}, nil
}

// StreamTranslator converts a stream of Anthropic SSE events into
// canonical response chunks. It is stateful but not safe for
// concurrent use; one translator per upstream stream.
type StreamTranslator struct{}

// Translate converts one SSE event into zero or more canonical
// chunks. Unknown events are ignored; terminal handling is driven by
// message_stop.
func (t *StreamTranslator) Translate(ev sse.Event) ([]*chat.ChatResponse, error) {
	if ev.Event == "" && ev.Data == "" {
		return nil, nil
	}
	switch ev.Event {
	case "message_start":
		return t.messageStart(ev.Data)
	case "content_block_delta":
		return t.contentDelta(ev.Data)
	case "message_delta":
		return t.messageDelta(ev.Data)
	case "message_stop":
		return nil, nil // chunks for message_stop were emitted by message_delta
	default:
		return nil, nil // ping, content_block_start/end, unknown
	}
}

type messageStartEvent struct {
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	} `json:"message"`
}

func (t *StreamTranslator) messageStart(data string) ([]*chat.ChatResponse, error) {
	var ev messageStartEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, fmt.Errorf("translate message_start: %w", err)
	}
	return []*chat.ChatResponse{{
		ID:    ev.Message.ID,
		Model: ev.Message.Model,
		Choices: []chat.Choice{{
			Index: 0,
			Delta: &chat.Message{Role: "assistant", Content: json.RawMessage(`""`)},
		}},
	}}, nil
}

type contentDeltaEvent struct {
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

func (t *StreamTranslator) contentDelta(data string) ([]*chat.ChatResponse, error) {
	var ev contentDeltaEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, fmt.Errorf("translate content_block_delta: %w", err)
	}
	if ev.Delta.Type != "text_delta" || ev.Delta.Text == "" {
		return nil, nil
	}
	chunk, err := deltaChunk(ev.Delta.Text, nil)
	if err != nil {
		return nil, err
	}
	return []*chat.ChatResponse{chunk}, nil
}

type messageDeltaEvent struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (t *StreamTranslator) messageDelta(data string) ([]*chat.ChatResponse, error) {
	var ev messageDeltaEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, fmt.Errorf("translate message_delta: %w", err)
	}
	if ev.Delta.StopReason == "" {
		return nil, nil
	}
	finish := stopReason(ev.Delta.StopReason)
	chunk, err := deltaChunk("", &finish)
	if err != nil {
		return nil, err
	}
	return []*chat.ChatResponse{chunk}, nil
}

// deltaChunk builds one canonical chunk; empty text with a finish
// reason emits the terminal delta.
func deltaChunk(text string, finish *string) (*chat.ChatResponse, error) {
	content, err := json.Marshal(text)
	if err != nil {
		return nil, err
	}
	return &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index:        0,
			Delta:        &chat.Message{Role: "assistant", Content: content},
			FinishReason: finish,
		}},
	}, nil
}

// stopReason maps Anthropic stop reasons to OpenAI finish reasons.
func stopReason(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "end_turn", "stop_sequence", "":
		return "stop"
	default:
		return "stop"
	}
}
