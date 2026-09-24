// Package config loads caramba's YAML configuration. Values may reference
// environment variables with ${VAR} so secrets stay out of the file.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
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

// ColorRule picks the attachment color for slack-attachment destinations
// when the alert's title matches the wildcard pattern.
type ColorRule struct {
	Title string `yaml:"title"`
	Color string `yaml:"color"`
}

type Config struct {
	Listen       string        `yaml:"listen"`
	WebhookToken string        `yaml:"webhook_token"`
	MCPToken     string        `yaml:"mcp_token"`
	Retention    Duration      `yaml:"retention"`
	Store        StoreConfig   `yaml:"store"`
	Destinations []Destination `yaml:"destinations"`
	Colors       []ColorRule   `yaml:"colors"`
}

// Destination types.
const (
	DestSlack           = "slack"            // plain text message
	DestSlackAttachment = "slack-attachment" // legacy attachment with a color bar
)

// Slack accepts hex colors or its named ones.
var colorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|good|warning|danger)$`)

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
		if d.Type != DestSlack && d.Type != DestSlackAttachment {
			return fmt.Errorf("destination %q: type must be %q or %q, got %q", d.Name, DestSlack, DestSlackAttachment, d.Type)
		}
		if d.WebhookURL == "" {
			return fmt.Errorf("destination %q: webhook_url is required", d.Name)
		}
	}
	for i, c := range c.Colors {
		if c.Title == "" {
			return fmt.Errorf("colors[%d]: title pattern is required", i)
		}
		if !colorPattern.MatchString(c.Color) {
			return fmt.Errorf("colors[%d]: color must be #RRGGBB, good, warning or danger, got %q", i, c.Color)
		}
	}
	return nil
}
