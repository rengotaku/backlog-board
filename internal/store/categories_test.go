package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestCategoryStore_MissingReturnsNotExists(t *testing.T) {
	s := NewCategoryStore(filepath.Join(t.TempDir(), "snapshot.json"))
	ids, exists, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if exists {
		t.Errorf("exists = true on missing file, want false")
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want empty", ids)
	}
}

func TestCategoryStore_SaveLoadDedup(t *testing.T) {
	s := NewCategoryStore(filepath.Join(t.TempDir(), "snapshot.json"))
	if err := s.Save([]int{10, 10, 0, -1, 20}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	ids, exists, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !exists {
		t.Errorf("exists = false after Save, want true")
	}
	if want := []int{10, 20}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestCategoryStore_PathNextToSnapshot(t *testing.T) {
	dir := t.TempDir()
	s := NewCategoryStore(filepath.Join(dir, "snapshot.json"))
	if want := filepath.Join(dir, "categories.json"); s.Path != want {
		t.Errorf("Path = %q, want %q", s.Path, want)
	}
}
