// Package notify delivers rendered messages to destinations. Destinations
// are defined in the config file (they carry secrets such as webhook URLs)
// and referenced by name from routing rules.
package notify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/engine"
)

// Message is a rendered template plus the alert context senders may use for
// presentation, such as picking a color.
type Message struct {
	engine.Rendered
	// Status is the alert group's status: "firing" or "resolved".
	Status string
	// AlertTitle is Grafana's title for the group, which color rules match.
	AlertTitle string
	// Link points at the alert in Grafana; empty when unknown.
	Link string
}

// Sender delivers one message to a destination.
type Sender interface {
	Send(ctx context.Context, msg *Message) error
}

// permanentError marks a failure that retrying cannot fix.
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Registry maps destination names to their senders.
type Registry struct {
	senders map[string]Sender
	// Backoff is the wait before each retry; its length bounds the attempts.
	Backoff []time.Duration
}

func NewRegistry(dests []config.Destination, colors []config.ColorRule) (*Registry, error) {
	r := &Registry{
		senders: map[string]Sender{},
		Backoff: []time.Duration{time.Second, 5 * time.Second},
	}
	colorizer, err := newColorizer(colors)
	if err != nil {
		return nil, err
	}
	for _, d := range dests {
		switch d.Type {
		case config.DestSlack:
			r.senders[d.Name] = NewSlack(d.WebhookURL)
		case config.DestSlackAttachment:
			r.senders[d.Name] = NewSlackAttachment(d.WebhookURL, colorizer)
		}
	}
	return r, nil
}

// Add registers a sender under a name; used by tests and future types.
func (r *Registry) Add(name string, s Sender) { r.senders[name] = s }

// Names returns the configured destination names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.senders))
	for n := range r.senders {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Send delivers msg to the named destination, retrying transient failures.
// It returns how many attempts were made.
func (r *Registry) Send(ctx context.Context, name string, msg *Message) (attempts int, err error) {
	s, ok := r.senders[name]
	if !ok {
		return 0, fmt.Errorf("unknown destination %q (not in config)", name)
	}
	for attempts = 1; ; attempts++ {
		err = s.Send(ctx, msg)
		var perm permanentError
		if err == nil || errors.As(err, &perm) || attempts > len(r.Backoff) {
			return attempts, err
		}
		select {
		case <-time.After(r.Backoff[attempts-1]):
		case <-ctx.Done():
			return attempts, err
		}
	}
}
