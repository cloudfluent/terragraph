package vendor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest_MissingFileIsEmpty(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty manifest, got %v", m)
	}
}

func TestManifest_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "vendor.yaml")

	m := Manifest{
		"vpc": Entry{
			Source:  "git::https://github.com/x/y.git?ref=v1.0.0",
			Exclude: []string{"*.md"},
		},
	}
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	got := loaded["vpc"]
	if got.Source != m["vpc"].Source {
		t.Fatalf("Source = %q, want %q", got.Source, m["vpc"].Source)
	}
	if len(got.Exclude) != 1 || got.Exclude[0] != "*.md" {
		t.Fatalf("Exclude = %v, want [*.md]", got.Exclude)
	}
}

func TestManifest_SaveIsDeterministicRegardlessOfInsertionOrder(t *testing.T) {
	entryA := Entry{Source: "git::https://a"}
	entryB := Entry{Source: "git::https://b"}

	m1 := Manifest{}
	m1["a"] = entryA
	m1["b"] = entryB

	m2 := Manifest{}
	m2["b"] = entryB
	m2["a"] = entryA

	path1 := filepath.Join(t.TempDir(), "vendor.yaml")
	path2 := filepath.Join(t.TempDir(), "vendor.yaml")
	if err := m1.Save(path1); err != nil {
		t.Fatalf("Save m1: %v", err)
	}
	if err := m2.Save(path2); err != nil {
		t.Fatalf("Save m2: %v", err)
	}

	data1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("reading path1: %v", err)
	}
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("reading path2: %v", err)
	}
	if string(data1) != string(data2) {
		t.Fatalf("expected byte-identical output regardless of map insertion order:\n--- m1 ---\n%s\n--- m2 ---\n%s", data1, data2)
	}
}
