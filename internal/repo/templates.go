package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/store"
)

const templatePrefix = "templates/"

type TemplateRepo struct {
	store store.Store
}

func NewTemplateRepo(s store.Store) *TemplateRepo {
	return &TemplateRepo{store: s}
}

// Save persists a template, assigning an ID on first save, and stamps
// UpdatedAt.
func (r *TemplateRepo) Save(ctx context.Context, t *model.Template) error {
	if t.ID == "" {
		t.ID = ulid.Make().String()
	}
	t.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := r.store.Put(ctx, templatePrefix+t.ID+".json", raw); err != nil {
		return fmt.Errorf("persisting template: %w", err)
	}
	return nil
}

func (r *TemplateRepo) Get(ctx context.Context, id string) (*model.Template, error) {
	raw, err := r.store.Get(ctx, templatePrefix+id+".json")
	if err != nil {
		return nil, err
	}
	var t model.Template
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("decoding template %s: %w", id, err)
	}
	return &t, nil
}

// List returns all templates sorted by name.
func (r *TemplateRepo) List(ctx context.Context) ([]*model.Template, error) {
	infos, err := r.store.List(ctx, templatePrefix)
	if err != nil {
		return nil, err
	}
	templates := make([]*model.Template, 0, len(infos))
	for _, info := range infos {
		raw, err := r.store.Get(ctx, info.Key)
		if err != nil {
			return nil, err
		}
		var t model.Template
		if err := json.Unmarshal(raw, &t); err != nil {
			continue // one corrupt object should not break the list
		}
		templates = append(templates, &t)
	}
	sort.Slice(templates, func(i, j int) bool { return templates[i].Name < templates[j].Name })
	return templates, nil
}

func (r *TemplateRepo) Delete(ctx context.Context, id string) error {
	return r.store.Delete(ctx, templatePrefix+id+".json")
}
