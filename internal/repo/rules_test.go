package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/store"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

func newRuleRepo(t *testing.T) *RuleRepo {
	t.Helper()
	s, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewRuleRepo(s)
}

func names(t *testing.T, r *RuleRepo) []string {
	t.Helper()
	rules, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range rules {
		out = append(out, x.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRuleOrdering(t *testing.T) {
	ctx := context.Background()
	r := newRuleRepo(t)
	if got := names(t, r); len(got) != 0 {
		t.Fatalf("empty repo: %v", got)
	}
	ids := map[string]string{}
	for _, n := range []string{"a", "b", "c"} {
		rule := &model.Rule{Name: n}
		if err := r.Save(ctx, rule); err != nil {
			t.Fatal(err)
		}
		ids[n] = rule.ID
	}
	if got := names(t, r); !equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("append order: %v", got)
	}

	steps := []struct {
		id    string
		delta int
		want  []string
	}{
		{ids["c"], -1, []string{"a", "c", "b"}},
		{ids["c"], -1, []string{"c", "a", "b"}},
		{ids["c"], -1, []string{"c", "a", "b"}}, // clamped at top
		{ids["c"], 5, []string{"a", "b", "c"}},  // clamped at bottom
	}
	for _, s := range steps {
		if err := r.Move(ctx, s.id, s.delta); err != nil {
			t.Fatal(err)
		}
		if got := names(t, r); !equal(got, s.want) {
			t.Fatalf("after move %d: got %v, want %v", s.delta, got, s.want)
		}
	}

	// Updating keeps the rule's position.
	if err := r.Save(ctx, &model.Rule{ID: ids["a"], Name: "a2"}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, r); !equal(got, []string{"a2", "b", "c"}) {
		t.Fatalf("update in place: %v", got)
	}

	if err := r.Delete(ctx, ids["b"]); err != nil {
		t.Fatal(err)
	}
	if got := names(t, r); !equal(got, []string{"a2", "c"}) {
		t.Fatalf("delete: %v", got)
	}
	if _, err := r.Get(ctx, ids["b"]); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted rule: want ErrNotFound, got %v", err)
	}
	if err := r.Save(ctx, &model.Rule{ID: "missing", Name: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("updating unknown rule: want ErrNotFound, got %v", err)
	}
}

func TestDispatchRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewDispatchRepo(s)
	if _, err := r.Get(ctx, "a1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	d := &model.Dispatch{RuleName: "default", Results: []model.DeliveryResult{{Destination: "ops", Attempts: 1}}}
	if err := r.Save(ctx, "a1", d); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "a1")
	if err != nil || got.RuleName != "default" || got.Results[0].Destination != "ops" {
		t.Errorf("got %+v, %v", got, err)
	}
}
