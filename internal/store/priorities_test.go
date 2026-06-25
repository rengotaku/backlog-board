package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPriorityStore_LoadMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := NewPriorityStore(filepath.Join(dir, "snapshot.json"))
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load() on missing file = %v, want empty", got)
	}
}

func TestPriorityStore_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := NewPriorityStore(filepath.Join(dir, "snapshot.json"))
	if err := p.Save([]int{30, 10, 20}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := []int{30, 10, 20}; !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %v, want %v", got, want)
	}
}

func TestPriorityStore_SaveDedupesAndDropsInvalid(t *testing.T) {
	dir := t.TempDir()
	p := NewPriorityStore(filepath.Join(dir, "snapshot.json"))
	if err := p.Save([]int{10, 10, 0, -5, 20, 10}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := []int{10, 20}; !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %v, want %v", got, want)
	}
}

func TestPriorityStore_FilePlacedNextToSnapshot(t *testing.T) {
	dir := t.TempDir()
	p := NewPriorityStore(filepath.Join(dir, "snapshot.json"))
	if err := p.Save([]int{1}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	want := filepath.Join(dir, "priorities.json")
	if p.Path != want {
		t.Errorf("Path = %q, want %q", p.Path, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat priorities.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("priorities.json perm = %o, want 600", perm)
	}
}
