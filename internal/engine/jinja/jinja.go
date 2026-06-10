// Package jinja renders Jinja2 templates via gonja. The render context is
// the webhook payload's JSON shape, so field names match what Grafana sends
// (alerts, commonLabels, groupLabels, status, ...).
package jinja

import (
	"encoding/json"
	"fmt"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"

	"github.com/trondhindenes/caramba/internal/model"
)

const Name = "jinja2"

type Engine struct{}

func New() *Engine { return &Engine{} }

func (e *Engine) Name() string { return Name }

func (e *Engine) Render(src string, p *model.Payload) (string, error) {
	tmpl, err := gonja.FromString(src)
	if err != nil {
		return "", err
	}
	ctx, err := payloadContext(p)
	if err != nil {
		return "", err
	}
	return tmpl.ExecuteToString(ctx)
}

func (e *Engine) Validate(src string) error {
	_, err := gonja.FromString(src)
	return err
}

// payloadContext exposes the payload to templates with its JSON field
// names, by round-tripping the struct through encoding/json.
func payloadContext(p *model.Payload) (*exec.Context, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("preparing render context: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("preparing render context: %w", err)
	}
	return exec.NewContext(data), nil
}
