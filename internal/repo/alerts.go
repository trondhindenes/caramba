// Package repo provides typed repositories on top of the blob store. All
// GUI and forwarding code goes through repositories, so the future move to
// an indexed database only changes this package.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/store"
)

// ErrInvalidPayload marks input that could not be decoded as a Grafana
// webhook payload (caller should treat it as a bad request, not a server
// error).
var ErrInvalidPayload = errors.New("invalid alert payload")

const (
	alertPrefix = "alerts/"
	// idTimeLayout is fixed-width RFC 3339 UTC with milliseconds, so keys
	// sort chronologically by name alone (retention relies on this too).
	idTimeLayout = "2006-01-02T15:04:05.000Z"
	// maxList caps how many alerts the list view loads; the GUI shows a
	// notice when the cap truncates results.
	maxList = 200
)

type StoredAlert struct {
	ID         string
	ReceivedAt time.Time
	Payload    *model.Payload
	Raw        []byte
}

type AlertRepo struct {
	store store.Store
}

func NewAlertRepo(s store.Store) *AlertRepo {
	return &AlertRepo{store: s}
}

func (r *AlertRepo) Save(ctx context.Context, raw []byte, receivedAt time.Time) (*StoredAlert, error) {
	var p model.Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	receivedAt = receivedAt.UTC()
	id := receivedAt.Format(idTimeLayout) + "-" + ulid.Make().String()
	if err := r.store.Put(ctx, alertPrefix+id+".json", raw); err != nil {
		return nil, fmt.Errorf("persisting alert: %w", err)
	}
	return &StoredAlert{ID: id, ReceivedAt: receivedAt, Payload: &p, Raw: raw}, nil
}

// List returns up to maxList stored alerts, newest first. truncated reports
// whether older alerts exist beyond the cap.
func (r *AlertRepo) List(ctx context.Context) (alerts []*StoredAlert, truncated bool, err error) {
	infos, err := r.store.List(ctx, alertPrefix)
	if err != nil {
		return nil, false, err
	}
	// Keys embed a fixed-width timestamp, so descending key order is
	// newest-first chronological order.
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key > infos[j].Key })
	truncated = len(infos) > maxList
	if truncated {
		infos = infos[:maxList]
	}
	for _, info := range infos {
		a, err := r.load(ctx, info)
		if err != nil {
			// A single corrupt object should not take down the list view.
			continue
		}
		alerts = append(alerts, a)
	}
	return alerts, truncated, nil
}

func (r *AlertRepo) Get(ctx context.Context, id string) (*StoredAlert, error) {
	key := alertPrefix + id + ".json"
	raw, err := r.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return parse(id, raw)
}

func (r *AlertRepo) Delete(ctx context.Context, id string) error {
	return r.store.Delete(ctx, alertPrefix+id+".json")
}

func (r *AlertRepo) load(ctx context.Context, info store.ObjectInfo) (*StoredAlert, error) {
	raw, err := r.store.Get(ctx, info.Key)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSuffix(strings.TrimPrefix(info.Key, alertPrefix), ".json")
	a, err := parse(id, raw)
	if err != nil {
		return nil, err
	}
	if a.ReceivedAt.IsZero() {
		a.ReceivedAt = info.ModTime
	}
	return a, nil
}

func parse(id string, raw []byte) (*StoredAlert, error) {
	var p model.Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	receivedAt, _ := time.Parse(idTimeLayout, idTimePart(id))
	return &StoredAlert{ID: id, ReceivedAt: receivedAt, Payload: &p, Raw: raw}, nil
}

func idTimePart(id string) string {
	if len(id) > len(idTimeLayout) {
		return id[:len(idTimeLayout)]
	}
	return id
}
