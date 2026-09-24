// Package dispatch routes received alerts: it picks the first matching
// rule, renders the rule's template and sends the result to each of the
// rule's destinations, recording the outcome beside the alert.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/trondhindenes/caramba/internal/engine"
	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/notify"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/route"
)

// routeTimeout bounds one alert's routing, retries included.
const routeTimeout = 2 * time.Minute

type Dispatcher struct {
	rules        *repo.RuleRepo
	templates    *repo.TemplateRepo
	dispatches   *repo.DispatchRepo
	engines      engine.Registry
	destinations *notify.Registry
	logger       *slog.Logger
	inflight     sync.WaitGroup
}

func New(rules *repo.RuleRepo, templates *repo.TemplateRepo, dispatches *repo.DispatchRepo,
	destinations *notify.Registry, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		rules:        rules,
		templates:    templates,
		dispatches:   dispatches,
		engines:      engine.NewRegistry(),
		destinations: destinations,
		logger:       logger,
	}
}

// Go routes an alert in the background, so the webhook can answer Grafana
// without waiting on Slack.
func (d *Dispatcher) Go(alert *repo.StoredAlert) {
	d.inflight.Add(1)
	go func() {
		defer d.inflight.Done()
		ctx, cancel := context.WithTimeout(context.Background(), routeTimeout)
		defer cancel()
		d.Route(ctx, alert)
	}()
}

// Wait blocks until background routing finishes; used on shutdown.
func (d *Dispatcher) Wait() { d.inflight.Wait() }

// Route matches, renders and sends one alert, and records the outcome.
func (d *Dispatcher) Route(ctx context.Context, alert *repo.StoredAlert) *model.Dispatch {
	rec := d.route(ctx, alert)
	if err := d.dispatches.Save(ctx, alert.ID, rec); err != nil {
		d.logger.Error("recording dispatch failed", "alert", alert.ID, "error", err)
	}
	return rec
}

func (d *Dispatcher) route(ctx context.Context, alert *repo.StoredAlert) *model.Dispatch {
	rules, err := d.rules.List(ctx)
	if err != nil {
		return d.failed(alert, &model.Dispatch{}, fmt.Errorf("loading rules: %w", err))
	}
	rule, err := route.FirstMatch(rules, alert.Raw)
	if err != nil {
		return d.failed(alert, &model.Dispatch{}, err)
	}
	if rule == nil {
		d.logger.Info("alert matched no rule", "alert", alert.ID)
		return &model.Dispatch{At: time.Now().UTC()}
	}
	return d.Deliver(ctx, rule, alert)
}

// Deliver renders the rule's template against the alert and sends it to
// all of the rule's destinations concurrently, whether or not the rule's
// matchers match. The GUI's test send uses it directly.
func (d *Dispatcher) Deliver(ctx context.Context, rule *model.Rule, alert *repo.StoredAlert) *model.Dispatch {
	rec := &model.Dispatch{At: time.Now().UTC(), RuleID: rule.ID, RuleName: rule.Name}
	if len(rule.Destinations) == 0 {
		d.logger.Info("alert dropped by rule without destinations", "alert", alert.ID, "rule", rule.Name)
		return rec
	}
	tmpl, err := d.templates.Get(ctx, rule.TemplateID)
	if err != nil {
		return d.failed(alert, rec, fmt.Errorf("loading template %q: %w", rule.TemplateID, err))
	}
	msg, err := d.engines.RenderTemplate(tmpl, alert.Payload)
	if err != nil {
		return d.failed(alert, rec, fmt.Errorf("rendering template %q: %w", tmpl.Name, err))
	}

	rec.Results = make([]model.DeliveryResult, len(rule.Destinations))
	var wg sync.WaitGroup
	for i, name := range rule.Destinations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempts, err := d.destinations.Send(ctx, name, msg)
			res := model.DeliveryResult{Destination: name, Attempts: attempts}
			if err != nil {
				res.Error = err.Error()
				d.logger.Error("delivery failed", "alert", alert.ID, "rule", rule.Name,
					"destination", name, "attempts", attempts, "error", err)
			} else {
				d.logger.Info("alert delivered", "alert", alert.ID, "rule", rule.Name, "destination", name)
			}
			rec.Results[i] = res
		}()
	}
	wg.Wait()
	return rec
}

func (d *Dispatcher) failed(alert *repo.StoredAlert, rec *model.Dispatch, err error) *model.Dispatch {
	d.logger.Error("routing failed", "alert", alert.ID, "rule", rec.RuleName, "error", err)
	rec.At = time.Now().UTC()
	rec.Error = err.Error()
	return rec
}
