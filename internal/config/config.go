// Package config loads the keystone gateway configuration.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Providers keystone can route to.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

// DefaultListen is the address used when none is configured.
const DefaultListen = ":8080"

// Config is the parsed gateway configuration.
type Config struct {
	Listen    string              `yaml:"listen"`
	APIKeys   []string            `yaml:"api_keys"`
	Upstreams []Upstream          `yaml:"upstreams"`
	Routes    map[string][]string `yaml:"routes"`
	RateLimit RateLimit           `yaml:"rate_limit"`
}

// Upstream describes one provider endpoint.
type Upstream struct {
	Name      string `yaml:"name"`
	Provider  string `yaml:"provider"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// RateLimit caps requests per minute per API key. Zero disables it.
type RateLimit struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
}

// Load reads and validates the configuration file at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the rules YAML tags cannot express.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = DefaultListen
	}
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("config: at least one upstream is required")
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("config: at least one route is required")
	}

	seen := make(map[string]bool, len(c.Upstreams))
	for _, u := range c.Upstreams {
		if strings.TrimSpace(u.Name) == "" {
			return fmt.Errorf("config: every upstream needs a name")
		}
		if seen[u.Name] {
			return fmt.Errorf("upstream %s: duplicate name", u.Name)
		}
		seen[u.Name] = true
		switch u.Provider {
		case ProviderOpenAI, ProviderAnthropic:
		default:
			return fmt.Errorf("upstream %s: unknown provider %q (want openai or anthropic)", u.Name, u.Provider)
		}
		if u.Provider == ProviderOpenAI && u.BaseURL == "" {
			u.BaseURL = "https://api.openai.com/v1" // applied in Load path below
		}
	}

	for model, chain := range c.Routes {
		if len(chain) == 0 {
			return fmt.Errorf("route %s: empty fallback chain", model)
		}
		for _, name := range chain {
			if !seen[name] {
				return fmt.Errorf("route %s: unknown upstream %q", model, name)
			}
		}
	}
	return nil
}
