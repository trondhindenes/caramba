package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/store"
)

// Dispatch records sit beside their alert, keyed by the alert ID.
const dispatchPrefix = "dispatches/"

type DispatchRepo struct {
	store store.Store
}

func NewDispatchRepo(s store.Store) *DispatchRepo {
	return &DispatchRepo{store: s}
}

func (r *DispatchRepo) Save(ctx context.Context, alertID string, d *model.Dispatch) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := r.store.Put(ctx, dispatchPrefix+alertID+".json", raw); err != nil {
		return fmt.Errorf("persisting dispatch: %w", err)
	}
	return nil
}

// Get returns store.ErrNotFound for alerts that were never routed (e.g.
// received before routing existed).
func (r *DispatchRepo) Get(ctx context.Context, alertID string) (*model.Dispatch, error) {
	raw, err := r.store.Get(ctx, dispatchPrefix+alertID+".json")
	if err != nil {
		return nil, err
	}
	var d model.Dispatch
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("decoding dispatch for %s: %w", alertID, err)
	}
	return &d, nil
}
