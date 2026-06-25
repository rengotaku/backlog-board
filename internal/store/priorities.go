package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// PriorityStore は「自分が手を付ける順番」（個人 backlog）を永続化する。
// snapshot.json とは別ファイルに持つことで、15 分ごとの定期 fetch による
// snapshot 上書きの影響を受けない。中身は issue_id の並びだけで、課題のメタ情報
// （summary / status 等）は描画時に snapshot 側と join する。
type PriorityStore struct {
	Path string
}

// priorityFile は priorities.json の JSON スキーマ。
type priorityFile struct {
	Version   int    `json:"version"`
	Order     []int  `json:"order"`
	UpdatedAt string `json:"updated_at"`
}

const priorityFileVersion = 1

// NewPriorityStore は snapshot.json と同じディレクトリに priorities.json を置く
// PriorityStore を返す。
func NewPriorityStore(snapshotPath string) *PriorityStore {
	return &PriorityStore{Path: filepath.Join(filepath.Dir(snapshotPath), "priorities.json")}
}

// Load は保存済みの優先順位（issue_id の並び）を返す。
// ファイル未作成時は空スライスを返す（エラーにしない）。
func (p *PriorityStore) Load() ([]int, error) {
	b, err := os.ReadFile(p.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []int{}, nil
		}
		return nil, err
	}
	var f priorityFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Order == nil {
		return []int{}, nil
	}
	return f.Order, nil
}

// Save は優先順位を atomic に書き込む。snapshot.json と同様 dir 0o700 / file 0o600。
// 重複 id は先勝ちで除去し、0 以下の id は捨てる。
func (p *PriorityStore) Save(order []int) error {
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		return err
	}
	cleaned := make([]int, 0, len(order))
	seen := make(map[int]bool, len(order))
	for _, id := range order {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}
	f := priorityFile{
		Version:   priorityFileVersion,
		Order:     cleaned,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.Path); err != nil {
		return err
	}
	_ = os.Chmod(p.Path, 0o600)
	return nil
}
