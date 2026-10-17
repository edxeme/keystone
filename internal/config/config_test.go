package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `listen: :9090
api_keys:
  - sk-local-test-key
upstreams:
  - name: openai
    provider: openai
  - name: anthropic
    provider: anthropic
routes:
  gpt-4o: [openai]
  claude-sonnet-4-5: [anthropic, openai]
rate_limit:
  requests_per_minute: 60
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keystone.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	c, err := Load(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.Listen != ":9090" {
		t.Errorf("Listen = %q, want :9090", c.Listen)
	}
	if len(c.Upstreams) != 2 || len(c.Routes) != 2 {
		t.Errorf("upstreams = %d, routes = %d; want 2, 2", len(c.Upstreams), len(c.Routes))
	}
	if chain := c.Routes["claude-sonnet-4-5"]; strings.Join(chain, ",") != "anthropic,openai" {
		t.Errorf("fallback chain = %v, want [anthropic openai]", chain)
	}
}

func TestListenDefault(t *testing.T) {
	c, err := Load(writeConfig(t, strings.Replace(validConfig, "listen: :9090\n", "", 1)))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.Listen != DefaultListen {
		t.Errorf("Listen = %q, want default %q", c.Listen, DefaultListen)
	}
}

func TestLoadFailures(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "no upstreams",
			content: "routes:\n  m: [openai]\n",
			wantErr: "at least one upstream",
		},
		{
			name:    "no routes",
			content: strings.Replace(validConfig, "routes:\n  gpt-4o: [openai]\n  claude-sonnet-4-5: [anthropic, openai]\n", "", 1),
			wantErr: "at least one route",
		},
		{
			name: "duplicate upstream",
			content: strings.Replace(validConfig,
				"  - name: anthropic\n    provider: anthropic\n",
				"  - name: openai\n    provider: anthropic\n", 1),
			wantErr: "duplicate name",
		},
		{
			name:    "unknown provider",
			content: strings.Replace(validConfig, "    provider: anthropic\n", "    provider: mistral\n", 1),
			wantErr: "unknown provider",
		},
		{
			name:    "route to unknown upstream",
			content: strings.Replace(validConfig, "  gpt-4o: [openai]", "  gpt-4o: [ovh]", 1),
			wantErr: "unknown upstream",
		},
		{
			name:    "empty route chain",
			content: strings.Replace(validConfig, "  gpt-4o: [openai]", "  gpt-4o: []", 1),
			wantErr: "empty fallback chain",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.content))
			if err == nil {
				t.Fatalf("Load() succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/keystone.yaml"); err == nil {
		t.Error("Load() succeeded, want read error")
	}
}
