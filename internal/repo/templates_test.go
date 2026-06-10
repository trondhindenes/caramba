package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/store"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

func newTemplateRepo(t *testing.T) *TemplateRepo {
	t.Helper()
	s, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewTemplateRepo(s)
}

func TestTemplateCRUD(t *testing.T) {
	ctx := context.Background()
	r := newTemplateRepo(t)

	tmpl := &model.Template{Name: "slack-default", Engine: "grafana", Title: "t", Body: "b"}
	if err := r.Save(ctx, tmpl); err != nil {
		t.Fatal(err)
	}
	if tmpl.ID == "" {
		t.Fatal("Save should assign an ID")
	}
	if tmpl.UpdatedAt.IsZero() {
		t.Error("Save should stamp UpdatedAt")
	}

	got, err := r.Get(ctx, tmpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "slack-default" || got.Engine != "grafana" {
		t.Errorf("round trip mismatch: %+v", got)
	}

	got.Body = "updated"
	if err := r.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := r.Get(ctx, tmpl.ID)
	if again.Body != "updated" {
		t.Errorf("update not persisted: %q", again.Body)
	}

	if err := r.Delete(ctx, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, tmpl.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestTemplateListSortedByName(t *testing.T) {
	ctx := context.Background()
	r := newTemplateRepo(t)
	for _, name := range []string{"zebra", "alpha", "midway"} {
		if err := r.Save(ctx, &model.Template{Name: name, Engine: "grafana"}); err != nil {
			t.Fatal(err)
		}
	}
	templates, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 3 {
		t.Fatalf("want 3, got %d", len(templates))
	}
	for i, want := range []string{"alpha", "midway", "zebra"} {
		if templates[i].Name != want {
			t.Errorf("index %d: want %q, got %q", i, want, templates[i].Name)
		}
	}
}
