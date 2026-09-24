// Package config loads caramba's YAML configuration. Values may reference
// environment variables with ${VAR} so secrets stay out of the file.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from Go duration strings
// (e.g. "720h"). 0 means "disabled" wherever it is used.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

type StoreConfig struct {
	Type   string `yaml:"type"`
	Path   string `yaml:"path"`
	Bucket string `yaml:"bucket"`
	Prefix string `yaml:"prefix"`
}

type Destination struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	WebhookURL string `yaml:"webhook_url"`
}

type Config struct {
	Listen       string        `yaml:"listen"`
	WebhookToken string        `yaml:"webhook_token"`
	MCPToken     string        `yaml:"mcp_token"`
	Retention    Duration      `yaml:"retention"`
	Store        StoreConfig   `yaml:"store"`
	Destinations []Destination `yaml:"destinations"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(strings.NewReader(os.ExpandEnv(string(raw))))
	dec.KnownFields(true)
	cfg := &Config{Listen: ":8080"}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.WebhookToken == "" {
		return errors.New("webhook_token is required (set WEBHOOK_TOKEN or put a value in the config)")
	}
	if c.MCPToken != "" && c.MCPToken == c.WebhookToken {
		return errors.New("mcp_token must differ from webhook_token")
	}
	switch c.Store.Type {
	case "local":
		if c.Store.Path == "" {
			return errors.New("store.path is required for the local store")
		}
	case "gcs":
		if c.Store.Bucket == "" {
			return errors.New("store.bucket is required for the gcs store")
		}
	default:
		return fmt.Errorf("store.type must be \"local\" or \"gcs\", got %q", c.Store.Type)
	}
	seen := map[string]bool{}
	for _, d := range c.Destinations {
		if d.Name == "" {
			return errors.New("destination name is required")
		}
		if seen[d.Name] {
			return fmt.Errorf("duplicate destination name %q", d.Name)
		}
		seen[d.Name] = true
		if d.Type != "slack" {
			return fmt.Errorf("destination %q: type must be \"slack\", got %q", d.Name, d.Type)
		}
		if d.WebhookURL == "" {
			return fmt.Errorf("destination %q: webhook_url is required", d.Name)
		}
	}
	return nil
}
