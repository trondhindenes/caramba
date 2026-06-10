// Package engine defines the pluggable template engine abstraction. Each
// stored template declares which engine renders it, so Grafana-compatible
// and Jinja2 templates can coexist.
package engine

import (
	"fmt"

	"github.com/trondhindenes/caramba/internal/engine/gotmpl"
	"github.com/trondhindenes/caramba/internal/engine/jinja"
	"github.com/trondhindenes/caramba/internal/model"
)

type Engine interface {
	Name() string
	// Render executes a template source against one webhook payload.
	Render(src string, p *model.Payload) (string, error)
	// Validate reports whether the source parses, for GUI feedback on save.
	Validate(src string) error
}

// Rendered is a templated message: the two parts a destination needs.
type Rendered struct {
	Title string
	Body  string
}

type Registry map[string]Engine

func NewRegistry() Registry {
	return Registry{
		gotmpl.Name: gotmpl.New(),
		jinja.Name:  jinja.New(),
	}
}

// Names returns the registered engine names in stable order for GUI menus.
func (r Registry) Names() []string {
	return []string{gotmpl.Name, jinja.Name}
}

func (r Registry) Get(name string) (Engine, error) {
	e, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("unknown template engine %q", name)
	}
	return e, nil
}

// RenderTemplate renders a template's title and body against a payload.
func (r Registry) RenderTemplate(t *model.Template, p *model.Payload) (*Rendered, error) {
	e, err := r.Get(t.Engine)
	if err != nil {
		return nil, err
	}
	title, err := e.Render(t.Title, p)
	if err != nil {
		return nil, fmt.Errorf("rendering title: %w", err)
	}
	body, err := e.Render(t.Body, p)
	if err != nil {
		return nil, fmt.Errorf("rendering body: %w", err)
	}
	return &Rendered{Title: title, Body: body}, nil
}

// ValidateTemplate checks both parts of a template without rendering.
func (r Registry) ValidateTemplate(t *model.Template) error {
	e, err := r.Get(t.Engine)
	if err != nil {
		return err
	}
	if err := e.Validate(t.Title); err != nil {
		return fmt.Errorf("title: %w", err)
	}
	if err := e.Validate(t.Body); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	return nil
}
