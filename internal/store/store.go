// Package store defines the blob-shaped persistence interface backing
// caramba. Implementations exist for a local directory and (later) a GCS
// bucket. Higher layers talk to typed repositories, never to Store directly,
// so an indexed database can replace the repository internals without
// touching this interface.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get for keys that do not exist.
var ErrNotFound = errors.New("object not found")

type ObjectInfo struct {
	Key     string
	ModTime time.Time
}

// Store is a minimal blob store. Keys are slash-separated paths
// (e.g. "alerts/2026-06-10T08:00:00.000Z-01ABC.json").
type Store interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	// Delete is idempotent: deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
}
