package translate

import (
	"strings"
	"testing"

	"github.com/edxeme/keystone/internal/chat"
	"github.com/edxeme/keystone/internal/sse"
)

func mustMessage(role, text string) chat.Message {
	content, err := chat.MarshalText(text)
	if err != nil {
		panic(err)
	}
	return chat.Message{Role: role, Content: content}
}

func TestToAnthropicRequestSplitsSystem(t *testing.T) {
	maxTokens := 512
	req := &chat.ChatRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: &maxTokens,
		Messages: []chat.Message{
			mustMessage("system", "be brief"),
			mustMessage("user", "hello"),
			mustMessage("assistant", "hi"),
			mustMessage("user", "bye"),
		},
	}
	raw, err := ToAnthropicRequest(req)
	if err != nil {
		t.Fatalf("ToAnthropicRequest() error = %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		`"model":"claude-sonnet-4-5"`,
		`"max_tokens":512`,
		`"system":"be brief"`,
		`"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"bye"}]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s\ngot: %s", want, body)
		}
	}
}

func TestToAnthropicRequestDefaultsMaxTokens(t *testing.T) {
	req := &chat.ChatRequest{Model: "m", Messages: []chat.Message{mustMessage("user", "hi")}}
	raw, err := ToAnthropicRequest(req)
	if err != nil {
		t.Fatalf("ToAnthropicRequest() error = %v", err)
	}
	if !strings.Contains(string(raw), `"max_tokens":4096`) {
		t.Errorf("default max_tokens missing: %s", raw)
	}
}

func TestFromAnthropicResponse(t *testing.T) {
	raw := []byte(`{
		"id": "msg_1", "model": "claude-sonnet-4-5", "type": "message", "role": "assistant",
		"content": [{"type":"text","text":"Hel"},{"type":"tool_use","id":"t"},{"type":"text","text":"lo"}],
		"stop_reason": "max_tokens",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)
	resp, err := FromAnthropicResponse(raw)
	if err != nil {
		t.Fatalf("FromAnthropicResponse() error = %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message == nil {
		t.Fatalf("choices = %+v", resp.Choices)
	}
	text, err := resp.Choices[0].Message.TextContent()
	if err != nil || text != "Hello" {
		t.Errorf("content = %q, %v; want Hello", text, err)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "length" {
		t.Errorf("finish = %v, want length", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestStreamTranslator(t *testing.T) {
	tr := &StreamTranslator{}
	var got []string
	var finish string
	events := []sse.Event{
		{Event: "message_start", Data: `{"type":"message_start","message":{"id":"msg_9","model":"claude-sonnet-4-5"}}`},
		{Event: "ping", Data: `{"type":"ping"}`},
		{Event: "content_block_delta", Data: `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}`},
		{Event: "content_block_delta", Data: `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}`},
		{Event: "content_block_delta", Data: `{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}`},
		{Event: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`},
		{Event: "message_stop", Data: `{"type":"message_stop"}`},
	}
	for _, ev := range events {
		chunks, err := tr.Translate(ev)
		if err != nil {
			t.Fatalf("Translate(%s) error = %v", ev.Event, err)
		}
		for _, c := range chunks {
			if c.Choices[0].FinishReason != nil {
				finish = *c.Choices[0].FinishReason
				continue
			}
			text, _ := c.Choices[0].Delta.TextContent()
			got = append(got, text)
		}
	}
	if strings.Join(got, "|") != "|Hel|lo" {
		t.Errorf("delta texts = %v, want role chunk + Hel + lo", got)
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
}

func TestStopReasonMapping(t *testing.T) {
	cases := map[string]string{
		"end_turn":      "stop",
		"max_tokens":    "length",
		"stop_sequence": "stop",
		"weird":         "stop",
	}
	for in, want := range cases {
		if got := stopReason(in); got != want {
			t.Errorf("stopReason(%q) = %q, want %q", in, got, want)
		}
	}
}
