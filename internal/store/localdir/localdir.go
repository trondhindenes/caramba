// Package localdir implements store.Store on a local directory tree.
package localdir

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trondhindenes/caramba/internal/store"
)

type Store struct {
	root string
}

func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("creating store root: %w", err)
	}
	return &Store{root: root}, nil
}

// path validates a key and maps it to a filesystem path. fs.ValidPath
// rejects empty, absolute, and ".."-containing keys, which also guards
// against path traversal from user-supplied IDs.
func (s *Store) path(key string) (string, error) {
	if !fs.ValidPath(key) || strings.HasSuffix(key, "/") {
		return "", fmt.Errorf("invalid store key %q", key)
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

func (s *Store) Put(_ context.Context, key string, data []byte) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// Write to a temp file in the same directory and rename, so readers
	// never observe a partially written object.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (s *Store) Get(_ context.Context, key string) ([]byte, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", store.ErrNotFound, key)
	}
	return data, err
}

func (s *Store) List(_ context.Context, prefix string) ([]store.ObjectInfo, error) {
	var infos []store.ObjectInfo
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		infos = append(infos, store.ObjectInfo{Key: key, ModTime: fi.ModTime()})
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return infos, err
}

func (s *Store) Delete(_ context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
