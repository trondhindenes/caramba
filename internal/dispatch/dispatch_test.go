package dispatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/notify"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

// recorder is a fake destination capturing what it was sent.
type recorder struct {
	mu   sync.Mutex
	msgs []*notify.Message
	err  error
}

func (r *recorder) Send(_ context.Context, m *notify.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return r.err
}

type env struct {
	d          *Dispatcher
	rules      *repo.RuleRepo
	templates  *repo.TemplateRepo
	dispatches *repo.DispatchRepo
	alert      *repo.StoredAlert
	ops, dev   *recorder
}

func setup(t *testing.T) *env {
	t.Helper()
	st, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		rules:      repo.NewRuleRepo(st),
		templates:  repo.NewTemplateRepo(st),
		dispatches: repo.NewDispatchRepo(st),
		ops:        &recorder{},
		dev:        &recorder{},
	}
	dests, _ := notify.NewRegistry(nil, nil)
	dests.Backoff = nil
	dests.Add("ops", e.ops)
	dests.Add("dev", e.dev)
	e.d = New(e.rules, e.templates, e.dispatches, dests, slog.New(slog.NewTextHandler(io.Discard, nil)))

	raw, err := os.ReadFile("../../testdata/grafana-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	if e.alert, err = repo.NewAlertRepo(st).Save(t.Context(), raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) template(t *testing.T, title string) string {
	t.Helper()
	tm := &model.Template{Name: title, Engine: "jinja2", Title: title, Body: "{{ status }}"}
	if err := e.templates.Save(t.Context(), tm); err != nil {
		t.Fatal(err)
	}
	return tm.ID
}

func (e *env) rule(t *testing.T, r *model.Rule) {
	t.Helper()
	if err := e.rules.Save(t.Context(), r); err != nil {
		t.Fatal(err)
	}
}

func TestFirstMatchingRuleSends(t *testing.T) {
	e := setup(t)
	e.rule(t, &model.Rule{Name: "restarts", TemplateID: e.template(t, "restarts"), Destinations: []string{"dev"},
		Matchers: []model.Matcher{{Field: "title", Pattern: "*container restarts*"}}})
	e.rule(t, &model.Rule{Name: "cpu", TemplateID: e.template(t, "cpu"), Destinations: []string{"ops", "dev"},
		Matchers: []model.Matcher{{Field: "commonLabels.alertname", Pattern: "HighCPU"}}})
	e.rule(t, &model.Rule{Name: "default", TemplateID: e.template(t, "default"), Destinations: []string{"ops"}})

	rec := e.d.Route(t.Context(), e.alert)
	if rec.RuleName != "cpu" || rec.Error != "" || len(rec.Results) != 2 {
		t.Fatalf("dispatch: %+v", rec)
	}
	if len(e.ops.msgs) != 1 || e.ops.msgs[0].Title != "cpu" || e.ops.msgs[0].Body != "firing" {
		t.Errorf("ops got %+v", e.ops.msgs)
	}
	if len(e.dev.msgs) != 1 {
		t.Errorf("dev got %d messages", len(e.dev.msgs))
	}
	if m := e.ops.msgs[0]; m.Status != "firing" || m.AlertTitle != "[FIRING:2] HighCPU infra" || m.Link == "" {
		t.Errorf("message context: %+v", m)
	}

	saved, err := e.dispatches.Get(t.Context(), e.alert.ID)
	if err != nil || saved.RuleName != "cpu" {
		t.Errorf("recorded dispatch: %+v, %v", saved, err)
	}
}

func TestNoMatchIsRecorded(t *testing.T) {
	e := setup(t)
	e.rule(t, &model.Rule{Name: "restarts", TemplateID: e.template(t, "x"), Destinations: []string{"ops"},
		Matchers: []model.Matcher{{Field: "title", Pattern: "*container restarts*"}}})
	rec := e.d.Route(t.Context(), e.alert)
	if rec.RuleID != "" || len(e.ops.msgs) != 0 {
		t.Fatalf("want no match: %+v", rec)
	}
	if _, err := e.dispatches.Get(t.Context(), e.alert.ID); err != nil {
		t.Errorf("no-match should still be recorded: %v", err)
	}
}

func TestRuleWithoutDestinationsDrops(t *testing.T) {
	e := setup(t)
	e.rule(t, &model.Rule{Name: "mute", TemplateID: e.template(t, "x")})
	e.rule(t, &model.Rule{Name: "default", TemplateID: e.template(t, "y"), Destinations: []string{"ops"}})
	rec := e.d.Route(t.Context(), e.alert)
	if rec.RuleName != "mute" || len(rec.Results) != 0 || len(e.ops.msgs) != 0 {
		t.Fatalf("want dropped by mute: %+v", rec)
	}
}

func TestFailuresAreRecorded(t *testing.T) {
	e := setup(t)
	e.ops.err = errors.New("boom")
	e.rule(t, &model.Rule{Name: "default", TemplateID: e.template(t, "x"), Destinations: []string{"ops", "dev", "gone"}})
	rec := e.d.Route(t.Context(), e.alert)
	errs := map[string]string{}
	for _, r := range rec.Results {
		errs[r.Destination] = r.Error
	}
	if errs["ops"] != "boom" || errs["dev"] != "" || errs["gone"] == "" {
		t.Errorf("results: %+v", rec.Results)
	}

	e2 := setup(t)
	e2.rule(t, &model.Rule{Name: "broken", TemplateID: "deleted", Destinations: []string{"ops"}})
	if rec := e2.d.Route(t.Context(), e2.alert); rec.Error == "" || len(e2.ops.msgs) != 0 {
		t.Errorf("missing template should fail before sending: %+v", rec)
	}
}

func TestGoRoutesInBackground(t *testing.T) {
	e := setup(t)
	e.rule(t, &model.Rule{Name: "default", TemplateID: e.template(t, "x"), Destinations: []string{"ops"}})
	e.d.Go(e.alert)
	e.d.Wait()
	if len(e.ops.msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(e.ops.msgs))
	}
}
