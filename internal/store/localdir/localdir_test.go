package localdir

import (
	"context"
	"errors"
	"testing"

	"github.com/trondhindenes/caramba/internal/store"
)

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Put(ctx, "alerts/a.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "alerts/a.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %q", got)
	}

	if err := s.Put(ctx, "alerts/a.json", []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "alerts/a.json")
	if string(got) != `{"a":2}` {
		t.Errorf("overwrite: got %q", got)
	}
}

func TestGetMissing(t *testing.T) {
	s, _ := New(t.TempDir())
	_, err := s.Get(context.Background(), "alerts/nope.json")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestListPrefix(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	for _, key := range []string{"alerts/1.json", "alerts/2.json", "templates/t.json"} {
		if err := s.Put(ctx, key, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := s.List(ctx, "alerts/")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 objects, got %d: %v", len(infos), infos)
	}
	for _, info := range infos {
		if info.ModTime.IsZero() {
			t.Errorf("%s: zero ModTime", info.Key)
		}
	}
}

func TestListEmptyPrefix(t *testing.T) {
	s, _ := New(t.TempDir())
	infos, err := s.List(context.Background(), "alerts/")
	if err != nil || len(infos) != 0 {
		t.Errorf("want empty list, got %v, %v", infos, err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	if err := s.Put(ctx, "alerts/a.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "alerts/a.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "alerts/a.json"); err != nil {
		t.Errorf("second delete should be a no-op, got %v", err)
	}
	if _, err := s.Get(ctx, "alerts/a.json"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	s, _ := New(t.TempDir())
	for _, key := range []string{"../escape.json", "/abs.json", "a/../../b", ""} {
		if err := s.Put(ctx, key, []byte("{}")); err == nil {
			t.Errorf("Put(%q) should fail", key)
		}
		if _, err := s.Get(ctx, key); err == nil {
			t.Errorf("Get(%q) should fail", key)
		}
	}
}
