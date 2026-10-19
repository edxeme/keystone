// Package sse implements the small slice of server-sent events that
// LLM streaming needs: named events, multi-line data, blank-line
// framing.
package sse

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Event is one decoded SSE event.
type Event struct {
	Event string // event name; empty for default events
	Data  string // data payload with newlines preserved
}

// Reader decodes an SSE stream event by event.
type Reader struct {
	sc *bufio.Scanner
}

// NewReader wraps r in an event reader.
func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &Reader{sc: sc}
}

// Next returns the next event. It returns io.EOF when the stream ends.
// Comment lines (":keep-alive") and "id"/"retry" fields are skipped.
func (r *Reader) Next() (Event, error) {
	var ev Event
	var data []string
	sawField := false
	for r.sc.Scan() {
		line := r.sc.Text()
		switch {
		case line == "": // blank line terminates the event
			if !sawField {
				continue
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		case strings.HasPrefix(line, ":"):
			// comment, ignore
		case strings.HasPrefix(line, "data:"):
			sawField = true
			data = append(data, trimField(line, "data:"))
		case strings.HasPrefix(line, "event:"):
			sawField = true
			ev.Event = trimField(line, "event:")
		default:
			sawField = true // id:, retry: and unknown fields
		}
	}
	if err := r.sc.Err(); err != nil {
		return Event{}, err
	}
	if sawField { // stream ended without a final blank line
		ev.Data = strings.Join(data, "\n")
		return ev, nil
	}
	return Event{}, io.EOF
}

func trimField(line, prefix string) string {
	v := strings.TrimPrefix(line, prefix)
	return strings.TrimPrefix(v, " ")
}

// WriteEvent writes one event with the exact framing clients expect.
func WriteEvent(w io.Writer, event, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\n")
	return err
}
