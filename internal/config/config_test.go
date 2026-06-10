package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const valid = `
webhook_token: ${CARAMBA_TEST_TOKEN}
retention: 720h
store:
  type: local
  path: /data
destinations:
  - name: ops-slack
    type: slack
    webhook_url: https://hooks.slack.com/services/x/y/z
`

func TestLoad(t *testing.T) {
	t.Setenv("CARAMBA_TEST_TOKEN", "s3cret")
	cfg, err := Load(write(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookToken != "s3cret" {
		t.Errorf("env expansion failed: %q", cfg.WebhookToken)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default listen: %q", cfg.Listen)
	}
	if time.Duration(cfg.Retention) != 720*time.Hour {
		t.Errorf("retention: %v", cfg.Retention)
	}
	if len(cfg.Destinations) != 1 || cfg.Destinations[0].Name != "ops-slack" {
		t.Errorf("destinations: %+v", cfg.Destinations)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name, yml, wantErr string
	}{
		{"missing token", "store: {type: local, path: /d}", "webhook_token"},
		{"bad store type", "webhook_token: t\nstore: {type: s3}", "store.type"},
		{"local without path", "webhook_token: t\nstore: {type: local}", "store.path"},
		{"gcs without bucket", "webhook_token: t\nstore: {type: gcs}", "store.bucket"},
		{"unknown field", "webhook_token: t\nstore: {type: local, path: /d}\nbogus: 1", "bogus"},
		{
			"duplicate destination",
			"webhook_token: t\nstore: {type: local, path: /d}\ndestinations: [{name: a, type: slack, webhook_url: u}, {name: a, type: slack, webhook_url: u}]",
			"duplicate",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.yml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
