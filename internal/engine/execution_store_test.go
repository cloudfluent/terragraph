package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/runlock"
)

func localStoreFixture(t *testing.T) *localExecutionStore {
	t.Helper()
	base := t.TempDir()
	lock, err := runlock.Acquire(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	store, err := openLocalExecutionStore(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	return store
}

func TestExecutionStore_StaleUpdateAndDeletePreserveNewRecord(t *testing.T) {
	s := localStoreFixture(t)
	ctx := context.Background()
	key := newExecutionID("run") + ".json"
	first, err := s.write(ctx, key, []byte("prepared"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.write(ctx, key, []byte("started"), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.write(ctx, key, []byte("success"), first); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v, want conflict", err)
	}
	if err := s.remove(ctx, key, first); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v, want conflict", err)
	}
	obj, err := s.read(ctx, key)
	if err != nil || string(obj.Data) != "started" || obj.Revision != second {
		t.Fatalf("got = %+v, %v", obj, err)
	}
	if err := s.remove(ctx, key, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(ctx, key); !errors.Is(err, errExecutionMissing) {
		t.Fatalf("got = %v, want missing", err)
	}
}

func TestExecutionStore_CreateNeverOverwritesExistingPlan(t *testing.T) {
	s := localStoreFixture(t)
	ctx := context.Background()
	key := newExecutionID("plan") + ".bin"
	if _, err := s.write(ctx, key, []byte("reviewed bytes"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write(ctx, key, []byte("different bytes"), ""); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v", err)
	}
	obj, err := s.read(ctx, key)
	if err != nil || string(obj.Data) != "reviewed bytes" {
		t.Fatalf("got = %+v, %v", obj, err)
	}
	keys, err := s.list(ctx)
	if err != nil || len(keys) != 1 || keys[0] != key {
		t.Fatalf("got = %v, %v", keys, err)
	}
}

func TestExecutionStore_RejectsPathsAndCancelledWrites(t *testing.T) {
	s := localStoreFixture(t)
	for _, key := range []string{"../outside", filepath.Join("..", "outside"), "/outside", "plan-" + strings.Repeat("a", 32) + ".bin/child"} {
		if _, err := s.write(context.Background(), key, []byte("unsafe"), ""); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.write(ctx, newExecutionID("run")+".json", []byte("started"), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("got = %v", err)
	}
	keys, err := s.list(context.Background())
	if err != nil || len(keys) != 0 {
		t.Fatalf("got = %v, %v", keys, err)
	}
}

func TestExecutionStore_IdenticalRewriteInvalidatesOldRevision(t *testing.T) {
	s := localStoreFixture(t)
	key := newExecutionID("run") + ".json"
	ctx := context.Background()
	first, err := s.write(ctx, key, []byte("same"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.write(ctx, key, []byte("same"), first)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("rewrite reused revision")
	}
	if _, err := s.write(ctx, key, []byte("lost update"), first); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v", err)
	}
}
