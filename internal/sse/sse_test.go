package sse

import (
	"io"
	"strings"
	"testing"
)

func TestReaderDecodesEvents(t *testing.T) {
	stream := strings.Join([]string{
		": keep-alive comment",
		"data: first",
		"",
		"event: error",
		"data: {\"code\":1,",
		"data:  \"msg\":\"broken\"}",
		"",
		"id: 42",
		"retry: 100",
		"data: last",
		// no trailing blank line on purpose
	}, "\n")

	r := NewReader(strings.NewReader(stream))
	var events []Event
	for {
		ev, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Event != "" || events[0].Data != "first" {
		t.Errorf("event 0 = %+v", events[0])
	}
	if events[1].Event != "error" || events[1].Data != "{\"code\":1,\n \"msg\":\"broken\"}" {
		t.Errorf("event 1 = %+v (multi-line data must join with \\n)", events[1])
	}
	if events[2].Data != "last" {
		t.Errorf("event 2 = %+v (final event without blank line)", events[2])
	}
}

func TestReaderSkipsBlankPrefix(t *testing.T) {
	r := NewReader(strings.NewReader("\n\n\n"))
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("Next() error = %v, want io.EOF", err)
	}
}

func TestWriteEventFraming(t *testing.T) {
	var b strings.Builder
	if err := WriteEvent(&b, "delta", "line1\nline2"); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	want := "event: delta\ndata: line1\ndata: line2\n\n"
	if b.String() != want {
		t.Errorf("WriteEvent() = %q, want %q", b.String(), want)
	}

	b.Reset()
	if err := WriteEvent(&b, "", "x"); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if b.String() != "data: x\n\n" {
		t.Errorf("default event framing = %q", b.String())
	}
}
