package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// CategoryStore は My Backlog の取込対象カテゴリ ID 群を永続化する。
// UI から変更できるよう snapshot とは別ファイル (categories.json) に持つ。
// config の category_id(s) は初回シード用で、以後はこのファイルが SoT。
type CategoryStore struct {
	Path string
}

type categoryFile struct {
	Version     int    `json:"version"`
	CategoryIDs []int  `json:"category_ids"`
	UpdatedAt   string `json:"updated_at"`
}

const categoryFileVersion = 1

func NewCategoryStore(snapshotPath string) *CategoryStore {
	return &CategoryStore{Path: filepath.Join(filepath.Dir(snapshotPath), "categories.json")}
}

// Load は保存済みのカテゴリ ID を返す。ファイル未作成時は (nil, false, nil) を返し、
// 呼び出し側が config からのシードを判断できるようにする。
func (s *CategoryStore) Load() (ids []int, exists bool, err error) {
	b, rerr := os.ReadFile(s.Path)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, rerr
	}
	var f categoryFile
	if jerr := json.Unmarshal(b, &f); jerr != nil {
		return nil, false, jerr
	}
	return clampCategoryIDs(f.CategoryIDs), true, nil
}

// Save は atomic に書き込む（dir 0o700 / file 0o600）。重複・不正 id は除去する。
func (s *CategoryStore) Save(ids []int) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	f := categoryFile{
		Version:     categoryFileVersion,
		CategoryIDs: clampCategoryIDs(ids),
		UpdatedAt:   time.Now().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return err
	}
	_ = os.Chmod(s.Path, 0o600)
	return nil
}

func clampCategoryIDs(in []int) []int {
	out := make([]int, 0, len(in))
	seen := make(map[int]bool, len(in))
	for _, id := range in {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
