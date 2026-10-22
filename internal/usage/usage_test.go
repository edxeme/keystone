package usage

import (
	"strings"
	"sync"
	"testing"
)

func TestRecordAndSnapshot(t *testing.T) {
	tr := NewTracker()
	tr.Record("k1", "gpt-4o", "openai", 10, 5)
	tr.Record("k1", "gpt-4o", "openai", 1, 1)
	tr.RecordError("k1", "gpt-4o", "openai")
	tr.Record("k1", "gpt-4o", "fallback", 2, 3)

	snap := tr.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("entries = %d, want 2", len(snap))
	}
	first := snap[0] // sorted: fallback < openai by upstream name
	if first.Upstream != "fallback" || first.Requests != 1 || first.PromptTokens != 2 {
		t.Errorf("entry = %+v", first)
	}
	second := snap[1]
	if second.Requests != 3 || second.Errors != 1 || second.PromptTokens != 11 || second.CompletionTokens != 6 {
		t.Errorf("entry = %+v", second)
	}
}

func TestMetricsFormat(t *testing.T) {
	tr := NewTracker()
	tr.Record("k1", "gpt-4o", "openai", 10, 5)
	tr.RecordError("k1", "gpt-4o", "openai")

	m := tr.Metrics()
	for _, want := range []string{
		`keystone_requests_total{key="k1",model="gpt-4o",upstream="openai"} 2`,
		`keystone_prompt_tokens_total{key="k1",model="gpt-4o",upstream="openai"} 10`,
		`keystone_completion_tokens_total{key="k1",model="gpt-4o",upstream="openai"} 5`,
		`keystone_errors_total{key="k1",model="gpt-4o",upstream="openai"} 1`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("metrics missing %q\n%s", want, m)
		}
	}
}

func TestConcurrentRecorders(t *testing.T) {
	tr := NewTracker()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				tr.Record("k", "m", "u", 1, 1)
			}
		}()
	}
	wg.Wait()
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Requests != 400 {
		t.Errorf("snapshot = %+v, want 400 requests", snap)
	}
}
