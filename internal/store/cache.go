package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rengotaku/backlog-board/internal/backlog"
)

type Cache struct {
	Path string
}

func New(path string) *Cache {
	return &Cache{Path: path}
}

func (c *Cache) Load() (*backlog.Snapshot, error) {
	return loadSnapshot(c.Path)
}

// LoadPrevious は直前世代の snapshot を読み込む。差分計算用。
// 初回保存より前は os.ErrNotExist を返す。
func (c *Cache) LoadPrevious() (*backlog.Snapshot, error) {
	return loadSnapshot(c.previousPath())
}

func loadSnapshot(path string) (*backlog.Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s backlog.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse cache %s: %w", path, err)
	}
	return &s, nil
}

func (c *Cache) previousPath() string {
	return c.Path + ".previous"
}

func (c *Cache) Save(s *backlog.Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	// 差分計算用に直前 snapshot を .previous へ退避（ベストエフォート）。
	if _, err := os.Stat(c.Path); err == nil {
		_ = os.Rename(c.Path, c.previousPath())
	}
	return os.Rename(tmp, c.Path)
}
