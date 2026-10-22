// Package usage tracks per-key, per-model token accounting in memory.
package usage

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Entry aggregates one (key, model, upstream) triple.
type Entry struct {
	Key              string `json:"key"`
	Model            string `json:"model"`
	Upstream         string `json:"upstream"`
	Requests         int64  `json:"requests"`
	Errors           int64  `json:"errors"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

// Tracker is a concurrency-safe in-memory accounting table.
type Tracker struct {
	mu      sync.Mutex
	entries map[string]*Entry
}

// NewTracker builds an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{entries: make(map[string]*Entry)}
}

// Record accounts one request. Tokens are taken from the upstream
// response when reported, or estimated by the gateway when not.
func (t *Tracker) Record(key, model, upstream string, promptTokens, completionTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(key, model, upstream)
	e.Requests++
	e.PromptTokens += int64(promptTokens)
	e.CompletionTokens += int64(completionTokens)
}

// RecordError accounts one failed request attempt.
func (t *Tracker) RecordError(key, model, upstream string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(key, model, upstream)
	e.Requests++
	e.Errors++
}

func entryKey(key, model, upstream string) string {
	return key + "\x00" + model + "\x00" + upstream
}

func (t *Tracker) entry(key, model, upstream string) *Entry {
	k := entryKey(key, model, upstream)
	e, ok := t.entries[k]
	if !ok {
		e = &Entry{Key: key, Model: model, Upstream: upstream}
		t.entries[k] = e
	}
	return e
}

// Snapshot returns the entries sorted by key, model and upstream.
func (t *Tracker) Snapshot() []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshot()
}

func (t *Tracker) snapshot() []Entry {
	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Upstream < b.Upstream
	})
	return out
}

// Metrics renders the table in Prometheus text format.
func (t *Tracker) Metrics() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var b strings.Builder
	for _, e := range t.snapshot() {
		labels := fmt.Sprintf("key=%q,model=%q,upstream=%q", e.Key, e.Model, e.Upstream)
		b.WriteString(fmt.Sprintf("keystone_requests_total{%s} %d\n", labels, e.Requests))
		b.WriteString(fmt.Sprintf("keystone_prompt_tokens_total{%s} %d\n", labels, e.PromptTokens))
		b.WriteString(fmt.Sprintf("keystone_completion_tokens_total{%s} %d\n", labels, e.CompletionTokens))
		b.WriteString(fmt.Sprintf("keystone_errors_total{%s} %d\n", labels, e.Errors))
	}
	return b.String()
}
