package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/store"
)

// Rules are order-sensitive (first match wins), so they live in a single
// object: reordering is one atomic write instead of renumbering many.
const rulesKey = "routing/rules.json"

// RuleRepo stores the ordered routing rule list. The mutex serializes
// read-modify-write cycles within this process.
type RuleRepo struct {
	store store.Store
	mu    sync.Mutex
}

func NewRuleRepo(s store.Store) *RuleRepo {
	return &RuleRepo{store: s}
}

// List returns the rules in evaluation order.
func (r *RuleRepo) List(ctx context.Context) ([]*model.Rule, error) {
	raw, err := r.store.Get(ctx, rulesKey)
	if errors.Is(err, store.ErrNotFound) {
		return []*model.Rule{}, nil
	}
	if err != nil {
		return nil, err
	}
	var rules []*model.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("decoding rules: %w", err)
	}
	return rules, nil
}

func (r *RuleRepo) Get(ctx context.Context, id string) (*model.Rule, error) {
	rules, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if i := indexOf(rules, id); i >= 0 {
		return rules[i], nil
	}
	return nil, store.ErrNotFound
}

// Save updates a rule in place, or appends it (assigning an ID) when new.
func (r *RuleRepo) Save(ctx context.Context, rule *model.Rule) error {
	return r.update(ctx, func(rules []*model.Rule) ([]*model.Rule, error) {
		rule.UpdatedAt = time.Now().UTC()
		if rule.ID == "" {
			rule.ID = ulid.Make().String()
			return append(rules, rule), nil
		}
		i := indexOf(rules, rule.ID)
		if i < 0 {
			return nil, store.ErrNotFound
		}
		rules[i] = rule
		return rules, nil
	})
}

// Delete is idempotent, like the store's.
func (r *RuleRepo) Delete(ctx context.Context, id string) error {
	return r.update(ctx, func(rules []*model.Rule) ([]*model.Rule, error) {
		return slices.DeleteFunc(rules, func(x *model.Rule) bool { return x.ID == id }), nil
	})
}

// Move shifts a rule up (delta < 0) or down (delta > 0) in evaluation
// order, clamped to the list bounds.
func (r *RuleRepo) Move(ctx context.Context, id string, delta int) error {
	return r.update(ctx, func(rules []*model.Rule) ([]*model.Rule, error) {
		i := indexOf(rules, id)
		if i < 0 {
			return nil, store.ErrNotFound
		}
		j := min(max(i+delta, 0), len(rules)-1)
		rule := rules[i]
		rules = slices.Delete(rules, i, i+1)
		return slices.Insert(rules, j, rule), nil
	})
}

func (r *RuleRepo) update(ctx context.Context, fn func([]*model.Rule) ([]*model.Rule, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rules, err := r.List(ctx)
	if err != nil {
		return err
	}
	if rules, err = fn(rules); err != nil {
		return err
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if err := r.store.Put(ctx, rulesKey, raw); err != nil {
		return fmt.Errorf("persisting rules: %w", err)
	}
	return nil
}

func indexOf(rules []*model.Rule, id string) int {
	return slices.IndexFunc(rules, func(x *model.Rule) bool { return x.ID == id })
}
