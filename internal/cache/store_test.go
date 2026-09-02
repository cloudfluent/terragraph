package cache

import (
	"path/filepath"
	"testing"
)

func TestStore_LoadMissingFileIsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s) != 0 {
		t.Fatalf("expected empty store, got %v", s)
	}
}

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cache.json")
	s := Store{"vpc": "hash-a", "eks": "hash-b"}

	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded["vpc"] != "hash-a" || loaded["eks"] != "hash-b" {
		t.Fatalf("unexpected round-tripped store: %v", loaded)
	}
}
