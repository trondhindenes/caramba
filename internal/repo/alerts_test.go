package repo

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/store"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/grafana-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newRepo(t *testing.T) *AlertRepo {
	t.Helper()
	s, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewAlertRepo(s)
}

func TestSaveAndGet(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	raw := fixture(t)

	saved, err := r.Save(ctx, raw, time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Payload.Status != "firing" || len(saved.Payload.Alerts) != 2 {
		t.Errorf("unexpected payload: status=%q alerts=%d", saved.Payload.Status, len(saved.Payload.Alerts))
	}

	got, err := r.Get(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Raw) != string(raw) {
		t.Error("raw payload not persisted verbatim")
	}
	if !got.ReceivedAt.Equal(saved.ReceivedAt) {
		t.Errorf("ReceivedAt mismatch: %v vs %v", got.ReceivedAt, saved.ReceivedAt)
	}
}

func TestSaveRejectsInvalidJSON(t *testing.T) {
	r := newRepo(t)
	_, err := r.Save(context.Background(), []byte("not json"), time.Now())
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("want ErrInvalidPayload, got %v", err)
	}
}

func TestListNewestFirst(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	raw := fixture(t)

	base := time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := r.Save(ctx, raw, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	alerts, truncated, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("3 alerts should not be truncated")
	}
	if len(alerts) != 3 {
		t.Fatalf("want 3 alerts, got %d", len(alerts))
	}
	for i := 1; i < len(alerts); i++ {
		if alerts[i-1].ReceivedAt.Before(alerts[i].ReceivedAt) {
			t.Errorf("not newest-first at index %d: %v before %v", i, alerts[i-1].ReceivedAt, alerts[i].ReceivedAt)
		}
	}
}

func TestGetMissing(t *testing.T) {
	r := newRepo(t)
	_, err := r.Get(context.Background(), "2026-06-10T00:00:00.000Z-NOPE")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
