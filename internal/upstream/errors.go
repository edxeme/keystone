package upstream

import "fmt"

// statusError builds an error for a non-200 upstream response,
// carrying a truncated body snippet for diagnosability.
func statusError(name string, code int, body []byte) error {
	return fmt.Errorf("upstream %s: status %d: %s", name, code, snippet(body, 512))
}

func snippet(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
